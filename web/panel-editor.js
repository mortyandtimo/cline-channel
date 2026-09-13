import { api, channels, element, runButton, toast } from './panel-api.js';

const dialog = document.getElementById('channel-dialog');
const selected = document.getElementById('selected-channels');
let current;
let validationAbort;

export function openEditor(value) {
  current = value;
  const pin = value.pin || {};
  document.getElementById('editor-title').textContent = value.model.upstreamId.split('/').at(-1);
  document.getElementById('editor-model').textContent = value.model.id;
  document.getElementById('pin-mode').value = pin.mode || 'strict';
  document.getElementById('pin-sort').value = pin.sort || '';
  document.getElementById('excluded-channels').value = (pin.exclude || []).join(', ');
  selected.value = (pin.only?.length ? pin.only : pin.order || []).join(', ');
  document.getElementById('validation-results').replaceChildren();
  document.getElementById('save-status').textContent = '';
  renderChannels();
  if (!dialog.open) dialog.showModal();
}

function renderChannels() {
  const probe = current.probe;
  const available = probe?.ok ? probe.channels : current.pin?.channels || [];
  const chosen = channels(selected.value);
  const container = document.getElementById('channel-options');
  container.replaceChildren();
  for (const name of [...new Set([...available, ...chosen])]) {
    const label = element('label', null, 'channel-option');
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.checked = chosen.includes(name);
    input.setAttribute('aria-label', name);
    input.addEventListener('change', () => {
      const list = channels(selected.value).filter(value => value !== name);
      if (input.checked) list.push(name);
      selected.value = list.join(', ');
    });
    label.append(input, element('span', name));
    container.append(label);
  }
  const status = document.getElementById('probe-status');
  status.textContent = probe?.ok
    ? `已发现 ${available.length} 个渠道 · ${probe.pipeline === 'planner' ? 'Cline 规划器' : 'Cline 直连'} · 勾选后保存即可生效`
    : '探测后选择渠道；可以勾选多个，按下方顺序尝试。';
  if (!available.length && !chosen.length) container.append(element('span', '尚未探测渠道', 'muted'));
  document.getElementById('validate-channels').disabled = !probe?.ok;
}

function closeEditor() {
  validationAbort?.abort();
  dialog.close();
}
document.getElementById('close-editor').addEventListener('click', closeEditor);
document.getElementById('cancel-editor').addEventListener('click', closeEditor);
dialog.addEventListener('cancel', () => validationAbort?.abort());
selected.addEventListener('change', renderChannels);

document.getElementById('probe-editor').addEventListener('click', event => runButton(event.currentTarget, '探测中…', async () => {
  const editor = current;
  const result = await api('probe', { model: editor.model.id }, true);
  if (!result.ok) throw new Error(result.error);
  if (current !== editor) return;
  editor.probe = result;
  renderChannels();
}));

document.getElementById('save-channels').addEventListener('click', event => runButton(event.currentTarget, '保存中…', async () => {
  const mode = document.getElementById('pin-mode').value;
  const chosen = channels(selected.value);
  const values = {
    model: current.model.id, mode, sort: document.getElementById('pin-sort').value,
    style: current.probe?.style || current.pin?.style || 'auto',
    exclude: channels(document.getElementById('excluded-channels').value),
    [mode === 'strict' ? 'only' : 'order']: chosen,
  };
  const result = await api('pin', values, true);
  if (!result.ok) throw new Error(result.error);
  const onSaved = current.onSaved;
  closeEditor();
  toast('Cline 渠道设置已生效');
  await onSaved();
}));

document.getElementById('validate-channels').addEventListener('click', event => runButton(event.currentTarget, '校验中…', async () => {
  const editor = current;
  const controller = new AbortController();
  validationAbort = controller;
  const container = document.getElementById('validation-results');
  container.replaceChildren();
  for (const channel of editor.probe.channels) {
    if (controller.signal.aborted || current !== editor) break;
    const row = element('div', null, 'validation-row');
    const status = element('span', '正在测试…');
    row.append(element('code', channel), status);
    container.append(row);
    try {
      const result = await api('test', { model: editor.model.id, only: [channel], style: editor.probe.style }, true, controller.signal);
      status.textContent = result.verified ? `已验证 · ${(result.ms / 1000).toFixed(2)} s` : result.message || result.error;
      row.classList.add(result.verified ? 'ok' : 'bad');
    } catch (error) {
      status.textContent = controller.signal.aborted ? '已停止' : error.message;
      row.classList.add('bad');
    }
  }
  if (validationAbort === controller) validationAbort = null;
}));
