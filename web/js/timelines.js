// A stack of timeline graphs that share one time window, one hover cursor
// and one data cache. Used by the monitor screen and pop-out windows.

import { S, series, settings, intervalOf, now, ensureLive, pref, setPref, addrOf, offRanges } from './store.js';
import { fetchSamples } from './api.js';
import { drawGraph, drawCursor, drawAxis, valueAt, seg, seriesSeg, lossAt, drawBand } from './graphs.js';
import { openLossDetail, openPeriodDetail } from './lossdetail.js';
import { fmtMs, fmtTime, fmtDateTime, esc } from './fmt.js';
import { icon, el } from './ui.js';

export const SPANS = [[60, '1m'], [300, '5m'], [600, '10m'], [1800, '30m'], [3600, '1h'], [21600, '6h'], [86400, '24h'], [172800, '48h'], [604800, '7d'], [2592000, '30d']];
const RAW_LIMIT = 4000;           // more samples than this per graph → ask the server for buckets
const MIN_SPAN = 30e3, MAX_SPAN = 90 * 86400e3;

/**
 * opts: {
 *   scroller: element that scrolls the rows (for lazy drawing)
 *   onFocus(t), onContext(e, id, t), onSelect(id), statsFor(id) → focus stats (for the band),
 *   prefKey: where to remember the span
 * }
 */
export class Timelines {
  constructor(opts) {
    this.o = opts;
    this.rows = [];             // { id, root, base, ovl, label, g, src, visible }
    this.span = pref(opts.prefKey || 'span', null);
    this.end = null;            // null = live
    this.cursor = null;
    this.cache = null;
    this.fetching = null;
    this.frame = 0;
    this.dirty = { graphs: false, overlay: false };
    this.io = new IntersectionObserver(entries => {
      for (const e of entries) {
        const row = this.rows.find(r => r.root === e.target);
        if (row) { row.visible = e.isIntersecting; if (row.visible) row.stale = true; }
      }
      this.request('graphs');
    }, { root: opts.scroller || null, rootMargin: '200px' });
  }

  spanMs() { return (this.span || settings().timelineSpanSec) * 1000; }
  live() { return this.end == null; }
  range() { const end = this.end ?? now(); return [end - this.spanMs(), end]; }

  setSpan(sec) {
    this.span = sec;
    setPref(this.o.prefKey || 'span', sec);
    const ms = sec * 1000;
    if (ms <= 3 * 3600e3) ensureLive(ms * 1.1).then(() => this.request('graphs'));
    this.cache = null;
    this.request('graphs');
    this.o.onRange && this.o.onRange();
  }

  goLive() { this.end = null; this.cache = null; this.request('graphs'); this.o.onRange && this.o.onRange(); }

  /** Build rows for these targets inside container. */
  build(container, targets, { selected, height } = {}) {
    for (const r of this.rows) this.io.unobserve(r.root);
    this.rows = [];
    container.innerHTML = '';
    for (const t of targets) {
      const root = el(`<div class="tl ${t.id === selected ? 'sel' : ''}" data-id="${t.id}" style="height:${height || settings().timelineHeight}px">
        <div class="tl-label" title="${esc(t.name)} (${esc(addrOf(t))})">
          <div class="n"><span class="sd"></span><span>${esc(t.name)}</span></div>
          <div class="s"></div>
          <div class="tools">
            <button data-act="hide" title="Hide this timeline graph">${icon('eyeOff')}</button>
            <button data-act="pop" title="Open in a new window">${icon('pop')}</button>
            <button data-act="history" title="Target history">${icon('history')}</button>
          </div>
        </div>
        <div class="tl-graph"><canvas class="base"></canvas><canvas class="ovl"></canvas></div>
      </div>`);
      container.append(root);
      const row = { id: t.id, root, base: root.querySelector('.base'), ovl: root.querySelector('.ovl'), label: root.querySelector('.s'), dot: root.querySelector('.sd'), graph: root.querySelector('.tl-graph'), visible: true, stale: true };
      this.rows.push(row);
      this.io.observe(root);
      this.wire(row);
    }
    this.request('graphs');
  }

  setSelected(id) {
    for (const r of this.rows) r.root.classList.toggle('sel', r.id === id);
  }

  timeAt(row, clientX) {
    const g = row.g;
    if (!g) return null;
    const rect = row.base.getBoundingClientRect();
    const x = clientX - rect.left - g.L;
    return g.from + Math.max(0, Math.min(1, x / g.pw)) * (g.to - g.from);
  }

  /** The red mark under the mouse, if any (see lossAt). */
  lossAt(row, clientX) {
    return lossAt(row.src, row.g, clientX - row.base.getBoundingClientRect().left);
  }

  wire(row) {
    const gEl = row.graph;
    let drag = null;
    gEl.addEventListener('pointermove', e => {
      if (drag && drag.select) {
        // Shift + drag: a period to inspect.
        this.selection.b = this.timeAt(row, e.clientX);
        if (Math.abs(e.clientX - drag.x) > 4) drag.moved = true;
        this.request('overlay');
        return;
      }
      if (drag) {
        const dx = e.clientX - drag.x;
        if (Math.abs(dx) > 3) drag.moved = true;
        if (drag.moved && row.g) {
          const end = drag.end - dx / row.g.pw * this.spanMs();
          this.end = end >= now() - 500 ? null : end;
          this.request('graphs');
        }
        return;
      }
      this.cursor = this.timeAt(row, e.clientX);
      gEl.classList.toggle('on-loss', !!this.lossAt(row, e.clientX));
      this.request('overlay');
    });
    gEl.addEventListener('pointerleave', () => { if (!drag) { this.cursor = null; this.request('overlay'); } });
    gEl.addEventListener('pointerdown', e => {
      if (e.button !== 0) return;
      // Shift + drag selects a period to look at closely (not on touch
      // screens, where a finger scrolls).
      if (e.shiftKey && e.pointerType !== 'touch' && row.g) {
        const t = this.timeAt(row, e.clientX);
        drag = { x: e.clientX, select: true, moved: false };
        this.selection = { id: row.id, a: t, b: t };
        gEl.setPointerCapture(e.pointerId);
        gEl.classList.add('selecting');
        return;
      }
      drag = { x: e.clientX, end: this.end ?? now(), moved: false };
      gEl.setPointerCapture(e.pointerId);
      gEl.classList.add('dragging');
    });
    const stop = e => {
      if (drag && drag.select) {
        const { moved } = drag;
        drag = null;
        gEl.classList.remove('selecting');
        const sel = this.selection;
        if (!moved || e.type !== 'pointerup') { this.selection = null; this.request('overlay'); return; }
        openPeriodDetail(row.id, Math.min(sel.a, sel.b), Math.max(sel.a, sel.b), () => { this.selection = null; this.request('overlay'); });
        return;
      }
      if (!drag) return;
      const moved = drag.moved;
      drag = null;
      gEl.classList.remove('dragging');
      if (moved) { this.o.onRange && this.o.onRange(); return; }
      this.o.onSelect && this.o.onSelect(row.id);
      // A click on a red mark explains it.
      const hit = this.lossAt(row, e.clientX);
      if (hit && e.type === 'pointerup') openLossDetail(row.id, hit);
    };
    gEl.addEventListener('pointerup', stop);
    gEl.addEventListener('pointercancel', stop);
    gEl.addEventListener('dblclick', e => {
      const t = this.timeAt(row, e.clientX);
      if (t != null && this.o.onFocus) this.o.onFocus(t);
    });
    gEl.addEventListener('wheel', e => {
      if (!row.g) return;
      const span = this.spanMs();
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        const t = this.timeAt(row, e.clientX);
        const f = e.deltaY > 0 ? 1.25 : 0.8;
        const ns = Math.max(MIN_SPAN, Math.min(MAX_SPAN, span * f));
        const end = this.end ?? now();
        let nend = t + (end - t) * (ns / span);
        if (this.live() && f < 1) nend = null; // zooming in while live stays live
        this.span = Math.round(ns / 1000);
        setPref(this.o.prefKey || 'span', this.span);
        this.end = nend == null || nend >= now() - 500 ? null : nend;
        this.cache = null;
        this.request('graphs');
        this.o.onRange && this.o.onRange();
      } else if (e.shiftKey && Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
        // Shift + wheel scrolls the list of timelines instead.
        if (!this.o.scroller) return;
        e.preventDefault();
        this.o.scroller.scrollTop += e.deltaY * (e.deltaMode === 1 ? 33 : 1);
      } else {
        // The wheel moves through time: down or right is later, up or left
        // earlier. Touchpads scroll sideways directly.
        e.preventDefault();
        const px = (Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY) * (e.deltaMode === 1 ? 33 : 1);
        const end = (this.end ?? now()) + px / row.g.pw * span * 1.5;
        this.end = end >= now() - 500 ? null : end;
        this.request('graphs');
        this.o.onRange && this.o.onRange();
      }
    }, { passive: false });
    gEl.addEventListener('contextmenu', e => {
      e.preventDefault();
      this.o.onContext && this.o.onContext(e, row.id, this.timeAt(row, e.clientX));
    });
    row.root.querySelector('.tools').addEventListener('click', e => {
      const b = e.target.closest('button');
      if (b && this.o.onTool) this.o.onTool(b.dataset.act, row.id);
    });
    row.root.querySelector('.tl-label').addEventListener('click', e => {
      if (!e.target.closest('button') && this.o.onSelect) this.o.onSelect(row.id);
    });
    row.root.querySelector('.tl-label').addEventListener('dblclick', e => {
      if (!e.target.closest('button')) location.hash = '#/target/' + row.id;
    });
  }

  /** Schedule a redraw. kind: 'graphs' (everything) or 'overlay' (cursor only). */
  request(kind = 'graphs') {
    this.dirty[kind] = true;
    if (kind === 'graphs') this.dirty.overlay = true;
    if (!this.frame) this.frame = requestAnimationFrame(() => { this.frame = 0; this.draw(); });
  }

  /** Data to draw for one target over [from, to]. */
  sourceFor(t, from, to) {
    const s = series(t.id);
    const iv = intervalOf(t);
    const covers = s.length && s.t[0] <= from + iv * 2;
    if (covers && (to - from) / iv <= RAW_LIMIT) return { raw: [seriesSeg(s, from, to)] };
    const c = this.cache, res = c && c.data.get(t.id);
    if (!res) return { raw: [seriesSeg(s, from, to)] };
    const tailFrom = Math.max(from, c.fetchedTo + 1);
    const tail = seriesSeg(s, tailFrom, to);
    if (res.mode === 'raw') return { raw: [seg(res.t, res.r), tail] };
    return { b: res, raw: [tail], rawFrom: c.fetchedTo + 1 };
  }

  needsCache(from, to) {
    for (const r of this.rows) {
      const t = S.byId.get(r.id);
      if (!t) continue;
      const s = series(t.id), iv = intervalOf(t);
      const covers = s.length && s.t[0] <= from + iv * 2;
      if (!covers || (to - from) / iv > RAW_LIMIT) return true;
    }
    return false;
  }

  /** Fetch server data for the window when the live buffers are not enough. */
  ensureCache(from, to, width) {
    if (!this.rows.length || !this.needsCache(from, to)) { this.cache = null; return; }
    const span = to - from;
    const c = this.cache;
    const fresh = c && Math.abs(c.width - width) < width * 0.15 && Math.abs(c.span - span) < span * 0.01 && c.from <= from &&
      (this.live() ? now() - c.fetchedTo < Math.min(20 * 60e3, span / 4) : c.to >= to);
    if (fresh || this.fetching) return;
    const reqFrom = from - span * 0.5, reqTo = this.live() ? now() : Math.min(now(), to + span * 0.5);
    const ids = this.rows.map(r => r.id);
    const key = `${reqFrom}|${reqTo}`;
    this.fetching = key;
    fetchSamples(ids, reqFrom, reqTo, width * (reqTo - reqFrom) / span)
      .then(res => {
        if (this.fetching !== key) return;
        this.cache = { from: reqFrom, to: reqTo, fetchedTo: res.to, span, width, data: new Map(res.series.map(x => [x.id, x])) };
        this.request('graphs');
      })
      .catch(err => console.error(err))
      .finally(() => { if (this.fetching === key) this.fetching = null; });
  }

  draw() {
    const [from, to] = this.range();
    if (this.dirty.graphs) {
      this.dirty.graphs = false;
      const width = this.rows[0]?.graph.clientWidth || 800;
      this.ensureCache(from, to, Math.max(100, width - 46));
      const s = settings();
      const comments = S.st.comments || [];
      for (const row of this.rows) {
        if (!row.visible) continue;
        const t = S.byId.get(row.id);
        if (!t) continue;
        row.src = this.sourceFor(t, from, to);
        const fst = this.o.statsFor ? this.o.statsFor(t.id) : null;
        row.g = drawGraph(row.base, row.src, {
          from, to, interval: intervalOf(t), s, layout: 'row', minY: 0.5,
          focus: fst && fst.n ? [fst.from - intervalOf(t) / 2, fst.to + intervalOf(t) / 2] : null,
          comments: comments.filter(c => !c.target || c.target === t.id),
          off: offRanges(t, from, to),
        });
        row.label.textContent = labelText(t, row.src, from, to);
        if (this.o.levelFor) row.dot.className = 'sd ' + this.o.levelFor(t.id);
      }
      if (this.axis) drawAxis(this.axis, from, to, this.cursor, this.live());
      this.o.onDraw && this.o.onDraw(from, to);
    }
    if (this.dirty.overlay) {
      this.dirty.overlay = false;
      for (const row of this.rows) {
        if (!row.visible || !row.g) continue;
        drawCursor(row.ovl, row.g, this.cursor, this.cursor != null ? valueAt(row.src, row.g, this.cursor) : null);
        const sel = this.selection;
        if (sel && sel.id === row.id) drawBand(row.ovl, row.g, Math.min(sel.a, sel.b), Math.max(sel.a, sel.b));
      }
      if (this.axis) drawAxis(this.axis, from, to, this.cursor, this.live());
    }
  }

  destroy() {
    if (this.frame) cancelAnimationFrame(this.frame);
    this.io.disconnect();
  }
}

/** "1.1.1.1 · avg 8.9 ms · 0.4% loss" over what the graph shows. */
function labelText(t, src, from, to) {
  let n = 0, lost = 0, sum = 0, got = 0;
  if (src.b) {
    const b = src.b;
    for (let i = 0; i < b.n.length; i++) {
      const t0 = b.b0 + i * b.step;
      if (t0 + b.step <= from || t0 > to || (src.rawFrom != null && t0 >= src.rawFrom)) continue;
      n += b.n[i]; lost += b.lost[i];
      const g = b.n[i] - b.lost[i];
      if (g > 0 && b.avg[i] >= 0) { got += g; sum += b.avg[i] * g; }
    }
  }
  for (const sg of src.raw || []) for (let i = sg.i0; i < sg.i1; i++) {
    if (sg.t[i] < from || sg.t[i] > to) continue;
    n++;
    if (sg.r[i] < 0) lost++; else { got++; sum += sg.r[i]; }
  }
  const addr = addrOf(t);
  if (!n) return `${addr} · no samples here`;
  return `${addr} · avg ${got ? fmtMs(sum / got) + ' ms' : '—'} · ${(lost * 100 / n).toFixed(1)}% loss`;
}

/** Text for the "sample set" line. */
export function rangeText(from, to) {
  if (new Date(from).toDateString() === new Date(to).toDateString()) return `${fmtDateTime(from)} – ${fmtTime(to)}`;
  return `${fmtDateTime(from)} – ${fmtDateTime(to)}`;
}
