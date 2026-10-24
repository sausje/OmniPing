// The report: uptime, packet loss and every outage over a period, to see how
// the connection really did or to send to an internet provider. Prints as a
// clean document (Save as PDF) and exports the outages as CSV.

import { S, pref, setPref, now } from './store.js';
import { api } from './api.js';
import { fmtMs, fmtDate, fmtDateTime, fmtDuration, fmtNum, fmtInterval, esc } from './fmt.js';
import { icon, errorToast } from './ui.js';
import { traceBlock } from './alerts.js';

const PERIODS = [['1', '24 hours'], ['7', '7 days'], ['30', '30 days'], ['custom', 'Custom']];
const DAY = 86_400_000;

const pct = (v, d = 2) => v == null ? '—' : (v >= 99.995 && v < 100 ? '99.99' : v.toFixed(d)).replace(/\.?0+$/, '') + '%';
const ms = v => v == null || v < 0 ? '—' : fmtMs(v) + ' ms';
const dur = v => v > 0 ? fmtDuration(v) : '—';

/** Value for a datetime-local input. */
function localInput(t) {
  const d = new Date(t);
  const p = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function mount(root) {
  const R = {
    period: pref('reportPeriod', '7'),
    pick: pref('reportPick', 'all'),
    // Targets and groups left out of "all targets" or a group.
    except: { targets: [], groups: [], ...pref('reportExcept', {}) },
    minLost: pref('reportMinLost', 3),
    from: null, to: null,
    data: null, loading: false,
  };
  const end = now();
  R.to = end; R.from = end - 7 * DAY;

  root.innerHTML = `<div class="page-scroll"><div class="page report">
    <div class="head noprint"><div style="flex:1;min-width:0"><h1>${icon('file')} Report</h1>
      <div class="meta rp-intro">Uptime, packet loss and every outage over a period: to check how your connection did, or to send to your internet provider.</div></div>
      <div class="acts"><button class="btn" data-csv disabled>${icon('download')} Outages as CSV</button><button class="btn" data-print disabled>${icon('file')} Print or save as PDF</button></div></div>
    <div class="card noprint"><div class="card-b rp-controls">
      <div class="f"><label>Period</label><div class="seg" data-periods>${PERIODS.map(([k, l]) => `<button data-period="${k}">${l}</button>`).join('')}</div></div>
      <div class="f" data-custom hidden><label for="rp-from">From</label><input class="input mono" id="rp-from" type="datetime-local"></div>
      <div class="f" data-custom hidden><label for="rp-to">To</label><input class="input mono" id="rp-to" type="datetime-local"></div>
      <div class="f"><label for="rp-pick">Targets</label><select class="input" id="rp-pick"></select></div>
      <div class="f"><label for="rp-min">An outage is at least</label><span class="field"><input class="input num-in mono" id="rp-min" type="number" min="1" max="1000" style="width:70px"><span class="lbl">lost pings in a row</span></span></div>
      <button class="btn primary" data-go>Make report</button>
    </div><div class="targets-pick except" data-except hidden></div></div>
    <div data-out></div>
  </div></div>`;
  const q = s => root.querySelector(s);
  const out = q('[data-out]');

  function pickOptions() {
    const groups = [...new Set([...S.st.groups, ...S.st.targets.map(t => t.group).filter(Boolean)])];
    return `<option value="all">All targets (${S.st.targets.length})</option>` +
      (groups.length ? `<optgroup label="Groups">${groups.map(g => `<option value="g:${esc(g)}">${esc(g)} (${S.st.targets.filter(t => t.group === g).length})</option>`).join('')}</optgroup>` : '') +
      `<optgroup label="One target">${S.st.targets.map(t => `<option value="t:${t.id}">${esc(t.name)}</option>`).join('')}</optgroup>`;
  }

  // The targets of the picked option, before anything is left out.
  function picked() {
    if (R.pick.startsWith('g:')) return S.st.targets.filter(t => t.group === R.pick.slice(2));
    if (R.pick.startsWith('t:')) return S.st.targets.filter(t => t.id === R.pick.slice(2));
    return S.st.targets;
  }

  // What is left out of the current pick: the saved exceptions that apply
  // to it. Groups only apply to "all targets"; one target has none.
  function leftOut() {
    if (R.pick.startsWith('t:')) return { targets: [], groups: [] };
    const base = picked();
    const groups = R.pick === 'all' ? R.except.groups.filter(g => base.some(t => t.group === g)) : [];
    const targets = R.except.targets.filter(id => base.some(t => t.id === id && !(t.group && groups.includes(t.group))));
    return { targets, groups };
  }

  function chosen() {
    const x = leftOut();
    return picked().filter(t => !x.targets.includes(t.id) && !(t.group && x.groups.includes(t.group)));
  }

  function exceptRow() {
    const box = q('[data-except]');
    const x = leftOut(), left = chosen();
    box.hidden = R.pick.startsWith('t:') || picked().length < 2;
    if (box.hidden) return;
    // Something always stays in the report.
    const groupOpts = R.pick === 'all' ? [...new Set(left.map(t => t.group).filter(Boolean))]
      .filter(g => left.some(t => t.group !== g)).map(g => `<option value="g:${esc(g)}">Group: ${esc(g)}</option>`).join('') : '';
    const targetOpts = left.length > 1 ? left.map(t => `<option value="t:${t.id}">${esc(t.name)}</option>`).join('') : '';
    const any = x.targets.length + x.groups.length > 0;
    box.innerHTML = `<span class="ex-l">Leave out</span>` +
      x.groups.map(g => `<span class="tchip ex">${icon('tag')} ${esc(g)}<button data-rm-xg="${esc(g)}" aria-label="Put group ${esc(g)} back in the report">×</button></span>`).join('') +
      x.targets.map(id => { const t = S.byId.get(id); return t ? `<span class="tchip ex"><span class="sd" style="width:7px;height:7px"></span>${esc(t.name)}<button data-rm-xt="${id}" aria-label="Put ${esc(t.name)} back in the report">×</button></span>` : ''; }).join('') +
      (groupOpts || targetOpts ? `<select class="input" data-add-except aria-label="Leave a target or group out of the report" style="width:auto;height:28px"><option value="">${any ? '+ Leave out another' : '+ Leave out a target'}</option>${groupOpts}${targetOpts}</select>` : '') +
      (any ? '' : '<span class="muted ex-n">Like a PC that is switched off at night.</span>');
  }

  function saveExcept() {
    setPref('reportExcept', R.except);
    exceptRow();
    load();
  }

  function controls() {
    root.querySelectorAll('[data-period]').forEach(b => b.classList.toggle('on', b.dataset.period === R.period));
    root.querySelectorAll('[data-custom]').forEach(f => { f.hidden = R.period !== 'custom'; });
    const sel = q('#rp-pick');
    sel.innerHTML = pickOptions();
    if (![...sel.options].some(o => o.value === R.pick)) R.pick = 'all';
    sel.value = R.pick;
    exceptRow();
    q('#rp-min').value = R.minLost;
    if (R.period !== 'custom') {
      R.to = now(); R.from = R.to - +R.period * DAY;
    }
    q('#rp-from').value = localInput(R.from);
    q('#rp-to').value = localInput(R.to);
  }

  async function load() {
    const ids = chosen().map(t => t.id);
    if (!ids.length) { out.innerHTML = '<div class="empty"><p>No targets to report on yet.</p></div>'; return; }
    if (R.period === 'custom') {
      R.from = new Date(q('#rp-from').value).getTime();
      R.to = Math.min(now(), new Date(q('#rp-to').value).getTime());
      if (!(R.from < R.to)) { errorToast(new Error('Pick a "from" before the "to".')); return; }
    } else { R.to = now(); R.from = R.to - +R.period * DAY; }
    R.loading = true;
    out.innerHTML = `<div class="empty"><p>${icon('history')} Going through ${ids.length} target${ids.length === 1 ? '' : 's'}… a month of history can take a few seconds on a Raspberry Pi.</p></div>`;
    q('[data-csv]').disabled = q('[data-print]').disabled = true;
    try {
      const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
      const qs = new URLSearchParams({ from: Math.round(R.from), to: Math.round(R.to), ids: ids.join(','), minLost: R.minLost, tz });
      R.data = await api.get('report?' + qs);
      render();
      q('[data-csv]').disabled = q('[data-print]').disabled = false;
    } catch (err) {
      out.innerHTML = `<div class="empty"><p class="err-text">${esc(err.message)}</p></div>`;
    }
    R.loading = false;
  }

  function render() {
    const d = R.data;
    const ts = d.targets.filter(t => t.samples > 0);
    const multi = ts.length >= 2;
    const monitored = ts.reduce((n, t) => n + t.monitored, 0);
    const downtime = ts.reduce((n, t) => n + t.downtime, 0);
    const outages = ts.reduce((n, t) => n + t.outageCount, 0);
    const longest = Math.max(0, ...ts.map(t => t.longest));
    const got = ts.reduce((n, t) => n + (t.samples - t.lost), 0);
    const avg = got ? ts.reduce((n, t) => n + (t.avg > 0 ? t.avg * (t.samples - t.lost) : 0), 0) / got : -1;
    const uptime = monitored ? 100 * (monitored - downtime) / monitored : null;
    const intervals = [...new Set(ts.map(t => t.interval))].map(fmtInterval).join(', ');
    const x = leftOut();
    const outNames = [...x.groups.map(g => `group ${g}`), ...x.targets.map(id => S.byId.get(id)?.name).filter(Boolean)];
    const sel = (R.pick === 'all' ? 'all targets' : R.pick.startsWith('g:') ? `group ${R.pick.slice(2)}` : ts[0]?.name || 'one target') +
      (outNames.length ? ` except ${outNames.join(', ')}` : '');
    const offNames = d.targets.filter(t => t.offTimes).map(t => esc(t.name));
    const offNote = offNames.length ? `<p class="muted rp-note">Times these hosts are expected to be off are left out, as if they weren't pinged: ${offNames.join(', ')}.</p>` : '';
    const minutesNote = ts.some(t => t.minutes) ? '<p class="muted rp-note">Part of this period only has one-minute summaries (older than the raw history you keep), so outages there are known to about a minute.</p>' : '';

    const allAtOnce = multi ? `<div class="rp-together ${d.together.length ? 'bad' : 'good'}">
        ${icon(d.together.length ? 'bell' : 'check')}
        <div><b>${d.together.length ? `All ${ts.length} targets were down at the same time ${d.together.length === 1 ? 'once' : d.together.length + ' times'}, ${fmtDuration(d.togetherDowntime)} in total.` : `The ${ts.length} targets were never all down at the same time.`}</b>
        <span>${d.together.length ? 'When every target fails at once, the problem is usually the connection they share, such as your internet line.' : 'Outages below affected some of the targets, not all of them at once.'}</span></div></div>` : '';

    const rows = ts.map(t => `<tr>
        <td class="l"><b>${esc(t.name)}</b></td><td class="l mono dim">${esc(t.address)}</td>
        <td class="${t.uptime < 99.9 ? 'bad' : ''}">${pct(t.uptime, 3)}</td><td class="${t.loss >= 1 ? 'bad' : t.loss > 0 ? 'warn' : ''}">${pct(t.loss)}</td>
        <td>${fmtNum(t.outageCount)}</td><td>${dur(t.downtime)}</td><td>${dur(t.longest)}</td>
        <td>${ms(t.avg)}</td><td>${ms(t.p95)}</td><td>${ms(t.max)}</td></tr>`).join('');
    const empty = d.targets.filter(t => !t.samples).map(t => esc(t.name));

    // Hours of the day: loss and latency, to spot evening trouble.
    const hours = d.hours;
    const maxLoss = Math.max(0.5, ...hours.map(h => h.loss));
    const maxAvg = Math.max(1, ...hours.map(h => h.avg));
    const bars = (val, max, cls, label) => hours.map(h => {
      const v = val(h);
      return `<div class="rp-bar" title="${String(h.key).padStart(2, '0')}:00 – ${String(+h.key + 1).padStart(2, '0')}:00 · ${label(h)}"><i class="${cls}" style="height:${v > 0 ? Math.max(2, v / max * 100) : 0}%"></i></div>`;
    }).join('');

    const days = d.days.map(x => `<tr><td class="l mono">${esc(fmtDate(new Date(x.key + 'T12:00').getTime()))}</td><td class="${x.loss >= 1 ? 'bad' : x.loss > 0 ? 'warn' : ''}">${pct(x.loss)}</td><td>${ms(x.avg)}</td>
        <td>${x.outages || '—'}</td><td>${dur(x.downtime)}</td>${multi ? `<td class="${x.together ? 'bad' : ''}">${x.together || '—'}</td><td>${dur(x.togetherD)}</td>` : ''}</tr>`).join('');

    const list = outageList(d);
    const shown = list.slice(0, 500);
    const outs = shown.map(o => `<tr class="${o.all ? 'all' : ''}"><td class="l mono">${esc(fmtDateTime(o.start))}</td><td class="l mono">${esc(fmtDateTime(o.end))}</td>
        <td>${fmtDuration(o.end - o.start)}</td><td class="l">${o.all ? `<b>All ${ts.length} targets</b>` : esc(o.name)}</td><td>${o.lost || '—'}</td></tr>`).join('');

    out.innerHTML = `
      <div class="card rp-doc">
        <div class="card-b rp-title">
          <h2>Connection report</h2>
          <p><b>${esc(fmtDateTime(d.from))}</b> to <b>${esc(fmtDateTime(d.to))}</b> (${fmtDuration(d.to - d.from)}) · ${esc(sel)} · ${ts.length} target${ts.length === 1 ? '' : 's'} pinged every ${esc(intervals || '—')}</p>
          <p class="muted">Measured by OmniPing ${esc(d.version)} on <span class="mono">${esc(d.host)}</span>. Times in ${esc(d.zone)}. An outage is ${d.minLost} or more lost pings in a row; it lasts from the first lost ping to the next reply.</p>
        </div>
        <div class="stats">
          <div><div class="k">Uptime</div><div class="v">${pct(uptime, 3)}</div></div>
          <div><div class="k">Packet loss</div><div class="v">${pct(d.loss)}</div></div>
          <div><div class="k">Outages</div><div class="v">${fmtNum(outages)}</div></div>
          <div><div class="k">Downtime</div><div class="v">${dur(downtime)}</div></div>
          <div><div class="k">Longest outage</div><div class="v">${dur(longest)}</div></div>
          <div><div class="k">Average latency</div><div class="v">${ms(avg)}</div></div>
        </div>
        ${allAtOnce}
        ${minutesNote}${offNote}
      </div>

      <div class="card"><div class="card-h"><h3>Targets</h3>${empty.length ? `<span class="muted">No data in this period for ${empty.join(', ')}</span>` : ''}</div>
        <div class="rp-scroll"><table class="rtable"><thead><tr><th class="l">Target</th><th class="l">Address</th><th>Uptime</th><th>Loss</th><th>Outages</th><th>Downtime</th><th>Longest</th><th>Average</th><th>95% under</th><th>Highest</th></tr></thead>
        <tbody>${rows || '<tr><td colspan="10" class="l dim">No measurements in this period.</td></tr>'}</tbody></table></div></div>

      <div class="card rp-hours"><div class="card-h"><h3>Hours of the day</h3><span class="muted">All ${ts.length > 1 ? 'selected targets' : 'samples'} together. Problems that come back at the same hours (busy evenings, a nightly task) show up here.</span></div>
        <div class="card-b">
          <div class="rp-chart"><span class="rp-y">Packet loss<br><b>${pct(maxLoss)}</b></span><div class="rp-bars">${bars(h => h.loss, maxLoss, 'loss', h => `${pct(h.loss)} lost of ${fmtNum(h.samples)} pings`)}</div></div>
          <div class="rp-chart"><span class="rp-y">Average latency<br><b>${ms(maxAvg)}</b></span><div class="rp-bars">${bars(h => h.avg, maxAvg, 'lat', h => `average ${ms(h.avg)}`)}</div></div>
          <div class="rp-axis">${hours.map(h => `<span>${+h.key % 3 === 0 ? String(h.key).padStart(2, '0') : ''}</span>`).join('')}</div>
        </div></div>

      <div class="card"><div class="card-h"><h3>Per day</h3></div>
        <div class="rp-scroll"><table class="rtable"><thead><tr><th class="l">Day</th><th>Loss</th><th>Average</th><th>Outages</th><th>Downtime</th>${multi ? '<th>All down at once</th><th>For</th>' : ''}</tr></thead>
        <tbody>${days || `<tr><td colspan="${multi ? 7 : 5}" class="l dim">No measurements in this period.</td></tr>`}</tbody></table></div></div>

      ${d.traces?.length ? `<div class="card"><div class="card-h"><h3>Traceroutes when the internet dropped</h3><span class="muted">${d.traces.length}</span></div><div class="card-b rp-traces">
        ${d.traces.map(e => `<div class="rp-trace"><div class="mono muted">${esc(fmtDateTime(e.t))} · ${esc(e.ruleName)} · to ${esc(e.targetName)}</div>${traceBlock(e)}</div>`).join('')}</div></div>` : ''}
      <div class="card"><div class="card-h"><h3>Outages</h3><span class="muted">${list.length ? `${fmtNum(list.length)} in total${list.length > shown.length ? `, the newest ${shown.length} shown (the CSV has them all)` : ''}` : ''}</span></div>
        <div class="rp-scroll"><table class="rtable"><thead><tr><th class="l">Started</th><th class="l">Ended</th><th>Lasted</th><th class="l">Target</th><th>Pings lost</th></tr></thead>
        <tbody>${outs || '<tr><td colspan="5" class="l dim">No outages in this period.</td></tr>'}</tbody></table></div></div>`;
  }

  /** Every outage, newest first; "all at once" outages are listed too. */
  function outageList(d) {
    const list = [];
    for (const t of d.targets) for (const o of t.outages) list.push({ ...o, name: t.name });
    for (const o of d.together) list.push({ ...o, all: true });
    return list.sort((a, b) => b.start - a.start || (b.all ? 1 : 0) - (a.all ? 1 : 0));
  }

  function csv() {
    const d = R.data;
    const iso = t => new Date(t).toISOString();
    const lines = [['start', 'end', 'duration_s', 'target', 'address', 'pings_lost'].join(',')];
    const quote = s => `"${String(s).replace(/"/g, '""')}"`;
    for (const o of outageList(d).reverse()) {
      const t = d.targets.find(x => x.name === o.name);
      lines.push([iso(o.start), iso(o.end), ((o.end - o.start) / 1000).toFixed(1), quote(o.all ? 'All selected targets' : o.name), quote(o.all ? '' : t?.address || ''), o.lost || ''].join(','));
    }
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([lines.join('\r\n') + '\r\n'], { type: 'text/csv' }));
    a.download = `omniping-outages-${new Date(d.from).toISOString().slice(0, 10)}-${new Date(d.to).toISOString().slice(0, 10)}.csv`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  root.addEventListener('click', e => {
    const p = e.target.closest('[data-period]');
    if (p) { R.period = p.dataset.period; setPref('reportPeriod', R.period); controls(); if (R.period !== 'custom') load(); return; }
    if (e.target.closest('[data-go]')) { load(); return; }
    const xt = e.target.closest('[data-rm-xt]'), xg = e.target.closest('[data-rm-xg]');
    if (xt) { R.except.targets = R.except.targets.filter(id => id !== xt.dataset.rmXt); saveExcept(); return; }
    if (xg) { R.except.groups = R.except.groups.filter(g => g !== xg.dataset.rmXg); saveExcept(); return; }
    if (e.target.closest('[data-csv]') && R.data) { csv(); return; }
    if (e.target.closest('[data-print]') && R.data) window.print();
  });
  root.addEventListener('change', e => {
    if (e.target.matches('#rp-pick')) { R.pick = e.target.value; setPref('reportPick', R.pick); exceptRow(); load(); }
    if (e.target.matches('[data-add-except]') && e.target.value) {
      const kind = e.target.value.slice(0, 1), v = e.target.value.slice(2);
      const list = kind === 'g' ? R.except.groups : R.except.targets;
      if (!list.includes(v)) list.push(v);
      saveExcept();
    }
    if (e.target.matches('#rp-min')) { R.minLost = Math.max(1, Math.round(+e.target.value) || 3); setPref('reportMinLost', R.minLost); load(); }
  });

  controls();
  load().catch(errorToast);
  return { unmount() {} };
}
