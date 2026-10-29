import {call, events, authenticated} from './api.js';
import {el, notice} from './dom.js';
import {rail} from './rail.js';
import {prompts} from './prompts.js';
import {serversPage} from './pages/servers.js';
import {browsePage} from './pages/browse.js';
import {downloadsPage} from './pages/downloads.js';
import {settingsPage} from './pages/settings.js';
if (authenticated()) {
  const main = document.getElementById('main');
  let selected = -1, stopped = false, summaryTimer, lastSummaryError, polling = false, since = 0;

  const setTheme = theme => {
    document.documentElement.dataset.theme = theme;
  };
  const browse = browsePage();
  const navigation = rail(select, quit);
  const pages = [
    serversPage(name => {
      select(1);
      browse.open(name);
    }),
    browse, downloadsPage(), settingsPage(setTheme)
  ];
  for (const page of pages) {
    page.node.hidden = true;
    main.append(page.node);
  }


  function select(index) {
    if (!authenticated() || stopped || selected === index) return;
    if (selected >= 0) {
      pages[selected].onHide?.();
      pages[selected].node.hidden = true;
    }
    selected = index;
    navigation.select(index);
    pages[index].node.hidden = false;
    pages[index].onShow?.();
  }
  const closeEvents = events(prompts());


  async function quit() {
    try {
      await call('app.quit');
      stop();
      document.getElementById('dialogs').replaceChildren();
      main.replaceChildren(el('p', {class:'page', text:'Spiid has stopped. You can close this tab.'}));
      for (const b of document.querySelectorAll('#rail button')) b.disabled = true;
    } catch (e) {
      if (authenticated()) notice(e.message, true);
    }
  }


  function stop() {
    stopped = true;
    clearTimeout(summaryTimer);
    closeEvents();
    pages[selected]?.onHide?.();
  }


  async function summary() {
    if (stopped || !authenticated() || document.hidden || polling) return;
    polling = true;
    try {
      const state = await call('downloads.state', {since});
      navigation.count(state.summary.active+state.summary.queued);
      pages[2].update(state);
      since = state.next;
    } catch (e) {
      if (e.message !== lastSummaryError) {
        lastSummaryError = e.message;
        if (authenticated()) notice(e.message, true);
      }
    }
    finally {
      polling = false;
      if (!stopped && authenticated() && !document.hidden) summaryTimer = setTimeout(summary, 500);
    }
  }


  async function init() {
    try {
      setTheme((await call('settings.get')).theme);
    }
    catch (e) {
      if (authenticated()) notice(e.message, true);
    }
    select(0);
    summary();
  }
  document.addEventListener('visibilitychange', () => {
    clearTimeout(summaryTimer);
    if (!document.hidden) summary();
  });
  window.addEventListener('pagehide', stop);
  window.addEventListener('pageshow', e => {
    if (e.persisted) location.reload();
  });
  init();

}
