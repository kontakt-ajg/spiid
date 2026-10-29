import {el, button} from '../dom.js';
import {selectionSummary} from '../format.js';

export function selection() {
  const items = new Map();
  return {
    items,
    toggle(entry) {
      if (!items.delete(entry.path)) items.set(entry.path, entry);
    },
    clear() {
      items.clear();
    },
    card(onClear, download) {
      let files = 0, folders = 0, bytes = 0;
      for (const entry of items.values()) {
        if (entry.is_dir) folders++;
        else {
          files++;
          bytes += entry.size;
        }
      }
      const clear = button('Clear', onClear, 'text-button');
      clear.title = 'Clear';
      return el('div', {class:'card row selection'}, [
        el('span', {
          class:'grow ellipsis', text:selectionSummary(files, folders, bytes)
        }),
        clear, button('Download', download, 'primary')
      ]);
    }
  };
}
