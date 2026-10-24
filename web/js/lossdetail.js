// The story behind a red mark: when pings were lost, how many and for how
// long, whether other targets lost pings at the same moment, which alerts
// fired, and the graph around it with some normal pinging before and after.

import { S, settings, intervalOf, now, statsOf, offRanges } from './store.js';
import { fetchSamples } from './api.js';
import { drawGraph, drawCursor, valueAt, seg } from './graphs.js';
import { fmtDateTime, fmtTime, fmtDuration, fmtMs, fmtNum, esc } from './fmt.js';
import { modal } from './ui.js';

const MAX = 20000; // the most samples the server returns per target
const MINIS = 6;   // small graphs of other targets; the rest are named

/** A fetched series as a graph source. */
const srcOf = s => s.mode === 'raw' ? { raw: [seg(s.t, s.r)] } : { b: s, raw: [] };

/** Pings sent and lost by a fetched series between a and b. */
function lossIn(s, a, b) {
  let n = 0, l = 0;
  if (s.mode === 'raw') {
    for (let i = 0; i < s.t.length; i++) if (s.t[i] >= a && s.t[i] <= b) { n++; if (s.r[i] < 0) l++; }
  } else {
    for (let i = 0; i < s.n.length; i++) {
      const t0 = s.b0 + i * s.step;
      if (t0 + s.step > a && t0 <= b) { n += s.n[i]; l += s.lost[i]; }
    }
  }
  return { n, l };
}

/** Small graphs of the other targets that lost pings, under the big one. */
function minisHTML(list) {
  if (!list.length) return '';
  const rest = list.slice(MINIS).map(x => esc(x.name));
  return `<div class="ld-minis"><h4>Also lost pings then</h4>${list.slice(0, MINIS).map(x => `<div class="ld-mini">
      <div class="ld-mini-head"><span>${esc(x.name)}</span><b>${fmtNum(x.l)} of ${fmtNum(x.n)} lost</b></div>
      <div class="ld-mini-graph"><canvas class="base"></canvas><canvas class="ovl"></canvas></div>
    </div>`).join('')}${rest.length ? `<p class="ld-hint">${rest.length} more: ${rest.join(', ')}</p>` : ''}</div>`;
}

/** The small graphs' canvases with what to draw in them. */
function minis(body, list, from, to, focus) {
  return [...body.querySelectorAll('.ld-mini')].map((el, i) => ({
    base: el.querySelector('.base'), ovl: el.querySelector('.ovl'), src: srcOf(list[i].s),
    opts: { from, to, interval: intervalOf(list[i].t), s: settings(), layout: 'row', minY: 0.5, left: 46, focus, off: offRanges(list[i].t, from, to) },
  }));
}

/**
 * Draw a window's graphs: [{ base, ovl, src, opts }]. They share one cursor,
 * like the timelines: hover one and all of them show that moment.
 */
function drawLinked(graphs) {
  const show = t => { for (const x of graphs) drawCursor(x.ovl, x.g, t, t == null ? null : valueAt(x.src, x.g, t)); };
  for (const x of graphs) {
    x.g = drawGraph(x.base, x.src, x.opts);
    x.ovl.addEventListener('pointermove', e => {
      const g = x.g, r = x.base.getBoundingClientRect();
      show(g.from + Math.max(0, Math.min(1, (e.clientX - r.left - g.L) / g.pw)) * (g.to - g.from));
    });
    x.ovl.addEventListener('pointerleave', () => show(null));
    new ResizeObserver(() => { x.g = drawGraph(x.base, x.src, x.opts); }).observe(x.base);
  }
}

/**
 * The loss event around time t in samples T, R: the lost ping nearest to t
 * (within within ms), plus the pings lost around it. Losses less than a gap
 * apart are one event, so a stretch of 30% loss reads as one, not as single
 * pings. Returns sample indexes { a, b }, or a = -1 when nothing was lost.
 */
function findEvent(T, R, t, within, iv) {
  let a = -1, best = Infinity;
  for (let i = 0; i < T.length; i++) {
    const d = Math.abs(T[i] - t);
    if (R[i] < 0 && d <= within && d < best) { best = d; a = i; }
  }
  if (a < 0) return { a, b: a };
  const gap = Math.max(10 * iv, 30e3);
  let b = a;
  for (;;) {
    let k = a - 1;
    while (k >= 0 && R[k] >= 0 && T[a] - T[k] <= gap) k--;
    if (k >= 0 && R[k] < 0 && T[a] - T[k] <= gap) a = k; else break;
  }
  for (;;) {
    let k = b + 1;
    while (k < T.length && R[k] >= 0 && T[k] - T[b] <= gap) k++;
    if (k < T.length && R[k] < 0 && T[k] - T[b] <= gap) b = k; else break;
  }
  return { a, b };
}

/** hit: { t, width } from lossAt: the moment clicked and the time one pixel covers. */
export async function openLossDetail(id, hit) {
  const tg = S.byId.get(id);
  if (!tg) return;
  const iv = intervalOf(tg);
  const m = modal({
    title: `Lost pings · ${tg.name}`,
    body: '<p class="muted" style="margin:0">Looking up what happened…</p>',
    footer: `<a class="btn" href="#/target/${tg.id}" data-close>Open history</a><span class="spacer"></span><button class="btn primary" data-close>Close</button>`,
  });
  const say = html => { m.body.innerHTML = html; };
  try {
    // The samples around the click, to find where the loss starts and ends.
    // A long event can run past them; then look again, wider.
    const half = Math.max(hit.width, iv) / 2 + iv;
    const most = MAX * iv / 2.2;
    let look = Math.min(most, Math.max(15 * 60e3, hit.width * 4));
    let T, R, a, b, anchor = null;
    for (let tries = 0; ; tries++) {
      const from = hit.t - look, to = Math.min(now(), hit.t + look);
      const res = (await fetchSamples([id], from, to, MAX)).series[0];
      if (!res || res.mode !== 'raw' || !res.t.length) {
        say(`<p style="margin:0">Only one-minute summaries are kept for this moment, so the pings can't be shown one by one. The history page still shows the loss per minute.</p>`);
        return;
      }
      T = res.t; R = res.r;
      ({ a, b } = findEvent(T, R, anchor ?? hit.t, anchor == null ? half * 3 : iv, iv));
      if (a < 0) { say('<p style="margin:0">No lost pings were found at this moment.</p>'); return; }
      anchor ??= T[a];
      // Cut off: the event touches an edge of what was fetched, and there may
      // be more samples beyond that edge.
      const cutStart = a === 0 && T[0] <= from + iv * 2;
      const cutEnd = b === T.length - 1 && T[b] >= to - iv * 2 && to < now() - iv * 3;
      if (!(cutStart || cutEnd) || look >= most || tries >= 3) break;
      look = Math.min(most, look * 3);
    }
    const start = T[a];
    const reply = b + 1 < T.length ? T[b + 1] : null;
    const end = reply ?? T[b];
    let lost = 0, run = 0, longest = 0;
    for (let i = a; i <= b; i++) {
      if (R[i] < 0) { lost++; longest = Math.max(longest, ++run); } else run = 0;
    }
    const sent = b - a + 1;
    const avg = (t0, t1) => {
      let n = 0, sum = 0;
      for (let i = 0; i < T.length; i++) if (T[i] >= t0 && T[i] < t1 && R[i] >= 0) { n++; sum += R[i]; }
      return n ? fmtMs(sum / n) + ' ms' : '—';
    };

    // The graphs: the loss in the middle with normal pinging around it.
    const pad = Math.max(60e3, Math.min((end - start) * 1.5, 30 * 60e3));
    const from = start - pad, to = Math.min(end + pad, Math.max(now(), end + iv));

    // Did other targets lose pings at the same moment?
    const others = S.st.targets.filter(x => x.id !== id && !x.paused);
    const os = others.length ? (await fetchSamples(others.map(x => x.id), from, to, MAX)).series : [];
    const alsoLost = [], fine = [];
    for (const s of os) {
      const x = S.byId.get(s.id);
      const { n, l } = lossIn(s, start - iv, end + iv);
      if (!x || !n) continue;
      // Over a longer stretch every target loses the odd ping; it only counts
      // when it lost at least half as large a share as this one.
      if (l && (sent <= 5 || l / n >= lost / sent / 2)) alsoLost.push({ t: x, name: x.name, group: x.group, l, n, s });
      else fine.push(x);
    }
    let hint = '';
    if (others.length && !alsoLost.length) {
      hint = `No other target lost pings at that moment, so the problem was with ${esc(tg.name)} itself or the way to it.`;
    } else if (alsoLost.length && !fine.length) {
      hint = 'Every target lost pings at the same moment, so the problem was most likely close to OmniPing: its own network connection or the server it runs on.';
    } else if (alsoLost.length) {
      // Did whole groups keep answering while everything else failed, like
      // the local network staying up while the internet was gone?
      const okGroups = [...new Set(fine.map(x => x.group))];
      const hitGroups = new Set([tg.group, ...alsoLost.map(x => x.group)]);
      if (okGroups.every(g => g && !hitGroups.has(g))) {
        const names = okGroups.map(g => `<b>${esc(g)}</b>`).join(' and ');
        hint = `Everything outside ${names} lost pings, while ${names} kept answering. The problem was most likely past your own network: your internet connection or your provider.`;
      } else {
        hint = `${alsoLost.length} of ${others.length} other targets also lost pings at that moment.`;
      }
    }

    // Alerts around it.
    const evs = (S.events || []).filter(e => e.kind !== 'test' && (e.target === id || !e.target) && e.t >= start - 60e3 && e.t <= end + 10 * 60e3);

    const dur = reply ? fmtDuration(reply - start) : (now() - end < iv * 3 ? 'still going' : fmtDuration(end - start));
    const day = new Date(start).toDateString() === new Date(end).toDateString();
    say(`<div class="ld">
      <div class="ld-when">${esc(fmtDateTime(start))} – ${esc(day ? fmtTime(end) : fmtDateTime(end))}</div>
      <div class="ld-stats">
        <div><span>Lost pings</span><b>${lost} of ${sent}</b></div>
        <div><span>Lasted</span><b>${esc(dur)}</b></div>
        <div><span>Most in a row</span><b>${longest}</b></div>
        <div><span>Before · after</span><b>${avg(start - 120e3, start)} · ${reply ? avg(reply, reply + 120e3) : '—'}</b></div>
      </div>
      <div class="ld-graph"><canvas class="base"></canvas><canvas class="ovl"></canvas></div>
      ${hint ? `<p class="ld-hint">${hint}</p>` : ''}
      ${minisHTML(alsoLost)}
      ${evs.length ? `<div class="ld-list"><h4>Alerts</h4>${evs.slice().reverse().map(e => `<div><span>${esc(e.title)}</span><b>${esc(fmtTime(e.t))}</b></div>`).join('')}</div>` : ''}
    </div>`);

    const focus = [start - iv / 2, end + iv / 2];
    drawLinked([
      { base: m.body.querySelector('.ld-graph .base'), ovl: m.body.querySelector('.ld-graph .ovl'), src: { raw: [seg(T, R)] },
        opts: { from, to, interval: iv, s: settings(), layout: 'big', minY: 1, focus } },
      ...minis(m.body, alsoLost, from, to, focus),
    ]);
  } catch (err) {
    say(`<p style="margin:0">Could not load the details: ${esc(err.message || String(err))}</p>`);
  }
}

/**
 * A period selected on a timeline with Shift + drag: its numbers, the graph
 * with the period highlighted, who else lost pings then and the alerts.
 * onClose runs when the window closes (to clear the highlight).
 */
export async function openPeriodDetail(id, from, to, onClose) {
  const tg = S.byId.get(id);
  if (!tg) return;
  const iv = intervalOf(tg);
  to = Math.min(to, now());
  if (to - from < iv) to = from + iv;
  const sameDay = new Date(from).toDateString() === new Date(to).toDateString();
  const when = `${fmtDateTime(from)} – ${sameDay ? fmtTime(to) : fmtDateTime(to)}`;
  const m = modal({
    title: `${tg.name} · ${fmtDuration(to - from)}`,
    body: '<p class="muted" style="margin:0">Looking at this period…</p>',
    footer: `<a class="btn" href="#/target/${tg.id}" data-close>Open history</a><span class="spacer"></span><button class="btn primary" data-close>Close</button>`,
    onClose,
  });
  const say = html => { m.body.innerHTML = html; };
  try {
    // The period with a little before and after, for the graph.
    const pad = Math.max(30e3, (to - from) * 0.15);
    const gFrom = from - pad, gTo = Math.min(now(), to + pad);
    const res = (await fetchSamples([id], gFrom, gTo, MAX)).series[0];
    if (!res) { say('<p style="margin:0">No measurements in this period.</p>'); return; }

    // Its numbers: from every ping, or from one-minute summaries when the
    // period is too long or too old for those.
    let st, runs = 0, longest = 0, minutes = false;
    if (res.mode === 'raw') {
      let i0 = 0, i1 = res.t.length;
      while (i0 < i1 && res.t[i0] < from) i0++;
      while (i1 > i0 && res.t[i1 - 1] > to) i1--;
      st = statsOf(res.t, res.r, i0, i1);
      let run = 0;
      for (let i = i0; i < i1; i++) {
        if (res.r[i] < 0) { run++; longest = Math.max(longest, run); if (run === 3) runs++; } else run = 0;
      }
    } else {
      minutes = true;
      st = { n: 0, lost: 0, loss: 0, avg: null, min: null, max: null, jit: null };
      let sum = 0, got = 0;
      for (let i = 0; i < res.n.length; i++) {
        const t0 = res.b0 + i * res.step;
        if (t0 + res.step <= from || t0 > to) continue;
        st.n += res.n[i]; st.lost += res.lost[i];
        const g = res.n[i] - res.lost[i];
        if (g > 0 && res.avg[i] >= 0) {
          got += g; sum += res.avg[i] * g;
          st.min = st.min == null ? res.min[i] : Math.min(st.min, res.min[i]);
          st.max = st.max == null ? res.max[i] : Math.max(st.max, res.max[i]);
        }
      }
      if (st.n) st.loss = st.lost * 100 / st.n;
      if (got) st.avg = sum / got;
    }
    if (!st.n) { say('<p style="margin:0">No measurements in this period.</p>'); return; }
    const ms = v => v == null ? '—' : fmtMs(v) + ' ms';

    // Who else lost pings in the same period?
    const others = S.st.targets.filter(x => x.id !== id && !x.paused);
    const os = others.length ? (await fetchSamples(others.map(x => x.id), gFrom, gTo, MAX)).series : [];
    const alsoLost = [];
    for (const s of os) {
      const x = S.byId.get(s.id);
      const { n, l } = lossIn(s, from, to);
      if (x && l) alsoLost.push({ t: x, name: x.name, l, n, s });
    }
    alsoLost.sort((a, b) => b.l / b.n - a.l / a.n);

    const evs = (S.events || []).filter(e => e.kind !== 'test' && (e.target === id || !e.target) && e.t >= from && e.t <= to);
    say(`<div class="ld">
      <div class="ld-when">${esc(when)}${minutes ? ' · from one-minute summaries' : ''}</div>
      <div class="ld-stats">
        <div><span>Lost pings</span><b>${fmtNum(st.lost)} of ${fmtNum(st.n)}</b></div>
        <div><span>Loss</span><b>${st.loss && st.loss < 100 ? st.loss.toFixed(st.loss < 1 ? 2 : 1) + '%' : st.loss ? '100%' : '0%'}</b></div>
        <div><span>Average</span><b>${ms(st.avg)}</b></div>
        <div><span>Lowest · highest</span><b>${ms(st.min)} · ${ms(st.max)}</b></div>
        ${minutes ? '' : `<div><span>Jitter</span><b>${ms(st.jit)}</b></div>
        <div><span>Outages</span><b>${runs ? `${runs} (3+ in a row)` : 'none'}</b></div>
        <div><span>Most in a row</span><b>${longest || '—'}</b></div>
        <div><span>Longest outage</span><b>${longest >= 3 ? esc(fmtDuration(longest * iv)) : '—'}</b></div>`}
      </div>
      <div class="ld-graph"><canvas class="base"></canvas><canvas class="ovl"></canvas></div>
      ${others.length && !alsoLost.length ? '<p class="ld-hint">No other target lost pings in this period.</p>' : minisHTML(alsoLost)}
      ${evs.length ? `<div class="ld-list"><h4>Alerts</h4>${evs.slice().reverse().map(e => `<div><span>${esc(e.title)}</span><b>${esc(fmtTime(e.t))}</b></div>`).join('')}</div>` : ''}
    </div>`);

    drawLinked([
      { base: m.body.querySelector('.ld-graph .base'), ovl: m.body.querySelector('.ld-graph .ovl'), src: srcOf(res),
        opts: { from: gFrom, to: gTo, interval: iv, s: settings(), layout: 'big', minY: 1, focus: [from, to], off: offRanges(tg, gFrom, gTo) } },
      ...minis(m.body, alsoLost, gFrom, gTo, [from, to]),
    ]);
  } catch (err) {
    say(`<p style="margin:0">Could not load this period: ${esc(err.message || String(err))}</p>`);
  }
}
