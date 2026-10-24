// Shared client state: the server's configuration, live sample buffers per
// target, alert events, and a tiny publish/subscribe bus.

import { api, fetchSamples } from './api.js';
import { setDateFormat } from './fmt.js';

export const S = {
  st: null,             // last /api/state
  byId: new Map(),      // target id → target
  series: new Map(),    // target id → Series (recent raw samples)
  events: [],           // alert events, newest first
  active: [],           // alerts firing right now
  scan: null,           // address-range scan progress
  connected: false,
  offset: 0,            // server clock minus browser clock
};

const subs = new Map();
export function on(topic, fn) {
  if (!subs.has(topic)) subs.set(topic, new Set());
  subs.get(topic).add(fn);
  return () => subs.get(topic).delete(fn);
}
export function emit(topic, data) {
  subs.get(topic)?.forEach(fn => {
    try { fn(data); } catch (e) { console.error(e); }
  });
}

/** Current time on the server's clock. */
export const now = () => Date.now() + S.offset;

/** Per-browser preferences (theme, sound, layout). */
export function pref(key, def) {
  try {
    const v = localStorage.getItem('op-' + key);
    return v == null ? def : JSON.parse(v);
  } catch { return def; }
}
export function setPref(key, v) {
  try { localStorage.setItem('op-' + key, JSON.stringify(v)); } catch { /* storage off */ }
}

/** Sorted ping results of one target. r < 0 means lost. */
export class Series {
  constructor() { this.t = []; this.r = []; }
  get length() { return this.t.length; }
  lower(t) {
    let lo = 0, hi = this.t.length;
    while (lo < hi) { const m = (lo + hi) >> 1; if (this.t[m] < t) lo = m + 1; else hi = m; }
    return lo;
  }
  push(t, r) {
    const n = this.t.length;
    if (!n || t > this.t[n - 1]) { this.t.push(t); this.r.push(r); return; }
    const i = this.lower(t);
    if (this.t[i] === t) return;
    this.t.splice(i, 0, t); this.r.splice(i, 0, r);
  }
  /** Add a sorted batch that may overlap what is already here. */
  merge(ts, rs) {
    if (!ts.length) return;
    if (!this.t.length || ts[ts.length - 1] < this.t[0]) {
      this.t = ts.concat(this.t); this.r = rs.concat(this.r); return;
    }
    for (let i = 0; i < ts.length; i++) this.push(ts[i], rs[i]);
  }
  trim(before) {
    const i = this.lower(before);
    if (i > 0) { this.t.splice(0, i); this.r.splice(0, i); }
  }
}

export function series(id) {
  let s = S.series.get(id);
  if (!s) { s = new Series(); S.series.set(id, s); }
  return s;
}

export const settings = () => S.st.settings;
export const intervalOf = t => (t && t.intervalMs) || S.st.settings.intervalMs;

/** Loss, current, average, min, max and jitter over samples [i0, i1). */
export function statsOf(ts, rs, i0, i1) {
  const out = { n: Math.max(0, i1 - i0), lost: 0, loss: 0, cur: null, curT: null, avg: null, min: null, max: null, jit: null, from: null, to: null };
  if (out.n === 0) return out;
  let sum = 0, got = 0, mn = Infinity, mx = -Infinity, jsum = 0, jn = 0, prev = null;
  for (let i = i0; i < i1; i++) {
    const r = rs[i];
    if (r < 0) { out.lost++; continue; }
    got++; sum += r;
    if (r < mn) mn = r;
    if (r > mx) mx = r;
    if (prev != null) { jsum += Math.abs(r - prev); jn++; }
    prev = r;
  }
  out.loss = out.lost * 100 / out.n;
  out.cur = rs[i1 - 1] < 0 ? null : rs[i1 - 1];
  out.curLost = rs[i1 - 1] < 0;
  out.curT = ts[i1 - 1];
  out.from = ts[i0]; out.to = ts[i1 - 1];
  if (got) { out.avg = sum / got; out.min = mn; out.max = mx; }
  if (jn) out.jit = jsum / jn;
  return out;
}

/** Statistics over the last n samples of a target, optionally ending at a moment. */
export function lastStats(id, n, end = null) {
  const s = series(id);
  const i1 = end == null ? s.length : s.lower(end + 1);
  return statsOf(s.t, s.r, Math.max(0, i1 - n), i1);
}

/** good / warn / bad / none, using the warning and critical thresholds. */
export function level(st) {
  if (!st || st.n === 0) return 'none';
  const s = S.st.settings;
  if (st.avg == null || st.loss >= 50 || st.avg >= s.critMs) return 'bad';
  if (st.loss > 0 || st.avg >= s.warnMs) return 'warn';
  return 'good';
}

export function latencyClass(v) {
  if (v == null) return '';
  const s = S.st.settings;
  if (v >= s.critMs) return 'v-bad';
  if (v >= s.warnMs) return 'v-warn';
  return '';
}

export function setState(st) {
  S.st = st;
  S.byId = new Map(st.targets.map(t => [t.id, t]));
  S.active = st.active || [];
  setDateFormat(st.settings.dateFormat);
  for (const id of [...S.series.keys()]) if (!S.byId.has(id)) S.series.delete(id);
  emit('state', st);
}

/** How much raw history to keep in each live buffer. */
let keepMs = 30 * 60e3;
export function wantLiveWindow(ms) { keepMs = Math.max(keepMs, Math.min(ms, 3 * 3600e3)); }
export function liveKeepMs() { return keepMs; }

async function backfill(ms) {
  if (!S.st || !S.st.targets.length) return;
  const to = now();
  const res = await fetchSamples([], to - ms, to, 40000);
  for (const r of res.series) {
    if (r.mode === 'raw') series(r.id).merge(r.t, r.r);
  }
}

/** Load (or reload after a reconnect) everything from the server. */
export async function reload() {
  setState(await api.get('state'));
  await backfill(keepMs);
  emit('backfill');
  S.events = (await api.get('events?limit=300')) || [];
  emit('events');
}

/** Make sure the live buffers reach back at least ms (when a longer span is picked). */
export async function ensureLive(ms) {
  if (ms <= keepMs) return;
  wantLiveWindow(ms);
  await backfill(keepMs);
  emit('backfill');
}

export function trimBuffers() {
  const cut = now() - keepMs;
  for (const s of S.series.values()) s.trim(cut);
}

export function connect() {
  const es = new EventSource('api/stream');
  const json = e => JSON.parse(e.data);
  es.addEventListener('hello', async e => {
    S.offset = json(e).now - Date.now();
    S.connected = true;
    emit('conn', true);
    try { await reload(); } catch (err) { console.error(err); }
  });
  es.addEventListener('samples', e => {
    const batch = json(e);
    for (const [id, t, r] of batch) series(id).push(t, r);
    emit('samples', batch);
  });
  es.addEventListener('state', e => setState(json(e)));
  // A name was looked up again: the address (or the name behind it) changed.
  es.addEventListener('target', e => {
    const t = json(e);
    if (!S.st) return;
    const i = S.st.targets.findIndex(x => x.id === t.id);
    if (i < 0) return;
    S.st.targets[i] = t;
    setState(S.st);
  });
  es.addEventListener('targetError', e => {
    const d = json(e);
    const t = S.byId.get(d.id);
    if (t) { t.error = d.error; emit('targetError', d); }
  });
  es.addEventListener('active', e => { S.active = json(e); emit('active', S.active); });
  es.addEventListener('event', e => {
    const ev = json(e);
    const i = S.events.findIndex(x => x.id === ev.id);
    if (i >= 0) S.events[i] = ev; else S.events.unshift(ev);
    if (S.events.length > 1000) S.events.length = 1000;
    emit('event', ev);
  });
  es.addEventListener('notify', e => emit('notify', json(e)));
  es.addEventListener('discover', e => { S.scan = json(e); emit('discover', S.scan); });
  es.addEventListener('info', e => { if (S.st) { S.st.info = json(e); emit('info', S.st.info); } });
  es.onerror = () => {
    if (S.connected) { S.connected = false; emit('conn', false); }
    // A live connection refused for want of a password doesn't come back.
    fetch('api/info', { cache: 'no-store' }).then(r => { if (r.status === 401) location.href = 'login'; }).catch(() => {});
  };
  return es;
}

/** The address a target pings, with ":port" for a TCP target and "→ name"
 * for a DNS check. */
export function addrOf(t) {
  let a = t.ip || t.host || '';
  if (t.port) a = a.includes(':') ? `[${a}]:${t.port}` : `${a}:${t.port}`;
  return t.lookup ? `${a} → ${t.lookup}` : a;
}

const DAY = 86_400_000;
const hm = s => { const [h, m] = s.split(':'); return (+h * 60 + +m) * 60_000; };
const onDay = (days, wd) => days === 'weekdays' ? wd >= 1 && wd <= 5 : days === 'weekends' ? wd === 0 || wd === 6 : true;

/** The times t is expected to be off between from and to, as [start, end]
 * pairs. They follow the server's clock, like the server does. */
export function offRanges(t, from, to) {
  if (!t.off?.length) return [];
  const so = (S.st?.info?.utcOffset ?? -new Date().getTimezoneOffset() * 60) * 1000;
  const out = [];
  // Server-local midnights, written as if UTC, from the day before from.
  for (let d = Math.floor((from + so) / DAY) * DAY - DAY; d <= to + so; d += DAY) {
    const wd = new Date(d).getUTCDay();
    for (const o of t.off) {
      if (!onDay(o.days, wd)) continue;
      const a = d + hm(o.from) - so;
      const b = d + hm(o.to) + (hm(o.to) <= hm(o.from) ? DAY : 0) - so;
      if (b > from && a < to) out.push([Math.max(a, from), Math.min(b, to)]);
    }
  }
  return out;
}

/** "every day 01:00–08:00" */
export function offText(o) {
  return `${o.days === 'weekdays' ? 'weekdays' : o.days === 'weekends' ? 'weekends' : 'every day'} ${o.from}–${o.to}`;
}

/** The server's clock differs from this browser's (Docker without TZ, say). */
export function serverClockDiffers() {
  const so = S.st?.info?.utcOffset;
  return so != null && so !== -new Date().getTimezoneOffset() * 60;
}
