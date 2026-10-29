import {call} from '../api.js';
import {el, button, clear} from '../dom.js';
import {humanSpeed} from '../format.js';
import {virtualList} from '../virtual_list.js';
import {jobCard} from './job_card.js';

export function downloadsPage() {
  const node = el('section', {class:'page flex-page'});
  const scans = el('div');
  const summary = el('p'), error = el('p', {class:'error'}), messagesNode = el('div');
  const cards = new Map();
  // Scan rows are kept and updated in place, so a click on Cancel is never lost to a re-render.
  const scanRows = new Map();
  const list = virtualList(224, (row, job) => {
    let card = cards.get(job.id);
    if (!card) { card = jobCard(job); cards.set(job.id, card); }
    else card.update(job);
    if (row.firstElementChild !== card) { clear(row); row.append(card); }
  });
  node.append(el('h2', {text:'Downloads'}), button('Clear finished', async () => {
    try { await call('downloads.clear_finished'); } catch (e) { error.textContent = e.message; }
  }), summary, error, scans, messagesNode, list.node);
  let messages = [];

  function renderMessages() {
    clear(messagesNode);
    for (const message of messages) messagesNode.append(el('div', {class:'row'}, [
      el('span', {class:'grow', text:message.text}), button('Dismiss', () => {
        messages = messages.filter(m => m !== message);
        renderMessages();
      }, 'text-button')
    ]));
  }

  function updateScans(list) {
    const ids = new Set(list.map(scan => scan.id));
    for (const [id, row] of scanRows) {
      if (!ids.has(id)) {
        row.remove();
        scanRows.delete(id);
      }
    }
    for (const scan of list) {
      let row = scanRows.get(scan.id);
      if (!row) {
        const text = el('span', {class:'grow'});
        row = el('div', {class:'row'}, [text, button('Cancel', async () => {
          try {
            await call('downloads.cancel_scan', {id:scan.id});
          } catch (e) {
            error.textContent = e.message;
          }
        })]);
        row.text = text;
        scanRows.set(scan.id, row);
        scans.append(row);
      }
      row.text.textContent = `Scanning ${scan.folder} on ${scan.server}… ${scan.files} files found`;
    }
  }

  return {
    node,
    update(state) {
      const {jobs, summary:s} = state;
      jobs.sort((a,b) => b.added-a.added || b.id-a.id);
      summary.textContent = jobs.length
        ? `${humanSpeed(s.speed)} · ${s.active} active${s.queued ? ` · ${s.queued} queued` : ''}`
        : 'No downloads yet';
      const ids = new Set(jobs.map(j => j.id));
      for (const [id, card] of cards) if (!ids.has(id)) { card.remove(); cards.delete(id); }
      list.set(jobs);
      updateScans(state.scans);
      if (state.messages.length) {
        messages = [...messages, ...state.messages].slice(-500);
        renderMessages();
      }
      error.textContent = '';
    },
    onShow() { list.refresh(); }
  };
}
