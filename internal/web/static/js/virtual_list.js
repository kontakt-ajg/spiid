import {el} from './dom.js';

export function virtualList(height, render) {
  const node = el('div', {
    class:'virtual', attrs:{tabindex:0}
  });
  const space = el('div', {class:'virtual-space'});
  node.append(space);
  let items = [], rows = [];

  function draw() {
    const first = Math.max(0, Math.floor(node.scrollTop / height) - 10);
    const last = Math.min(items.length, Math.ceil((node.scrollTop + node.clientHeight) / height) +
        10);
    const count = Math.max(0, last-first);
    while (rows.length > count) rows.pop().remove();
    while (rows.length < count) {
      const row = el('div', {class:'virtual-row'});
      row.style.setProperty('--row-height', height + 'px');
      rows.push(row);
      space.append(row);
    }
    rows.forEach((row, i) => {
      row.style.setProperty('--offset', (first+i)*height + 'px');
      render(row, items[first+i]);
    });
  }
  node.addEventListener('scroll', () => draw());
  const observer = new ResizeObserver(() => draw());
  observer.observe(node);
  return {
    node, refresh:draw, set(values) {
      items = values;
      space.style.setProperty('--height', items.length*height + 'px');
      draw();
    }, destroy() {
      observer.disconnect();
    }
  };
}
