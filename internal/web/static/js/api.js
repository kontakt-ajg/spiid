let token = '';
let failed = false;
const streams = new Set();
export function authenticated() {
  return !failed && !!token;
}
export function loginRequired() {
  failed = true;
  for (const source of streams) source.close();
  document.body.textContent = 'Open the link printed in the terminal where Spiid is running.';
}
try {
  const fragment = new URLSearchParams(location.hash.slice(1));
  if (fragment.has('t')) {
    sessionStorage.setItem('spiid-token', fragment.get('t'));
    history.replaceState(null, '', '/');
  }
  token = sessionStorage.getItem('spiid-token') || '';
} catch { /* Storage unavailable: require the terminal link. */ }
if (!token) loginRequired();

async function post(path, body) {
  if (!authenticated()) throw {kind:'auth', message:'Open the terminal link.'};
  try {
    const response = await fetch(path, {
      method:'POST', headers:{'Content-Type':'application/json', Authorization:'Bearer ' + token},
      body:JSON.stringify(body)
    });
    if (response.status === 401) loginRequired();
    if (!response.ok) throw {kind:'http', message:await response.text()};
    return response;
  } catch (error) {
    throw {kind:error.kind || 'network', message:error.message || String(error)};
  }
}
export async function call(method, params = {}) {
  const response = await post('/api/call', {method, params});
  const payload = await response.json();
  if (payload.error) throw payload.error;
  return payload.result;
}
export async function answer(id, value) {
  await post('/api/answer', {id, answer:value});
}
export function events(onEvent) {
  let source, timer, closed = false;
  const open = () => {
    if (closed || !authenticated()) return;
    source = new EventSource('/api/events?t=' + encodeURIComponent(token));
    streams.add(source);
    source.onmessage = event => {
      try { onEvent(JSON.parse(event.data)); } catch (error) { console.error(error); }
    };
    source.onerror = async () => {
      source.close();
      streams.delete(source);
      // EventSource hides HTTP status; an authenticated call detects expired credentials.
      try { await call('app.info'); } catch { /* post handles unauthorized responses. */ }
      if (!closed && authenticated()) timer = setTimeout(open, 2000);
    };
  };
  open();
  return () => {
    closed = true;
    clearTimeout(timer);
    source?.close();
    streams.delete(source);
  };
}
