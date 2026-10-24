// Canvas renderers: timelines, the history chart, the table's graph column,
// sparklines and the 7-day overview. Colours come from the CSS theme tokens,
// so every graph follows light and dark mode.

import { scaleFmt, fmtTime, fmtDayMonth, fmtWeekday, fmtMs } from './fmt.js';

let P = null;
const TOKENS = ['bg', 'panel', 'panel-2', 'line', 'grid', 'fg', 'muted', 'dim', 'ink', 'accent', 'focus', 'focus-edge',
  'loss', 'loss-soft', 'good', 'warn', 'bad', 'zone-good', 'zone-warn', 'zone-bad', 'off', 'mono', 'sans', 'accent-soft'];

/** Re-read colours after a theme change. */
export function refreshPalette() {
  const cs = getComputedStyle(document.documentElement);
  P = {};
  for (const k of TOKENS) P[k] = cs.getPropertyValue('--' + k).trim();
}
export function pal() { if (!P) refreshPalette(); return P; }

/** Size a canvas for the screen's pixel density and clear it. */
export function setup(cv) {
  const dpr = window.devicePixelRatio || 1;
  const W = cv.clientWidth, H = cv.clientHeight;
  const bw = Math.max(1, Math.round(W * dpr)), bh = Math.max(1, Math.round(H * dpr));
  if (cv.width !== bw || cv.height !== bh) { cv.width = bw; cv.height = bh; }
  const c = cv.getContext('2d');
  c.setTransform(dpr, 0, 0, dpr, 0, 0);
  c.clearRect(0, 0, W, H);
  return { c, W, H };
}

const NICE = [0.1, 0.2, 0.5, 1, 2, 3, 5, 10, 15, 20, 30, 50, 75, 100, 150, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 10000, 20000, 30000, 60000];
export const niceCeil = v => NICE.find(n => n >= v) ?? Math.ceil(v);

const TSTEPS = [1e3, 2e3, 5e3, 10e3, 15e3, 30e3, 60e3, 120e3, 300e3, 600e3, 900e3, 1800e3, 3600e3, 7200e3, 10800e3, 21600e3, 43200e3, 86400e3, 172800e3, 604800e3];
export function tickStep(spanMs, px, minGap = 80) {
  for (const s of TSTEPS) if (px * s / spanMs >= minGap) return s;
  return TSTEPS[TSTEPS.length - 1];
}
/** Tick times aligned to the local clock (whole minutes, hours, midnights). */
export function ticks(from, to, step) {
  const off = -new Date(from).getTimezoneOffset() * 60000;
  const out = [];
  for (let t = Math.ceil((from + off) / step) * step - off; t <= to; t += step) out.push(t);
  return out;
}
export function tickLabel(t, step) {
  const d = new Date(t);
  if (step >= 86400e3 || (step >= 3600e3 && d.getHours() === 0 && d.getMinutes() === 0)) return `${fmtWeekday(t)} ${fmtDayMonth(t)}`;
  return fmtTime(t, step < 60e3);
}

function line(c, x0, y0, x1, y1) { c.beginPath(); c.moveTo(x0, y0); c.lineTo(x1, y1); c.stroke(); }

export function pill(c, x, y, text, bg, fg, border, align = 'left') {
  const p = pal();
  c.font = `500 10.5px ${p.mono}`;
  const w = c.measureText(text).width + 10, h = 16;
  if (align === 'right') x -= w;
  c.fillStyle = bg;
  c.beginPath(); c.roundRect(x, y - h / 2, w, h, 4); c.fill();
  if (border) { c.strokeStyle = border; c.lineWidth = 1; c.stroke(); }
  c.fillStyle = fg; c.textBaseline = 'middle'; c.textAlign = 'left';
  c.fillText(text, x + 5, y + 0.5);
  return w;
}

/** A slice [i0, i1) of sorted arrays t (ms) and r (ms, <0 = lost). */
export function seg(t, r, i0 = 0, i1 = t.length) { return { t, r, i0, i1 }; }
export function seriesSeg(s, from, to) { return seg(s.t, s.r, s.lower(from), s.lower(to + 1)); }

/**
 * Fold data into one column per pixel. Columns are fixed slices of clock time,
 * not of the screen, so a sample stays in its column while the graph scrolls
 * and the line moves smoothly instead of reshuffling. Column j starts at pixel
 * j + off (off is between -1 and 0); there is one column more than pixels
 * because the first and last can be partly outside. src.b holds server
 * buckets, src.raw raw slices; buckets that start at or after src.rawFrom are
 * skipped because raw samples cover that part.
 */
export function columns(src, from, to, W) {
  const colMs = Math.max(1, Math.round(to - from)) / W;
  const c0 = Math.floor(from / colMs);
  const col = t => Math.floor(t / colMs) - c0;
  const N = W + 1;
  const n = new Int32Array(N), lost = new Int32Array(N), got = new Int32Array(N);
  const mn = new Float32Array(N).fill(Infinity), mx = new Float32Array(N).fill(-Infinity), sum = new Float64Array(N);
  const b = src.b;
  if (b) {
    for (let i = 0; i < b.n.length; i++) {
      if (!b.n[i]) continue;
      const t0 = b.b0 + i * b.step, t1 = t0 + b.step;
      if (t1 <= from || t0 > to) continue;
      if (src.rawFrom != null && t0 >= src.rawFrom) continue;
      const j0 = Math.max(0, col(t0)), j1 = Math.min(N - 1, Math.max(col(t0), col(t1 - 1)));
      const g = b.n[i] - b.lost[i];
      for (let j = j0; j <= j1; j++) {
        n[j] += b.n[i]; lost[j] += b.lost[i];
        if (g > 0 && b.avg[i] >= 0) {
          got[j] += g; sum[j] += b.avg[i] * g;
          if (b.min[i] < mn[j]) mn[j] = b.min[i];
          if (b.max[i] > mx[j]) mx[j] = b.max[i];
        }
      }
    }
  }
  for (const s of src.raw || []) {
    for (let i = s.i0; i < s.i1; i++) {
      const t = s.t[i];
      if (t < from || t > to) continue;
      const j = col(t);
      if (j < 0 || j >= N) continue;
      n[j]++;
      const r = s.r[i];
      if (r < 0) { lost[j]++; continue; }
      got[j]++; sum[j] += r;
      if (r < mn[j]) mn[j] = r;
      if (r > mx[j]) mx[j] = r;
    }
  }
  return { n, lost, got, mn, mx, sum, W: N, off: (c0 * colMs - from) / colMs, colMs, c0 };
}

/**
 * Red bars for packet loss per column, from the bottom: full height is the
 * loss scale, and any loss at all gets at least a short stub, so one lost ping
 * among thousands stays visible however far you zoom out.
 */
function lossColumns(c, cols, x0, top, bot, lossScale) {
  const ph = bot - top, w = 2, stub = Math.min(ph, Math.max(6, Math.min(ph * .3, 14)));
  c.globalAlpha = .85;
  for (let j = 0; j < cols.W; j++) {
    if (!cols.lost[j]) continue;
    const h = Math.max(Math.min(cols.lost[j] * 100 / cols.n[j] / lossScale, 1) * ph, stub);
    c.fillRect(x0 + j + cols.off, bot - h, w, h);
  }
  c.globalAlpha = 1;
}

/**
 * Draw a latency graph.
 * o: { from, to, interval, s: settings, focus: [a,b], comments: [{t,text}], minY, layout: 'row' | 'big',
 *      off: [[a,b]] the times the target is expected to be off,
 *      left: room on the left, to line a row graph up with a big one above it }
 * Returns the geometry, used for hover and click handling.
 */
export function drawGraph(cv, src, o) {
  const { c, W, H } = setup(cv);
  const p = pal(), s = o.s;
  const big = o.layout === 'big';
  const L = o.left ?? (big ? 46 : 0), R = 46, T = big ? 30 : 5, B = big ? 24 : 3;
  const pw = Math.max(1, Math.floor(W - L - R)), ph = Math.max(1, H - T - B), bot = T + ph;
  const span = Math.max(1, o.to - o.from);
  const X = t => L + (t - o.from) / span * pw;
  const interval = Math.max(o.interval || 1000, 50);
  // Up to a few samples per pixel, every sample is drawn where it belongs;
  // beyond that they are folded into one column per pixel.
  const pointMode = !src.b && span / interval / pw <= 3;

  let ymaxData = 0, cols = null;
  if (pointMode) {
    for (const sg of src.raw || []) for (let i = sg.i0; i < sg.i1; i++) {
      const t = sg.t[i], r = sg.r[i];
      if (t >= o.from && t <= o.to && r > ymaxData) ymaxData = r;
    }
  } else {
    cols = columns(src, o.from, o.to, pw);
    for (let j = 0; j < cols.W; j++) if (cols.got[j]) {
      const v = s.drawMinMax ? cols.mx[j] : cols.sum[j] / cols.got[j];
      if (v > ymaxData) ymaxData = v;
    }
  }
  const ymax = s.scaleMode === 'fixed' ? s.scaleMs : niceCeil(Math.max(o.minY ?? 0.5, ymaxData * 1.08));
  const Y = v => bot - Math.min(Math.max(v, 0) / ymax, 1) * ph;

  if (s.zoneTint) {
    const band = (a, b, col) => {
      const y0 = Y(Math.min(a, ymax)), y1 = Y(Math.min(b, ymax));
      if (y0 > y1) { c.fillStyle = col; c.fillRect(L, y1, pw, y0 - y1); }
    };
    band(0, s.warnMs, p['zone-good']);
    if (ymax > s.warnMs) band(s.warnMs, s.critMs, p['zone-warn']);
    if (ymax > s.critMs) band(s.critMs, ymax, p['zone-bad']);
  }
  // Expected-off times: a grey band, so a gap there reads as normal.
  for (const [a, b] of o.off || []) {
    const x0 = X(a), x1 = X(b);
    c.fillStyle = p.off; c.fillRect(x0, T, Math.max(1, x1 - x0), ph);
    if (big && x1 - x0 > 90) {
      c.fillStyle = p.muted; c.font = `11px ${p.sans}`; c.textAlign = 'center';
      c.fillText('expected off', (x0 + x1) / 2, T + 13);
    }
  }

  // grid
  const step = tickStep(span, pw, big ? 90 : 80);
  c.strokeStyle = p.grid; c.lineWidth = 1;
  const tks = ticks(o.from, o.to, step);
  for (const t of tks) { const x = Math.round(X(t)) + .5; line(c, x, T, x, bot); }
  const fracs = big ? [0, .25, .5, .75, 1] : [.5, 1];
  for (const f of fracs) { const y = Math.round(Y(ymax * f)) + .5; line(c, L, y, L + pw, y); }

  // scale labels
  c.font = `10px ${p.mono}`; c.textBaseline = 'middle';
  if (big) {
    c.fillStyle = p.dim; c.textAlign = 'right';
    for (const f of fracs) c.fillText(scaleFmt(ymax * f) + (f === 1 ? ' ms' : ''), L - 6, Y(ymax * f));
    if (s.lossScale > 0) {
      // With every ping drawn on its own, a red bar is one lost ping and
      // always full height, so a percentage scale would be wrong there.
      c.textAlign = 'left'; c.fillStyle = p.loss; c.globalAlpha = .8;
      if (pointMode) c.fillText('lost', L + pw + 6, Y(ymax));
      else for (const f of fracs) if (Number.isInteger(s.lossScale * f)) c.fillText(s.lossScale * f + '%', L + pw + 6, Y(ymax * f));
      c.globalAlpha = 1;
    }
    c.fillStyle = p.dim; c.textAlign = 'center'; c.font = `10.5px ${p.mono}`;
    for (const t of tks) {
      const x = X(t), lab = tickLabel(t, step);
      const w = c.measureText(lab).width / 2;
      if (x - w < L - 20 || x + w > L + pw + 20) continue;
      const d = new Date(t);
      if (step < 86400e3 && d.getHours() === 0 && d.getMinutes() === 0) { c.fillStyle = p.fg; c.font = `600 10.5px ${p.mono}`; }
      c.fillText(lab, x, bot + 13);
      c.fillStyle = p.dim; c.font = `10.5px ${p.mono}`;
    }
  } else {
    c.fillStyle = p.dim; c.textAlign = 'left';
    c.fillText(scaleFmt(ymax) + ' ms', L + pw + 6, Y(ymax) + 5);
    c.fillText(scaleFmt(ymax / 2), L + pw + 6, Y(ymax / 2));
  }

  // focus band
  if (o.focus) {
    const x0 = Math.max(L, X(o.focus[0])), x1 = Math.min(L + pw, X(o.focus[1]));
    if (x1 > x0 - 1) {
      c.fillStyle = p.focus; c.fillRect(x0, T - (big ? 0 : 5), Math.max(2, x1 - x0), ph + (big ? 0 : 8));
      c.strokeStyle = p['focus-edge']; c.lineWidth = 1;
      for (const x of [x0, x1]) { const xx = Math.round(x) + .5; line(c, xx, T, xx, bot); }
    }
  }

  // Columns at the edges can stick out a little; keep them inside the plot.
  c.save();
  c.beginPath(); c.rect(L, 0, pw, H); c.clip();

  // packet loss: red bars from the bottom, full height = loss scale
  if (s.lossScale > 0) {
    c.fillStyle = p.loss;
    if (pointMode) {
      c.globalAlpha = .85;
      const w = Math.max(1.5, pw / (span / interval) * .8);
      for (const sg of src.raw || []) for (let i = sg.i0; i < sg.i1; i++) {
        const t = sg.t[i];
        if (sg.r[i] < 0 && t >= o.from && t <= o.to) c.fillRect(X(t) - w / 2, T, w, ph);
      }
      c.globalAlpha = 1;
    } else {
      lossColumns(c, cols, L, T, bot, s.lossScale);
    }
  }

  // latency
  c.lineJoin = 'round'; c.lineCap = 'round';
  if (pointMode) {
    let pts = [];
    const flush = () => {
      if (pts.length > 1) {
        c.beginPath(); c.moveTo(pts[0][0], bot);
        for (const [x, y] of pts) c.lineTo(x, y);
        c.lineTo(pts[pts.length - 1][0], bot); c.closePath();
        c.fillStyle = p.ink; c.globalAlpha = .07; c.fill(); c.globalAlpha = 1;
        c.beginPath(); pts.forEach(([x, y], i) => i ? c.lineTo(x, y) : c.moveTo(x, y));
        c.strokeStyle = p.ink; c.lineWidth = 1.25; c.stroke();
      } else if (pts.length === 1) {
        c.fillStyle = p.ink; c.beginPath(); c.arc(pts[0][0], pts[0][1], 1.5, 0, 7); c.fill();
      }
      pts = [];
    };
    let prevT = -Infinity;
    for (const sg of src.raw || []) for (let i = sg.i0; i < sg.i1; i++) {
      const t = sg.t[i];
      if (t < o.from || t > o.to) continue;
      if (t - prevT > interval * 2.5) flush(); // no samples here (server was stopped): leave a gap
      prevT = t;
      if (sg.r[i] < 0) flush(); else pts.push([X(t), Y(sg.r[i])]);
    }
    flush();
  } else {
    const x0 = L + cols.off;
    if (s.drawMinMax) {
      c.fillStyle = p.ink; c.globalAlpha = big ? .13 : .16;
      for (let j = 0; j < cols.W; j++) if (cols.got[j]) {
        const y1 = Y(cols.mx[j]), y0 = Y(cols.mn[j]);
        c.fillRect(x0 + j, y1, 1, Math.max(1, y0 - y1));
      }
      c.globalAlpha = 1;
    }
    c.strokeStyle = p.ink; c.lineWidth = 1.2; c.beginPath();
    let pen = false;
    for (let j = 0; j < cols.W; j++) {
      if (!cols.got[j]) { pen = false; continue; }
      const y = Y(cols.sum[j] / cols.got[j]);
      if (pen) c.lineTo(x0 + j + .5, y); else c.moveTo(x0 + j + .5, y);
      pen = true;
    }
    c.stroke();
  }
  c.restore();

  // comments
  for (const cm of o.comments || []) {
    if (cm.t < o.from || cm.t > o.to) continue;
    const x = Math.round(X(cm.t)) + .5;
    c.strokeStyle = p.accent; c.lineWidth = 1; c.setLineDash([2, 3]);
    line(c, x, big ? T - 10 : 0, x, bot); c.setLineDash([]);
    if (big) {
      c.font = `500 11px ${p.sans}`;
      const text = cm.text.length > 40 ? cm.text.slice(0, 39) + '…' : cm.text;
      const w = c.measureText(text).width + 14;
      const fx = Math.min(x, L + pw - w);
      c.fillStyle = p['accent-soft']; c.beginPath(); c.roundRect(fx, 3, w, 19, 4); c.fill();
      c.fillStyle = p.accent; c.fillRect(x - .5, 3, 2, 19);
      c.textAlign = 'left'; c.textBaseline = 'middle'; c.fillText(text, fx + 8, 13);
    } else {
      c.fillStyle = p.accent; c.beginPath(); c.moveTo(x - 4, 0); c.lineTo(x + 4, 0); c.lineTo(x, 6); c.closePath(); c.fill();
    }
  }

  return { from: o.from, to: o.to, L, T, R, pw, ph, bot, W, H, ymax, pointMode, cols, interval, X, Y };
}

/** Value under the mouse: nearest sample (short spans) or the pixel's summary. */
export function valueAt(src, g, t) {
  if (!g || t < g.from || t > g.to) return null;
  if (g.pointMode) {
    let bs = null, bi = -1, bd = Infinity;
    for (const sg of src.raw || []) {
      let lo = sg.i0, hi = sg.i1;
      while (lo < hi) { const m = (lo + hi) >> 1; if (sg.t[m] < t) lo = m + 1; else hi = m; }
      for (const i of [lo - 1, lo]) {
        if (i < sg.i0 || i >= sg.i1) continue;
        const d = Math.abs(sg.t[i] - t);
        if (d < bd) { bd = d; bs = sg; bi = i; }
      }
    }
    if (!bs || bd > g.interval) return null;
    if (bs.r[bi] >= 0) return { t: bs.t[bi], v: bs.r[bi] };
    // How many were lost in a row with this one.
    let a = bi, b = bi;
    while (a > 0 && bs.r[a - 1] < 0) a--;
    while (b < bs.r.length - 1 && bs.r[b + 1] < 0) b++;
    return { t: bs.t[bi], lost: true, run: b - a + 1 };
  }
  const cl = g.cols;
  if (!cl) return null;
  const j = Math.floor(t / cl.colMs) - cl.c0;
  if (j < 0 || j >= cl.W || !cl.n[j]) return null;
  const loss = cl.lost[j] * 100 / cl.n[j];
  if (!cl.got[j]) return { t, lost: true, loss: 100, n: cl.n[j], nLost: cl.lost[j] };
  return { t, v: cl.sum[j] / cl.got[j], min: cl.mn[j], max: cl.mx[j], loss, n: cl.n[j], nLost: cl.lost[j] };
}

/** "0.4%" or "12%": small shares keep a decimal, so they don't read as 0%. */
function pct(v) { return (v < 1 ? v.toFixed(1) : Math.round(v)) + '%'; }

/**
 * The red mark under canvas position x (CSS pixels), if any within tol pixels:
 * { t, width } with the moment to look at and the time one pixel covers there.
 */
export function lossAt(src, g, x, tol = 4) {
  if (!g || !src) return null;
  if (g.pointMode) {
    // Zoomed in, a bar is wider than tol: anywhere on it counts.
    let best = null, bd = Math.max(tol, g.pw / ((g.to - g.from) / g.interval) * .4) + .5;
    for (const sg of src.raw || []) for (let i = sg.i0; i < sg.i1; i++) {
      const t = sg.t[i];
      if (sg.r[i] >= 0 || t < g.from || t > g.to) continue;
      const d = Math.abs(g.X(t) - x);
      if (d < bd) { bd = d; best = t; }
    }
    return best == null ? null : { t: best, width: g.interval };
  }
  const cl = g.cols;
  if (!cl) return null;
  const j = Math.floor(x - g.L - cl.off);
  for (let d = 0; d <= tol; d++) {
    for (const k of d ? [j - d, j + d] : [j]) {
      if (k >= 0 && k < cl.W && cl.lost[k]) return { t: (cl.c0 + k + .5) * cl.colMs, width: cl.colMs };
    }
  }
  return null;
}

/** A selected period on an overlay that drawCursor just drew. */
export function drawBand(ov, g, a, b) {
  if (!g || b < g.from || a > g.to) return;
  const c = ov.getContext('2d'), p = pal();
  const x0 = Math.max(g.L, g.X(a)), x1 = Math.min(g.L + g.pw, g.X(b));
  c.fillStyle = p['accent-soft']; c.fillRect(x0, 0, Math.max(1, x1 - x0), ov.clientHeight);
  c.strokeStyle = p.accent; c.lineWidth = 1;
  line(c, Math.round(x0) + .5, 0, Math.round(x0) + .5, ov.clientHeight);
  line(c, Math.round(x1) - .5, 0, Math.round(x1) - .5, ov.clientHeight);
}

/**
 * Hover overlay: dashed cursor line plus the value. A lost ping says how many
 * were lost in a row; where a pixel holds several pings, it says what share
 * of them was lost.
 */
export function drawCursor(ov, g, t, val) {
  const { c, H } = setup(ov);
  if (!g || t == null || t < g.from || t > g.to) return;
  const p = pal();
  const x = Math.round(g.X(t)) + .5;
  c.strokeStyle = p.accent; c.lineWidth = 1; c.setLineDash([3, 3]);
  line(c, x, 0, x, H); c.setLineDash([]);
  if (!val) return;
  const right = x > g.L + g.pw - 90;
  if (val.lost) {
    const text = val.n > 1 ? `100% lost · ${val.nLost} of ${val.n}` : val.run > 1 ? `lost · ${val.run} in a row` : 'lost';
    pill(c, right ? x - 5 : x + 5, H / 2, text, p.loss, '#fff', null, right ? 'right' : 'left');
    return;
  }
  const y = g.Y(val.v);
  c.fillStyle = p.accent; c.beginPath(); c.arc(g.pointMode ? g.X(val.t) : x, y, 2.8, 0, 7); c.fill();
  let text = fmtMs(val.v) + ' ms';
  if (val.loss) text += ` · ${pct(val.loss)} lost (${val.nLost} of ${val.n})`;
  const yy = Math.max(g.T + 8, Math.min(g.bot - 8, y));
  pill(c, right ? x - 6 : x + 6, yy, text, p.panel, p.fg, p.line, right ? 'right' : 'left');
}

/** The shared time axis above the timelines. */
export function drawAxis(cv, from, to, cursor, live, padL = 0) {
  const { c, W, H } = setup(cv);
  const p = pal();
  const pw = W - padL - 46;
  const X = t => padL + (t - from) / (to - from) * pw;
  const step = tickStep(to - from, pw, 80);
  c.font = `10.5px ${p.mono}`; c.textBaseline = 'middle'; c.textAlign = 'left';
  const cx = cursor != null ? X(cursor) : null;
  for (const t of ticks(from, to, step)) {
    const x = X(t);
    c.fillStyle = p.dim;
    c.fillRect(Math.round(x), H - 5, 1, 5);
    const lab = tickLabel(t, step);
    if ((cx == null || Math.abs(x + 30 - cx) > 70) && x + c.measureText(lab).width < padL + pw) c.fillText(lab, x + 4, H / 2);
  }
  if (live) { c.fillStyle = p.accent; c.font = `600 10.5px ${p.mono}`; c.fillText('now', padL + pw + 6, H / 2); }
  if (cx != null && cx >= padL && cx <= padL + pw) {
    const text = step < 86400e3 ? fmtTime(cursor) : `${fmtDayMonth(cursor)} ${fmtTime(cursor, false)}`;
    c.font = `500 10.5px ${p.mono}`;
    const w = c.measureText(text).width + 10;
    pill(c, Math.max(padL, Math.min(padL + pw - w, cx - w / 2)), H / 2, text, p.accent, '#fff');
  }
}

/** The "Graph" column: loss bar, min–max line, average tick, current ×. */
export const LOG0 = 0.1, LOG1 = 1000;
export function logX(v, w) {
  return (Math.log10(Math.min(Math.max(v, LOG0), LOG1)) - Math.log10(LOG0)) / (Math.log10(LOG1) - Math.log10(LOG0)) * w;
}
export function drawGraphCell(cv, st, s, linMax) {
  const { c, W, H } = setup(cv);
  const p = pal();
  const mid = H / 2;
  const log = s.graphScale !== 'linear';
  const X = v => log ? logX(v, W) : Math.min(Math.max(v, 0) / linMax, 1) * W;
  if (st.n && st.loss > 0) { c.fillStyle = p['loss-soft']; c.fillRect(0, 2, W * st.loss / 100, H - 4); }
  c.strokeStyle = p.grid; c.lineWidth = 1;
  const marks = log ? [1, 10, 100] : [linMax / 4, linMax / 2, linMax * 3 / 4];
  for (const d of marks) { const x = Math.round(X(d)) + .5; line(c, x, 0, x, H); }
  if (st.avg == null) {
    if (st.n) {
      c.fillStyle = p.bad; c.font = `600 10.5px ${p.mono}`; c.textBaseline = 'middle'; c.textAlign = 'right';
      c.fillText('no reply', W - 6, mid + .5);
    }
    return;
  }
  c.lineCap = 'round';
  if (s.drawMinMax) {
    c.strokeStyle = p.dim; c.lineWidth = 2;
    line(c, X(st.min), mid, Math.max(X(st.max), X(st.min) + 1), mid);
  }
  c.strokeStyle = p.ink; c.lineWidth = 2.5;
  const ax = X(st.avg); line(c, ax, mid - 7, ax, mid + 7);
  if (s.drawCurrent && st.cur != null) {
    const cx = X(st.cur);
    c.strokeStyle = p.accent; c.lineWidth = 1.8; c.lineCap = 'butt';
    c.beginPath(); c.moveTo(cx - 4, mid - 4); c.lineTo(cx + 4, mid + 4); c.moveTo(cx + 4, mid - 4); c.lineTo(cx - 4, mid + 4); c.stroke();
  }
}

/** Phone sparkline. */
export function drawSpark(cv, sg) {
  const { c, W, H } = setup(cv);
  const p = pal();
  const n = sg.i1 - sg.i0;
  if (n < 1) return;
  let ymax = 0;
  for (let i = sg.i0; i < sg.i1; i++) if (sg.r[i] > ymax) ymax = sg.r[i];
  ymax = ymax * 1.1 || 1;
  const X = i => (i - sg.i0) / Math.max(1, n - 1) * W, Y = v => H - 2 - v / ymax * (H - 4);
  c.fillStyle = p.loss;
  for (let i = sg.i0; i < sg.i1; i++) if (sg.r[i] < 0) c.fillRect(X(i) - .75, 0, 1.5, H);
  c.strokeStyle = p.ink; c.lineWidth = 1.2; c.lineJoin = 'round';
  c.beginPath();
  let pen = false;
  for (let i = sg.i0; i < sg.i1; i++) {
    if (sg.r[i] < 0) { pen = false; continue; }
    if (pen) c.lineTo(X(i), Y(sg.r[i])); else c.moveTo(X(i), Y(sg.r[i]));
    pen = true;
  }
  c.stroke();
}

/** The 7-day strip under the history chart, with the visible window outlined. */
export function drawOverview(cv, src, from, to, win, s) {
  const { c, W, H } = setup(cv);
  const p = pal();
  const L = 46, R = 46, pw = Math.max(1, Math.floor(W - L - R)), T = 2, bot = H - 16, ph = bot - T;
  const X = t => L + (t - from) / (to - from) * pw;
  c.fillStyle = p['panel-2']; c.fillRect(L, T, pw, ph);
  const cols = columns(src, from, to, pw);
  let ymax = 0;
  for (let j = 0; j < cols.W; j++) if (cols.got[j]) ymax = Math.max(ymax, cols.sum[j] / cols.got[j]);
  ymax = niceCeil(ymax * 1.1 || 1);
  c.save();
  c.beginPath(); c.rect(L, 0, pw, H); c.clip();
  if (s.lossScale > 0) { c.fillStyle = p.loss; lossColumns(c, cols, L, T, bot, s.lossScale); }
  c.strokeStyle = p.ink; c.lineWidth = 1; c.beginPath();
  let pen = false;
  for (let j = 0; j < cols.W; j++) {
    if (!cols.got[j]) { pen = false; continue; }
    const x = L + cols.off + j, y = bot - Math.min(cols.sum[j] / cols.got[j] / ymax, 1) * ph;
    if (pen) c.lineTo(x, y); else c.moveTo(x, y);
    pen = true;
  }
  c.stroke();
  c.restore();
  c.font = `10.5px ${p.mono}`; c.fillStyle = p.dim; c.textBaseline = 'middle'; c.textAlign = 'left';
  for (const t of ticks(from, to, 86400e3)) {
    const x = X(t);
    if (x < L) continue;
    c.fillRect(Math.round(x), T, 1, ph + 4);
    c.fillText(`${fmtWeekday(t)} ${new Date(t).getDate()}`, x + 4, H - 6);
  }
  if (win) {
    const x0 = Math.max(L, X(win[0])), x1 = Math.min(L + pw, X(win[1]));
    c.fillStyle = p.focus; c.fillRect(x0, 0, Math.max(2, x1 - x0), bot + 2);
    c.strokeStyle = p.accent; c.lineWidth = 1.5; c.strokeRect(x0 + .75, .75, Math.max(2, x1 - x0) - 1.5, bot + .5);
  }
  return { from, to, L, pw, X };
}
