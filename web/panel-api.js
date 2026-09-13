const base = '/v0/resource/plugins/cline-channel';
const token = document.querySelector('meta[name="cline-panel-token"]').content;

export async function api(name, values = {}, write = false, signal) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    query.set(key, Array.isArray(value) ? value.join(',') : String(value ?? ''));
  }
  const response = await fetch(`${base}/${name}${query.size ? `?${query}` : ''}`, {
    cache: 'no-store', credentials: 'same-origin',
    headers: write ? { 'X-Cline-Panel-Token': token } : {},
    signal: signal || AbortSignal.timeout(60000),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
  return data;
}

// post 把载荷放进请求体。key 这类敏感值不能走 query，
// 否则会留在浏览器历史、代理日志和 CPA 的请求日志里。
export async function post(name, payload, signal) {
  const response = await fetch(`${base}/${name}`, {
    method: 'POST',
    cache: 'no-store', credentials: 'same-origin',
    headers: { 'X-Cline-Panel-Token': token, 'Content-Type': 'application/json' },
    body: JSON.stringify(payload ?? {}),
    signal: signal || AbortSignal.timeout(60000),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
  return data;
}

let toastTimer;
export function toast(message, error = false) {
  const box = document.getElementById('toast');
  box.textContent = message;
  box.className = `visible${error ? ' error' : ''}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { box.className = ''; }, error ? 7000 : 4000);
}

export function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text != null) node.textContent = text;
  if (className) node.className = className;
  return node;
}

export function channels(text) {
  return [...new Set(String(text || '').split(/[,，\n]/).map(value => value.trim()).filter(Boolean))];
}

let pendingActions = 0;
export function actionsPending() { return pendingActions > 0; }

export async function runButton(button, label, action) {
  pendingActions++;
  const previous = button.textContent;
  button.disabled = true;
  button.textContent = label;
  try { return await action(); }
  catch (error) { toast(error.message, true); }
  finally { pendingActions--; button.disabled = false; button.textContent = previous; }
}
