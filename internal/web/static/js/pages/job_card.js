import {call} from '../api.js';
import {el, button, clear, notice} from '../dom.js';
import {icon} from '../icons.js';
import {humanSize, humanSpeed, humanDuration} from '../format.js';

export function jobCard(job) {
  const labels = {
    running:'Downloading', queued:'Queued', done:'Done', skipped:'Skipped', failed:'Failed',
    canceled:'Canceled'
  };
  const name = el('strong', {class:'ellipsis'}), remote = el('div', {class:'ellipsis'});
  const badge = el('span', {class:'badge'}), fill = el('div', {class:'progress-fill'});
  const bar = el('div', {
    class:'progress', attrs:{role:'progressbar', 'aria-valuemin':0, 'aria-valuemax':100}
  }, [fill]);
  const detailsP = el('p'), errorP = el('p', {class:'error'}), actions = el('div', {class:'row wrap'});
  const node = el('article', {class:'card job'}, [
    el('div', {class:'row'}, [icon('file'), el('div', {class:'grow'}, [name, remote]), badge]),
    bar, detailsP, errorP, actions
  ]);
  node.jobId = job.id;
  let actionKey = null, current = job;

  function action(text, method, params) {
    actions.append(button(text, async () => {
      try {
        await call(method, params());
      } catch (e) {
        notice(e.message, true);
      }
    }, 'text-button'));
  }
  node.update = j => {
    node.setAttribute('data-test-id', j.id);
    node.setAttribute('data-test-state', j.state);
    current = j;
    const running = j.state === 'running', failed = j.state === 'failed', canceled = j.state === 'canceled';
    const progress = j.size === 0 ? 0 : Math.max(0, Math.min(1, j.done / j.size));
    name.textContent = j.local.replaceAll('\\', '/').split('/').pop();
    remote.textContent = `${j.server}:${j.remote}`;
    badge.textContent = labels[j.state] || j.state;
    badge.className = 'badge' + (failed || canceled ? ' error' : '');
    fill.style.setProperty('--progress', progress*100 + '%');
    bar.setAttribute('aria-valuenow', Math.floor(progress*100));
    const details = [`${Math.floor(progress*100)} %`, `${humanSize(j.done)} of ${humanSize(j.size)}`];
    if (running) {
      details.push(humanSpeed(j.speed), `${j.active_segments} of ${j.segments} threads`);
      if (j.speed > 0) details.push(humanDuration(Math.ceil(Math.max(0, j.size-j.done)/j.speed)) +
          ' left');
    }
    detailsP.textContent = details.join(' · ');
    errorP.textContent = failed ? j.error : '';
    errorP.hidden = !failed;
    const key = j.state + '|' + j.error_kind;
    if (key !== actionKey) {
      actionKey = key;
      clear(actions);
      const id = () => ({id:current.id});
      if (running || j.state === 'queued') action('Cancel', 'downloads.cancel',
        id);
      if (failed || canceled) action('Retry', 'downloads.retry', id);
      if (failed && j.error_kind === 'local_exists') action('Overwrite', 'downloads.overwrite',
        id);
      if (j.state === 'done') action('Show in folder', 'local.reveal', () => ({path:current.local}));
    }
  };
  node.update(job);
  return node;
}
