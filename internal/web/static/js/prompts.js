import {answer} from './api.js';
import {el, button, modal, field, notice} from './dom.js';

export function prompts() {
  const queue = [], seen = new Set();
  let busy = false;

  async function next() {
    if (busy || !queue.length) return;
    busy = true;
    const p = queue.shift(), host = p.prompt === 'host_key';
    const noun = p.kind === 'passphrase' ? 'Passphrase' : 'Password';
    const d = modal(host ? 'New host key' : p.kind === 'master' ? 'Master password' : `${noun} for ${p.server}`);
    const error = el('p', {class:'error'});
    let value, save;
    if (host) d.append(el('p', {
      text:`The authenticity of ${p.host}:${p.port} (server ${p.server}) can't be established. ` +
        `${p.key_type} key fingerprint is ${p.fingerprint}. Trust it and remember it?`
    }));
    else {
      if (p.retry) d.append(el('p', {class:'error', text:"That didn't work. Try again."}));
      if (p.rejected) d.append(el('p', {class:'error', text:'The saved password was rejected.'}));
      value = field(noun, '', 'password');
      d.append(value.node);
      save = el('input', {
        attrs:{type:'checkbox', checked:true}
      });
      if (p.offer_save) d.append(el('label', {
        class:'check', text:`Save ${noun.toLowerCase()}`
      }, [save]));
    }
    let sending = false;

    async function send(result) {
      if (sending) return;
      sending = true;
      try {
        await answer(p.id, result);
        d.close();
        busy = false;
        next();
      }
      catch (e) {
        error.textContent = e.message;
        sending = false;
        notice(e.message, true);
      }
    }
    const cancel = () => send(host ? {accept:false}
        : {cancel:true});
    const ok = () => send(host ? {accept:true}
        : {value:value.input.value, save:!!p.offer_save && save.checked});
    d.addEventListener('cancel', e => {
      e.preventDefault();
      cancel();
    });
    d.append(error, el('div', {class:'actions'}, [
      button(host ? 'Reject' : 'Cancel', cancel),
      button(host ? 'Trust' : 'OK', ok, 'primary')
    ]));
    if (value) {
      value.input.focus();
      value.input.addEventListener('keydown', e => {
        if (e.key === 'Enter') ok();
      });
    }
  }
  return event => {
    if (event.type === 'error') notice(event.error.message, true);
    if (event.type !== 'prompt' || seen.has(event.id)) return;
    seen.add(event.id);
    queue.push(event);
    next();
  };
}
