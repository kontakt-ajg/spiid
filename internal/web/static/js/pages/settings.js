import {call} from '../api.js';
import {el, button, clear, notice, confirmDialog} from '../dom.js';
import {localPicker} from '../local_picker.js';
import {masterPassword} from './master_password.js';

export function settingsPage(setTheme) {
  const node = el('section', {class:'page'}), frame = el('div', {class:'frame'});
  const error = el('p', {class:'error'});
  node.append(frame);
  let settings, info, encrypted = false, busy = false, loaded = false;

  async function run(action) {
    if (busy) return;
    busy = true;
    render();
    try {
      await action();
    } catch (e) {
      notice(e.message, true);
    }
    finally {
      busy = false;
      render();
    }
  }

  function save(change) {
    return run(async () => {
      settings = await call('settings.save', {...settings, ...change});
      setTheme(settings.theme);
    });
  }

  async function master() {
    const value = await masterPassword(encrypted);
    if (value === null) return;
    await run(async () => {
      await call(encrypted ? 'credentials.change' : 'credentials.protect', value);
      encrypted = (await call('credentials.status')).encrypted;
    });
  }

  async function remove() {
    const value = await masterPassword(true, true);
    if (value === null) return;
    await run(async () => {
      await call('credentials.unprotect', value);
      encrypted = (await call('credentials.status')).encrypted;
    });
  }

  function control(text, action, className = '') {
    const b = button(text, action, className);
    b.disabled = busy;
    return b;
  }

  function section(title, children) {
    frame.append(el('h3', {text:title}), el('div', {class:'card stack'}, children));
  }

  function stepper(label, key, max) {
    const value = settings[key];
    const minus = control('−', () => save({[key]:value-1}));
    const plus = control('+', () => save({[key]:value+1}));
    minus.title = 'Decrease ' + label;
    minus.setAttribute('aria-label', minus.title);
    plus.title = 'Increase ' + label;
    plus.setAttribute('aria-label', plus.title);
    minus.disabled = busy || value <= 1;
    plus.disabled = busy || value >= max;
    return el('div', {
      class:'row', attrs:{'data-test':key}
    }, [el('span', {class:'grow', text:label}), minus, el('span', {text:value}), plus]);
  }

  function render() {
    clear(frame);
    frame.append(el('h2', {text:'Settings'}), error);
    if (!loaded) return;
    section('General', [el('span', {text:'Theme'}), el('div', {class:'row'}, ['system','light','dark'].map(theme => {
      const b = control(
        theme[0].toUpperCase()+theme.slice(1), () => save({theme}),
        settings.theme === theme ? 'primary' : ''
      );
      b.setAttribute('aria-pressed', String(settings.theme === theme));
      return b;
    }))]);
    section('Downloads', [
      el('span', {text:'Download folder'}),
      el('div', {class:'row'}, [
        el('span', {class:'grow ellipsis', text:settings.download_dir}),
        control('Change…', async () => {
          const path = await localPicker('folder', settings.download_dir);
          if (path !== null) await save({download_dir:path});
        }, 'text-button')]),
      stepper('Threads per file', 'threads_per_file', 32),
      stepper('Parallel files', 'parallel_files', 16),
      stepper('Connections per server', 'max_connections', 64),
      el('p', {text:'Thread and connection changes apply to new downloads.'})
    ]);
    const passwords = [
      el('p', {
        text:encrypted ? 'Saved passwords are protected by a master password.'
          : `Saved passwords are stored as plain text in ${info.config_dir}/config.json.`
      }),
      control(encrypted ? 'Change master password…' : 'Protect with a master password…',
        master, 'text-button')
    ];
    if (encrypted) passwords.push(control('Remove master password', remove, 'text-button'));
    if (encrypted) passwords.push(control('Forgot master password?', async () => {
      if (!await confirmDialog('Reset master password?', 'Saved passwords will be deleted.', 'Reset')) return;
      await run(async () => {
        await call('credentials.reset');
        encrypted = false;
      });
    }, 'text-button'));
    section('Passwords', passwords);
    section('About', [
      `Spiid ${info.version}`, `Config folder: ${info.config_dir}`,
      'Design inspired by LocalSend (Apache-2.0)', 'Font: Inter (SIL Open Font License 1.1)',
      'Icons: Material Symbols (Apache-2.0)'
    ].map(text => el('div', {text})));
  }

  async function load() {
    try {
      const results = await Promise.allSettled([call('settings.get'), call('credentials.status'),
        call('app.info')]);
      const failure = results.find(r => r.status === 'rejected');
      if (failure) throw failure.reason;
      [settings, {encrypted}, info] = results.map(r => r.value);
      loaded = true;
      error.textContent = '';
      render();
    } catch (e) {
      error.textContent = e.message;
      render();
    }
  }
  render();
  return {
    node, onShow() {
      if (!loaded) load();
    }
  };
}
