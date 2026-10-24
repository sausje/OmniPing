// Number, time and size formatting shared by every view.

let dateFormat = 'dmy';
export function setDateFormat(f) { dateFormat = f || 'dmy'; }

const p2 = n => String(n).padStart(2, '0');

/** Latency in ms: two decimals below 1 ms, one below 100 ms, none above. */
export function fmtMs(v) {
  if (v == null || v < 0 || !isFinite(v)) return '—';
  if (v < 1) return v.toFixed(2);
  if (v < 100) return v.toFixed(1);
  return Math.round(v).toString();
}

/** Axis label for a latency scale. */
export function scaleFmt(v) {
  if (v >= 10) return Math.round(v).toString();
  if (v >= 1) return Number.isInteger(v) ? String(v) : v.toFixed(1);
  return v.toFixed(2);
}

export function fmtDate(ts) {
  const d = new Date(ts);
  const D = p2(d.getDate()), M = p2(d.getMonth() + 1), Y = d.getFullYear();
  if (dateFormat === 'ymd') return `${Y}-${M}-${D}`;
  if (dateFormat === 'mdy') return `${M}/${D}/${Y}`;
  return `${D}-${M}-${Y}`;
}

export function fmtDayMonth(ts) {
  const d = new Date(ts);
  if (dateFormat === 'mdy') return `${p2(d.getMonth() + 1)}/${p2(d.getDate())}`;
  if (dateFormat === 'ymd') return `${p2(d.getMonth() + 1)}-${p2(d.getDate())}`;
  return `${p2(d.getDate())}-${p2(d.getMonth() + 1)}`;
}

export function fmtTime(ts, secs = true) {
  const d = new Date(ts);
  const s = `${p2(d.getHours())}:${p2(d.getMinutes())}`;
  return secs ? `${s}:${p2(d.getSeconds())}` : s;
}

export function fmtDateTime(ts, secs = true) { return `${fmtDate(ts)} ${fmtTime(ts, secs)}`; }

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
export function fmtWeekday(ts) { return WEEKDAYS[new Date(ts).getDay()]; }

/** "Today 14:33:58", "Mon 22:13:05" or a full date for older moments. */
export function fmtWhen(ts, now = Date.now()) {
  const d = new Date(ts), n = new Date(now);
  const sameDay = d.toDateString() === n.toDateString();
  if (sameDay) return `Today ${fmtTime(ts)}`;
  if (now - ts < 6 * 86400e3) return `${fmtWeekday(ts)} ${fmtTime(ts)}`;
  return fmtDateTime(ts);
}

export function fmtDuration(ms) {
  ms = Math.max(0, ms);
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ${s % 60} s`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ${m % 60} min`;
  return `${Math.floor(h / 24)} days ${h % 24} h`;
}

/** Short span label: 30 s, 5 min, 1 h, 7 d. */
export function fmtSpan(sec) {
  if (sec < 60) return `${sec} s`;
  if (sec < 3600) return `${Math.round(sec / 60)} min`;
  if (sec < 86400 * 2) return `${+(sec / 3600).toFixed(1)} h`;
  return `${+(sec / 86400).toFixed(1)} d`;
}

export function fmtInterval(ms) {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${+(ms / 1000).toFixed(2)} s`;
  return `${+(ms / 60000).toFixed(1)} min`;
}

const nf = new Intl.NumberFormat();
export function fmtNum(n) { return nf.format(Math.round(n)); }

export function fmtBytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} KB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(2)} GB`;
}

/** Escape text for use inside HTML. */
export function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
