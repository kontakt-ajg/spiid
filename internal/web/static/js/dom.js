export function el(tag, options = {}, children = []) {
  const node = document.createElement(tag);
  if (options.class) node.className = options.class;
  if (options.text !== undefined) node.textContent = options.text;
  for (const [key, value] of Object.entries(options.attrs || {})) {
    if (value !== false && value != null) node.setAttribute(key, value === true ? '' :
      value);
  }
  for (const [event, handler] of Object.entries(options.on || {})) node.addEventListener(event, handler);
  node.append(...children);
  return node;
}

export function clear(node) {
  node.replaceChildren();
}

export function button(text, action, className = '') {
  return el('button', {
    text, class:className, attrs:{type:'button'}, on:{click:action}
  });
}
let noticeTimer;

export function notice(message, error = false) {
  const node = document.getElementById('notice');
  node.textContent = message;
  node.className = error ? 'error' : '';
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => {
    node.textContent = '';
  }, error ? 8000 : 3000);
}

export function modal(title) {
  const node = el('dialog', {}, [el('h2', {text:title})]);
  document.getElementById('dialogs').append(node);
  node.addEventListener('close', () => node.remove(), {once:true});
  node.showModal();
  return node;
}

export function field(text, value = '', type = 'text') {
  const input = el('input', {
    attrs:{type, value, 'data-test':text}
  });
  return {
    input, node:el('label', {text}, [input])
  };
}

export function confirmDialog(title, text, accept) {
  return new Promise(resolve => {
    const d = modal(title);
    d.append(el('p', {text}), el('div', {class:'actions'}, [
      button('Cancel', () => d.close()), button(accept, () => {
        resolve(true);
        d.close();
      }, 'primary')
    ]));
    d.addEventListener('close', () => resolve(false), {once:true});
  });
}
