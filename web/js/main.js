// OmniPing in the browser: boots the app, draws the top bar and routes
// between the monitor, target history, alerts and settings pages.

import { S, on, emit, connect, pref, setPref, trimBuffers, settings, now } from './store.js';
import { api } from './api.js';
import { fmtInterval, esc } from './fmt.js';
import { LOGO, icon, el, themeSwitch, toast, chime, unlockAudio, setAlertBadge, errorToast, promptDialog, modalOpen } from './ui.js';
import { refreshPalette } from './graphs.js';
import * as monitor from './monitor.js';
import * as targetPage from './target.js';
import * as alertsPage from './alerts.js';
import * as settingsPage from './settings.js';
import * as reportPage from './report.js';
import { openAddTargets, addTargets, rememberEntry } from './targets-ui.js';

const INTERVALS = [250, 500, 1000, 2000, 2500, 5000, 10000, 30000, 60000];
const SAMPLES = [5, 10, 20, 30, 60, 100, 300, 600, 3600];
let current = null;
let booted = false;

function route() {
  const parts = location.hash.replace(/^#\/?/, '').split('/');
  return { name: parts[0] || 'monitor', arg: parts[1] ? decodeURIComponent(parts[1]) : '', arg2: parts[2] ? decodeURIComponent(parts[2]) : '' };
}

function render() {
  if (!S.st) return;
  const r = route();
  if (current && current.unmount) current.unmount();
  current = null;
  const app = document.getElementById('app');
  app.innerHTML = '';
  if (r.name === 'pop') { current = monitor.mountPop(app, r.arg); return; }
  app.append(topbar(r.name));
  const view = el('<div class="view"></div>');
  app.append(view, tabbar(r.name));
  try {
    if (r.name === 'target') current = targetPage.mount(view, r.arg);
    else if (r.name === 'alerts') current = alertsPage.mount(view, r.arg, r.arg2);
    else if (r.name === 'settings') current = settingsPage.mount(view, r.arg);
    else if (r.name === 'report') current = reportPage.mount(view);
    else if (r.name === 'timelines') current = monitor.mount(view, { timelinesOnly: monitor.isPhone() });
    else current = monitor.mount(view, {});
  } catch (err) {
    console.error(err);
    view.innerHTML = `<div class="empty"><h3>This page could not be shown</h3><p>${esc(err.message)}</p></div>`;
  }
  updateTopbar();
}

function selectOptions(values, cur, fmt) {
  const list = values.includes(cur) ? values : [...values, cur].sort((a, b) => a - b);
  return list.map(v => `<option value="${v}" ${v === cur ? 'selected' : ''}>${fmt(v)}</option>`).join('') + '<option value="custom">Custom…</option>';
}

function topbar(page) {
  const s = settings();
  const bar = el(`<header class="topbar">
    <a class="brand" href="#/" title="OmniPing">${LOGO}<span class="hide-sm">OmniPing</span></a>
    <span class="vsep hide-sm"></span>
    <button class="btn hide-sm" data-run></button>
    <span class="live" data-live-state></span>
    <span class="vsep hide-md"></span>
    <label class="field hide-tb"><span class="lbl">Ping interval</span>
      <select class="input mono" data-interval style="height:30px">${selectOptions(INTERVALS, s.intervalMs, fmtInterval)}</select></label>
    <label class="field hide-tb"><span class="lbl">Samples to include</span>
      <select class="input mono" data-samples style="height:30px">${selectOptions(SAMPLES, s.samplesToInclude, v => String(v))}</select></label>
    <form class="addbox hide-sm" data-addbox>
      <input class="input" list="op-history" placeholder="Enter new target: IP or name, then an alias" aria-label="Enter new target">
      <datalist id="op-history"></datalist>
      <button class="btn primary" title="Add target (Enter)">+</button>
    </form>
    <span class="spacer"></span>
    <a class="navbtn hide-sm ${page === 'monitor' || page === 'target' ? 'on' : ''}" href="#/" title="Monitor">${icon('chart')}<span class="hide-lg">Monitor</span></a>
    <button class="navbtn" data-add-dialog title="Add targets or scan the network">${icon('radar')}<span class="hide-lg">Add & scan</span></button>
    <a class="navbtn hide-sm ${page === 'alerts' ? 'on' : ''}" href="#/alerts" title="Alerts">${icon('bell')}<span class="hide-lg">Alerts</span><span class="badge" data-badge hidden></span></a>
    <a class="navbtn hide-sm ${page === 'report' ? 'on' : ''}" href="#/report" title="Report: uptime, loss and outages">${icon('file')}<span class="hide-lg">Report</span></a>
    <a class="navbtn hide-sm ${page === 'settings' ? 'on' : ''}" href="#/settings" title="Settings">${icon('gear')}<span class="hide-lg">Settings</span></a>
    <a class="update-pill" href="#/settings/about" data-update hidden></a>
    ${themeSwitch()}
  </header>`);

  bar.querySelector('[data-run]').addEventListener('click', () => {
    api.put('settings', { running: !settings().running }).catch(errorToast);
  });
  const custom = async (sel, key, title, label, toValue) => {
    if (sel.value !== 'custom') return api.put('settings', { [key]: toValue(sel.value) }).catch(errorToast);
    const v = await promptDialog(title, { label, value: '', ok: 'Set' });
    if (v != null && +v > 0) api.put('settings', { [key]: toValue(v) }).catch(errorToast);
    else sel.value = String(settings()[key]);
  };
  const iv = bar.querySelector('[data-interval]');
  iv.addEventListener('change', () => custom(iv, 'intervalMs', 'Custom ping interval', 'Milliseconds between pings to each target (100 or more)', v => Math.round(+v)));
  const sm = bar.querySelector('[data-samples]');
  sm.addEventListener('change', () => custom(sm, 'samplesToInclude', 'Samples to include', 'Number of recent samples the table statistics use', v => Math.round(+v)));
  bar.querySelector('[data-add-dialog]').addEventListener('click', () => openAddTargets(S.st.targets.length ? 'single' : 'scan'));
  const form = bar.querySelector('[data-addbox]');
  const input = form.querySelector('input');
  form.querySelector('datalist').innerHTML = pref('history', []).map(h => `<option value="${esc(h)}">`).join('');
  form.addEventListener('submit', async e => {
    e.preventDefault();
    const text = input.value.trim();
    if (!text) { openAddTargets('single'); return; }
    try {
      await addTargets(text);
      rememberEntry(text);
      input.value = '';
    } catch (err) { errorToast(err); }
  });
  return bar;
}

function updateTopbar() {
  if (!S.st) return;
  const s = settings();
  const run = document.querySelector('[data-run]');
  if (run) run.innerHTML = s.running ? '<span class="dot-stop"></span> Stop' : '<span class="dot-play"></span> Start';
  const live = document.querySelector('[data-live-state]');
  if (live) {
    const n = S.st.targets.filter(t => !t.paused).length;
    const [cls, text] = !S.connected ? ['bad', 'Reconnecting…'] : S.st.info.pingerError ? ['bad', 'Pinging unavailable']
      : !s.running ? ['off', 'Stopped'] : ['', `Pinging ${n} target${n === 1 ? '' : 's'}`];
    live.innerHTML = `<span class="pulse ${cls}"></span><span class="live-t">${text}</span>`;
    live.title = text;
  }
  const iv = document.querySelector('[data-interval]');
  if (iv && document.activeElement !== iv) iv.innerHTML = selectOptions(INTERVALS, s.intervalMs, fmtInterval);
  const sm = document.querySelector('[data-samples]');
  if (sm && document.activeElement !== sm) sm.innerHTML = selectOptions(SAMPLES, s.samplesToInclude, v => String(v));
  const up = document.querySelector('[data-update]');
  if (up) {
    const u = S.st.info.update;
    up.hidden = !u;
    if (u) { up.textContent = `${u.version} is available`; up.title = `OmniPing ${u.version} is available; you have ${S.st.info.version}`; }
  }
  const n = S.active.length;
  document.querySelectorAll('[data-badge]').forEach(b => { b.hidden = !n; b.textContent = n; });
  setAlertBadge(n);
}

function tabbar(page) {
  const n = S.active.length;
  return el(`<nav class="tabbar" aria-label="Sections">
    <a href="#/" class="${page === 'monitor' || page === 'target' ? 'on' : ''}">${icon('list')}Targets</a>
    <a href="#/timelines" class="${page === 'timelines' ? 'on' : ''}">${icon('chart')}Timelines</a>
    <a href="#/alerts" class="${page === 'alerts' ? 'on' : ''}">${icon('bell')}Alerts<span class="badge" data-badge ${n ? '' : 'hidden'}>${n}</span></a>
    <a href="#/report" class="${page === 'report' ? 'on' : ''}">${icon('file')}Report</a>
    <a href="#/settings" class="${page === 'settings' ? 'on' : ''}">${icon('gear')}Settings</a>
  </nav>`);
}

// Alerts sent to the "Open OmniPing pages" channel.
on('notify', n => {
  const kind = n.kind === 'end' || n.kind === 'ok' ? 'good' : n.kind === 'test' ? 'info' : 'bad';
  toast(n.title, n.body, {
    kind, timeout: kind === 'bad' ? 30000 : 9000,
    onClick: n.target ? () => { location.hash = '#/target/' + n.target; } : null,
  });
  if (n.sound && pref('sound', true)) chime(n.kind);
  if (pref('desktop', false) && 'Notification' in window && Notification.permission === 'granted' && document.hidden) {
    try { new Notification(n.title, { body: n.body, tag: (n.target || 'omniping') + n.kind, icon: 'favicon.svg' }); } catch { /* not allowed here */ }
  }
});

on('state', () => {
  if (!booted) { booted = true; render(); return; }
  updateTopbar();
});
on('active', updateTopbar);
on('info', updateTopbar);
on('conn', updateTopbar);
on('theme', () => { refreshPalette(); });
window.addEventListener('hashchange', render);
let wasPhone = monitor.isPhone();
window.addEventListener('resize', () => {
  if (monitor.isPhone() !== wasPhone) { wasPhone = monitor.isPhone(); render(); }
});
setInterval(trimBuffers, 60e3);

unlockAudio();
connect();
