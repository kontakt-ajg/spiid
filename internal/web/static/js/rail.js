import {el, button} from './dom.js';
import {icon} from './icons.js';

export function rail(select, quit) {
  const node = document.getElementById('rail');
  node.append(el('h1', {text:'Spiid'}));
  const buttons = ['Servers','Browse','Downloads','Settings'].map((text, i) => {
    const b = button('', () => select(i));
    b.append(icon(['computer','folder','download','settings'][i]), el('span',
      {text}));
    node.append(b);
    return b;
  });
  node.append(button('Quit Spiid', quit, 'quit'));
  return {
    select(index) {
      buttons.forEach((b,i) => {
        b.classList.toggle('selected', i === index);
        b.setAttribute('aria-current', i === index ? 'page' : 'false');
      });
    },
    count(n) {
      buttons[2].lastChild.textContent = n > 0 ? `Downloads (${n})` : 'Downloads';
    }
  };
}
