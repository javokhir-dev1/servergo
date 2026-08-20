'use strict';

/* ============ API ============
 * Server har bir /api/ so'rovida X-Token ni tekshiradi. Token oyna ochilganda
 * URL orqali keladi; uni o'qib olgach manzil satridan tozalaymiz. */
const TOKEN = new URLSearchParams(location.search).get('t') || '';
history.replaceState(null, '', location.pathname);

async function req(path, options = {}) {
  try {
    const res = await fetch(path, {
      ...options,
      headers: { 'X-Token': TOKEN, ...(options.headers || {}) },
    });
    const data = await res.json();
    return data && typeof data.ok === 'boolean' ? data : { ok: false, error: 'buzuq javob' };
  } catch (err) {
    return { ok: false, error: err.message };
  }
}

const apiGet = (path) => req(path);
const apiPost = (path, body) =>
  req(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });

const api = {
  list: () => apiGet('/api/pm2/list'),
  info: () => apiGet('/api/pm2/info'),
  logs: (id, lines) => apiGet(`/api/pm2/logs?id=${encodeURIComponent(id)}&lines=${lines}`),
  action: (type, id) => apiPost('/api/pm2/action', { type, id: Number(id) }),
  flush: (id) => apiPost('/api/pm2/flush', { id: Number(id) }),
  snapshot: (accurate) => apiGet(`/api/sys/snapshot?accurate=${accurate ? 1 : 0}`),
  kill: (pid) => apiPost('/api/sys/kill', { pid: Number(pid) }),
  openPath: (target) => apiPost('/api/open', { target }),

  // Tunnellar bo'limi
  tunSetup: () => apiGet('/api/tunnel/setup'),
  tunDetect: (path) => apiPost('/api/tunnel/detect', { path }),
  tunLogin: () => apiPost('/api/tunnel/login', {}),
  tunAddDomain: (domain) => apiPost('/api/tunnel/domain/add', { domain }),
  tunRemoveDomain: (domain) => apiPost('/api/tunnel/domain/remove', { domain }),
  tunSetDomain: (domain) => apiPost('/api/tunnel/domain/active', { domain }),
  tunProjects: () => apiGet('/api/tunnel/projects'),
  tunCreate: (input, force) => apiPost('/api/tunnel/project/create', { ...input, force: !!force }),
  tunUpdate: (id, input) => apiPost('/api/tunnel/project/update', { ...input, id }),
  tunAction: (type, id) => apiPost('/api/tunnel/project/action', { type, id }),
  tunDelete: (id) => apiPost('/api/tunnel/project/delete', { id }),
  tunLogs: (id) => apiGet(`/api/tunnel/project/logs?id=${encodeURIComponent(id)}`),
  tunDiagnose: (id) => apiPost('/api/tunnel/project/diagnose', { id }),
  tunFixDNS: (id) => apiPost('/api/tunnel/project/fixdns', { id }),
  tunCheckPort: (port) => apiGet(`/api/tunnel/checkport?port=${Number(port)}`),
  tunEvents: (since) => apiGet(`/api/tunnel/events?since=${Number(since) || 0}`),
  tunAppLogs: () => apiGet('/api/tunnel/applogs'),

  // Ilovalar bo'limi
  appsList: () => apiGet('/api/apps/list'),
  appsCreate: (input) => apiPost('/api/apps/create', input),
  appsUpdate: (id, input) => apiPost('/api/apps/update', { ...input, id }),
  appsAction: (type, id) => apiPost('/api/apps/action', { type, id }),
  appsDelete: (id) => apiPost('/api/apps/delete', { id }),
  appsLogs: (id) => apiGet(`/api/apps/logs?id=${encodeURIComponent(id)}`),
  appsEvents: (since) => apiGet(`/api/apps/events?since=${Number(since) || 0}`),

  // Bulut bo'limi
  authStatus: () => apiGet('/api/auth/status'),
  authLogin: (email, password) => apiPost('/api/auth/login', { email, password, deviceName: 'ServerGo GUI' }),
  authLogout: () => apiPost('/api/auth/logout', {}),
  syncPush: () => apiPost('/api/sync/push', {}),
  syncPull: () => apiPost('/api/sync/pull', {}),
};

/* ============ Holat ============ */

const state = {
  view: 'overview',
  procs: [],
  search: '',
  filter: 'all',
  sortKey: 'id',
  sortDir: 'asc',
  selectedId: null,
  tab: 'info',
  autoRefresh: true,
  interval: 2000,
  busy: false,
  lastError: null,
  // RAM bo'limi
  groups: [],
  ramSearch: '',
  ramAccurate: false,
  ramAccurateActive: false,
  ramError: null,
  // Tunnellar bo'limi
  tunSetup: null,
  tunProjects: [],
  tunSearch: '',
  tunSelectedId: null,
  tunTab: 'info',
  tunLogs: [],
  tunError: null,
  tunSeq: 0,
  tunEditingId: null, // null — yangi loyiha, aks holda tahrir
  tunPending: null,   // DNS band bo'lganda "almashtirish" uchun saqlangan forma
  // Ilovalar bo'limi
  appList: [],
  appSearch: '',
  appSelectedId: null,
  appTab: 'info',
  appLogs: [],
  appSeq: 0,
  appEditingId: null, // null — yangi ilova, aks holda tahrir
  // Bulut bo'limi
  cloudStatus: null,
  cloudError: null,
};

const $ = (id) => document.getElementById(id);

const el = {
  rows: $('rows'),
  empty: $('empty'),
  search: $('search'),
  filters: $('filters'),
  totals: $('totals'),
  daemonDot: $('daemon-dot'),
  daemonText: $('daemon-text'),
  host: $('host-metrics'),
  detail: $('detail'),
  dName: $('d-name'),
  dSub: $('d-sub'),
  tabInfo: $('tab-info'),
  outLog: $('out-log'),
  errLog: $('err-log'),
  errPath: $('err-path'),
  toast: $('toast'),
  autoRefresh: $('auto-refresh'),
  intervalSel: $('interval'),
  ramRows: $('ram-rows'),
  ramEmpty: $('ram-empty'),
  ramSearch: $('ram-search'),
  ramMeta: $('ram-meta'),
  ovPm2Dot: $('ov-pm2-dot'),
  ovPm2Sub: $('ov-pm2-sub'),
  ovPm2List: $('ov-pm2-list'),
  ovTunDot: $('ov-tun-dot'),
  ovTunSub: $('ov-tun-sub'),
  ovTunList: $('ov-tun-list'),
};

/* ============ Formatlash ============ */

function formatBytes(n) {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i <= 1 ? 0 : 1)} ${units[i]}`;
}

function formatUptime(startedAt) {
  if (!startedAt) return '—';
  let s = Math.floor((Date.now() - startedAt) / 1000);
  if (s < 0) s = 0;
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  // pm2 CLI dagi kabi d/h/m/s birliklari — kun/soat/daqiqa aralashib ketmasligi uchun.
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${sec}s`;
  return `${sec}s`;
}

function formatDate(ts) {
  if (!ts) return '—';
  return new Date(ts).toLocaleString('uz-UZ');
}

// hostFor — subdomen bo'sh bo'lsa (domenning o'zi uchun tunnel) faqat
// domenni, aks holda "subdomen.domen"ni qaytaradi. Go tarafdagi
// store.HostnameFor bilan bir xil mantiq.
function hostFor(subdomain, baseDomain) {
  return subdomain ? `${subdomain}.${baseDomain}` : baseDomain;
}

function statusLabel(s) {
  const map = {
    online: 'online',
    stopped: "to'xtagan",
    errored: 'xato',
    stopping: "to'xtayapti",
    launching: 'ishga tushyapti',
    'one-launch-status': 'bir martalik',
  };
  return map[s] || s;
}

function esc(str) {
  return String(str ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

/* ============ Toast ============ */

let toastTimer = null;
function toast(msg, kind = '') {
  el.toast.textContent = msg;
  el.toast.className = `toast ${kind}`;
  el.toast.hidden = false;
  clearTimeout(toastTimer);
  // Havola bo'lsa (masalan cloudflared login manzili) — o'qib, nusxalab
  // ulgurishi uchun ancha uzoqroq turadi.
  const hasLink = /https?:\/\//.test(msg);
  const ms = hasLink ? 45000 : kind === 'error' ? 6000 : 2600;
  toastTimer = setTimeout(() => {
    el.toast.hidden = true;
  }, ms);
}

/* ============ Tasdiqlash oynasi ============
 * Electron dagi native dialog o'rniga. webview'da window.confirm ishlashiga
 * tayanmaymiz va shu bilan birga ko'p satrli tafsilotni ham ko'rsata olamiz. */

let modalResolve = null;

function confirmDialog({ title, message, detail, confirmLabel }) {
  $('modal-title').textContent = title || 'Tasdiqlang';
  $('modal-message').textContent = message || '';
  const d = $('modal-detail');
  d.textContent = detail || '';
  d.hidden = !detail;
  $('modal-ok').textContent = confirmLabel || 'Ha';
  $('modal').hidden = false;
  $('modal-ok').focus();

  return new Promise((resolve) => {
    modalResolve = resolve;
  });
}

function closeModal(result) {
  $('modal').hidden = true;
  if (modalResolve) {
    modalResolve(result);
    modalResolve = null;
  }
}

$('modal-ok').addEventListener('click', () => closeModal(true));
$('modal-cancel').addEventListener('click', () => closeModal(false));
$('modal').addEventListener('click', (e) => {
  if (e.target === $('modal')) closeModal(false);
});

/* ============ Kichik kiritish oynasi ============
 * confirmDialog dan farqi: matn kiritiladi va tekshiruv xatosi oynani yopmaydi —
 * foydalanuvchi yozganini qaytadan terishga majbur bo'lmasligi uchun. */

// onSubmit(qiymat) -> xato matni (oyna ochiq qoladi) yoki null (yopiladi).
function promptDialog({ title, message, placeholder, value = '', okLabel, onSubmit }) {
  const inp = $('prompt-input');
  $('prompt-title').textContent = title || '';
  $('prompt-message').textContent = message || '';
  $('prompt-message').hidden = !message;
  inp.value = value;
  inp.placeholder = placeholder || '';
  $('prompt-note').hidden = true;
  $('prompt-ok').textContent = okLabel || 'Qo\'shish';
  $('prompt-ok').disabled = false;
  $('prompt-modal').hidden = false;
  inp.focus();
  inp.select();
  promptSubmit = onSubmit;
}

let promptSubmit = null;

function closePrompt() {
  $('prompt-modal').hidden = true;
  promptSubmit = null;
}

async function runPromptSubmit() {
  if (!promptSubmit) return;
  const note = $('prompt-note');
  $('prompt-ok').disabled = true;
  const err = await promptSubmit($('prompt-input').value.trim());
  $('prompt-ok').disabled = false;
  if (err) {
    note.textContent = err;
    note.className = 'form-note error';
    note.hidden = false;
    $('prompt-input').focus();
    return;
  }
  closePrompt();
}

$('prompt-ok').addEventListener('click', runPromptSubmit);
$('prompt-cancel').addEventListener('click', closePrompt);
$('prompt-modal').addEventListener('click', (e) => {
  if (e.target === $('prompt-modal')) closePrompt();
});

/* ============ Bo'lim almashtirish ============ */

function setView(view) {
  state.view = view;
  $('view-overview').hidden = view !== 'overview';
  $('view-pm2').hidden = view !== 'pm2';
  $('view-apps').hidden = view !== 'apps';
  $('view-ram').hidden = view !== 'ram';
  $('view-tunnel').hidden = view !== 'tunnel';
  $('view-cloud').hidden = view !== 'cloud';
  document.querySelectorAll('.nav-btn').forEach((b) => {
    b.classList.toggle('active', b.dataset.view === view);
  });
  refreshActive();
}

/* ============ PM2: ma'lumot ============ */

async function refresh() {
  const res = await api.list();
  if (!res.ok) {
    state.lastError = res.error;
    state.procs = [];
    render();
    return;
  }
  state.lastError = null;
  state.procs = res.data || [];
  render();
  if (state.selectedId !== null && state.tab !== 'info') loadLogs();
}

async function refreshInfo() {
  const res = await api.info();
  if (!res.ok) {
    el.daemonDot.className = 'dot off';
    el.daemonText.textContent = 'pm2 bilan bog\'lanib bo\'lmadi';
    return;
  }
  const data = res.data;
  const alive = data.daemon.alive;
  el.daemonDot.className = `dot ${alive ? 'on' : 'off'}`;
  el.daemonText.textContent = alive
    ? `pm2 daemon ishlayapti${data.pm2Version ? ` · v${data.pm2Version}` : ''}`
    : 'pm2 daemon ishlamayapti';
  renderHost(data.host);
}

function renderHost(h) {
  const memPct = h.memTotal ? Math.round((h.memUsed / h.memTotal) * 100) : 0;
  const temp = typeof h.cpuTempC === 'number'
    ? `<span>CPU harorat <b${h.cpuTempC >= 80 ? ' class="hot"' : ''}>${h.cpuTempC.toFixed(1)}°C</b></span>`
    : '';
  el.host.innerHTML =
    `<span>CPU yuk <b>${h.loadavg[0].toFixed(2)}</b> / ${h.cpus}</span>` +
    temp +
    `<span>RAM <b>${formatBytes(h.memUsed)}</b> / ${formatBytes(h.memTotal)} (${memPct}%)</span>`;
}

/* ============ PM2: render ============ */

function visibleProcs() {
  const q = state.search.trim().toLowerCase();
  let list = state.procs.filter((p) => {
    if (state.filter !== 'all' && p.status !== state.filter) return false;
    if (!q) return true;
    return (
      p.name.toLowerCase().includes(q) ||
      String(p.id) === q ||
      p.namespace.toLowerCase().includes(q)
    );
  });

  const dir = state.sortDir === 'asc' ? 1 : -1;
  const key = state.sortKey;
  list = list.slice().sort((a, b) => {
    let av = a[key];
    let bv = b[key];
    if (key === 'uptime') {
      av = av ? Date.now() - av : -1;
      bv = bv ? Date.now() - bv : -1;
    }
    if (typeof av === 'string') return av.localeCompare(bv) * dir;
    return ((av ?? 0) - (bv ?? 0)) * dir;
  });
  return list;
}

// CSP inline style atributini bloklaydi, shuning uchun kenglik render'dan keyin
// CSSOM orqali beriladi (applyBars).
function bar(pct, cls) {
  return `<span class="bar"><i class="${cls}" data-pct="${Math.min(100, pct).toFixed(1)}"></i></span>`;
}

function cpuBar(cpu) {
  const pct = Math.min(100, cpu);
  return bar(pct, pct >= 80 ? 'max' : pct >= 40 ? 'hot' : '');
}

function applyBars(root) {
  for (const i of root.querySelectorAll('.bar > i')) {
    i.style.width = `${i.dataset.pct}%`;
  }
}

function render() {
  const counts = { all: state.procs.length, online: 0, stopped: 0, errored: 0 };
  let totalCpu = 0;
  let totalMem = 0;
  for (const p of state.procs) {
    if (counts[p.status] !== undefined) counts[p.status]++;
    totalCpu += p.cpu;
    totalMem += p.memory;
  }
  $('c-all').textContent = counts.all;
  $('c-online').textContent = counts.online;
  $('c-stopped').textContent = counts.stopped;
  $('c-errored').textContent = counts.errored;
  el.totals.textContent = `Jami: CPU ${totalCpu.toFixed(1)}% · RAM ${formatBytes(totalMem)}`;

  const list = visibleProcs();

  el.rows.innerHTML = list
    .map((p) => {
      const sel = String(p.id) === String(state.selectedId) ? ' selected' : '';
      const canStart = p.status !== 'online' && p.status !== 'launching';
      return `<tr class="row${sel}" data-id="${p.id}">
        <td class="id">${p.id}</td>
        <td class="name" title="${esc(p.execPath)}">${esc(p.name)}${
          p.namespace && p.namespace !== 'default' ? `<span class="ns">${esc(p.namespace)}</span>` : ''
        }</td>
        <td><span class="status ${esc(p.status)}">${esc(statusLabel(p.status))}</span></td>
        <td class="num">${p.cpu.toFixed(1)}%${cpuBar(p.cpu)}</td>
        <td class="num">${formatBytes(p.memory)}</td>
        <td class="num dim">${formatUptime(p.uptime)}</td>
        <td class="num ${p.restarts > 0 ? '' : 'dim'}">${p.restarts}</td>
        <td class="num dim">${p.pid || '—'}</td>
        <td class="dim">${p.mode}${p.mode === 'cluster' ? ` ×${p.instances}` : ''}</td>
        <td class="actions">
          ${canStart
            ? `<button class="act start" data-act="start" data-id="${p.id}" title="Ishga tushirish">▶ Start</button>`
            : `<button class="act" data-act="restart" data-id="${p.id}" title="Qayta ishga tushirish">↻ Restart</button>
               <button class="act stop" data-act="stop" data-id="${p.id}" title="To'xtatish">■ Stop</button>`}
          <button class="act del" data-act="delete" data-id="${p.id}" title="PM2 ro'yxatidan o'chirish">🗑 O'chirish</button>
        </td>
      </tr>`;
    })
    .join('');

  applyBars(el.rows);

  if (list.length === 0) {
    el.empty.hidden = false;
    el.empty.textContent = state.lastError
      ? `PM2 bilan bog'lanib bo'lmadi.\n\n${state.lastError}`
      : state.procs.length === 0
        ? "PM2 da hech qanday jarayon yo'q.\n\nTerminalda 'pm2 start app.js' bilan jarayon qo'shing."
        : "Filtrga mos jarayon topilmadi.";
  } else {
    el.empty.hidden = true;
  }

  // Tanlangan jarayon o'chirilgan bo'lsa panelni yopamiz.
  if (state.selectedId !== null && !state.procs.some((p) => String(p.id) === String(state.selectedId))) {
    closeDetail();
  } else if (state.selectedId !== null) {
    renderDetail();
  }

  document.querySelectorAll('th.sortable').forEach((th) => {
    th.classList.toggle('sorted', th.dataset.key === state.sortKey);
    th.classList.toggle('asc', th.dataset.key === state.sortKey && state.sortDir === 'asc');
  });
}

function selectedProc() {
  return state.procs.find((p) => String(p.id) === String(state.selectedId)) || null;
}

function renderDetail() {
  const p = selectedProc();
  if (!p) return;
  el.dName.textContent = p.name;
  el.dSub.textContent = `ID ${p.id} · ${statusLabel(p.status)} · ${p.mode}${
    p.mode === 'cluster' ? ` ×${p.instances}` : ''
  }`;

  el.tabInfo.innerHTML = `
    <div class="section-title">Holat</div>
    <dl class="kv">
      <dt>Holat</dt><dd><span class="status ${esc(p.status)}">${esc(statusLabel(p.status))}</span></dd>
      <dt>PID</dt><dd>${p.pid || '—'}</dd>
      <dt>Uptime</dt><dd>${formatUptime(p.uptime)}</dd>
      <dt>CPU</dt><dd>${p.cpu.toFixed(1)}%</dd>
      <dt>Xotira</dt><dd>${formatBytes(p.memory)}</dd>
      <dt>Restartlar</dt><dd>${p.restarts}${p.unstableRestarts ? ` (beqaror: ${p.unstableRestarts})` : ''}</dd>
      <dt>Yaratilgan</dt><dd>${formatDate(p.createdAt)}</dd>
    </dl>

    <div class="section-title">Sozlama</div>
    <dl class="kv">
      <dt>Namespace</dt><dd>${esc(p.namespace)}</dd>
      <dt>Skript</dt><dd class="mono">${esc(p.execPath)}</dd>
      <dt>Papka</dt><dd class="mono">${esc(p.cwd)}</dd>
      ${p.args ? `<dt>Argumentlar</dt><dd class="mono">${esc(p.args)}</dd>` : ''}
      <dt>Interpretator</dt><dd class="mono">${esc(p.interpreter || '—')}</dd>
      ${p.nodeVersion ? `<dt>Node</dt><dd>v${esc(p.nodeVersion)}</dd>` : ''}
      <dt>Avto-restart</dt><dd>${p.autorestart ? 'yoqilgan' : "o'chirilgan"}</dd>
      <dt>Watch</dt><dd>${p.watching ? 'yoqilgan' : "o'chirilgan"}</dd>
      ${p.maxMemoryRestart ? `<dt>Max xotira</dt><dd>${esc(String(p.maxMemoryRestart))}</dd>` : ''}
    </dl>

    <div class="section-title">Log fayllari</div>
    <dl class="kv">
      <dt>stdout</dt><dd class="mono">${esc(p.outLog || '—')}</dd>
      <dt>stderr</dt><dd class="mono">${esc(p.errLog || '—')}</dd>
    </dl>`;
}

/* ============ PM2: loglar ============ */

let logsLoading = false;

async function loadLogs() {
  const p = selectedProc();
  if (!p || logsLoading) return;
  logsLoading = true;
  try {
    const res = await api.logs(p.id, 500);
    if (!res.ok) {
      el.outLog.textContent = res.error;
      return;
    }
    const follow = $('log-follow').checked;
    const atBottomOut = el.outLog.scrollHeight - el.outLog.scrollTop - el.outLog.clientHeight < 40;
    const atBottomErr = el.errLog.scrollHeight - el.errLog.scrollTop - el.errLog.clientHeight < 40;

    el.outLog.textContent = res.data.out.text || (res.data.out.exists ? "(log bo'sh)" : '(log fayli topilmadi)');
    el.errLog.textContent = res.data.err.text || (res.data.err.exists ? "(xatolar yo'q)" : '(log fayli topilmadi)');
    el.errPath.textContent = res.data.err.path || '';

    if (follow || atBottomOut) el.outLog.scrollTop = el.outLog.scrollHeight;
    if (follow || atBottomErr) el.errLog.scrollTop = el.errLog.scrollHeight;
  } finally {
    logsLoading = false;
  }
}

/* ============ PM2: amallar ============ */

const ACTION_META = {
  start: { label: 'ishga tushirildi', confirm: false },
  restart: { label: 'qayta ishga tushirildi', confirm: false },
  reload: { label: 'reload qilindi', confirm: false },
  stop: {
    label: "to'xtatildi",
    confirm: true,
    title: "Jarayonni to'xtatish",
    message: (p) => `"${p.name}" jarayonini to'xtatasizmi?`,
    detail: "Jarayon PM2 ro'yxatida qoladi, keyin qayta ishga tushirish mumkin.",
    ok: "To'xtatish",
  },
  delete: {
    label: "o'chirildi",
    confirm: true,
    title: "Jarayonni o'chirish",
    message: (p) => `"${p.name}" jarayonini PM2 ro'yxatidan o'chirasizmi?`,
    detail: "Jarayon to'xtatiladi va ro'yxatdan butunlay olib tashlanadi. Buni qaytarib bo'lmaydi — jarayonni qaytadan qo'lda qo'shishingiz kerak bo'ladi.",
    ok: "O'chirish",
  },
};

async function doAction(type, id) {
  const p = state.procs.find((x) => String(x.id) === String(id));
  if (!p) return;
  const meta = ACTION_META[type];

  if (meta.confirm) {
    const okd = await confirmDialog({
      title: meta.title,
      message: meta.message(p),
      detail: meta.detail,
      confirmLabel: meta.ok,
    });
    if (!okd) return;
  }

  state.busy = true;
  const res = await api.action(type, id);
  state.busy = false;
  if (!res.ok) {
    toast(`"${p.name}" — xato: ${res.error}`, 'error');
  } else {
    toast(`"${p.name}" ${meta.label}`, 'success');
  }
  await refresh();
}

async function bulk(type) {
  const list = visibleProcs();
  if (list.length === 0) {
    toast('Jarayon topilmadi');
    return;
  }
  const isStop = type === 'stop';
  const okd = await confirmDialog({
    title: isStop ? "Hammasini to'xtatish" : 'Hammasini restart qilish',
    message: `${list.length} ta jarayon ${isStop ? "to'xtatiladi" : 'qayta ishga tushiriladi'}.`,
    detail: list.map((p) => `• ${p.name} (id ${p.id})`).join('\n'),
    confirmLabel: isStop ? "To'xtatish" : 'Restart',
  });
  if (!okd) return;

  state.busy = true;
  let failed = 0;
  for (const p of list) {
    const res = await api.action(type, p.id);
    if (!res.ok) failed++;
  }
  state.busy = false;
  toast(
    failed
      ? `${list.length - failed} ta bajarildi, ${failed} tasida xato`
      : `${list.length} ta jarayon ${isStop ? "to'xtatildi" : 'restart qilindi'}`,
    failed ? 'error' : 'success'
  );
  await refresh();
}

/* ============ PM2: batafsil panel ============ */

function openDetail(id) {
  state.selectedId = id;
  el.detail.hidden = false;
  renderDetail();
  if (state.tab !== 'info') loadLogs();
  render();
}

function closeDetail() {
  state.selectedId = null;
  el.detail.hidden = true;
  document.querySelectorAll('tr.selected').forEach((tr) => tr.classList.remove('selected'));
}

function setTab(tab) {
  state.tab = tab;
  document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t.dataset.tab === tab));
  $('tab-info').hidden = tab !== 'info';
  $('tab-out').hidden = tab !== 'out';
  $('tab-err').hidden = tab !== 'err';
  if (tab !== 'info') loadLogs();
}

/* ============ RAM bo'limi ============ */

async function refreshRam() {
  const res = await api.snapshot(state.ramAccurate);
  if (!res.ok) {
    state.ramError = res.error;
    state.groups = [];
    renderRam();
    return;
  }
  state.ramError = null;
  state.groups = res.data.groups || [];
  state.ramAccurateActive = !!res.data.accurate;
  renderMem(res.data.mem);
  el.ramMeta.textContent =
    `${state.groups.length} ta ilova · ${res.data.took} · ` +
    (res.data.accurate ? 'PSS (aniq)' : 'RSS (tez)');
  renderRam();
}

function renderMem(m) {
  if (!m) return;
  const pct = (v) => (m.total ? (v / m.total) * 100 : 0);
  $('seg-used').style.width = `${pct(m.used).toFixed(2)}%`;
  $('seg-cache').style.width = `${pct(m.buffers + m.cached).toFixed(2)}%`;
  $('m-used').textContent = formatBytes(m.used);
  $('m-cache').textContent = formatBytes(m.buffers + m.cached);
  $('m-free').textContent = formatBytes(m.free);
  $('m-avail').textContent = formatBytes(m.available);
  $('m-swap').textContent = m.swapTotal
    ? `${formatBytes(m.swapUsed)} / ${formatBytes(m.swapTotal)}`
    : "yo'q";
  $('m-total').textContent = formatBytes(m.total);
}

// Aniq rejimda PSS, aks holda RSS.
function groupMem(g) {
  return state.ramAccurateActive && g.pss > 0 ? g.pss : g.rss;
}

function visibleGroups() {
  const q = state.ramSearch.trim().toLowerCase();
  if (!q) return state.groups;
  return state.groups.filter(
    (g) => g.name.toLowerCase().includes(q) || (g.exe || '').toLowerCase().includes(q)
  );
}

function renderRam() {
  const list = visibleGroups().slice(0, 80);

  el.ramRows.innerHTML = list
    .map((g) => {
      const tag = g.protected
        ? `<span class="tag">himoyalangan</span>`
        : g.warn
          ? `<span class="tag warn">${esc(g.warn)}</span>`
          : '';
      return `<tr data-pid="${g.rootPid}">
        <td class="app">
          <div class="app-name">${esc(g.name)}${tag}</div>
          <div class="app-path" title="${esc(g.exe)}">pid ${g.rootPid} · ${esc(g.user)} · ${esc(g.exe)}</div>
        </td>
        <td class="num dim">${g.count}</td>
        <td class="num">${formatBytes(groupMem(g))}</td>
        <td class="num dim">${g.percent.toFixed(1)}%${bar(g.percent, g.percent >= 20 ? 'hot' : '')}</td>
        <td class="actions">
          <button class="act del" data-kill="${g.rootPid}" ${g.protected ? 'disabled' : ''}
            title="Jarayon va uning barcha bolalarini to'xtatish">■ To'xtatish</button>
        </td>
      </tr>`;
    })
    .join('');

  applyBars(el.ramRows);

  if (list.length === 0) {
    el.ramEmpty.hidden = false;
    el.ramEmpty.textContent = state.ramError
      ? `Tizim ma'lumotini o'qib bo'lmadi.\n\n${state.ramError}`
      : 'Qidiruvga mos ilova topilmadi.';
  } else {
    el.ramEmpty.hidden = true;
  }
}

async function killGroup(pid) {
  const g = state.groups.find((x) => String(x.rootPid) === String(pid));
  if (!g || g.protected) return;

  const lines = [
    `Ildiz pid: ${g.rootPid}`,
    `Guruhdagi jarayonlar: ${g.count}`,
    `Xotira: ${formatBytes(groupMem(g))}`,
    `Yo'l: ${g.exe}`,
  ];
  if (g.warn) lines.push('', `⚠ ${g.warn}`);
  lines.push('', 'Bu jarayon va uning BARCHA bola jarayonlari yopiladi.');
  lines.push('Saqlanmagan ma\'lumotlar yo\'qoladi.');

  const okd = await confirmDialog({
    title: "Ilovani to'xtatish",
    message: `"${g.name}" daraxti to'xtatilsinmi?`,
    detail: lines.join('\n'),
    confirmLabel: "To'xtatish",
  });
  if (!okd) return;

  state.busy = true;
  const res = await api.kill(pid);
  state.busy = false;

  if (!res.ok) {
    toast(`To'xtatib bo'lmadi: ${res.error}`, 'error');
  } else {
    const d = res.data;
    const denied = d.denied || [];
    let msg = `${g.name}: ${d.total} ta jarayondan ${d.termed.length} tasi yopildi`;
    if (d.killed.length) msg += `, ${d.killed.length} tasi majburan (SIGKILL)`;
    if (denied.length) msg += `. ${denied.length} tasiga ruxsat berilmadi`;
    else if (d.survived.length) msg += `. Tirik qoldi: ${d.survived.join(', ')}`;
    // Sabab bo'lsa pid ro'yxatidan ko'ra muhimroq — u nima qilish kerakligini aytadi.
    if (d.reason) msg += `. ${d.reason}`;
    toast(msg, d.survived.length ? 'error' : 'success');
  }
  await refreshRam();
}

/* ============ Bulut bo'limi ============
 * Markaziy backend (api.servergo.uz) bilan login/logout va apps/tunnel
 * loyihalarini sync push/pull qilish. Tunnel "sozlash sehrgari" bilan bir
 * xil naqsh: box.dataset.key o'zgarmasa qayta chizilmaydi, aks holda
 * foydalanuvchi yozayotgan email/parol maydonlari har yangilanishda
 * (2 soniyada bir) tozalanib ketardi. */

async function refreshCloud() {
  const res = await api.authStatus();
  if (!res.ok) {
    state.cloudError = res.error;
    state.cloudStatus = null;
  } else {
    state.cloudError = null;
    state.cloudStatus = res.data;
  }
  renderCloud();
}

function cloudHTML(html, key) {
  const box = $('cloud-box');
  if (box.dataset.key === key) return;
  box.innerHTML = html;
  box.dataset.key = key;
}

function renderCloud() {
  if (state.cloudError) {
    cloudHTML(
      `<div class="setup-head"><h2>Bulut bo'limi ishga tushmadi</h2><p>${esc(state.cloudError)}</p></div>`,
      'err:' + state.cloudError);
    return;
  }

  const s = state.cloudStatus;
  if (!s || !s.loggedIn) {
    cloudHTML(`
      <div class="setup-head">
        <h2>Bulut sinxronizatsiyasi</h2>
        <p>Ilovalar va tunnel loyihalaringizni (Cloudflare tunnel maxfiy
           kalitlari bilan birga) hisobingizga bog'lab, boshqa PC'da qayta
           tiklashingiz mumkin — domenlar uchun qayta login qilmasdan.</p>
      </div>
      <div class="form narrow">
        <label class="field"><span>Email</span>
          <input type="email" id="cloud-email" class="search" placeholder="email@misol.com" autocomplete="username" />
        </label>
        <label class="field"><span>Parol</span>
          <input type="password" id="cloud-password" class="search" autocomplete="current-password" />
        </label>
        <div class="form-note" id="cloud-note" hidden></div>
        <div class="step-actions"><button class="btn solid" data-cloud="login">Kirish</button></div>
      </div>
    `, 'login-form');
    return;
  }

  cloudHTML(`
    <div class="setup-head">
      <h2>Bulut sinxronizatsiyasi</h2>
      <p>Kirilgan: <b>${esc(s.email)}</b> — ${esc(s.backendUrl)}</p>
    </div>
    <div class="step-actions">
      <button class="btn solid" data-cloud="push">Push — yuborish</button>
      <button class="btn solid" data-cloud="pull">Pull — olish</button>
      <button class="btn" data-cloud="logout">Chiqish</button>
    </div>
    <div class="form-note" id="cloud-result" hidden></div>
  `, 'account:' + s.email);
}

$('cloud-box').addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && e.target.id === 'cloud-password') {
    e.preventDefault();
    $('cloud-box').querySelector('[data-cloud="login"]')?.click();
  }
});

$('cloud-box').addEventListener('click', async (e) => {
  const btn = e.target.closest('[data-cloud]');
  if (!btn) return;
  const what = btn.dataset.cloud;

  if (what === 'login') {
    const note = $('cloud-note');
    const email = ($('cloud-email').value || '').trim();
    const password = $('cloud-password').value || '';
    if (!email || !password) {
      note.textContent = 'Email va parolni kiriting';
      note.className = 'form-note error';
      note.hidden = false;
      return;
    }
    btn.disabled = true;
    const res = await api.authLogin(email, password);
    btn.disabled = false;
    if (!res.ok) {
      note.textContent = res.error;
      note.className = 'form-note error';
      note.hidden = false;
      return;
    }
    toast(`Kirdingiz: ${res.data.email}`, 'success');
    await refreshCloud();
    return;
  }

  if (what === 'logout') {
    btn.disabled = true;
    const res = await api.authLogout();
    btn.disabled = false;
    if (!res.ok) { toast(res.error, 'error'); return; }
    toast('Chiqdingiz', 'success');
    await refreshCloud();
    return;
  }

  if (what === 'push' || what === 'pull') {
    document.querySelectorAll('[data-cloud]').forEach((b) => { b.disabled = true; });
    toast(what === 'push' ? 'Yuborilmoqda…' : 'Olinmoqda…');
    const res = what === 'push' ? await api.syncPush() : await api.syncPull();
    document.querySelectorAll('[data-cloud]').forEach((b) => { b.disabled = false; });

    const result = $('cloud-result');
    if (!res.ok) {
      toast(res.error, 'error');
      if (result) {
        result.textContent = res.error;
        result.className = 'form-note error';
        result.hidden = false;
      }
      return;
    }
    const d = res.data;
    const summary = `${d.apps} ilova, ${d.projects} loyiha, ${d.domains} domen sertifikati`;
    const label = what === 'push' ? 'Yuborildi' : 'Olindi';
    toast(`${label}: ${summary}`, 'success');
    if (result) {
      result.textContent = `Oxirgi ${what === 'push' ? 'push' : 'pull'}: ${summary}`;
      result.className = 'form-note';
      result.hidden = false;
    }
  }
});

/* ============ Hodisalar ============ */

$('nav').addEventListener('click', (e) => {
  const btn = e.target.closest('.nav-btn');
  if (btn) setView(btn.dataset.view);
});

el.rows.addEventListener('click', (e) => {
  const actBtn = e.target.closest('[data-act]');
  if (actBtn) {
    e.stopPropagation();
    doAction(actBtn.dataset.act, actBtn.dataset.id);
    return;
  }
  const row = e.target.closest('tr[data-id]');
  if (!row) return;
  if (String(state.selectedId) === row.dataset.id) closeDetail();
  else openDetail(row.dataset.id);
});

el.ramRows.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-kill]');
  if (btn && !btn.disabled) killGroup(btn.dataset.kill);
});

el.search.addEventListener('input', (e) => {
  state.search = e.target.value;
  render();
});

el.ramSearch.addEventListener('input', (e) => {
  state.ramSearch = e.target.value;
  renderRam();
});

$('ram-accurate').addEventListener('change', (e) => {
  state.ramAccurate = e.target.checked;
  refreshRam();
});

el.filters.addEventListener('click', (e) => {
  const chip = e.target.closest('.chip');
  if (!chip) return;
  state.filter = chip.dataset.status;
  document.querySelectorAll('.chip').forEach((c) => c.classList.toggle('active', c === chip));
  render();
});

document.querySelectorAll('th.sortable').forEach((th) => {
  th.addEventListener('click', () => {
    const key = th.dataset.key;
    if (state.sortKey === key) state.sortDir = state.sortDir === 'asc' ? 'desc' : 'asc';
    else {
      state.sortKey = key;
      state.sortDir = key === 'name' || key === 'status' ? 'asc' : 'desc';
    }
    render();
  });
});

document.querySelectorAll('.tab').forEach((t) => {
  t.addEventListener('click', () => setTab(t.dataset.tab));
});

$('detail-close').addEventListener('click', closeDetail);
$('refresh-btn').addEventListener('click', () => {
  refreshInfo();
  refreshActive();
});
$('restart-all').addEventListener('click', () => bulk('restart'));
$('stop-all').addEventListener('click', () => bulk('stop'));

$('log-flush').addEventListener('click', async () => {
  const p = selectedProc();
  if (!p) return;
  const okd = await confirmDialog({
    title: 'Loglarni tozalash',
    message: `"${p.name}" jarayonining log fayllari tozalansinmi?`,
    detail: 'stdout va stderr fayllari bo\'shatiladi.',
    confirmLabel: 'Tozalash',
  });
  if (!okd) return;
  const res = await api.flush(p.id);
  toast(res.ok ? 'Loglar tozalandi' : `Xato: ${res.error}`, res.ok ? 'success' : 'error');
  loadLogs();
});

$('log-open').addEventListener('click', () => {
  const p = selectedProc();
  if (p && p.outLog) api.openPath(p.outLog);
});
$('err-open').addEventListener('click', () => {
  const p = selectedProc();
  if (p && p.errLog) api.openPath(p.errLog);
});

el.autoRefresh.addEventListener('change', (e) => {
  state.autoRefresh = e.target.checked;
  if (state.autoRefresh) tick();
});

el.intervalSel.addEventListener('change', (e) => {
  state.interval = Number(e.target.value);
});

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    if (!$('prompt-modal').hidden) closePrompt();
    else if (!$('modal').hidden) closeModal(false);
    else if (!$('form-modal').hidden) closeProjectForm();
    else if (state.view === 'tunnel') closeTunDetail();
    else closeDetail();
    return;
  }
  // Enter — eng ustki ochiq oynani tasdiqlaydi (textarea yo'q, xavfsiz).
  if (e.key === 'Enter' && !$('prompt-modal').hidden) {
    e.preventDefault();
    runPromptSubmit();
    return;
  }
  if (e.key === 'Enter' && !$('modal').hidden) {
    closeModal(true);
    return;
  }
  if (e.key === 'Enter' && !$('form-modal').hidden) {
    e.preventDefault();
    saveProjectForm();
    return;
  }
  if (e.key === 'r' && e.ctrlKey) {
    e.preventDefault();
    refreshInfo();
    refreshActive();
    return;
  }
  if (e.key === '/' && !['INPUT', 'TEXTAREA'].includes(document.activeElement.tagName)) {
    e.preventDefault();
    (state.view === 'pm2' ? el.search : el.ramSearch).focus();
  }
});

/* ============ Yangilash sikli ============ */

// Faqat ochiq bo'limni so'raymiz — fonda turgan bo'lim uchun pm2 jlist yoki
// /proc ni bekorga o'qib o'tirmaymiz.
/* ============ Tunnellar: ma'lumot ============ */

const TUN_STATUS = {
  stopped: "to'xtagan",
  starting: 'ishga tushyapti',
  running: 'ishlamoqda',
  error: 'xato',
};

async function refreshTunnel() {
  const [setupRes, projRes] = await Promise.all([api.tunSetup(), api.tunProjects()]);

  if (!setupRes.ok) {
    state.tunError = setupRes.error;
    state.tunSetup = null;
  } else {
    state.tunError = null;
    state.tunSetup = setupRes.data;
  }
  state.tunProjects = projRes.ok ? projRes.data || [] : [];

  await pollTunEvents();
  renderTunnel();
  if (state.tunSelectedId && state.tunTab === 'logs') await loadTunLogs();
}

// Hodisalar: progress xabarlari va manager'ning maslahatlari (project_hint).
async function pollTunEvents() {
  const res = await api.tunEvents(state.tunSeq);
  if (!res.ok) return;
  const { events, seq } = res.data;
  const first = state.tunSeq === 0;
  state.tunSeq = seq;
  if (first) return; // birinchi so'rovda eski tarixni ko'rsatmaymiz
  for (const e of events) {
    if (e.name === 'progress' && typeof e.data === 'string') toast(e.data);
    else if (e.name === 'project_hint' && e.data && e.data.hint) toast(e.data.hint, 'error');
  }
}

/* ============ Tunnellar: render ============ */

function renderTunnel() {
  const s = state.tunSetup;
  const ready = !!(s && s.ready);

  $('tun-setup').hidden = ready;
  $('tun-layout').hidden = !ready;
  $('tun-new').disabled = !ready;
  $('tun-search').disabled = !ready;

  // Domen tanlagich
  const sel = $('tun-domain');
  const domains = (s && s.domains) || [];
  sel.hidden = domains.length === 0;
  // Birinchi domen sozlash sehrgari orqali qo'shiladi; bu tugma esa keyingilari
  // uchun — sehrgar yopilgach domen qo'shishning boshqa yo'li qolmasligi uchun.
  $('tun-add-domain').hidden = domains.length === 0;
  $('tun-remove-domain').hidden = domains.length === 0;
  if (domains.length) {
    const current = s.activeDomain;
    const want = domains.map((d) => `<option value="${esc(d)}"${d === current ? ' selected' : ''}>${esc(d)}</option>`).join('');
    if (sel.dataset.rendered !== want) {
      sel.innerHTML = want;
      sel.dataset.rendered = want;
    }
  }

  if (!ready) {
    renderTunSetup();
    $('tun-meta').textContent = '';
    return;
  }
  renderTunProjects();
}

// Panelni faqat holat haqiqatan o'zgarganda qayta quramiz. Aks holda har
// yangilanishda (2 soniyada bir) innerHTML almashib, foydalanuvchi yozayotgan
// <input> yo'q bo'lib ketadi.
function setupHTML(html, key) {
  const box = $('tun-setup');
  if (box.dataset.key === key) return;
  box.innerHTML = html;
  box.dataset.key = key;
}

function renderTunSetup() {
  const s = state.tunSetup;
  if (state.tunError || !s) {
    setupHTML(
      `<div class="setup-head"><h2>Tunnellar bo'limi ishga tushmadi</h2>
       <p>${esc(state.tunError || 'Noma\'lum xato')}</p></div>`,
      'err:' + (state.tunError || '?'));
    return;
  }
  if (s.fatalError) {
    setupHTML(
      `<div class="setup-head"><h2>Tunnellar bo'limi ishga tushmadi</h2><p>${esc(s.fatalError)}</p></div>`,
      'fatal:' + s.fatalError);
    return;
  }

  const cfDone = s.cloudflaredFound;
  const loginDone = s.loggedIn;
  const domainDone = !!s.activeDomain;

  const step = (n, done, active, title, desc, actions) => `
    <div class="step ${done ? 'done' : active ? 'active' : ''}">
      <div class="step-num">${done ? '✓' : n}</div>
      <div class="step-body">
        <div class="step-title">${title}</div>
        <div class="step-desc">${desc}</div>
        ${actions ? `<div class="step-actions">${actions}</div>` : ''}
      </div>
    </div>`;

  setupHTML(`
    <div class="setup-head">
      <h2>Tunnellarni sozlash</h2>
      <p>Lokal <code>localhost:PORT</code> serverlaringizni Cloudflare Tunnel orqali
         o'z domeningizning subdomenlari ostida internetga chiqaring.</p>
    </div>

    ${step(1, cfDone, !cfDone, 'cloudflared',
      cfDone
        ? `Topildi: <code>${esc(s.cloudflaredPath || 'PATH')}</code>${s.version ? ` · ${esc(s.version)}` : ''}`
        : `Topilmadi. Uni o'rnating, so'ng "Qayta tekshirish" bosing. Boshqa joyga o'rnatgan bo'lsangiz to'liq yo'lni kiriting.`,
      cfDone
        ? `<button class="btn small" data-tsetup="detect">Qayta tekshirish</button>`
        : `<input type="text" class="search" id="cf-path" placeholder="/usr/local/bin/cloudflared (ixtiyoriy)" />
           <button class="btn small solid" data-tsetup="detect">Qayta tekshirish</button>
           <button class="btn small" data-tsetup="docs">O'rnatish yo'riqnomasi</button>`)}

    ${step(2, loginDone, cfDone && !loginDone, 'Cloudflare bilan bog\'lanish',
      loginDone
        ? `Sertifikat mavjud: <code>~/.cloudflared/cert.pem</code>`
        : `Brauzer ochiladi — domeningizni tanlaysiz. Domen nameserver'lari Cloudflare'ga yo'naltirilgan bo'lishi shart.`,
      loginDone ? '' : `<button class="btn small solid" data-tsetup="login" ${cfDone ? '' : 'disabled'}>Bog'lanish</button>`)}

    ${step(3, domainDone, loginDone && !domainDone, 'Bazaviy domen',
      domainDone
        ? `Faol domen: <code>${esc(s.activeDomain)}</code>`
        : `Subdomenlar shu domen ostida yaratiladi (masalan <code>todo.javohir.uz</code>).`,
      `<input type="text" class="search" id="new-domain" placeholder="javohir.uz" />
       <button class="btn small solid" data-tsetup="domain" ${loginDone ? '' : 'disabled'}>Qo'shish</button>`)}
  `,
    // Qadamlar ko'rinishiga ta'sir qiladigan hamma narsa — shu kalitda.
    JSON.stringify([cfDone, loginDone, domainDone, s.cloudflaredPath, s.version, s.activeDomain]));
}

function visibleTunProjects() {
  const q = state.tunSearch.trim().toLowerCase();
  if (!q) return state.tunProjects;
  return state.tunProjects.filter(
    (p) => p.name.toLowerCase().includes(q) || p.subdomain.toLowerCase().includes(q)
  );
}

function renderTunProjects() {
  const list = visibleTunProjects();
  const running = state.tunProjects.filter((p) => p.status === 'running').length;
  $('tun-meta').textContent = `${state.tunProjects.length} ta loyiha · ${running} tasi ishlamoqda`;

  $('tun-rows').innerHTML = list
    .map((p) => {
      const sel = p.id === state.tunSelectedId ? ' selected' : '';
      const isRunning = p.status === 'running' || p.status === 'starting';
      return `<tr class="row${sel}" data-tid="${esc(p.id)}">
        <td class="name">${esc(p.name)}</td>
        <td class="url"><a href="#" data-topen="${esc(p.url)}">${esc(hostFor(p.subdomain, p.baseDomain))}</a></td>
        <td class="num dim">${p.port}</td>
        <td><span class="status ${esc(p.status)}">${esc(TUN_STATUS[p.status] || p.status)}</span></td>
        <td><span class="pill ${p.autostart ? 'on' : ''}">${p.autostart ? 'yoqilgan' : "o'chirilgan"}</span></td>
        <td class="actions">
          ${isRunning
            ? `<button class="act" data-tact="restart" data-tid="${esc(p.id)}" title="Qayta ishga tushirish">↻ Restart</button>
               <button class="act stop" data-tact="stop" data-tid="${esc(p.id)}" title="To'xtatish">■ Stop</button>`
            : `<button class="act start" data-tact="start" data-tid="${esc(p.id)}" title="Ishga tushirish">▶ Run</button>`}
          <button class="act" data-tact="edit" data-tid="${esc(p.id)}" title="Tahrirlash">✎ Tahrir</button>
          <button class="act del" data-tact="delete" data-tid="${esc(p.id)}" title="O'chirish">🗑 O'chirish</button>
        </td>
      </tr>`;
    })
    .join('');

  if (list.length === 0) {
    $('tun-empty').hidden = false;
    $('tun-empty').textContent = state.tunProjects.length === 0
      ? "Hali loyiha yo'q.\n\n\"+ Yangi loyiha\" tugmasi bilan birinchi tunnelni yarating."
      : 'Qidiruvga mos loyiha topilmadi.';
  } else {
    $('tun-empty').hidden = true;
  }

  if (state.tunSelectedId && !state.tunProjects.some((p) => p.id === state.tunSelectedId)) {
    closeTunDetail();
  } else if (state.tunSelectedId) {
    renderTunDetail();
  }
}

function selectedTunProject() {
  return state.tunProjects.find((p) => p.id === state.tunSelectedId) || null;
}

function renderTunDetail() {
  const p = selectedTunProject();
  if (!p) return;
  $('tun-d-name').textContent = p.name;
  $('tun-d-sub').textContent = `${TUN_STATUS[p.status] || p.status} · localhost:${p.port} → ${hostFor(p.subdomain, p.baseDomain)}`;

  $('ttab-info').innerHTML = `
    <div class="section-title">Holat</div>
    <dl class="kv">
      <dt>Holat</dt><dd><span class="status ${esc(p.status)}">${esc(TUN_STATUS[p.status] || p.status)}</span></dd>
      <dt>Manzil</dt><dd><a href="#" data-topen="${esc(p.url)}">${esc(p.url)}</a></dd>
      <dt>Lokal port</dt><dd>${p.port} (${esc(p.protocol)})</dd>
      <dt>Avtostart</dt><dd>${p.autostart ? 'yoqilgan' : "o'chirilgan"}</dd>
      ${p.lastError ? `<dt>Oxirgi xato</dt><dd>${esc(p.lastError)}</dd>` : ''}
      <dt>Yaratilgan</dt><dd>${formatDate(p.createdAt)}</dd>
    </dl>

    <div class="section-title">Tunnel</div>
    <dl class="kv">
      <dt>Nom</dt><dd class="mono">${esc(p.tunnelName)}</dd>
      <dt>UUID</dt><dd class="mono">${esc(p.tunnelId)}</dd>
    </dl>`;
}

async function loadTunLogs() {
  const p = selectedTunProject();
  if (!p) return;
  const res = await api.tunLogs(p.id);
  if (!res.ok) {
    $('tun-log').textContent = res.error;
    return;
  }
  state.tunLogs = res.data || [];
  const box = $('tun-log');
  const follow = $('tun-log-follow').checked;
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
  box.textContent = state.tunLogs.length ? state.tunLogs.join('\n') : "(log bo'sh — Run bosilgandan keyin to'ladi)";
  if (follow || atBottom) box.scrollTop = box.scrollHeight;
}

async function runDiagnose() {
  const p = selectedTunProject();
  if (!p) return;
  $('ttab-diag').innerHTML = '<div class="diag-summary">Tekshirilmoqda…</div>';
  const res = await api.tunDiagnose(p.id);
  if (!res.ok) {
    $('ttab-diag').innerHTML = `<div class="diag-summary">${esc(res.error)}</div>`;
    return;
  }
  const mark = { ok: '✓', warn: '!', fail: '✕' };
  $('ttab-diag').innerHTML = `
    <div class="diag-summary">${esc(res.data.summary)}</div>
    ${res.data.checks.map((c) => `
      <div class="check ${esc(c.status)}">
        <div class="check-mark">${mark[c.status] || '·'}</div>
        <div class="check-name">${esc(c.name)}</div>
        <div class="check-detail">${esc(c.detail)}</div>
      </div>`).join('')}
    <div class="step-actions">
      <button class="btn small" data-tdiag="rerun">Qayta tekshirish</button>
      ${res.data.canFix ? `<button class="btn small solid" data-tdiag="fix">DNS'ni tuzatish</button>` : ''}
    </div>`;
}

/* ============ Tunnellar: amallar ============ */

function openTunDetail(id) {
  state.tunSelectedId = id;
  $('tun-detail').hidden = false;
  renderTunDetail();
  if (state.tunTab === 'logs') loadTunLogs();
  if (state.tunTab === 'diag') runDiagnose();
  renderTunProjects();
}

function closeTunDetail() {
  state.tunSelectedId = null;
  $('tun-detail').hidden = true;
  document.querySelectorAll('#tun-rows tr.selected').forEach((tr) => tr.classList.remove('selected'));
}

function setTunTab(tab) {
  state.tunTab = tab;
  document.querySelectorAll('.tab[data-ttab]').forEach((t) => t.classList.toggle('active', t.dataset.ttab === tab));
  $('ttab-info').hidden = tab !== 'info';
  $('ttab-logs').hidden = tab !== 'logs';
  $('ttab-diag').hidden = tab !== 'diag';
  if (tab === 'logs') loadTunLogs();
  if (tab === 'diag') runDiagnose();
}

async function tunAct(type, id) {
  const p = state.tunProjects.find((x) => x.id === id);
  if (!p) return;

  if (type === 'delete') {
    const okd = await confirmDialog({
      title: "Loyihani o'chirish",
      message: `"${p.name}" loyihasi o'chirilsinmi?`,
      detail: `Tunnel Cloudflare tomonida ham o'chiriladi, DNS yozuvi va lokal fayllar tozalanadi.\n\n${p.url}\n\nBuni qaytarib bo'lmaydi.`,
      confirmLabel: "O'chirish",
    });
    if (!okd) return;
    state.busy = true;
    const res = await api.tunDelete(id);
    state.busy = false;
    if (!res.ok) toast(`Xato: ${res.error}`, 'error');
    else {
      toast(res.data.warning || `"${p.name}" o'chirildi`, res.data.warning ? 'error' : 'success');
      if (state.tunSelectedId === id) closeTunDetail();
    }
    await refreshTunnel();
    return;
  }

  if (type === 'edit') {
    openProjectForm(id);
    return;
  }

  state.busy = true;
  const res = await api.tunAction(type, id);
  state.busy = false;
  if (!res.ok) toast(`"${p.name}" — xato: ${res.error}`, 'error');
  else {
    const label = { start: 'ishga tushirildi', stop: "to'xtatildi", restart: 'qayta ishga tushirildi' }[type];
    toast(`"${p.name}" ${label}`, 'success');
  }
  await refreshTunnel();
}

/* ============ Tunnellar: loyiha formasi ============ */

function openProjectForm(id) {
  const s = state.tunSetup;
  if (!s || !s.ready) return;
  state.tunEditingId = id || null;
  state.tunPending = null;

  const p = id ? state.tunProjects.find((x) => x.id === id) : null;
  $('form-title').textContent = p ? 'Loyihani tahrirlash' : 'Yangi loyiha';
  $('form-save').textContent = p ? 'Saqlash' : 'Yaratish';
  $('f-name').value = p ? p.name : '';
  $('f-port').value = p ? p.port : '';
  $('f-sub').value = p ? p.subdomain : '';
  $('f-protocol').value = p ? p.protocol : 'http';
  $('f-autostart').checked = p ? p.autostart : false;

  const domains = s.domains || [];
  $('f-domain').innerHTML = domains
    .map((d) => `<option value="${esc(d)}"${(p ? p.baseDomain : s.activeDomain) === d ? ' selected' : ''}>${esc(d)}</option>`)
    .join('');

  setFormNote('');
  updateFormPreview();
  $('form-modal').hidden = false;
  $('f-name').focus();
}

function closeProjectForm() {
  $('form-modal').hidden = true;
  state.tunEditingId = null;
  state.tunPending = null;
}

function setFormNote(msg, kind = '') {
  const n = $('f-note');
  n.textContent = msg || '';
  n.className = `form-note ${kind}`;
  n.hidden = !msg;
}

function updateFormPreview() {
  let sub = $('f-sub').value.trim().toLowerCase();
  if (sub === '@') sub = '';
  const dom = $('f-domain').value;
  const port = $('f-port').value.trim();
  $('f-preview').textContent =
    dom ? `localhost:${port || '?'} → https://${hostFor(sub, dom)}` : '—';
}

function formInput() {
  return {
    name: $('f-name').value.trim(),
    port: Number($('f-port').value),
    subdomain: $('f-sub').value.trim().toLowerCase(),
    baseDomain: $('f-domain').value,
    protocol: $('f-protocol').value,
    autostart: $('f-autostart').checked,
  };
}

async function saveProjectForm() {
  const input = formInput();
  const id = state.tunEditingId;

  $('form-save').disabled = true;
  setFormNote(id ? 'Saqlanmoqda…' : 'Tunnel yaratilmoqda — bu bir necha soniya olishi mumkin…');

  const res = id
    ? await api.tunUpdate(id, input)
    : await api.tunCreate(input, state.tunPending === 'force');
  $('form-save').disabled = false;

  if (!res.ok) {
    // Server DNS band bo'lganda "DNS_EXISTS|matn" qaytaradi — almashtirishni taklif qilamiz.
    if (res.error.startsWith('DNS_EXISTS|')) {
      setFormNote(res.error.slice('DNS_EXISTS|'.length) +
        '\n\n"Yaratish" ni yana bosing — mavjud DNS yozuvi shu tunnelga almashtiriladi.');
      state.tunPending = 'force';
      return;
    }
    setFormNote(res.error, 'error');
    return;
  }

  toast(id ? 'Loyiha saqlandi' : `"${input.name}" yaratildi`, 'success');
  closeProjectForm();
  await refreshTunnel();

  // Port bo'sh bo'lsa foydalanuvchini ogohlantiramiz — tunnel ishlaydi, servis yo'q.
  const portRes = await api.tunCheckPort(input.port);
  if (portRes.ok && portRes.data === false) {
    toast(`Eslatma: localhost:${input.port} da hozir hech narsa javob bermayapti`, 'error');
  }
}

/* ============ Tunnellar: dastur loglari ============ */

async function openTunAppLogs() {
  const res = await api.tunAppLogs();
  if (!res.ok) {
    toast(`Loglarni olishda xato: ${res.error}`, 'error');
    return;
  }
  const lines = (res.data || [])
    .slice(-200)
    .map((e) => `${e.time} [${e.level}] ${e.msg}`)
    .join('\n');
  await confirmDialog({
    title: "Tunnellar bo'limi loglari",
    message: state.tunSetup && state.tunSetup.logDir ? `Fayllar: ${state.tunSetup.logDir}` : '',
    detail: lines || "Log yo'q",
    confirmLabel: 'Yopish',
  });
}

/* ============ Tunnellar: hodisalar ============ */

$('tun-rows').addEventListener('click', (e) => {
  const link = e.target.closest('[data-topen]');
  if (link) {
    e.preventDefault();
    api.openPath(link.dataset.topen);
    return;
  }
  const actBtn = e.target.closest('[data-tact]');
  if (actBtn) {
    e.stopPropagation();
    tunAct(actBtn.dataset.tact, actBtn.dataset.tid);
    return;
  }
  const row = e.target.closest('tr[data-tid]');
  if (!row) return;
  if (state.tunSelectedId === row.dataset.tid) closeTunDetail();
  else openTunDetail(row.dataset.tid);
});

$('tun-detail').addEventListener('click', (e) => {
  const link = e.target.closest('[data-topen]');
  if (link) {
    e.preventDefault();
    api.openPath(link.dataset.topen);
    return;
  }
  const diag = e.target.closest('[data-tdiag]');
  if (!diag) return;
  if (diag.dataset.tdiag === 'rerun') runDiagnose();
  else if (diag.dataset.tdiag === 'fix') fixTunDNS();
});

async function fixTunDNS() {
  const p = selectedTunProject();
  if (!p) return;
  const res = await api.tunFixDNS(p.id);
  if (!res.ok) {
    toast(`DNS tuzatilmadi: ${res.error}`, 'error');
    return;
  }
  toast('DNS yozuvi yangilandi — tarqalishi 1-2 daqiqa olishi mumkin', 'success');
  runDiagnose();
}

/* ============ Ilovalar bo'limi ============
 * pm2'ga bog'liq bo'lmagan, ServerGo'ning o'z jarayon boshqaruvchisi:
 * buyruq + ishchi papka, "Avtostart" bazada saqlanadi, qulab tushsa
 * o'zi qayta tiklaydi. Tunnellar bo'limi bilan bir xil naqsh. */

const APP_STATUS = {
  stopped: "to'xtagan",
  starting: 'ishga tushyapti',
  running: 'ishlamoqda',
  error: 'xato',
};

async function refreshApps() {
  const res = await api.appsList();
  state.appList = res.ok ? res.data || [] : [];
  if (!res.ok) toast(res.error, 'error');
  await pollAppEvents();
  renderApps();
  if (state.appSelectedId && state.appTab === 'logs') await loadAppLogs();
}

async function pollAppEvents() {
  const res = await api.appsEvents(state.appSeq);
  if (!res.ok) return;
  const { seq } = res.data;
  state.appSeq = seq;
}

function visibleApps() {
  const q = state.appSearch.trim().toLowerCase();
  if (!q) return state.appList;
  return state.appList.filter((a) => a.name.toLowerCase().includes(q));
}

function renderApps() {
  const list = visibleApps();
  const running = state.appList.filter((a) => a.status === 'running').length;
  $('app-meta').textContent = `${state.appList.length} ta ilova · ${running} tasi ishlamoqda`;

  $('app-rows').innerHTML = list
    .map((a) => {
      const sel = a.id === state.appSelectedId ? ' selected' : '';
      const isRunning = a.status === 'running' || a.status === 'starting';
      return `<tr class="row${sel}" data-aid="${esc(a.id)}">
        <td class="name">${esc(a.name)}</td>
        <td class="dim mono">${esc(a.command)}</td>
        <td><span class="status ${esc(a.status)}">${esc(APP_STATUS[a.status] || a.status)}</span></td>
        <td><span class="pill ${a.autostart ? 'on' : ''}">${a.autostart ? 'yoqilgan' : "o'chirilgan"}</span></td>
        <td class="actions">
          ${isRunning
            ? `<button class="act" data-aact="restart" data-aid="${esc(a.id)}" title="Qayta ishga tushirish">↻ Restart</button>
               <button class="act stop" data-aact="stop" data-aid="${esc(a.id)}" title="To'xtatish">■ Stop</button>`
            : `<button class="act start" data-aact="start" data-aid="${esc(a.id)}" title="Ishga tushirish">▶ Run</button>`}
          <button class="act" data-aact="edit" data-aid="${esc(a.id)}" title="Tahrirlash">✎ Tahrir</button>
          <button class="act del" data-aact="delete" data-aid="${esc(a.id)}" title="O'chirish">🗑 O'chirish</button>
        </td>
      </tr>`;
    })
    .join('');

  if (list.length === 0) {
    $('app-empty').hidden = false;
    $('app-empty').textContent = state.appList.length === 0
      ? "Hali ilova qo'shilmagan. \"+ Yangi ilova\" bilan boshlang — masalan \"node server.js\" yoki \"python3 bot.py\"."
      : 'Qidiruvga mos ilova topilmadi.';
  } else {
    $('app-empty').hidden = true;
  }

  if (state.appSelectedId && !state.appList.some((a) => a.id === state.appSelectedId)) {
    closeAppDetail();
  } else if (state.appSelectedId) {
    renderAppDetail();
  }
}

function selectedApp() {
  return state.appList.find((a) => a.id === state.appSelectedId) || null;
}

function renderAppDetail() {
  const a = selectedApp();
  if (!a) return;
  $('app-d-name').textContent = a.name;
  $('app-d-sub').textContent = `${APP_STATUS[a.status] || a.status} · ${a.command}`;

  $('atab-info').innerHTML = `
    <div class="section-title">Holat</div>
    <dl class="kv">
      <dt>Holat</dt><dd><span class="status ${esc(a.status)}">${esc(APP_STATUS[a.status] || a.status)}</span></dd>
      <dt>Buyruq</dt><dd class="mono">${esc(a.command)}</dd>
      <dt>Ishchi papka</dt><dd class="mono">${esc(a.cwd || '(uy papkasi)')}</dd>
      <dt>Avtostart</dt><dd>${a.autostart ? 'yoqilgan' : "o'chirilgan"}</dd>
      ${a.lastError ? `<dt>Oxirgi xato</dt><dd>${esc(a.lastError)}</dd>` : ''}
      <dt>Yaratilgan</dt><dd>${formatDate(a.createdAt)}</dd>
    </dl>`;
}

async function loadAppLogs() {
  const a = selectedApp();
  if (!a) return;
  const res = await api.appsLogs(a.id);
  if (!res.ok) {
    $('app-log').textContent = res.error;
    return;
  }
  state.appLogs = res.data || [];
  const box = $('app-log');
  const follow = $('app-log-follow').checked;
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
  box.textContent = state.appLogs.length ? state.appLogs.join('\n') : "(log bo'sh — Run bosilgandan keyin to'ladi)";
  if (follow || atBottom) box.scrollTop = box.scrollHeight;
}

function openAppDetail(id) {
  state.appSelectedId = id;
  $('app-detail').hidden = false;
  renderAppDetail();
  if (state.appTab === 'logs') loadAppLogs();
  renderApps();
}

function closeAppDetail() {
  state.appSelectedId = null;
  $('app-detail').hidden = true;
  document.querySelectorAll('#app-rows tr.selected').forEach((tr) => tr.classList.remove('selected'));
}

function setAppTab(tab) {
  state.appTab = tab;
  document.querySelectorAll('.tab[data-atab]').forEach((t) => t.classList.toggle('active', t.dataset.atab === tab));
  $('atab-info').hidden = tab !== 'info';
  $('atab-logs').hidden = tab !== 'logs';
  if (tab === 'logs') loadAppLogs();
}

async function appAct(type, id) {
  const a = state.appList.find((x) => x.id === id);
  if (!a) return;

  if (type === 'delete') {
    const okd = await confirmDialog({
      title: "Ilovani o'chirish",
      message: `"${a.name}" ilovasi o'chirilsinmi?`,
      detail: "Ishlab tursa avval to'xtatiladi. Loglar ham o'chadi. Buni qaytarib bo'lmaydi.",
      confirmLabel: "O'chirish",
    });
    if (!okd) return;
    state.busy = true;
    const res = await api.appsDelete(id);
    state.busy = false;
    if (!res.ok) toast(`Xato: ${res.error}`, 'error');
    else {
      toast(`"${a.name}" o'chirildi`, 'success');
      if (state.appSelectedId === id) closeAppDetail();
    }
    await refreshApps();
    return;
  }

  if (type === 'edit') {
    openAppForm(id);
    return;
  }

  state.busy = true;
  const res = await api.appsAction(type, id);
  state.busy = false;
  if (!res.ok) toast(`"${a.name}" — xato: ${res.error}`, 'error');
  else {
    const label = { start: 'ishga tushirildi', stop: "to'xtatildi", restart: 'qayta ishga tushirildi' }[type];
    toast(`"${a.name}" ${label}`, 'success');
  }
  await refreshApps();
}

/* ============ Ilovalar: forma ============ */

function openAppForm(id) {
  state.appEditingId = id || null;
  const a = id ? state.appList.find((x) => x.id === id) : null;
  $('app-form-title').textContent = a ? 'Ilovani tahrirlash' : 'Yangi ilova';
  $('app-form-save').textContent = a ? 'Saqlash' : 'Yaratish';
  $('af-name').value = a ? a.name : '';
  $('af-command').value = a ? a.command : '';
  $('af-cwd').value = a ? a.cwd : '';
  $('af-autostart').checked = a ? a.autostart : false;

  setAppFormNote('');
  $('app-form-modal').hidden = false;
  $('af-name').focus();
}

function closeAppForm() {
  $('app-form-modal').hidden = true;
  state.appEditingId = null;
}

function setAppFormNote(msg, kind = '') {
  const n = $('af-note');
  n.textContent = msg || '';
  n.className = `form-note ${kind}`;
  n.hidden = !msg;
}

function appFormInput() {
  return {
    name: $('af-name').value.trim(),
    command: $('af-command').value.trim(),
    cwd: $('af-cwd').value.trim(),
    autostart: $('af-autostart').checked,
  };
}

async function saveAppForm() {
  const input = appFormInput();
  const id = state.appEditingId;

  $('app-form-save').disabled = true;
  setAppFormNote(id ? 'Saqlanmoqda…' : 'Yaratilmoqda…');

  const res = id ? await api.appsUpdate(id, input) : await api.appsCreate(input);
  $('app-form-save').disabled = false;

  if (!res.ok) {
    setAppFormNote(res.error, 'error');
    return;
  }

  toast(id ? 'Ilova saqlandi' : `"${input.name}" yaratildi`, 'success');
  closeAppForm();
  await refreshApps();
}

$('app-rows').addEventListener('click', (e) => {
  const actBtn = e.target.closest('[data-aact]');
  if (actBtn) {
    e.stopPropagation();
    appAct(actBtn.dataset.aact, actBtn.dataset.aid);
    return;
  }
  const row = e.target.closest('tr[data-aid]');
  if (!row) return;
  if (state.appSelectedId === row.dataset.aid) closeAppDetail();
  else openAppDetail(row.dataset.aid);
});

$('app-search').addEventListener('input', (e) => {
  state.appSearch = e.target.value;
  renderApps();
});

$('app-new').addEventListener('click', () => openAppForm(null));
$('app-detail-close').addEventListener('click', closeAppDetail);
document.querySelectorAll('.tab[data-atab]').forEach((t) => {
  t.addEventListener('click', () => setAppTab(t.dataset.atab));
});

$('app-form-cancel').addEventListener('click', closeAppForm);
$('app-form-save').addEventListener('click', saveAppForm);
$('app-form-modal').addEventListener('click', (e) => {
  if (e.target === $('app-form-modal')) closeAppForm();
});

$('tun-setup').addEventListener('click', async (e) => {
  const btn = e.target.closest('[data-tsetup]');
  if (!btn) return;
  const what = btn.dataset.tsetup;

  if (what === 'docs') {
    api.openPath('https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/');
    return;
  }

  btn.disabled = true;
  try {
    if (what === 'detect') {
      const pathEl = $('cf-path');
      const res = await api.tunDetect(pathEl ? pathEl.value.trim() : '');
      if (!res.ok) toast(res.error, 'error');
      else toast('cloudflared topildi', 'success');
    } else if (what === 'login') {
      toast('Brauzer ochilmoqda — domeningizni tanlang…');
      const res = await api.tunLogin();
      if (!res.ok) toast(res.error, 'error');
      else toast('Cloudflare bilan bog\'landi', 'success');
    } else if (what === 'domain') {
      const el = $('new-domain');
      const res = await api.tunAddDomain(el ? el.value.trim() : '');
      if (!res.ok) toast(res.error, 'error');
      else toast('Domen qo\'shildi', 'success');
    }
  } finally {
    btn.disabled = false;
    await refreshTunnel();
  }
});

$('tun-domain').addEventListener('change', async (e) => {
  const res = await api.tunSetDomain(e.target.value);
  if (!res.ok) toast(res.error, 'error');
  await refreshTunnel();
});

$('tun-search').addEventListener('input', (e) => {
  state.tunSearch = e.target.value;
  renderTunProjects();
});

$('tun-add-domain').addEventListener('click', () => {
  promptDialog({
    title: 'Yangi bazaviy domen',
    message: "Subdomenlar shu domen ostida yaratiladi. Domen nameserver'lari Cloudflare'ga yo'naltirilgan bo'lishi shart. " +
      "Bu — hisobingizdagi qo'shimcha domen bo'lsa, brauzer ochiladi: ochilgan sahifada aynan shu domenni tanlang " +
      "(cloudflared har bir domen uchun alohida ruxsat talab qiladi).",
    placeholder: 'javohir.uz',
    okLabel: "Qo'shish",
    onSubmit: async (val) => {
      if (!val) return 'Domenni kiriting';
      toast(`Tekshirilmoqda… kerak bo'lsa brauzer ochiladi, "${val}"ni tanlang`);
      const res = await api.tunAddDomain(val);
      if (!res.ok) return res.error;
      toast(`"${val}" qo'shildi va faol domen qilib tanlandi`, 'success');
      await refreshTunnel();
      return null;
    },
  });
});

$('tun-remove-domain').addEventListener('click', async () => {
  const s = state.tunSetup;
  const domain = s && s.activeDomain;
  if (!domain) return;
  const okd = await confirmDialog({
    title: 'Domenni o\'chirish',
    message: `"${domain}" ro'yxatdan o'chirilsinmi?`,
    detail: "Cloudflare akkauntingizga tegmaydi — bu faqat lokal ro'yxat. Domen biror loyihada ishlatilayotgan bo'lsa o'chirilmaydi.",
    confirmLabel: "O'chirish",
  });
  if (!okd) return;
  const res = await api.tunRemoveDomain(domain);
  if (!res.ok) toast(res.error, 'error');
  else toast(`"${domain}" ro'yxatdan o'chirildi`, 'success');
  await refreshTunnel();
});

$('tun-new').addEventListener('click', () => openProjectForm(null));
$('tun-applogs').addEventListener('click', openTunAppLogs);
$('tun-detail-close').addEventListener('click', closeTunDetail);
document.querySelectorAll('.tab[data-ttab]').forEach((t) => {
  t.addEventListener('click', () => setTunTab(t.dataset.ttab));
});
$('tun-log-open').addEventListener('click', () => {
  const s = state.tunSetup;
  const p = selectedTunProject();
  if (s && s.logDir && p) api.openPath(`${s.logDir}/${p.id}.log`);
});

$('form-cancel').addEventListener('click', closeProjectForm);
$('form-save').addEventListener('click', saveProjectForm);
$('form-modal').addEventListener('click', (e) => {
  if (e.target === $('form-modal')) closeProjectForm();
});
['f-sub', 'f-port'].forEach((id) => $(id).addEventListener('input', updateFormPreview));
$('f-domain').addEventListener('change', updateFormPreview);
// Subdomen o'zgarsa avvalgi "force" tasdig'i eskiradi.
$('f-sub').addEventListener('input', () => {
  state.tunPending = null;
});

function refreshActive() {
  if (state.view === 'overview') return refreshOverview();
  if (state.view === 'pm2') return refresh();
  if (state.view === 'apps') return refreshApps();
  if (state.view === 'ram') return refreshRam();
  if (state.view === 'cloud') return refreshCloud();
  return refreshTunnel();
}

/* ============ Umumiy: kuzatish paneli ============
 * Chapda — jarayonlar va tunnellar ro'yxati. O'ngda — RAM/CPU/harorat/disk
 * gauge diagrammalari. Faqat ko'rish uchun — hech qanday amal tugmasi yo'q. */

async function refreshOverview() {
  const [procsRes, setupRes, infoRes] = await Promise.all([api.list(), api.tunSetup(), api.info()]);
  let tunProjects = [];
  if (setupRes.ok && setupRes.data && setupRes.data.ready) {
    const pRes = await api.tunProjects();
    if (pRes.ok) tunProjects = pRes.data;
  }
  renderOverview(procsRes, setupRes, tunProjects, infoRes);
}

function setDot(el2, cls, text) {
  el2.className = `status ${cls}`;
  el2.textContent = text;
}

function renderOverview(procsRes, setupRes, tunProjects, infoRes) {
  // Chap ustun — Jarayonlar (pm2)
  if (!procsRes.ok) {
    setDot(el.ovPm2Dot, 'errored', 'Xato');
    el.ovPm2Sub.textContent = procsRes.error;
    el.ovPm2List.innerHTML = '';
    $('ov-pm2-empty').hidden = true;
  } else {
    const procs = procsRes.data || [];
    const bad = procs.filter((p) => p.status !== 'online').length;
    setDot(el.ovPm2Dot, bad ? 'errored' : procs.length ? 'online' : 'stopped', bad ? 'Diqqat' : 'OK');
    el.ovPm2Sub.textContent = procs.length ? `${procs.length - bad} / ${procs.length} online` : '';
    el.ovPm2List.innerHTML = procs.map((p) => `<tr>` +
      `<td class="name"><span class="status ${esc(p.status)}"></span>${esc(p.name)}</td>` +
      `<td>${esc(statusLabel(p.status))}</td>` +
      `<td class="num">${p.cpu.toFixed(0)}%</td>` +
      `<td class="num">${formatBytes(p.memory)}</td></tr>`
    ).join('');
    $('ov-pm2-empty').hidden = procs.length !== 0;
  }

  // Chap ustun — Tunnellar
  if (!setupRes.ok) {
    setDot(el.ovTunDot, 'errored', 'Xato');
    el.ovTunSub.textContent = setupRes.error;
    el.ovTunList.innerHTML = '';
    $('ov-tun-empty').hidden = true;
  } else if (!setupRes.data.ready) {
    setDot(el.ovTunDot, 'stopped', 'Sozlanmagan');
    el.ovTunSub.textContent = "cloudflared/login/domen kutilmoqda";
    el.ovTunList.innerHTML = '';
    $('ov-tun-empty').hidden = true;
  } else {
    const s = setupRes.data;
    const errored = tunProjects.filter((p) => p.lastError).length;
    setDot(el.ovTunDot, errored ? 'errored' : 'online', errored ? 'Diqqat' : 'OK');
    el.ovTunSub.textContent = `faol domen: ${s.activeDomain}`;
    el.ovTunList.innerHTML = tunProjects.map((p) => `<tr>` +
      `<td class="name"><span class="status ${esc(p.status)}"></span>${esc(p.name)}</td>` +
      `<td>${esc(TUN_STATUS[p.status] || p.status)}</td>` +
      `<td class="dim">${esc(hostFor(p.subdomain, p.baseDomain))}</td></tr>`
    ).join('');
    $('ov-tun-empty').hidden = tunProjects.length !== 0;
  }

  // O'ng ustun — gauge diagrammalar
  if (!infoRes.ok) return;
  const h = infoRes.data.host || {};

  const memPct = h.memTotal ? (h.memUsed / h.memTotal) * 100 : 0;
  setGauge('g-ram', memPct, `${Math.round(memPct)}%`, `${formatBytes(h.memUsed)} / ${formatBytes(h.memTotal)}`);

  const cpuPct = h.cpus ? (h.loadavg[0] / h.cpus) * 100 : 0;
  setGauge('g-cpu', cpuPct, h.loadavg[0].toFixed(2), `${h.cpus} yadro`);

  if (typeof h.cpuTempC === 'number') {
    const tempPct = (h.cpuTempC / 100) * 100;
    setGauge('g-temp', tempPct, `${h.cpuTempC.toFixed(0)}°C`, '', 60, 80);
    $('g-temp-card').hidden = false;
  } else {
    $('g-temp-card').hidden = true;
  }

  const diskPct = h.diskTotal ? (h.diskUsed / h.diskTotal) * 100 : 0;
  setGauge('g-disk', diskPct, `${Math.round(diskPct)}%`, `${formatBytes(h.diskUsed)} / ${formatBytes(h.diskTotal)}`);
}

const GAUGE_R = 52;
const GAUGE_CIRC = 2 * Math.PI * GAUGE_R;

// setGauge — id prefiksi (masalan "g-ram") bo'yicha doiraviy diagrammani
// to'ldiradi. pct — 0-100 (undan oshsa 100 da to'yinadi). warnAt/critAt —
// rang o'zgaradigan chegaralar (standart 70/90%).
function setGauge(idPrefix, pct, valueText, subText, warnAt = 70, critAt = 90) {
  const fg = $(`${idPrefix}-fg`);
  const clamped = Math.max(0, Math.min(100, pct));
  fg.style.strokeDasharray = `${GAUGE_CIRC}`;
  fg.style.strokeDashoffset = `${GAUGE_CIRC * (1 - clamped / 100)}`;
  fg.classList.toggle('warn', pct >= warnAt && pct < critAt);
  fg.classList.toggle('crit', pct >= critAt);
  $(`${idPrefix}-value`).textContent = valueText;
  $(`${idPrefix}-sub`).textContent = subText || '';
}

let timer = null;
async function tick() {
  clearTimeout(timer);
  if (!state.autoRefresh) return;
  if (!state.busy && !document.hidden) await refreshActive();
  timer = setTimeout(tick, state.interval);
}

// Sof yangi o'rnatish (hali kirilmagan VA lokal apps/loyihalar bo'sh) —
// foydalanuvchi "Bulut" tugmasini o'zi qidirib topmasin, to'g'ridan-to'g'ri
// login formasidan boshlaymiz. Mavjud (allaqachon ma'lumoti bor yoki
// kirilgan) o'rnatishlarga tegilmaydi — ular odatdagidek "Umumiy"dan boshlanadi.
async function decideInitialView() {
  const statusRes = await api.authStatus();
  state.cloudStatus = statusRes.ok ? statusRes.data : null;
  state.cloudError = statusRes.ok ? null : statusRes.error;
  if (state.cloudStatus && state.cloudStatus.loggedIn) return;

  const [appsRes, projRes] = await Promise.all([api.appsList(), api.tunProjects()]);
  const noApps = appsRes.ok && Array.isArray(appsRes.data) && appsRes.data.length === 0;
  const noProjects = !projRes.ok || (Array.isArray(projRes.data) && projRes.data.length === 0);
  if (noApps && noProjects) setView('cloud');
}

(async function init() {
  await decideInitialView();
  await refreshInfo();
  await refreshActive();
  setInterval(refreshInfo, 3000);
  tick();
})();
