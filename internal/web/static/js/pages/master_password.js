import {el, button, field, modal} from '../dom.js';

export function masterPassword(changing, removing = false) {
  return new Promise(resolve => {
    const d = modal(removing ? 'Remove master password' : changing ? 'Change master password' : 'Set master password');
    const password = field('Password', '', 'password'), repeat = field('Repeat',
      '', 'password');
    const current = field('Current master password', '', 'password');
    const error = el('p', {class:'error'});

    function submit() {
      if (!removing && (Array.from(password.input.value).length < 8 || password.input.value !== repeat.input.value)) {
        error.textContent = Array.from(password.input.value).length < 8
          ? 'Use at least 8 characters.' : 'Passwords do not match.';
        return;
      }
      resolve(removing ? {current:current.input.value} : changing
        ? {master:password.input.value, current:current.input.value} : {master:password.input.value});
      d.close();
    }
    (removing ? current : repeat).input.addEventListener('keydown', e => {
      if (e.key === 'Enter') submit();
    });
    d.append(el('div', {class:'stack'}, [...(changing ? [current.node] : []),
        ...(!removing ? [password.node, repeat.node] : []), error]),
      el('div', {class:'actions'}, [button('Cancel', () => d.close()), button('OK', submit, 'primary')]));
    d.addEventListener('close', () => resolve(null), {once:true});
    (changing ? current : password).input.focus();
  });
}
