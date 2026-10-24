// Shared UI pieces: icons, dialogs, toasts, context menus, sound and theme.

import { esc } from './fmt.js';
import { emit } from './store.js';

const svg = (d, extra = '') => `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;

export const ICONS = {
  chart: svg('<path d="M1.5 12.5l4-5 3 3 6-7"/>'),
  radar: svg('<circle cx="8" cy="8" r="6"/><circle cx="8" cy="8" r="2.5"/><path d="M8 8l4.5-3"/>'),
  bell: svg('<path d="M4 11V7a4 4 0 1 1 8 0v4l1.2 1.5H2.8z"/><path d="M6.5 14h3"/>'),
  gear: svg('<circle cx="8" cy="8" r="2.2"/><path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4"/>'),
  pop: svg('<path d="M9 2.5h4.5V7M13.5 2.5 7.5 8.5"/><path d="M12 9.5v4H2.5V4h4"/>'),
  eye: svg('<path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="2"/>'),
  eyeOff: svg('<path d="M2 2l12 12M6.6 6.6a2 2 0 0 0 2.8 2.8M4.3 4.4C2.5 5.6 1.5 8 1.5 8S4 12.5 8 12.5c1.4 0 2.6-.5 3.6-1.2M7 3.6c.3 0 .7-.1 1-.1 4 0 6.5 4.5 6.5 4.5s-.5 1-1.5 2.1"/>'),
  grip: '<svg viewBox="0 0 16 16" fill="currentColor"><circle cx="6" cy="4" r="1.2"/><circle cx="10" cy="4" r="1.2"/><circle cx="6" cy="8" r="1.2"/><circle cx="10" cy="8" r="1.2"/><circle cx="6" cy="12" r="1.2"/><circle cx="10" cy="12" r="1.2"/></svg>',
  note: svg('<path d="M3 2.5h10v8l-3 3H3z"/><path d="M10 13.5v-3h3"/>'),
  list: svg('<path d="M5 4h9M5 8h9M5 12h9M2 4h.5M2 8h.5M2 12h.5"/>'),
  plus: svg('<path d="M8 3v10M3 8h10"/>'),
  x: svg('<path d="M4 4l8 8M12 4l-8 8"/>'),
  trash: svg('<path d="M2.5 4h11M6.5 4V2.5h3V4M4 4l.7 9.5h6.6L12 4"/>'),
  edit: svg('<path d="M10.5 2.5l3 3-8 8h-3v-3z"/>'),
  pause: svg('<path d="M5.5 3v10M10.5 3v10"/>'),
  play: svg('<path d="M4.5 3l8 5-8 5z"/>'),
  sun: svg('<circle cx="8" cy="8" r="2.8"/><path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1"/>'),
  moon: svg('<path d="M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7z"/>'),
  system: svg('<rect x="1.5" y="2.5" width="13" height="9" rx="1.5"/><path d="M5.5 14h5M8 11.5V14"/>'),
  // A screen with a notification in its corner: Windows notifications.
  desktop: svg('<rect x="1.5" y="2.5" width="13" height="9" rx="1.5"/><path d="M5.5 14h5M8 11.5V14"/><rect x="8" y="6.25" width="4.5" height="3" rx=".75" fill="currentColor" stroke="none"/>'),
  download: svg('<path d="M8 2v8.5M4.5 7 8 10.5 11.5 7M2.5 13.5h11"/>'),
  upload: svg('<path d="M8 10.5V2M4.5 5.5 8 2l3.5 3.5M2.5 13.5h11"/>'),
  history: svg('<path d="M2 8a6 6 0 1 0 1.8-4.3M2 2.5v2.5h2.5"/><path d="M8 5v3l2 1.5"/>'),
  focus: svg('<path d="M2 5V2h3M11 2h3v3M14 11v3h-3M5 14H2v-3"/><circle cx="8" cy="8" r="1.5"/>'),
  push: svg('<rect x="4" y="1.5" width="8" height="13" rx="1.5"/><path d="M7 12.5h2"/>'),
  mail: svg('<rect x="1.5" y="3.5" width="13" height="9" rx="1.5"/><path d="M2 4.5l6 4.5 6-4.5"/>'),
  sound: svg('<path d="M2.5 6h2.5l3.5-3v10L5 10H2.5z"/><path d="M11 5.5a3.5 3.5 0 0 1 0 5M12.8 3.5a6 6 0 0 1 0 9"/>'),
  file: svg('<path d="M3.5 1.5h6l3 3v10h-9z"/><path d="M6 8h4M6 10.5h4"/>'),
  hook: svg('<path d="M6 10.5a2.5 2.5 0 1 1-2.2-4.2M10 5.5a2.5 2.5 0 1 1 2.2 4.2M6.5 4.5l3 7"/>'),
  run: svg('<rect x="1.5" y="2.5" width="13" height="11" rx="1.5"/><path d="M4.5 6.5l2 1.5-2 1.5M8 10h3.5"/>'),
  chat: svg('<path d="M2.5 3.5h11v7.5H7L4 13.5V11H2.5z"/>'),
  check: svg('<path d="M3 8.5l3 3 7-7"/>'),
  back: svg('<path d="M10 3 5 8l5 5"/>'),
  next: svg('<path d="M6 3l5 5-5 5"/>'),
  live: svg('<circle cx="8" cy="8" r="2" fill="currentColor"/><path d="M4.5 4.5a5 5 0 0 0 0 7M11.5 4.5a5 5 0 0 1 0 7"/>'),
  menu: svg('<path d="M2.5 4h11M2.5 8h11M2.5 12h11"/>'),
  tag: svg('<path d="M1.5 8.5V2.5h6l7 7-6 6z"/><circle cx="5" cy="5.5" r="1"/>'),
};

export function icon(name, cls = 'i') { return (ICONS[name] || '').replace('<svg', `<svg class="${cls}" aria-hidden="true"`); }

// The logo: a latency line with two spikes that stops where pings were lost,
// with the red loss line standing in the break, as on the timelines.
export const LOGO = '<svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M2 17H9.7M14.7 17H22" stroke="var(--ink)" stroke-width="2" stroke-linecap="butt"/><circle cx="2" cy="17" r="1" fill="var(--ink)"/><circle cx="22" cy="17" r="1" fill="var(--ink)"/><path d="M2.9 17L4.6 3.5L6.3 17ZM6.8 17L7.9 10.4L9 17Z" fill="var(--ink)"/><rect x="11.5" y="4" width="1.4" height="16.5" rx=".3" fill="var(--loss)"/></svg>';

/** Build an element from an HTML string. */
export function el(html) {
  const t = document.createElement('template');
  t.innerHTML = html.trim();
  return t.content.firstElementChild;
}

let openModals = 0;
export function modalOpen() { return openModals > 0; }

/** A dialog. body and footer are HTML strings or elements. */
export function modal({ title, body = '', footer = '', size = '', tabs = '', onClose }) {
  const root = el(`<div class="scrim"><div class="modal ${size}" role="dialog" aria-modal="true" aria-label="${esc(title)}">
    <div class="modal-h"><h3>${esc(title)}</h3><button class="btn ghost icon" data-close aria-label="Close">${icon('x')}</button></div>
    ${tabs}<div class="modal-b"></div>${footer !== null ? '<div class="modal-f"></div>' : ''}</div></div>`);
  const b = root.querySelector('.modal-b'), f = root.querySelector('.modal-f');
  if (typeof body === 'string') b.innerHTML = body; else b.append(body);
  if (f) { if (typeof footer === 'string') f.innerHTML = footer; else f.append(footer); if (!footer) f.remove(); }
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true; openModals--;
    root.remove();
    document.removeEventListener('keydown', onKey);
    onClose && onClose();
  };
  const onKey = e => { if (e.key === 'Escape') close(); };
  root.addEventListener('mousedown', e => { if (e.target === root) root._down = true; });
  root.addEventListener('click', e => {
    if ((e.target === root && root._down) || e.target.closest('[data-close]')) close();
    root._down = false;
  });
  document.addEventListener('keydown', onKey);
  document.body.append(root);
  openModals++;
  const first = root.querySelector('[autofocus]') || root.querySelector('.modal-b input, .modal-b textarea, .modal-b select');
  if (first) setTimeout(() => first.focus(), 30);
  return { root, body: b, foot: root.querySelector('.modal-f'), close };
}

export function confirmDialog(text, { title = 'Are you sure?', ok = 'OK', danger = false } = {}) {
  return new Promise(resolve => {
    let answer = false;
    const m = modal({
      title, size: 'sm', body: `<p style="margin:0;line-height:1.5">${esc(text)}</p>`,
      footer: `<span class="spacer"></span><button class="btn" data-close>Cancel</button><button class="btn ${danger ? 'danger solid' : 'primary'}" data-ok>${esc(ok)}</button>`,
      onClose: () => resolve(answer),
    });
    m.root.querySelector('[data-ok]').addEventListener('click', () => { answer = true; m.close(); });
    setTimeout(() => m.root.querySelector('[data-ok]').focus(), 30);
  });
}

export function promptDialog(title, { label = '', value = '', ok = 'Save', placeholder = '', multiline = false, help = '' } = {}) {
  return new Promise(resolve => {
    let answer = null;
    const input = multiline
      ? `<textarea class="input" rows="4" placeholder="${esc(placeholder)}" aria-label="${esc(label || title)}" autofocus>${esc(value)}</textarea>`
      : `<input class="input" value="${esc(value)}" placeholder="${esc(placeholder)}" aria-label="${esc(label || title)}" autofocus>`;
    const m = modal({
      title, size: 'sm',
      body: `<form class="f">${label ? `<label>${esc(label)}</label>` : ''}${input}${help ? `<span class="help">${esc(help)}</span>` : ''}</form>`,
      footer: `<span class="spacer"></span><button class="btn" data-close>Cancel</button><button class="btn primary" data-ok>${esc(ok)}</button>`,
      onClose: () => resolve(answer),
    });
    const field = m.root.querySelector('input, textarea');
    const done = () => { answer = field.value; m.close(); };
    m.root.querySelector('[data-ok]').addEventListener('click', done);
    m.root.querySelector('form').addEventListener('submit', e => { e.preventDefault(); done(); });
    if (!multiline) field.select();
  });
}

/** Small notice in the top-right corner. kind: good, warn, bad, info. */
export function toast(title, body = '', { kind = 'info', timeout = 6000, onClick } = {}) {
  const box = document.getElementById('toasts');
  const t = el(`<div class="toast" role="status"><span class="sd ${kind === 'info' ? '' : kind}" style="${kind === 'info' ? 'background:var(--accent)' : ''}"></span>
    <div><b>${esc(title)}</b>${body ? `<p>${esc(body)}</p>` : ''}</div><button class="x" aria-label="Dismiss">×</button></div>`);
  const close = () => t.remove();
  t.querySelector('.x').addEventListener('click', e => { e.stopPropagation(); close(); });
  if (onClick) { t.style.cursor = 'pointer'; t.addEventListener('click', () => { onClick(); close(); }); }
  box.prepend(t);
  while (box.children.length > 5) box.lastElementChild.remove();
  if (timeout) setTimeout(close, timeout);
  return close;
}

export function errorToast(err) { toast('Something went wrong', err.message || String(err), { kind: 'bad', timeout: 8000 }); }

let menuEl = null;
export function closeMenu() { if (menuEl) { menuEl.remove(); menuEl = null; } }
/** Right-click menu. items: { label, icon, check, danger, action } or '-' */
export function contextMenu(x, y, { header = '', items = [] }) {
  closeMenu();
  const m = el(`<div class="menu" role="menu">${header}</div>`);
  for (const it of items) {
    if (it === '-') { m.append(el('<hr>')); continue; }
    const b = el(`<button role="menuitem" class="${it.danger ? 'danger' : ''}"><span class="ck">${it.check === true ? '✓' : ''}</span>${it.icon ? icon(it.icon) : ''}<span>${esc(it.label)}</span></button>`);
    if (it.check === undefined) b.querySelector('.ck').remove();
    b.addEventListener('click', () => { closeMenu(); it.action(); });
    m.append(b);
  }
  document.body.append(m);
  const r = m.getBoundingClientRect();
  m.style.left = Math.min(x, innerWidth - r.width - 8) + 'px';
  m.style.top = Math.min(y, innerHeight - r.height - 8) + 'px';
  menuEl = m;
}
document.addEventListener('mousedown', e => { if (menuEl && !menuEl.contains(e.target)) closeMenu(); });
document.addEventListener('keydown', e => { if (e.key === 'Escape') closeMenu(); });
window.addEventListener('blur', closeMenu);

// ---------- sound ----------
let audio = null;
function ctx() {
  if (!audio) {
    try { audio = new (window.AudioContext || window.webkitAudioContext)(); } catch { return null; }
  }
  return audio;
}
/** Browsers only allow sound after a click, so wake the audio on the first one. */
export function unlockAudio() {
  const go = () => { const a = ctx(); if (a && a.state === 'suspended') a.resume(); };
  document.addEventListener('pointerdown', go, { once: true });
  document.addEventListener('keydown', go, { once: true });
}
/** Two-tone chime: falling for a problem, rising when it clears. */
export function chime(kind = 'start') {
  const a = ctx();
  if (!a || a.state !== 'running') return;
  const notes = kind === 'end' || kind === 'ok' ? [660, 880] : kind === 'test' ? [880, 880] : [880, 587];
  notes.forEach((f, i) => {
    const o = a.createOscillator(), g = a.createGain();
    o.type = 'sine'; o.frequency.value = f;
    const t0 = a.currentTime + i * 0.22;
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(0.25, t0 + 0.02);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.32);
    o.connect(g).connect(a.destination);
    o.start(t0); o.stop(t0 + 0.35);
  });
}

// ---------- theme ----------
const darkQuery = window.matchMedia('(prefers-color-scheme: dark)');
export function themeMode() {
  try { const t = localStorage.getItem('op-theme'); return t === 'light' || t === 'dark' ? t : 'system'; } catch { return 'system'; }
}
export function isDark() {
  const m = themeMode();
  return m === 'dark' || (m === 'system' && darkQuery.matches);
}
export function setTheme(mode) {
  try { localStorage.setItem('op-theme', mode); } catch { /* storage off */ }
  if (mode === 'light' || mode === 'dark') document.documentElement.dataset.theme = mode;
  else delete document.documentElement.dataset.theme;
  emit('theme', mode);
}
darkQuery.addEventListener('change', () => emit('theme', themeMode()));

export function themeSwitch() {
  const m = themeMode();
  const b = (mode, ic, label) => `<button type="button" data-theme-set="${mode}" class="${m === mode ? 'on' : ''}" title="${label}" aria-pressed="${m === mode}">${icon(ic)}<span class="t-label">${label}</span></button>`;
  return `<div class="seg sans theme-seg" role="group" aria-label="Theme">${b('light', 'sun', 'Light')}${b('system', 'system', 'System')}${b('dark', 'moon', 'Dark')}</div>`;
}
document.addEventListener('click', e => {
  const b = e.target.closest('[data-theme-set]');
  if (!b) return;
  setTheme(b.dataset.themeSet);
  document.querySelectorAll('[data-theme-set]').forEach(x => {
    x.classList.toggle('on', x.dataset.themeSet === b.dataset.themeSet);
    x.setAttribute('aria-pressed', x.dataset.themeSet === b.dataset.themeSet);
  });
});

// ---------- tab title and icon ----------
export function setAlertBadge(n) {
  document.title = n ? `(${n}) OmniPing` : 'OmniPing';
  const link = document.getElementById('favicon');
  if (!link) return;
  if (!n) { link.href = 'favicon.svg'; return; }
  // While an alert fires: the favicon on a red tile, which stands out in a row of tabs.
  const s = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" rx="3.5" fill="#e0363b"/><g fill="#ffffff"><rect x="2" y="10" width="6" height="2" shape-rendering="crispEdges"/><rect x="12" y="10" width="2" height="2" shape-rendering="crispEdges"/><path d="M2 11L3.5 2L5 11ZM5.6 11L6.5 7L7.4 11Z"/></g><rect x="9" y="3" width="2" height="11" fill="#121920" shape-rendering="crispEdges"/></svg>`;
  link.href = 'data:image/svg+xml,' + encodeURIComponent(s);
}
