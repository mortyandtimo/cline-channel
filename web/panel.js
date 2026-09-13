import { actionsPending, api, element, post, runButton, toast } from './panel-api.js';
import { openEditor } from './panel-editor.js';

let state;
let loading = false;
const query = document.getElementById('model-search');
const sourceLabels = { 'cline-official': 'Cline 官方', cache: '最近成功列表', 'switcher-snapshot': '原版订阅快照', config: '指定模型列表' };
const keySourceLabels = { 'auth-file': '来自 CPA 凭据文件', config: '来自插件配置 api_key', accounts: '来自账号池 accounts', none: '尚未接入' };

async function refresh() {
  if (loading) return;
  loading = true;
  try {
    state = await api('status');
    render();
  } catch (error) {
    document.getElementById('connection').textContent = '连接中断';
    document.getElementById('connection').className = 'status-pill';
    showNotice(`读取 Cline 插件失败：${error.message}`);
  } finally { loading = false; }
}

function showNotice(message) {
  const notice = document.getElementById('notice');
  notice.hidden = !message;
  notice.textContent = message || '';
}

function render() {
  const catalog = state.catalog;
  document.getElementById('model-count').textContent = catalog.count;
  document.getElementById('model-source').textContent = sourceLabels[catalog.source] || catalog.source;
  document.getElementById('model-updated').textContent = catalog.fetchedAt?.startsWith('0001')
    ? '等待成功同步' : `更新于 ${new Date(catalog.fetchedAt).toLocaleString('zh-CN', { hour12: false })}`;
  document.getElementById('account-status').textContent = state.accountReady ? '已接入' : '尚未接入';
  document.getElementById('account-mode').textContent = state.accountMode === 'roundrobin' ? '轮询选择账号' : '使用当前 Cline 账号';
  document.getElementById('account-key').textContent = state.accountReady
    ? `Key ${state.accountMasked}` : '未设置 Key';
  const valid = new Set(catalog.entries.map(model => model.upstreamId));
  document.getElementById('pin-count').textContent = state.pins.filter(pin => valid.has(pin.model)).length;
  const connection = document.getElementById('connection');
  connection.textContent = '插件运行中';
  connection.className = 'status-pill ready';
  showNotice(catalog.stale ? '官方列表暂时无法更新，正在使用最近可用的订阅列表。' : catalog.error);
  renderModels();
  renderHistory();
}

function action(label, model, callback, extraClass = '') {
  const button = element('button', label, `small ${extraClass}`);
  button.setAttribute('aria-label', `${model.upstreamId} ${label}`);
  button.addEventListener('click', () => callback(button));
  return button;
}

function renderModels() {
  const container = document.getElementById('model-groups');
  container.replaceChildren();
  const models = state.catalog.entries.filter(model => `${model.id} ${model.name}`.toLowerCase().includes(query.value.toLowerCase().trim()));
  const groups = new Map();
  for (const model of models) {
    if (!groups.has(model.group)) groups.set(model.group, []);
    groups.get(model.group).push(model);
  }
  const pins = new Map(state.pins.map(pin => [pin.model, pin]));
  const observed = new Map(state.observed.map(route => [route.model, route]));
  for (const [name, entries] of groups) {
    const group = element('div', null, 'model-group');
    const heading = element('div', name, 'group-heading');
    heading.append(element('span', entries.length, 'count'), element('span', 'Cline 专属分组', 'group-label'));
    const wrap = element('div', null, 'table-wrap');
    const table = element('table');
    const head = element('thead');
    const headingRow = element('tr');
    for (const label of ['模型 / 请求名称', '渠道设置', '最近实际命中', '操作']) headingRow.append(element('th', label));
    head.append(headingRow);
    const body = element('tbody');
    for (const model of entries) body.append(modelRow(model, pins.get(model.upstreamId), observed.get(model.upstreamId)));
    table.append(head, body);
    wrap.append(table);
    group.append(heading, wrap);
    container.append(group);
  }
  if (!models.length) container.append(element('div', '没有匹配的 Cline 模型', 'empty'));
}

function modelRow(model, pin, route) {
  const row = element('tr');
  row.dataset.model = model.upstreamId;
  const name = element('td');
  name.append(element('div', model.upstreamId.split('/').at(-1), 'model-title'), element('code', model.id, 'model-id'));
  const chosen = pin?.only?.length ? pin.only : pin?.order || [];
  const strategy = element('td');
  strategy.append(element('div', chosen.join(' → ') || '自动选择', 'channel-value'));
  let detail = chosen.length ? (pin.mode === 'preferred' ? '优先 + 回退' : '严格钉住') : 'Cline 默认路由';
  if (pin?.exclude?.length) detail += ` · 排除 ${pin.exclude.length} 个`;
  strategy.append(element('div', detail, 'strategy'));
  const actual = element('td');
  actual.append(element('span', route?.provider || '等待请求', route ? '' : 'muted'));
  if (route && chosen.length) {
    const normalize = value => value.toLowerCase().replace(/[^a-z0-9]/g, '');
    const matches = chosen.some(channel => normalize(channel) === normalize(route.provider));
    actual.append(element('span', matches ? '命中所选' : '其他渠道', `badge ${matches ? 'ok' : 'warn'}`));
  }
  const actions = element('td', null, 'actions-cell');
  const buttons = element('div', null, 'actions');
  buttons.append(action('设置渠道', model, () => openEditor({ model, pin, probe: state.runtime.probes[model.upstreamId], onSaved: refresh })));
  buttons.append(action('探测', model, button => runButton(button, '探测中…', async () => {
    const result = await api('probe', { model: model.id }, true);
    if (!result.ok) throw new Error(result.error);
    toast(`${model.upstreamId.split('/').at(-1)}：发现 ${result.channels.length} 个渠道`);
    await refresh();
    openEditor({ model, pin, probe: result, onSaved: refresh });
  })));
  buttons.append(action('测试', model, button => runButton(button, '测试中…', async () => {
    const result = await api('test', { model: model.id }, true);
    toast(result.message || result.error, !result.ok || (result.target && !result.verified));
    await refresh();
  })));
  if (pin) buttons.append(action('清除', model, button => runButton(button, '清除中…', async () => {
    const result = await api('unpin', { model: model.id }, true);
    if (!result.ok) throw new Error(result.error);
    toast('已恢复 Cline 默认路由');
    await refresh();
  }), 'ghost'));
  actions.append(buttons);
  row.append(name, strategy, actual, actions);
  return row;
}

function renderHistory() {
  const body = document.getElementById('history-rows');
  body.replaceChildren();
  for (const item of (state.runtime.history || []).slice(0, 12)) {
    const row = element('tr');
    const values = [new Date(item.at).toLocaleTimeString('zh-CN', { hour12: false }), item.model,
      `${item.kind}${item.stream ? ' · 流式' : ''}`, `${item.target || '自动'} → ${item.provider || '未返回'}`,
      String(item.status), `${(item.ms / 1000).toFixed(2)} s / ${item.attempts} 次`];
    values.forEach((value, index) => row.append(element('td', value, index === 4 ? (item.status < 400 ? 'status-ok' : 'status-error') : '')));
    if (item.error) row.title = item.error;
    body.append(row);
  }
  if (!body.children.length) {
    const cell = element('td', '还没有请求记录。选择一个模型进行测试，或从客户端发起对话。', 'empty');
    cell.colSpan = 6;
    const row = element('tr');
    row.append(cell);
    body.append(row);
  }
}

query.addEventListener('input', () => state && renderModels());
document.getElementById('refresh-models').addEventListener('click', event => runButton(event.currentTarget, '同步中…', async () => {
  const result = await api('refresh-models', {}, true);
  toast(result.stale ? '官方接口暂时不可用，已保留最近列表' : `已同步 ${result.count} 个 Cline 模型`, result.stale);
  if (result.registrySynced === false) toast(result.registryError, true);
  await refresh();
}));
// ——— Cline API Key ———
// 页面只持有模糊化结果；完整 key 仅在保存时单向上行。
const keyDialog = document.getElementById('key-dialog');

async function loadKeyInfo() {
  const status = document.getElementById('key-status');
  status.textContent = '正在读取当前 Key…';
  try {
    const info = await api('key');
    document.getElementById('key-current').value = info.hasKey ? info.masked : '（尚未设置）';
    document.getElementById('key-source').textContent = keySourceLabels[info.source] || info.source || '';
    const file = (info.authFiles || []).join(', ');
    status.textContent = info.hasKey
      ? `已接入 · ${String(info.length)} 个字符${file ? ` · 写入 ${file}` : ''}`
      : '尚未接入 Cline 账号，请在下方填写 Key。';
  } catch (error) {
    document.getElementById('key-current').value = '（读取失败）';
    status.textContent = `读取当前 Key 失败：${error.message}`;
  }
}

document.getElementById('manage-key').addEventListener('click', () => {
  document.getElementById('key-input').value = '';
  document.getElementById('key-save-status').textContent = '';
  keyDialog.showModal();
  loadKeyInfo();
});
document.getElementById('close-key').addEventListener('click', () => keyDialog.close());
document.getElementById('cancel-key').addEventListener('click', () => keyDialog.close());
document.getElementById('save-key').addEventListener('click', event => runButton(event.currentTarget, '保存中…', async () => {
  const value = document.getElementById('key-input').value.trim();
  if (!value) { toast('请先填写新的 Key', true); return; }
  const result = await post('key-save', { api_key: value });
  if (!result.ok) throw new Error(result.error || '保存失败');
  document.getElementById('key-input').value = '';
  document.getElementById('key-save-status').textContent = `已更新为 ${result.masked}`;
  toast('Cline API Key 已更新，立即生效');
  await loadKeyInfo();
  await refresh();
}));

await refresh();
setInterval(() => {
  if (document.hidden || actionsPending()) return;
  if (document.getElementById('channel-dialog').open || keyDialog.open) return;
  refresh();
}, 15000);
