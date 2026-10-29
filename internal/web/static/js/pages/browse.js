import {call} from '../api.js';
import {el, button, clear, notice} from '../dom.js';
import {humanSize, shortDate} from '../format.js';
import {icon} from '../icons.js';
import {virtualList} from '../virtual_list.js';
import {selection} from './selection.js';

export function browsePage() {
  const node = el('section', {class:'page flex-page'});
  const picker = el('select', {
    class:'grow', attrs:{'aria-label':'Select a server'}
  });
  const hint = el('p', {text:'Add a server first'}), crumbs = el('div', {class:'crumbs'});
  const loading = el('div', {
    class:'loading', attrs:{role:'status'}
  });
  const error = el('p', {class:'error'}), bottom = el('div');
  let server = '', listing = {path:'', entries:[]}, request = 0, generation = 0, serverRequest = 0;
  const selected = selection();
  const list = virtualList(40, (row, entry) => {
    if (row.entry === entry) {
      row.check.checked = selected.items.has(entry.path);
      return;
    }
    clear(row);
    const check = el('input', {
      attrs:{
        type:'checkbox', 'aria-label':`Select ${entry.name}`
      },
      on:{click:e => e.stopPropagation(), change:() => toggle(entry, check)}
    });
    check.checked = selected.items.has(entry.path);
    row.entry = entry;
    row.check = check;
    const contents = [check, icon(entry.is_dir ? 'folder' : 'file')];
    if (entry.is_link) contents.push(icon('link'));
    contents.push(el('span', {class:'grow ellipsis', text:entry.name}),
      el('span', {class:'size', text:entry.is_dir ? '' : humanSize(entry.size)}),
      el('span', {
        class:'date ellipsis', text:entry.mtime === 0 ? '' : shortDate(entry.mtime)
      }));
    row.append(el('div', {
      class:'browse-row', on:{click:() => entry.is_dir ? go(entry.path) : toggle(entry, check)}
    }, contents));
  });

  function toggle(entry, check) {
    selected.toggle(entry);
    check.checked = selected.items.has(entry.path);
    updateSelection();
  }

  function updateSelection() {
    clear(bottom);
    if (selected.items.size) bottom.append(selected.card(() => {
      selected.clear();
      updateSelection();
      list.refresh();
    }, download));
  }

  async function download() {
    const old = generation, name = server, paths = [...selected.items.keys()];
    try {
      await call('downloads.add', {server:name, paths, overwrite:false});
      if (old !== generation) return;
      selected.clear();
      updateSelection();
      list.refresh();
      notice('Added to Downloads');
    } catch (e) {
      if (old === generation) error.textContent = e.message;
    }
  }

  function navigation() {
    clear(crumbs);
    if (!server) return;
    crumbs.append(button('/', () => go('/'), 'text-button'));
    let accumulated = '';
    for (const part of listing.path.split('/').filter(Boolean)) {
      accumulated += '/' + part;
      const path = accumulated;
      crumbs.append(button(part, () => go(path), 'text-button'));
    }
  }

  async function go(dir) {
    if (!server) return;
    const current = ++request, name = server;
    loading.textContent = 'Loading…';
    error.textContent = '';
    try {
      const result = await call('browse.list', {server:name, dir});
      if (current !== request) return;
      listing = result;
      list.node.scrollTop = 0;
      list.set(listing.entries);
      navigation();
    } catch (e) {
      if (current === request) error.textContent = e.message;
    }
    finally {
      if (current === request) loading.textContent = '';
    }
  }

  function pick(name) {
    generation++;
    request++;
    server = name;
    picker.value = name;
    selected.clear();
    loading.textContent = '';
    error.textContent = '';
    listing = {path:'', entries:[]};
    list.set([]);
    updateSelection();
    navigation();
    up.disabled = refresh.disabled = !server;
    go('');
  }

  async function loadServers() {
    const current = ++serverRequest;
    try {
      const result = await call('servers.list');
      if (current !== serverRequest) return;
      clear(picker);
      picker.append(el('option', {
        text:'Select a server', attrs:{value:''}
      }));
      for (const s of result.servers) picker.append(el('option', {
        text:s.name, attrs:{value:s.name}
      }));
      picker.value = server;
      picker.hidden = !result.servers.length;
      hint.hidden = !!result.servers.length;
    } catch (e) {
      error.textContent = e.message;
    }
  }
  const up = button('Up', () => go(listing.path.replace(/\/[^/]+\/?$/, '') || '/'));
  const refresh = button('Refresh', () => go(listing.path));
  up.title = 'Up';
  refresh.title = 'Refresh';
  up.disabled = refresh.disabled = true;
  picker.addEventListener('change', () => pick(picker.value));
  node.append(el('div', {class:'row'}, [hint, picker, up, refresh]), crumbs, loading, error,
    el('div', {class:'row'}, [
      el('span', {class:'grow', text:'Name'}), el('span', {class:'size', text:'Size'}),
      el('span', {class:'date', text:'Modified'})
    ]),
    list.node, bottom);
  return {
    node, onShow:loadServers, open(name) {
      pick(name);
      loadServers();
    }
  };
}
