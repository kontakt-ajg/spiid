import {el, button, modal, field, confirmDialog} from '../dom.js';
import {localPicker} from '../local_picker.js';

export function serverForm(existing) {
  return new Promise(resolve => {
    const s = existing || {}, d = modal(existing ? 'Edit server' : 'Add server');
    const form = el('div', {class:'stack'}), fields = {};
    for (const [key, label, value, type] of [
      ['name','Name',s.name], ['host','Host',s.host], ['port','Port',s.port || 22,
        'number'],
      ['user','User',s.user], ['key_file','Key file',s.key_file],
      ['password','Password','', 'password'], ['start_dir','Start folder (optional)',
        s.start_dir]
    ]) fields[key] = field(label, value ?? '', type || 'text');
    const login = el('select', {
      attrs:{'aria-label':'Login'}
    });
    for (const [value, text] of [['agent','Agent'],['key','Key'],['password',
      'Password']])
    login.append(el('option', {
      text, attrs:{value}
    }));
    login.value = s.auth || 'agent';
    fields.key_file.input.readOnly = true;
    const choose = button('Choose…', async () => {
      const path = await localPicker('file', '~/.ssh');
      if (path !== null) fields.key_file.input.value = path;
    });
    const keyRow = el('div', {class:'row'}, [fields.key_file.node, choose]);
    if (existing) fields.password.input.placeholder = 'Leave empty to keep the saved password';

    function auth() {
      keyRow.hidden = login.value !== 'key';
      fields.password.node.hidden = login.value !== 'password';
    }
    login.addEventListener('change', auth);
    form.append(fields.name.node, fields.host.node, fields.port.node, fields.user.node,
      el('label', {text:'Login'}, [login]), keyRow, fields.password.node, fields.start_dir.node);
    auth();
    const errors = el('div', {
      class:'error', attrs:{role:'alert'}
    });

    function finish(value) {
      resolve(value);
      d.close();
    }

    function save() {
      errors.replaceChildren();
      const server = {};
      for (const key of ['name','host','user','start_dir']) server[key] = fields[key].input.value.trim();
      const problems = [];
      for (const key of ['name','host','user']) if (!server[key]) problems.push(key[0].toUpperCase() +
          key.slice(1) + ': Required');
      if (/[:/]/.test(server.name)) problems.push("Name: Must not contain ':' or '/'");
      server.port = Number(fields.port.input.value);
      if (!Number.isInteger(server.port) || server.port < 1 || server.port > 65535)
        problems.push('Port: Port must be 1-65535');
      server.auth = login.value;
      server.key_file = login.value === 'key' ? fields.key_file.input.value : '';
      if (server.auth === 'key' && !server.key_file) problems.push('Key file: Choose a key file');
      if (problems.length) {
        errors.append(...problems.map(text => el('p', {text})));
        return;
      }
      const secret = server.auth === 'password' && fields.password.input.value ? fields.password.input.value :
      null;
      finish({server, secret});
    }
    const actions = el('div', {class:'actions'});
    if (existing) actions.append(button('Delete', async () => {
      if (await confirmDialog('Delete server', `Delete ${s.name}? Its saved password is removed too.`,
        'Delete')) finish({delete:true});
    }, 'error text-button'));
    actions.append(button('Cancel', () => d.close()), button('Save', save, 'primary'));
    d.append(form, errors, actions);
    d.addEventListener('close', () => resolve(null), {once:true});
    fields.name.input.focus();
  });
}
