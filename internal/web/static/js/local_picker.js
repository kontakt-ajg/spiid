import {call} from './api.js';
import {el, button, modal, clear} from './dom.js';
import {icon} from './icons.js';

export function localPicker(mode, start = '') {
  return new Promise(resolve => {
    const d = modal(mode === 'file' ? 'Choose a key file' : 'Choose download folder');
    const path = el('p', {
      class:'ellipsis', attrs:{'data-test':'picker-path'}
    }), error = el('p', {class:'error'});
    const list = el('div', {class:'picker-list'}), up = button('Up', () => load(parent));
    up.title = 'Up';
    let current = '', parent = '', selected = '', generation = 0;
    const choose = button(mode === 'file' ? 'Choose' : 'Select folder', () => finish(mode === 'file' ?
      selected : current), 'primary');

    function finish(value) {
      resolve(value);
      d.close();
    }

    async function load(dir, knownFolder = false) {
      const request = ++generation;
      choose.disabled = true;
      up.disabled = true;
      selected = '';
      error.textContent = 'Loading…';
      clear(list);
      try {
        if (dir.startsWith('~/')) {
          const home = await call('local.list', {dir:''});
          dir = home.path.replace(/[\\/]$/, '') + '/' + dir.slice(2);
        }
        path.textContent = dir;
        const old = current;
        current = dir;
        parent = dir.replace(/[\\/][^\\/]+[\\/]?$/, '') || (dir.startsWith('/') ? '/' :
          old);
        up.disabled = !parent || parent === dir;
        const result = await call('local.list', {dir});
        if (request !== generation || !d.open) return;
        current = result.path;
        parent = result.parent;
        path.textContent = current;
        error.textContent = '';
        up.disabled = !parent;
        choose.disabled = mode === 'file';
        const entries = result.entries.filter(e => mode === 'file' || e.is_dir)
        .sort((a,b) => Number(b.is_dir)-Number(a.is_dir) || a.name.toLowerCase().localeCompare(b.name.toLowerCase()));
        for (const entry of entries) {
          const row = button('', () => {
            if (entry.is_dir) load(entry.path, true);
            else {
              selected = entry.path;
              choose.disabled = false;
              path.textContent = selected;
            }
          });
          row.append(icon(entry.is_dir ? 'folder' : 'file'), el('span', {text:entry.name}));
          row.addEventListener('dblclick', () => {
            if (!entry.is_dir) finish(entry.path);
          });
          list.append(row);
        }
      } catch (e) {
        if (request === generation) {
          error.textContent = e.message;
          choose.disabled = mode !== 'folder' || !knownFolder;
        }
      }
    }
    d.append(up, path, error, list, el('div', {class:'actions'}, [button('Cancel', () => d.close()), choose]));
    d.addEventListener('close', () => {
      generation++;
      resolve(null);
    }, {once:true});
    load(start);
  });
}
