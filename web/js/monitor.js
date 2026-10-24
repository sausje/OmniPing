// The main screen: the target table on top, timelines below.
// On a phone it becomes a list with sparklines.

import { S, on, series, settings, intervalOf, lastStats, statsOf, level, latencyClass, pref, setPref, now, ensureLive, addrOf } from './store.js';
import { api, fetchSamples } from './api.js';
import { drawGraphCell, drawSpark, seriesSeg, LOG0, LOG1, niceCeil } from './graphs.js';
import { fmtMs, fmtTime, fmtNum, fmtBytes, fmtDuration, fmtSpan, esc } from './fmt.js';
import { icon, contextMenu, promptDialog, toast, errorToast, themeSwitch } from './ui.js';
import { Timelines, SPANS, rangeText } from './timelines.js';
import { openAddTargets, editTarget, deleteTarget, renameTarget, addComment, openMoveToGroup, moveGroup } from './targets-ui.js';

const phone = window.matchMedia('(max-width: 760px)');
export const isPhone = () => phone.matches;

const COLS = {
  address: { label: 'Address', cls: 'l', sort: t => ipKey(t) },
  loss: { label: 'Loss', sort: (t, st) => st.n ? st.loss : -1 },
  cur: { label: 'Cur', sort: (t, st) => st.cur ?? (st.n ? 1e9 : -1) },
  avg: { label: 'Avg', sort: (t, st) => st.avg ?? (st.n ? 1e9 : -1) },
  min: { label: 'Min', sort: (t, st) => st.min ?? (st.n ? 1e9 : -1) },
  max: { label: 'Max', sort: (t, st) => st.max ?? (st.n ? 1e9 : -1) },
  jitter: { label: 'Jitter', sort: (t, st) => st.jit ?? -1 },
};
const NUM_COLS = ['loss', 'cur', 'avg', 'min', 'max', 'jitter'];

function ipKey(t) {
  const ip = t.ip || t.host || '';
  const m = ip.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/);
  return m ? m.slice(1).map(x => x.padStart(3, '0')).join('.') : 'z' + ip;
}

// Monitor state that survives switching pages.
const M = {
  focus: null,           // null = live, else { end } (statistics end at this moment)
  focusData: null,       // Map id → {t, r} fetched for a focus beyond the live buffers
  selected: pref('selected', null),
  sort: pref('sort', null), // { key, dir }
  collapsed: new Set(pref('collapsed', [])),
};

/** Statistics shown in the table: the last N samples, or N samples up to the focus. */
function tableStats(id) {
  const K = settings().samplesToInclude;
  if (!M.focus) return lastStats(id, K);
  const s = series(id);
  const t = S.byId.get(id);
  if (s.length && s.t[0] <= M.focus.end - K * intervalOf(t)) return lastStats(id, K, M.focus.end);
  const fd = M.focusData && M.focusData.get(id);
  if (fd) {
    let i1 = 0;
    while (i1 < fd.t.length && fd.t[i1] <= M.focus.end) i1++;
    return statsOf(fd.t, fd.r, Math.max(0, i1 - K), i1);
  }
  return lastStats(id, K, M.focus.end);
}

function visibleTargets() {
  return S.st.targets;
}

function groupsInOrder() {
  const targets = S.st.targets;
  if (!targets.some(t => t.group)) return null;
  const names = [...S.st.groups];
  for (const t of targets) if (t.group && !names.includes(t.group)) names.push(t.group);
  if (targets.some(t => !t.group)) names.push('');
  return names;
}

function sortTargets(list, stats) {
  const so = M.sort;
  if (!so) return list;
  const key = so.key === 'name' ? t => t.name.toLowerCase() : t => (COLS[so.key]?.sort || (() => 0))(t, stats.get(t.id));
  return [...list].sort((a, b) => {
    const x = key(a), y = key(b);
    return (x < y ? -1 : x > y ? 1 : 0) * so.dir;
  });
}

async function setFocus(t) {
  if (t == null) { M.focus = null; M.focusData = null; return; }
  M.focus = { end: t };
  const K = settings().samplesToInclude;
  const needFetch = S.st.targets.some(x => {
    const s = series(x.id);
    return !s.length || s.t[0] > t - K * intervalOf(x);
  });
  if (!needFetch) { M.focusData = null; return; }
  const maxIv = Math.max(...S.st.targets.map(intervalOf));
  const from = t - (K + 2) * maxIv * 1.5 - 10e3;
  const res = await fetchSamples([], from, t + maxIv, 40000);
  M.focusData = new Map(res.series.filter(r => r.mode === 'raw').map(r => [r.id, r]));
}

function targetMenu(e, id, t, ctx) {
  const tg = S.byId.get(id);
  if (!tg) return;
  const s = settings();
  const items = [
    { label: 'Customize view…', icon: 'list', action: () => columnsMenu(e.clientX, e.clientY) },
    { label: 'Graph current sample', check: s.drawCurrent, action: () => api.put('settings', { drawCurrent: !s.drawCurrent }).catch(errorToast) },
    { label: 'Graph min/max range', check: s.drawMinMax, action: () => api.put('settings', { drawMinMax: !s.drawMinMax }).catch(errorToast) },
    '-',
    { label: 'Add new ping target…', icon: 'plus', action: () => openAddTargets() },
    { label: 'Delete selected target', icon: 'trash', danger: true, action: () => deleteTarget(tg) },
    { label: 'Rename selected target…', icon: 'edit', action: () => renameTarget(tg) },
    { label: 'Edit target…', icon: 'gear', action: () => editTarget(tg) },
    { label: 'Expected off…', icon: 'pause', action: () => editTarget(tg, 'off') },
    { label: 'Move to group…', icon: 'tag', action: () => openMoveToGroup([id]) },
    { label: tg.paused ? 'Resume pinging' : 'Pause pinging', icon: tg.paused ? 'play' : 'pause', action: () => api.patch('targets/' + id, { paused: !tg.paused }).catch(errorToast) },
    '-',
  ];
  if (t != null) {
    items.push({ label: `Focus the table on ${fmtTime(t)}`, icon: 'focus', action: () => ctx.focusAt(t) });
    items.push({ label: 'Add comment here…', icon: 'note', action: () => addComment(id, t) });
  }
  items.push(
    { label: 'Watch this target (alerts)…', icon: 'bell', action: () => { location.hash = '#/alerts/new/' + id; } },
    { label: 'Float this timeline graph', icon: 'pop', action: () => popOut(id) },
    { label: 'Show this timeline graph', check: !tg.hidden, action: () => api.patch('targets/' + id, { hidden: !tg.hidden }).catch(errorToast) },
    { label: 'Target history', icon: 'history', action: () => { location.hash = '#/target/' + id; } },
  );
  contextMenu(e.clientX, e.clientY, {
    header: `<div class="mh"><b>${esc(tg.name)}</b><span>${esc(addrOf(tg))}</span></div>`,
    items,
  });
}

function columnsMenu(x, y) {
  const s = settings();
  const all = [['address', 'Address'], ['loss', 'Loss'], ['cur', 'Current'], ['avg', 'Average'], ['min', 'Minimum'], ['max', 'Maximum'], ['jitter', 'Jitter'], ['graph', 'Graph']];
  contextMenu(x, y, {
    header: '<div class="mh"><b>Columns</b><span>shown in the target table</span></div>',
    items: all.map(([k, label]) => ({
      label, check: s.columns.includes(k),
      action: () => {
        const cols = s.columns.includes(k) ? s.columns.filter(c => c !== k) : all.map(a => a[0]).filter(c => c === k || s.columns.includes(c));
        api.put('settings', { columns: cols }).catch(errorToast);
      },
    })),
  });
}

export function popOut(id) {
  const w = window.open('#/pop/' + id, 'op-pop-' + id, 'width=1100,height=300');
  if (!w) toast('Pop-up blocked', 'Allow pop-ups for this page to float a timeline in its own window.', { kind: 'warn' });
}

/** The desktop monitor. opts.timelinesOnly shows just the timelines (phone tab). */
export function mount(root, opts = {}) {
  if (isPhone() && !opts.timelinesOnly) return mountPhone(root);
  const offs = [];
  let tl = null;
  const stats = new Map();
  let rowRefs = new Map();
  let grpRefs = new Map();
  let linMax = 10;

  const ctx = {
    focusAt: async t => { await setFocus(t).catch(errorToast); updateTable(); tl.request('graphs'); },
  };

  root.innerHTML = `
    <div class="banner" data-ping-err hidden></div>
    ${opts.timelinesOnly ? '' : `<section class="upper" style="${pref('upperH', 0) ? `height:${pref('upperH', 0)}px` : 'max-height:46vh'}">
      <div class="paneh">
        <h2>Targets</h2>
        <span class="chip" data-focus-chip></span>
        <span class="hint mono hide-md" data-sample-set></span>
        <span data-alert-chips style="display:contents"></span>
        <span class="spacer"></span>
        <span data-legend></span>
        <button class="btn ghost sm" data-columns title="Choose columns">${icon('list')} Columns</button>
      </div>
      <div class="table-scroll" data-table-scroll></div>
    </section>
    <div class="splitter" data-splitter title="Drag to resize"><i></i></div>`}
    <section class="lower">
      <div class="paneh">
        <h2>Timelines</h2>
        <div class="seg" data-spans>${SPANS.map(([s, l]) => `<button data-span="${s}">${l}</button>`).join('')}</div>
        <span class="field hide-md"><span class="lbl">Latency scale</span>
          <select class="input" data-scale style="height:28px"><option value="auto">Automatic</option><option value="fixed">Fixed</option></select></span>
        <button class="chip accent" data-live></button>
        <span class="spacer"></span>
        <span class="hint hide-md" data-hidden-note></span>
        <span class="hint hide-md">Wheel or drag to scroll · <span class="kbd">Ctrl</span> + wheel to zoom · <span class="kbd">Shift</span> + wheel for the list · <span class="kbd">Shift</span> + drag to inspect a period · double-click to focus</span>
      </div>
      <div class="tl-axis"><div data-axis-note></div><canvas data-axis></canvas></div>
      <div class="tl-scroll" data-tl-scroll><div data-tl-rows></div><div class="empty" data-tl-empty hidden></div></div>
    </section>
    <footer class="statusbar" data-status></footer>`;

  const q = sel => root.querySelector(sel);
  const tlScroll = q('[data-tl-scroll]');
  tl = new Timelines({
    scroller: tlScroll,
    statsFor: id => (M.focus || settings().samplesToInclude) ? stats.get(id) : null,
    levelFor: id => level(stats.get(id)),
    onFocus: t => ctx.focusAt(t),
    onSelect: id => select(id),
    onContext: (e, id, t) => targetMenu(e, id, t, ctx),
    onTool: (act, id) => {
      if (act === 'hide') api.patch('targets/' + id, { hidden: true }).catch(errorToast);
      if (act === 'pop') popOut(id);
      if (act === 'history') location.hash = '#/target/' + id;
    },
    onRange: () => toolbar(),
    onDraw: () => status(),
  });
  tl.axis = q('[data-axis]');

  // ---------- table ----------
  function computeStats() {
    stats.clear();
    let mx = 0;
    for (const t of S.st.targets) {
      const st = tableStats(t.id);
      stats.set(t.id, st);
      if (st.max != null && st.max > mx) mx = st.max;
    }
    linMax = niceCeil(Math.max(1, mx * 1.05));
  }

  function buildTable() {
    const box = q('[data-table-scroll]');
    if (!box) return;
    const s = settings();
    const cols = s.columns;
    const has = c => cols.includes(c);
    const targets = visibleTargets();
    if (!targets.length) {
      box.innerHTML = `<div class="empty"><h3>No targets yet</h3>
        <p>Type an IP address or a name in “Add target” above, paste a list (.lst and .mpl files work), or scan your network for devices.</p>
        <div style="display:flex;gap:8px"><button class="btn primary" data-add>${icon('plus')} Add targets</button><button class="btn" data-scan>${icon('radar')} Scan my network</button></div></div>`;
      box.querySelector('[data-add]').onclick = () => openAddTargets('single');
      box.querySelector('[data-scan]').onclick = () => openAddTargets('scan');
      rowRefs = new Map();
      return;
    }
    computeStats();
    const groups = groupsInOrder();
    const sortKey = M.sort?.key;
    const th = (key, label, cls = '') => `<th class="sortable ${cls} ${sortKey === key ? 'sorted' + (M.sort.dir > 0 ? ' asc' : '') : ''}" data-sort="${key}">${label}</th>`;
    const pct = v => (Math.log10(v) - Math.log10(LOG0)) / (Math.log10(LOG1) - Math.log10(LOG0)) * 100;
    const axis = s.graphScale === 'linear'
      ? [0, linMax / 2, linMax].map((v, i, a) => `<span class="${i === 0 ? 'first' : i === a.length - 1 ? 'last' : ''}" data-lin="${i}" style="left:${i * 50}%">${i === a.length - 1 ? fmtMs(v) + ' ms' : fmtMs(v)}</span>`).join('')
      : [0.1, 1, 10, 100, 1000].map((v, i, a) => `<span class="${i === 0 ? 'first' : i === a.length - 1 ? 'last' : ''}" style="left:${pct(v)}%">${v >= 1000 ? '1 s' : v + (i === 0 ? ' ms' : '')}</span>`).join('');
    let html = `<table class="tg"><colgroup><col style="width:34px"><col style="width:190px">${has('address') ? '<col style="width:250px">' : ''}${NUM_COLS.filter(has).map(() => '<col style="width:76px">').join('')}${has('graph') ? '<col>' : ''}</colgroup>
      <thead><tr><th></th>${th('name', 'Name', 'l')}${has('address') ? th('address', 'Address', 'l') : ''}${NUM_COLS.filter(has).map(c => th(c, COLS[c].label)).join('')}${has('graph') ? `<th class="gcell"><div class="gaxis" data-gaxis>${axis}</div></th>` : ''}</tr></thead><tbody>`;
    const ncols = 2 + (has('address') ? 1 : 0) + NUM_COLS.filter(has).length + (has('graph') ? 1 : 0);
    const row = t => `<tr class="row ${t.id === M.selected ? 'sel' : ''}" data-id="${t.id}" draggable="${!M.sort}">
      <td class="st"><span class="sd"></span></td>
      <td class="name l" title="${esc(t.name)}"><span>${esc(t.name)}</span><span data-tags></span></td>
      ${has('address') ? `<td class="addr l" title="${esc(addrOf(t))} ${esc(t.dnsName || '')}">${esc(addrOf(t))}${t.dnsName && t.dnsName !== t.name && t.dnsName !== t.ip ? ` <span>${esc(t.dnsName)}</span>` : ''}</td>` : ''}
      ${NUM_COLS.filter(has).map(c => `<td class="${c === 'loss' ? 'loss' : ''}" data-k="${c}"></td>`).join('')}
      ${has('graph') ? '<td class="gcell"><canvas></canvas></td>' : ''}</tr>`;
    if (groups) {
      for (const g of groups) {
        const list = sortTargets(targets.filter(t => (t.group || '') === g), stats);
        if (!list.length) continue;
        const col = M.collapsed.has(g);
        html += `<tr class="grp ${col ? 'collapsed' : ''}" data-group="${esc(g)}"><td colspan="${ncols}"><span style="margin-left:14px" class="caret">▾</span> ${esc(g || 'Ungrouped')}<span class="gs"></span></td></tr>`;
        if (!col) html += list.map(row).join('');
      }
    } else {
      html += sortTargets(targets, stats).map(row).join('');
    }
    box.innerHTML = html + '</tbody></table>';
    rowRefs = new Map();
    for (const tr of box.querySelectorAll('tr.row')) {
      const r = { tr, sd: tr.querySelector('.sd'), tags: tr.querySelector('[data-tags]'), cv: tr.querySelector('canvas'), cells: {} };
      for (const td of tr.querySelectorAll('[data-k]')) r.cells[td.dataset.k] = td;
      rowRefs.set(tr.dataset.id, r);
    }
    grpRefs = new Map([...box.querySelectorAll('tr.grp')].map(tr => [tr.dataset.group, tr.querySelector('.gs')]));
    updateTable();
  }

  function updateTable() {
    if (!q('[data-table-scroll]')) { computeStats(); return; }
    computeStats();
    const s = settings();
    for (const [id, r] of rowRefs) {
      const t = S.byId.get(id), st = stats.get(id);
      if (!t || !st) continue;
      const lv = t.paused ? 'paused' : level(st);
      r.sd.className = 'sd ' + lv;
      r.tags.innerHTML = (t.paused ? '<span class="paused-tag">paused</span>' : '') + (t.error ? `<span class="err-tag" title="${esc(t.error)}">${esc(t.error)}</span>` : '');
      const c = r.cells;
      if (c.loss) { c.loss.textContent = st.n ? st.loss.toFixed(0) + '%' : '—'; c.loss.className = 'loss ' + (!st.n ? 'zero' : st.loss === 0 ? 'zero' : st.loss >= 20 ? 'bad' : 'warn'); }
      if (c.cur) { c.cur.textContent = !st.n ? '—' : st.curLost ? 'lost' : fmtMs(st.cur); c.cur.className = st.curLost ? 'lost' : latencyClass(st.cur); }
      if (c.avg) { c.avg.textContent = fmtMs(st.avg); c.avg.className = latencyClass(st.avg); }
      if (c.min) c.min.textContent = fmtMs(st.min);
      if (c.max) { c.max.textContent = fmtMs(st.max); c.max.className = latencyClass(st.max); }
      if (c.jitter) c.jitter.textContent = fmtMs(st.jit);
      if (r.cv) drawGraphCell(r.cv, st, s, linMax);
    }
    if (s.graphScale === 'linear') {
      root.querySelectorAll('[data-lin]').forEach(sp => {
        const v = [0, linMax / 2, linMax][+sp.dataset.lin];
        sp.textContent = +sp.dataset.lin === 2 ? fmtMs(v) + ' ms' : fmtMs(v);
      });
    }
    for (const [g, span] of grpRefs) {
      const list = S.st.targets.filter(t => (t.group || '') === g);
      const down = list.filter(t => { const st = stats.get(t.id); return st && st.n && st.avg == null; }).length;
      const lossy = list.filter(t => { const st = stats.get(t.id); return st && st.avg != null && st.loss > 0; }).length;
      span.textContent = [`${list.length} target${list.length === 1 ? '' : 's'}`, down ? `${down} not responding` : '', lossy ? `${lossy} with loss` : '', !down && !lossy ? 'all clear' : ''].filter(Boolean).join(' · ');
    }
    header();
  }

  function header() {
    const chip = q('[data-focus-chip]');
    if (!chip) return;
    const s = settings();
    const K = s.samplesToInclude;
    if (M.focus) {
      chip.className = 'chip accent';
      chip.innerHTML = `${icon('focus')} Focused on ${esc(fmtTime(M.focus.end))} · ${K} samples <button class="btn ghost sm" data-unfocus style="height:20px;margin-right:-6px">Back to live</button>`;
      chip.querySelector('[data-unfocus]').onclick = () => { setFocus(null); updateTable(); tl.request('graphs'); };
    } else {
      chip.className = 'chip';
      const ms = K * s.intervalMs;
      chip.innerHTML = `Statistics over the last <b>&nbsp;${K} samples&nbsp;</b> (${fmtSpan(Math.round(ms / 1000)) || ms + ' ms'})`;
    }
    let from = Infinity, to = -Infinity;
    for (const st of stats.values()) if (st.n) { from = Math.min(from, st.from); to = Math.max(to, st.to); }
    const ss = q('[data-sample-set]');
    if (ss) ss.textContent = isFinite(from) ? `Sample set: ${rangeText(from, to)}` : '';
    const ac = q('[data-alert-chips]');
    if (ac) {
      const act = S.active.slice(0, 3);
      ac.innerHTML = act.map(a => {
        if (!a.target) return `<a class="chip bad" href="#/alerts/${a.rule}" title="${esc(a.reason)}"><span class="sd bad" style="width:7px;height:7px;box-shadow:none"></span>${esc(a.ruleName)}</a>`;
        const t = S.byId.get(a.target);
        return t ? `<a class="chip bad" href="#/target/${a.target}" title="${esc(a.reason)}"><span class="sd bad" style="width:7px;height:7px;box-shadow:none"></span>${esc(t.name)}: ${esc(firstReason(a.reason))}</a>` : '';
      }).join('') + (S.active.length > 3 ? `<a class="chip bad" href="#/alerts">+${S.active.length - 3} more</a>` : '');
    }
    const lg = q('[data-legend]');
    if (lg) lg.innerHTML = s.showLegend ? `<span class="legend hide-md" title="Latency colours (Settings → Display)"><span class="g">0–${s.warnMs} ms</span><span class="w">${s.warnMs + 1}–${s.critMs} ms</span><span class="c">${s.critMs + 1}+ ms</span></span>` : '';
  }

  // ---------- timelines ----------
  function buildTimelines() {
    const box = q('[data-tl-rows]');
    const groups = groupsInOrder();
    let list = S.st.targets.filter(t => !t.hidden && !(groups && M.collapsed.has(t.group || '')));
    if (groups) {
      const order = new Map(groups.map((g, i) => [g, i]));
      list = [...list].sort((a, b) => (order.get(a.group || '') - order.get(b.group || '')) || 0);
    }
    if (M.sort) {
      const sorted = sortTargets(list, stats);
      if (groups) {
        const order = new Map(groups.map((g, i) => [g, i]));
        list = sorted.sort((a, b) => order.get(a.group || '') - order.get(b.group || ''));
      } else list = sorted;
    }
    tl.build(box, list, { selected: M.selected });
    const hidden = S.st.targets.filter(t => t.hidden).length;
    const note = q('[data-hidden-note]');
    note.innerHTML = hidden ? `${hidden} hidden · <a href="#" data-showall>show all</a>` : '';
    const sa = note.querySelector('[data-showall]');
    if (sa) sa.onclick = e => {
      e.preventDefault();
      Promise.all(S.st.targets.filter(t => t.hidden).map(t => api.patch('targets/' + t.id, { hidden: false }))).catch(errorToast);
    };
    const empty = q('[data-tl-empty]');
    empty.hidden = list.length > 0;
    if (!list.length) empty.innerHTML = S.st.targets.length ? '<p>All timeline graphs are hidden.</p>' : '<p>Timelines appear here once you add targets.</p>';
  }

  function toolbar() {
    const span = tl.span || settings().timelineSpanSec;
    root.querySelectorAll('[data-span]').forEach(b => b.classList.toggle('on', +b.dataset.span === span));
    const live = q('[data-live]');
    if (tl.live()) {
      live.className = 'chip accent';
      live.innerHTML = `<span class="pulse" style="background:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)"></span> Live · following latest`;
    } else {
      live.className = 'chip warn';
      live.innerHTML = `${icon('live')} Viewing the past · go live`;
    }
    q('[data-scale]').value = settings().scaleMode;
    const custom = !SPANS.some(([s]) => s === span);
    q('[data-axis-note]').textContent = `${custom ? fmtSpan(span) + ' · ' : ''}${fmtSpanText(span)} · interval ${settings().intervalMs >= 1000 ? +(settings().intervalMs / 1000).toFixed(2) + ' s' : settings().intervalMs + ' ms'}`;
  }

  function status() {
    const sb = q('[data-status]');
    const info = S.st.info;
    const [from, to] = tl.range();
    const sampleCount = S.st.targets.reduce((n, t) => n + (t.count || 0), 0);
    sb.innerHTML = `
      <span><span class="pulse ${S.connected ? '' : 'bad'}" style="display:inline-block;vertical-align:middle;width:7px;height:7px"></span>&nbsp; ${S.connected ? `Connected to <span class="num">${esc(info.hostname)}</span>` : 'Reconnecting…'}</span>
      <span class="hide-sm">${settings().running ? 'Querying' : 'Stopped'}</span>
      <span class="hide-sm">Samples collected: <span class="num">${fmtNum(info.samples || sampleCount)}</span></span>
      <span class="hide-md">Displayed: <span class="num">${esc(rangeText(from, to))}</span></span>
      <span class="spacer"></span>
      <span class="hide-md">Disk <span class="num">${fmtBytes(info.diskBytes || 0)}</span> · RAM <span class="num">${fmtBytes(info.memBytes || 0)}</span> · ${esc(info.mode)}</span>`;
    const banner = q('[data-ping-err]');
    banner.hidden = !info.pingerError;
    if (info.pingerError) banner.textContent = 'Pinging is not working: ' + info.pingerError;
  }

  function select(id) {
    M.selected = id;
    setPref('selected', id);
    for (const [rid, r] of rowRefs) r.tr.classList.toggle('sel', rid === id);
    tl.setSelected(id);
  }

  function rebuild() {
    buildTable();
    buildTimelines();
    toolbar();
    status();
  }

  // ---------- events ----------
  root.addEventListener('click', e => {
    const sp = e.target.closest('[data-span]');
    if (sp) { tl.setSpan(+sp.dataset.span); toolbar(); return; }
    if (e.target.closest('[data-live]')) { tl.goLive(); toolbar(); return; }
    if (e.target.closest('[data-columns]')) { const r = e.target.closest('[data-columns]').getBoundingClientRect(); columnsMenu(r.left, r.bottom + 4); return; }
    const th = e.target.closest('th[data-sort]');
    if (th) {
      const k = th.dataset.sort;
      if (!M.sort || M.sort.key !== k) M.sort = { key: k, dir: k === 'name' || k === 'address' ? 1 : -1 };
      else if (M.sort.dir === (k === 'name' || k === 'address' ? 1 : -1)) M.sort = { key: k, dir: -M.sort.dir };
      else M.sort = null;
      setPref('sort', M.sort);
      rebuild();
      return;
    }
    const grp = e.target.closest('tr.grp');
    if (grp) {
      const g = grp.dataset.group;
      if (M.collapsed.has(g)) M.collapsed.delete(g); else M.collapsed.add(g);
      setPref('collapsed', [...M.collapsed]);
      rebuild();
      return;
    }
    const tr = e.target.closest('tr.row');
    if (tr) select(tr.dataset.id);
  });
  root.addEventListener('dblclick', e => {
    const tr = e.target.closest('tr.row');
    if (tr) location.hash = '#/target/' + tr.dataset.id;
  });
  root.addEventListener('contextmenu', e => {
    const tr = e.target.closest('tr.row');
    if (tr) { e.preventDefault(); select(tr.dataset.id); targetMenu(e, tr.dataset.id, null, ctx); return; }
    const grp = e.target.closest('tr.grp');
    if (grp) {
      e.preventDefault();
      const g = grp.dataset.group;
      if (!g) return;
      contextMenu(e.clientX, e.clientY, {
        header: `<div class="mh"><b>${esc(g)}</b><span>group</span></div>`,
        items: [
          { label: 'Move targets into this group…', icon: 'tag', action: () => openMoveToGroup([], g) },
          { label: 'Move group up', action: () => moveGroup(g, -1) },
          { label: 'Move group down', action: () => moveGroup(g, 1) },
          '-',
          { label: 'Rename group…', icon: 'edit', action: async () => { const to = await promptDialog('Rename group', { label: 'Name', value: g }); if (to != null && to.trim() && to !== g) api.put('groups', { rename: { from: g, to: to.trim() } }).catch(errorToast); } },
          { label: 'Ungroup these targets', icon: 'x', action: () => api.put('groups', { rename: { from: g, to: '' } }).catch(errorToast) },
        ],
      });
    }
  });
  q('[data-scale]').addEventListener('change', e => api.put('settings', { scaleMode: e.target.value }).catch(errorToast));

  // drag rows to reorder (when the table is in manual order)
  let dragId = null;
  root.addEventListener('dragstart', e => {
    const tr = e.target.closest('tr.row');
    if (!tr || M.sort) return;
    dragId = tr.dataset.id;
    tr.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', dragId);
  });
  root.addEventListener('dragover', e => {
    const tr = e.target.closest('tr.row');
    if (!dragId || !tr) return;
    e.preventDefault();
    root.querySelectorAll('.drop-before').forEach(x => x.classList.remove('drop-before'));
    tr.classList.add('drop-before');
  });
  root.addEventListener('dragend', () => {
    dragId = null;
    root.querySelectorAll('.dragging, .drop-before').forEach(x => x.classList.remove('dragging', 'drop-before'));
  });
  root.addEventListener('drop', e => {
    const tr = e.target.closest('tr.row');
    if (!dragId || !tr) return;
    e.preventDefault();
    const ids = S.st.targets.map(t => t.id).filter(id => id !== dragId);
    const moved = S.byId.get(dragId), over = S.byId.get(tr.dataset.id);
    ids.splice(ids.indexOf(tr.dataset.id), 0, dragId);
    const p = moved && over && moved.group !== over.group ? api.patch('targets/' + dragId, { group: over.group }) : Promise.resolve();
    p.then(() => api.post('targets/order', { ids })).catch(errorToast);
  });

  // splitter
  const split = q('[data-splitter]');
  if (split) {
    split.addEventListener('pointerdown', e => {
      const upper = q('.upper');
      const startY = e.clientY, startH = upper.getBoundingClientRect().height;
      split.setPointerCapture(e.pointerId);
      const move = ev => {
        const h = Math.max(80, Math.min(window.innerHeight - 200, startH + ev.clientY - startY));
        upper.style.height = h + 'px'; upper.style.maxHeight = '';
        tl.request('graphs');
      };
      const up = () => {
        split.removeEventListener('pointermove', move);
        setPref('upperH', Math.round(upper.getBoundingClientRect().height));
      };
      split.addEventListener('pointermove', move);
      split.addEventListener('pointerup', up, { once: true });
    });
    split.addEventListener('dblclick', () => { setPref('upperH', 0); q('.upper').style.height = ''; q('.upper').style.maxHeight = '46vh'; tl.request('graphs'); });
  }

  let lastTable = 0;
  offs.push(on('samples', () => {
    const t = performance.now();
    if (t - lastTable > 200) { lastTable = t; if (!M.focus) updateTable(); }
    if (tl.live()) tl.request('graphs');
  }));
  offs.push(on('state', () => rebuild()));
  offs.push(on('backfill', () => { updateTable(); tl.request('graphs'); }));
  offs.push(on('targetError', () => updateTable()));
  offs.push(on('active', () => header()));
  offs.push(on('theme', () => { updateTable(); tl.request('graphs'); }));
  offs.push(on('info', () => status()));
  offs.push(on('conn', () => status()));
  const tick = setInterval(() => { if (tl.live()) tl.request('graphs'); }, 1000);
  const ro = new ResizeObserver(() => { tl.request('graphs'); updateTable(); });
  ro.observe(q('.lower'));

  rebuild();
  const sp = tl.span || settings().timelineSpanSec;
  if (sp * 1000 <= 3 * 3600e3) ensureLive(sp * 1100).then(() => tl.request('graphs'));

  return {
    unmount() {
      offs.forEach(f => f());
      clearInterval(tick);
      ro.disconnect();
      tl.destroy();
    },
  };
}

function fmtSpanText(sec) {
  const [, label] = SPANS.find(([s]) => s === sec) || [];
  if (!label) return 'Custom span';
  return `Graph time = ${fmtSpan(sec).replace(' min', ' minutes').replace(/ h$/, ' hours').replace(/ d$/, ' days')}`;
}

function firstReason(r) { return (r || '').split(',')[0] || 'problem'; }

// ---------- phone ----------
function mountPhone(root) {
  const offs = [];
  const K = () => settings().samplesToInclude;
  root.innerHTML = `<div class="msum" data-sum></div><div class="mlist" data-list></div>`;
  const list = root.querySelector('[data-list]');

  function build() {
    const targets = S.st.targets;
    if (!targets.length) {
      list.innerHTML = `<div class="empty"><h3>No targets yet</h3><p>Add an IP address or a name to start pinging.</p><button class="btn primary" data-add>${icon('plus')} Add targets</button></div>`;
      list.querySelector('[data-add]').onclick = () => openAddTargets('single');
      return;
    }
    const groups = groupsInOrder() || [''];
    let html = '<div data-alerts></div>';
    for (const g of groups) {
      const ts = targets.filter(t => groups.length === 1 || (t.group || '') === g);
      if (!ts.length) continue;
      if (groups.length > 1 || g) html += `<div class="mgh"><span>${esc(g || 'Ungrouped')}</span><span>${ts.length} target${ts.length === 1 ? '' : 's'}</span></div>`;
      html += ts.map(t => `<button class="mrow" data-id="${t.id}"><span class="sd"></span>
        <div style="min-width:0"><div class="nm">${esc(t.name)}</div><div class="ip">${esc(addrOf(t))}</div></div>
        <canvas></canvas><div class="v"><b></b><small></small></div></button>`).join('');
    }
    list.innerHTML = html;
    update();
  }

  function update() {
    let ok = 0, lossy = 0, down = 0;
    const span = 5 * 60e3, t1 = now(), t0 = t1 - span;
    for (const r of list.querySelectorAll('.mrow')) {
      const id = r.dataset.id, t = S.byId.get(id);
      if (!t) continue;
      const st = lastStats(id, K());
      const lv = t.paused ? 'paused' : level(st);
      if (st.n && st.avg == null) down++; else if (st.loss > 0) lossy++; else if (st.n) ok++;
      r.querySelector('.sd').className = 'sd ' + lv;
      const b = r.querySelector('b'), sm = r.querySelector('small');
      if (!st.n) { b.textContent = '—'; sm.textContent = t.paused ? 'paused' : ''; sm.className = ''; }
      else if (st.curLost) { b.innerHTML = '<span class="bad">lost</span>'; sm.textContent = st.loss.toFixed(0) + '%'; sm.className = st.loss >= 20 ? 'l' : 'w'; }
      else { b.textContent = fmtMs(st.cur); sm.textContent = 'ms · ' + st.loss.toFixed(0) + '%'; sm.className = st.loss >= 20 ? 'l' : st.loss > 0 ? 'w' : ''; }
      drawSpark(r.querySelector('canvas'), seriesSeg(series(id), t0, t1));
    }
    root.querySelector('[data-sum]').innerHTML = `<span class="chip good"><b>${ok}</b>&nbsp;OK</span>${lossy ? `<span class="chip warn"><b>${lossy}</b>&nbsp;losing packets</span>` : ''}${down ? `<span class="chip bad"><b>${down}</b>&nbsp;down</span>` : ''}<span class="chip">last ${K()} samples</span>`;
    const al = list.querySelector('[data-alerts]');
    if (al) al.innerHTML = S.active.map(a => {
      if (!a.target) return `<button class="malert" data-href="#/alerts/${a.rule}"><span class="n">${esc(a.ruleName)}</span><span class="d">${esc(a.reason)}</span><span class="t">${fmtDuration(now() - a.since)}</span></button>`;
      const t = S.byId.get(a.target);
      return t ? `<button class="malert" data-id="${a.target}"><span class="n">${esc(t.name)}</span><span class="d">${esc(a.reason || a.ruleName)}</span><span class="t">${fmtDuration(now() - a.since)}</span></button>` : '';
    }).join('');
  }

  list.addEventListener('click', e => {
    const h = e.target.closest('[data-href]');
    if (h) { location.hash = h.dataset.href; return; }
    const b = e.target.closest('[data-id]');
    if (b) location.hash = '#/target/' + b.dataset.id;
  });
  let last = 0;
  offs.push(on('samples', () => { const t = performance.now(); if (t - last > 900) { last = t; update(); } }));
  offs.push(on('state', build));
  offs.push(on('backfill', update));
  offs.push(on('active', update));
  offs.push(on('theme', update));
  build();
  return { unmount() { offs.forEach(f => f()); } };
}

// ---------- pop-out window ----------
export function mountPop(root, id) {
  const offs = [];
  root.innerHTML = `<div class="pop">
    <div class="paneh" style="background:var(--panel)">
      <span class="sd" data-dot></span><b data-name style="font-size:14px"></b><span class="mono muted" data-addr></span>
      <span class="mono" data-now></span>
      <span class="spacer"></span>
      <div class="seg" data-spans>${SPANS.slice(0, 8).map(([s, l]) => `<button data-span="${s}">${l}</button>`).join('')}</div>
      <button class="chip accent" data-live>Live</button>
      ${themeSwitch()}
    </div>
    <div class="tl-axis" style="--label-w:0px"><div></div><canvas data-axis></canvas></div>
    <div class="tl-scroll" data-scroll style="display:flex;flex-direction:column;--label-w:0px"><div data-rows style="flex:1;display:flex;flex-direction:column"></div></div>
  </div>`;
  const q = s => root.querySelector(s);
  const tl = new Timelines({
    scroller: q('[data-scroll]'), prefKey: 'popSpan',
    statsFor: x => lastStats(x, settings().samplesToInclude),
    onContext: (e, tid, t) => contextMenu(e.clientX, e.clientY, { items: [{ label: 'Add comment here…', icon: 'note', action: () => addComment(tid, t) }] }),
    onRange: () => bar(),
  });
  tl.axis = q('[data-axis]');
  function build() {
    const t = S.byId.get(id);
    if (!t) { q('[data-rows]').innerHTML = '<div class="empty"><p>This target no longer exists.</p></div>'; return; }
    document.title = `${t.name} · OmniPing`;
    q('[data-name]').textContent = t.name;
    q('[data-addr]').textContent = addrOf(t);
    tl.build(q('[data-rows]'), [t], { height: 100 });
    bar();
  }
  function bar() {
    const span = tl.span || settings().timelineSpanSec;
    root.querySelectorAll('[data-span]').forEach(b => b.classList.toggle('on', +b.dataset.span === span));
    q('[data-live]').textContent = tl.live() ? 'Live' : 'Go live';
    const st = lastStats(id, settings().samplesToInclude);
    q('[data-dot]').className = 'sd ' + level(st);
    q('[data-now]').textContent = st.n ? `cur ${st.curLost ? 'lost' : fmtMs(st.cur) + ' ms'} · avg ${fmtMs(st.avg)} ms · ${st.loss.toFixed(0)}% loss` : '';
  }
  root.addEventListener('click', e => {
    const sp = e.target.closest('[data-span]');
    if (sp) { tl.setSpan(+sp.dataset.span); bar(); }
    if (e.target.closest('[data-live]')) { tl.goLive(); bar(); }
  });
  offs.push(on('samples', () => { if (tl.live()) tl.request('graphs'); bar(); }));
  offs.push(on('state', build));
  offs.push(on('theme', () => tl.request('graphs')));
  const ro = new ResizeObserver(() => tl.request('graphs'));
  ro.observe(root);
  const tick = setInterval(() => { if (tl.live()) tl.request('graphs'); }, 1000);
  build();
  return { unmount() { offs.forEach(f => f()); ro.disconnect(); clearInterval(tick); tl.destroy(); } };
}
