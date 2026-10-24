// Settings: General, Display and Packet, plus storage,
// notification channels, import/export, the Windows app and server details.

import { S, on, pref, setPref, settings, now } from './store.js';
import { api } from './api.js';
import { fmtBytes, fmtNum, fmtDuration, fmtDateTime, fmtDate, fmtInterval, esc } from './fmt.js';
import { icon, modal, toast, errorToast, confirmDialog, promptDialog, themeSwitch, chime } from './ui.js';
import { openAddTargets, openMoveToGroup, moveGroup, groupOrder } from './targets-ui.js';
import { CHANNEL_ICON } from './alerts.js';

const SECTIONS = [
  ['general', 'General'], ['display', 'Display'], ['packet', 'Packet'], ['storage', 'Storage'],
  ['notifications', 'Notifications'], ['data', 'Targets & data'], ['computer', 'This computer'], ['about', 'About'],
];
// "This computer" is only there in the Windows app.
const sections = () => SECTIONS.filter(([id]) => id !== 'computer' || S.st.info.desktop);

const CHANNEL_TYPES = {
  windows: {
    label: 'Windows notification', desc: 'Shows alerts as Windows notifications on this PC, also when no OmniPing page is open.', fields: [],
    help: 'Notifications follow Windows’ Do Not Disturb, so keep ntfy, Telegram or email for alerts you can’t miss.',
  },
  ntfy: {
    label: 'ntfy push', desc: 'Free push notifications on Android, iPhone and in a desktop browser.',
    fields: [['server', 'Server', 'https://ntfy.sh'], ['topic', 'Topic', ''], ['priority', 'Priority for problems', 'high', ['min', 'low', 'default', 'high', 'urgent']], ['token', 'Access token (optional)', '']],
    help: 'Install the ntfy app on your phone (or open ntfy.sh/app in a desktop browser and allow notifications), then subscribe to the same topic. Anyone who knows the topic name can read it, so use a name nobody will guess.',
  },
  telegram: {
    label: 'Telegram', desc: 'Messages from your own Telegram bot, on phone and desktop.',
    fields: [['token', 'Bot token', ''], ['chatId', 'Chat ID', '']],
    help: 'Create a bot by messaging @BotFather in Telegram and copy its token. Send your bot any message, then open https://api.telegram.org/bot<token>/getUpdates in a browser: the number after "chat":{"id": is your chat ID.',
  },
  discord: { label: 'Discord', desc: 'Posts to a Discord channel through a webhook.', fields: [['url', 'Webhook URL', '']], help: 'In Discord: channel settings → Integrations → Webhooks → New webhook → Copy webhook URL.' },
  pushover: { label: 'Pushover', desc: 'Push notifications through Pushover.', fields: [['token', 'Application token', ''], ['user', 'User key', '']], help: 'Create an application at pushover.net to get a token; your user key is on the dashboard.' },
  gotify: { label: 'Gotify', desc: 'Your own Gotify push server.', fields: [['server', 'Server URL', ''], ['token', 'Application token', '']], help: '' },
  email: {
    label: 'Email', desc: 'Email through any SMTP server.',
    fields: [['host', 'SMTP server', ''], ['port', 'Port', ''], ['security', 'Security', 'starttls', ['starttls', 'tls', 'none']], ['username', 'User name', ''], ['password', 'Password', ''], ['from', 'From address', ''], ['to', 'Send to (comma separated)', '']],
    help: 'STARTTLS normally uses port 587, TLS port 465. For Gmail, create an app password in your Google account and use smtp.gmail.com.',
  },
  webhook: { label: 'Webhook', desc: 'Calls a URL with the alert as JSON, for Home Assistant and other tools.', fields: [['url', 'URL', ''], ['method', 'Method', 'POST', ['POST', 'PUT', 'GET']]], help: 'The JSON body has event, title, message, reason, target, ip, host, group, rule and time.' },
  log: { label: 'Log file', desc: 'Appends a line per alert to a text file.', fields: [['path', 'File (in the logs folder of the data folder)', 'logs/alerts.log']], help: 'Tab-separated: time, event, target, address, rule, message. The file must be in the logs folder, like logs/alerts.log.' },
  script: { label: 'Run a script', desc: 'Runs a command on the server.', fields: [['command', 'Command', '']], help: 'Runs with sh -c on Linux and macOS, or cmd /C on Windows, in the data folder. Alert details are in the environment: OMNIPING_EVENT, OMNIPING_TARGET, OMNIPING_IP, OMNIPING_MESSAGE and more. In Docker only tools inside the container are available.' },
  browser: { label: 'Open OmniPing pages', desc: 'A pop-up and sound in every open OmniPing page.', fields: [['sound', 'Play a sound', 'on', ['on', 'off']]], help: 'Only works while a page is open. For alerts that reach you anywhere, add ntfy, Telegram or email.' },
};

export function mount(root, section) {
  const offs = [];
  const sec = sections().some(s => s[0] === section) ? section : 'general';
  root.innerHTML = `<div class="st-layout">
    <nav class="st-nav">${sections().map(([id, label]) => `<a href="#/settings/${id}" class="${id === sec ? 'on' : ''}">${label}</a>`).join('')}</nav>
    <div class="st-main" data-main></div>
  </div>`;
  const main = root.querySelector('[data-main]');
  const savedAt = {};

  const row = (label, desc, ctl, key = '') => `<div class="srow"><div><div class="k">${label}</div>${desc ? `<div class="d">${desc}</div>` : ''}</div>
    <div class="ctl">${ctl}${key ? `<span class="saved-flag ${Date.now() - (savedAt[key] || 0) < 1500 ? 'on' : ''}" data-saved="${key}">Saved</span>` : ''}</div></div>`;
  const sw = key => `<label class="switch"><input type="checkbox" data-key="${key}" ${settings()[key] ? 'checked' : ''}><span></span></label>`;
  const num = (key, unit, { min = 0, max = 1e9, step = 1, w = 100 } = {}) =>
    `<input class="input num-in" style="width:${w}px" type="number" data-key="${key}" min="${min}" max="${max}" step="${step}" value="${settings()[key]}">${unit ? `<span class="unit">${unit}</span>` : ''}`;
  const sel = (key, opts) => `<select class="input" data-key="${key}" style="width:auto">${opts.map(([v, l]) => `<option value="${v}" ${String(settings()[key]) === String(v) ? 'selected' : ''}>${esc(l)}</option>`).join('')}</select>`;
  const group = (title, rows) => `<div class="st-group"><h3>${title}</h3>${rows}</div>`;

  function render() {
    const s = settings();
    const n = S.st.targets.length;
    let html = '';
    if (sec === 'general') {
      const secure = window.isSecureContext && 'Notification' in window;
      const perm = secure ? Notification.permission : 'unavailable';
      html = `<h2>General</h2><p class="intro">Starting and stopping, new targets, and settings that only apply to this browser.</p>` +
        group('Pinging', row('Pinging is running', 'Same as the Start/Stop button in the top bar. Stopping keeps all history.', sw('running'), 'running') +
          row('Check for new versions', 'Once a day OmniPing asks GitHub for its latest version and tells you when there is a newer one. Nothing about you or your network is sent.', sw('checkUpdates'), 'checkUpdates') +
          row('Start pinging new targets right away', 'Off: new targets are added paused, and you start each one yourself.', sw('autoStart'), 'autoStart')) +
        group('This browser', row('Theme', 'Light, dark, or follow your device’s setting (System).', themeSwitch().replace('theme-seg', 'theme-seg show-labels')) +
          row('Play a sound for alerts', 'For rules that send to “Open OmniPing pages”. Browsers only play sound after you have clicked the page once.', `<label class="switch"><input type="checkbox" data-local="sound" ${pref('sound', true) ? 'checked' : ''}><span></span></label><button class="btn sm" data-test-sound>${icon('sound')} Test</button>`) +
          row('Desktop notifications', secure
            ? 'Show a system notification when an alert fires while this page is in the background.'
            : 'Browsers only allow this on HTTPS or localhost, and this page is on plain HTTP. Use ntfy or Telegram for notifications on this PC instead (Settings → Notifications).',
            secure ? `<label class="switch"><input type="checkbox" data-local="desktop" ${pref('desktop', false) && perm === 'granted' ? 'checked' : ''}><span></span></label><span class="dim" style="font-size:12px">${perm === 'denied' ? 'Blocked in the browser settings' : ''}</span>` : '<span class="chip">Not available here</span>') +
          row('Date format', `Used everywhere in OmniPing. Today is ${fmtDate(Date.now())}.`, sel('dateFormat', [['dmy', 'DD-MM-YYYY'], ['ymd', 'YYYY-MM-DD'], ['mdy', 'MM/DD/YYYY']]), 'dateFormat'));
      html += passwordGroup();
    }
    if (sec === 'display') {
      const cols = [['address', 'Address'], ['loss', 'Loss'], ['cur', 'Current'], ['avg', 'Average'], ['min', 'Minimum'], ['max', 'Maximum'], ['jitter', 'Jitter'], ['graph', 'Graph']];
      html = `<h2>Display</h2><p class="intro">How the table and graphs look. These apply to every browser.</p>` +
        group('Latency colours', row('Show the legend of good, warning and critical times', '', sw('showLegend'), 'showLegend') +
          row('Warning (yellow) from', 'Averages at or above this are shown in yellow.', num('warnMs', 'ms', { min: 1 }), 'warnMs') +
          row('Critical (red) from', 'Averages at or above this are shown in red.', num('critMs', 'ms', { min: 1 }), 'critMs') +
          row('Tint graph backgrounds by latency zone', 'Green, yellow and red bands behind the timelines.', sw('zoneTint'), 'zoneTint')) +
        group('Graph column', row('Draw × for the current sample', '', sw('drawCurrent'), 'drawCurrent') +
          row('Draw a line for the min–max range', 'Also draws the min–max band on long timelines.', sw('drawMinMax'), 'drawMinMax') +
          row('Scale', 'Logarithmic fits LAN (under 1 ms) and internet targets on one axis. Linear runs from 0 to the highest maximum.', sel('graphScale', [['log', 'Logarithmic, 0.1 ms – 1 s'], ['linear', 'Linear']]), 'graphScale')) +
        group('Timelines', row('Latency scale', 'Automatic fits each graph to its own data.', sel('scaleMode', [['auto', 'Automatic'], ['fixed', 'Fixed']]) + (s.scaleMode === 'fixed' ? num('scaleMs', 'ms', { min: 1, w: 90 }) : ''), 'scaleMode') +
          row('Packet loss scale', 'Loss at or above this fills the graph height. 0 hides packet loss.', num('lossScale', '%', { min: 0, max: 100, w: 80 }), 'lossScale') +
          row('Default time span', 'Each browser remembers the span you pick above the timelines; this is the starting point.', sel('timelineSpanSec', [[60, '1 minute'], [300, '5 minutes'], [600, '10 minutes'], [1800, '30 minutes'], [3600, '1 hour'], [21600, '6 hours'], [86400, '24 hours']]), 'timelineSpanSec') +
          row('Height of each timeline', '', num('timelineHeight', 'px', { min: 32, max: 400, w: 80 }), 'timelineHeight')) +
        group('Columns', row('Columns in the target table', 'Name and status are always shown.', `<span style="display:flex;flex-wrap:wrap;gap:6px 14px;max-width:420px">${cols.map(([k, l]) => `<label class="check"><input type="checkbox" data-col="${k}" ${s.columns.includes(k) ? 'checked' : ''}> ${l}</label>`).join('')}</span>`, 'columns'));
    }
    if (sec === 'packet') {
      html = `<h2>Packet</h2><p class="intro">How pings are sent. Changes apply immediately.</p>` +
        group('Timing', row('Ping interval', 'Time between pings to each target. 1000 ms is one ping per second. Targets can override this in their own settings.', num('intervalMs', 'ms', { min: 100, step: 50 }), 'intervalMs') +
          row('Timeout', 'A reply slower than this counts as lost. While a lost packet waits for its timeout, that graph pauses briefly.', num('timeoutMs', 'ms', { min: 100, step: 100 }), 'timeoutMs') +
          row('Time between pings to different targets', 'Spreads pings out so they don’t all leave at once.', num('staggerMs', 'ms', { min: 0 }), 'staggerMs')) +
        group('Packets', row('Packet size', 'Bytes of data in each ping. 56 gives a 64-byte ICMP packet, like ping on Linux.', num('packetSize', 'bytes', { min: 0, max: 65000 }), 'packetSize') +
          row('Samples to include', 'How many recent samples the table statistics and the focus band use.', num('samplesToInclude', 'samples', { min: 1 }), 'samplesToInclude'));
    }
    if (sec === 'storage') {
      const perTarget = s.memorySamples * 16;
      const hours = s.memorySamples * s.intervalMs / 3600e3;
      html = `<h2>Storage</h2><p class="intro">Every sample is saved to disk in the data folder, so history survives restarts. Recent samples are also kept in memory so graphs open instantly.</p>` +
        group('Memory', row('Samples per target kept in memory', `About ${fmtBytes(perTarget)} per target (${fmtBytes(perTarget * Math.max(1, n))} for ${n} target${n === 1 ? '' : 's'}). At the current interval that is ${hours >= 48 ? (hours / 24).toFixed(1) + ' days' : hours.toFixed(1) + ' hours'}.`, num('memorySamples', 'samples', { min: 1000, step: 1000, w: 120 }), 'memorySamples')) +
        group('Disk', row('Keep every sample for', '0 keeps them forever. Older samples are deleted; one-minute summaries can be kept longer.', num('rawDays', 'days', { min: 0, w: 90 }), 'rawDays') +
          row('Keep one-minute summaries for', 'Used for long time spans (7 or 30 days). They take about 30 KB per target per day. 0 keeps them forever.', num('summaryDays', 'days', { min: 0, w: 90 }), 'summaryDays') +
          row('Write to disk every', 'Samples are collected in memory and written in batches, which is easier on SD cards. A power cut loses at most this much.', num('flushSec', 'seconds', { min: 1, w: 90 }), 'flushSec') +
          row('Disk used now', `${fmtNum(S.st.info.samples)} samples stored in <code>${esc(S.st.info.dataDir)}</code>.`, `<span class="mono" style="font-size:15px">${fmtBytes(S.st.info.diskBytes)}</span>`)) +
        group('Names', row('Look names up again every', 'Targets entered by name follow their address when it changes (dynamic DNS). Alerts can fire when that happens.', num('dnsRefreshMin', 'minutes', { min: 1, w: 90 }), 'dnsRefreshMin'));
    }
    if (sec === 'notifications') {
      html = `<h2>Notifications</h2><p class="intro">Channels are the ways OmniPing can reach you. Alert rules choose which channels to use and when. Use <b>Test</b> to check a channel works.</p>
        <div class="st-group"><h3>Channels</h3>${S.st.channels.map(ch => `<div class="chan">
          <span class="ico">${icon(CHANNEL_ICON[ch.type] || 'bell')}</span>
          <div style="min-width:0"><div class="n">${esc(ch.name)} ${ch.enabled ? '' : '<span class="chip" style="height:20px">off</span>'}</div><div class="m">${esc(CHANNEL_TYPES[ch.type]?.label || ch.type)}${channelSummary(ch)}</div></div>
          <div class="acts"><button class="btn sm" data-test-ch="${ch.id}">Test</button><button class="btn sm" data-edit-ch="${ch.id}">${icon('edit')} Edit</button></div></div>`).join('')}</div>
        ${weeklyGroup(s)}
        <div class="st-group"><h3>Add a channel</h3>${Object.entries(CHANNEL_TYPES).filter(([k]) => k !== 'browser' && (k !== 'windows' || S.st.info.desktop)).map(([k, t]) => {
          const off = k === 'script' && !S.st.info.scripts;
          return `<div class="chan">
          <span class="ico">${icon(CHANNEL_ICON[k])}</span><div><div class="n">${t.label}</div><div class="d muted" style="font-size:12.5px">${off ? 'Turned off. Anyone who can open this page could run commands with it, so it needs <code>OMNIPING_ALLOW_SCRIPTS=true</code> in the server’s environment.' : t.desc}</div></div>
          <div class="acts"><button class="btn sm" data-new-ch="${k}" ${off ? 'disabled' : ''}>${icon('plus')} Add</button></div></div>`;
        }).join('')}</div>
        <div class="tip" style="max-width:900px">${icon('push')}<span><b>Phone and PC, quickest setup:</b> add an ntfy channel with a hard-to-guess topic, install the ntfy app on your phone and open ntfy.sh/app on your PC, subscribe to that topic on both, then add the channel to the “Packet loss” rule under Alerts.</span></div>`;
    }
    if (sec === 'data') {
      const t = Math.round(now());
      const order = groupOrder();
      const ungrouped = S.st.targets.filter(x => !x.group).length;
      const count = g => S.st.targets.filter(x => x.group === g).length;
      html = `<h2>Targets & data</h2><p class="intro">Groups, moving targets in and out, exporting measurements, and backing up your settings.</p>` +
        group('Groups', row('Group new targets automatically', 'A new target without a group goes to Local network, DNS servers or Internet, or to your own group that means the same, like LAN. A group you choose yourself always wins.', sw('autoGroup'), 'autoGroup') +
          (ungrouped ? row('Group the targets that have none', `${ungrouped} target${ungrouped === 1 ? ' has' : 's have'} no group yet.`, `<button class="btn" data-autogroup>${icon('tag')} Group them</button>`) : '') +
          row('Move targets to a group', 'Pick targets and a group, or type a new name to make a group. You can also right-click a target, or drag it onto a group in the table.', `<button class="btn" data-move>${icon('tag')} Move…</button>`) +
          order.map((g, i) => row(esc(g), count(g) ? `${count(g)} target${count(g) === 1 ? '' : 's'}` : 'No targets', `<button class="btn sm icon" data-gmove="-1" data-g="${esc(g)}" title="Move up" ${i ? '' : 'disabled'}>↑</button><button class="btn sm icon" data-gmove="1" data-g="${esc(g)}" title="Move down" ${i < order.length - 1 ? '' : 'disabled'}>↓</button><button class="btn sm" data-grename="${esc(g)}">${icon('edit')} Rename</button><button class="btn sm" data-gungroup="${esc(g)}" data-empty="${count(g) ? '' : 1}">${count(g) ? 'Ungroup' : 'Remove'}</button>`)).join('')) +
        group('Targets', row('Export the target list', 'A .lst file with one address and alias per line.', `<a class="btn" href="api/export/targets" download>${icon('download')} Download .lst</a>`) +
          row('Import targets', 'Paste a list or load a .lst or .mpl file.', `<button class="btn" data-import>${icon('upload')} Import…</button>`) +
          row('Remove all targets', 'Deletes every target and its history. Settings, alert rules and channels stay.', `<button class="btn danger" data-clear>${icon('trash')} Remove all</button>`)) +
        group('Measurements', row('Export samples as CSV', 'Every sample of every target: time, target, address, round trip and lost.', `<select class="input" data-csv-range style="width:auto"><option value="3600000">Last hour</option><option value="86400000" selected>Last 24 hours</option><option value="604800000">Last 7 days</option></select><a class="btn" data-csv href="api/export/csv?from=${t - 86400000}&to=${t}" download>${icon('download')} Download CSV</a>`)) +
        group('Backup', row('Download settings backup', 'config.json with targets, rules, channels and comments. It includes channel passwords and tokens, so keep it private.', `<a class="btn" href="api/export/config" download>${icon('download')} Download</a>`) +
          row('Restore a backup', 'Replaces targets, alert rules, channels and settings with the ones in a backup. History stays, and targets in the backup pick theirs up again. The settings you have now are kept as config.before-restore.json in the data folder.', `<label class="btn">${icon('upload')} Restore…<input type="file" accept=".json,application/json" hidden data-restore></label>`));
    }
    if (sec === 'computer') { html = computer(); shown = appKey(app) + appErr; }
    if (sec === 'about') {
      const i = S.st.info;
      const u = i.update;
      const how = i.desktop ? 'Download the installer or the portable version from the release page. The installer updates OmniPing in place and keeps your settings and history.'
        : i.install === 'docker' ? 'In Docker, pull the new image: in Portainer, open the stack, choose Update the stack and turn on Re-pull image. Settings and history stay in the data volume.'
        : i.install === 'package' ? 'Download the new .deb or .rpm package from the release page and install it the same way as the first one. The service restarts by itself, and settings and history stay in /var/lib/omniping.'
        : 'Download the new program from the release page and put it in place of this one. Settings and history stay in its data folder.';
      html = `<h2>About</h2><p class="intro">OmniPing ${esc(i.version)}: pings many hosts at once and graphs the results live.</p>
        ${u ? `<div class="update-note">${icon('download')}<div><b>OmniPing ${esc(u.version)} is available.</b> You have ${esc(i.version)}. ${how}</div><a class="btn sm primary" href="${esc(u.url)}" target="_blank" rel="noopener">What's new</a></div>` : ''}
        <div class="st-group"><h3>Server</h3><dl class="about">
          <dt>Host</dt><dd>${esc(i.hostname)} (${esc(i.os)}/${esc(i.arch)})</dd>
          <dt>Pinging with</dt><dd>${esc(i.mode)}${i.pingerError ? ` <span class="bad">— ${esc(i.pingerError)}</span>` : ''}</dd>
          <dt>Running since</dt><dd>${esc(fmtDateTime(i.started))} (${fmtDuration(i.now - i.started)})</dd>
          <dt>Data folder</dt><dd>${esc(i.dataDir)}</dd>
          <dt>For Grafana</dt><dd>Prometheus metrics at <a href="metrics" target="_blank" rel="noopener">/metrics</a></dd>
          <dt>Samples stored</dt><dd>${fmtNum(i.samples)}</dd>
          <dt>Disk used</dt><dd>${fmtBytes(i.diskBytes)}</dd>
          <dt>Memory used</dt><dd>${fmtBytes(i.memBytes)}</dd>
          <dt>Clock difference</dt><dd>${Math.abs(S.offset) < 1000 ? 'in sync with this device' : `this device is ${fmtDuration(Math.abs(S.offset))} ${S.offset > 0 ? 'behind' : 'ahead of'} the server`}</dd>
        </dl></div>`;
    }
    // Redrawing replaces every control; put the keyboard focus back on the
    // same one, found by its data- attributes.
    const had = main.contains(document.activeElement) ? document.activeElement : null;
    const focusAt = had && [...had.attributes].filter(x => x.name.startsWith('data-'))
      .map(x => `[${x.name}="${CSS.escape(x.value)}"]`).join('');
    main.innerHTML = html;
    if (focusAt) main.querySelector(focusAt)?.focus({ preventScroll: true });
  }

  // ---------- Password ----------
  let auth = null; // { enabled, fromEnv }
  const loadAuth = async () => {
    try { auth = await api.get('auth'); } catch { auth = null; }
    if (sec === 'general') render();
  };
  function passwordGroup() {
    if (!auth) return '';
    const here = S.st.info.desktop ? ' In the Windows app, this PC itself never needs it.' : '';
    const desc = auth.fromEnv ? `Set with OMNIPING_PASSWORD; change it there.${here}`
      : auth.enabled ? `Every browser and device needs it, and stays signed in for 30 days. Prometheus sends it as basic auth for /metrics.${here}`
        : 'Off: anyone who can reach OmniPing can open it. Set one if OmniPing can be reached from outside your home.';
    const ctl = auth.fromEnv ? '<button class="btn sm" data-pw-out>Sign out</button>'
      : auth.enabled ? '<button class="btn sm" data-pw-set>Change…</button><button class="btn sm" data-pw-off>Turn off</button><button class="btn sm" data-pw-out>Sign out</button>'
        : '<button class="btn sm" data-pw-set>Set a password…</button>';
    return group('Password', row(auth.enabled ? 'Password: on' : 'Password: off', desc, ctl));
  }
  function passwordDialog() {
    const m = modal({
      title: auth.enabled ? 'Change the password' : 'Set a password', size: 'sm',
      body: `<form class="f" style="gap:8px"><label for="pw-1">New password</label><input class="input" type="password" id="pw-1" autocomplete="new-password" autofocus>
        <label for="pw-2">Type it again</label><input class="input" type="password" id="pw-2" autocomplete="new-password">
        <span class="help">At least 8 characters. Other browsers and devices are signed out and need it from now on; this browser stays signed in.</span><button hidden></button></form>`,
      footer: '<span class="spacer"></span><button class="btn" data-close>Cancel</button><button class="btn primary" data-ok>Save</button>',
    });
    const save = async () => {
      const a = m.root.querySelector('#pw-1').value, b = m.root.querySelector('#pw-2').value;
      if ([...a].length < 8) { toast('Too short', 'Use at least 8 characters.', { kind: 'warn' }); return; }
      if (a !== b) { toast('They are not the same', 'Type the same password twice.', { kind: 'warn' }); return; }
      try {
        await api.put('auth', { password: a });
        m.close();
        toast('Password set', 'Other browsers and devices now need it to open OmniPing.', { kind: 'good' });
        loadAuth();
      } catch (err) { errorToast(err); }
    };
    m.root.querySelector('[data-ok]').addEventListener('click', save);
    m.root.querySelector('form').addEventListener('submit', e => { e.preventDefault(); save(); });
  }

  // ---------- Weekly summary ----------
  const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  // The preview is fetched at most once a minute; it reads a week of history.
  let preview = { text: '', at: 0, loading: false };
  function weeklyGroup(s) {
    const chans = S.st.channels.filter(c => c.type !== 'browser');
    const picked = s.weeklyChannels || [];
    const when = `<select class="input" data-key="weeklyDay" style="width:auto" aria-label="Day">${DAYS.map((d, i) => `<option value="${i}" ${s.weeklyDay === i ? 'selected' : ''}>${d}</option>`).join('')}</select>
      <input class="input mono" type="time" data-key="weeklyTime" value="${esc(s.weeklyTime)}" style="width:110px" aria-label="Time">`;
    const to = chans.length ? `<div class="weekly-chans">${chans.map(c => `<label class="check"><input type="checkbox" data-weekly-ch="${esc(c.id)}" ${picked.includes(c.id) ? 'checked' : ''}> ${esc(c.name)}${c.enabled ? '' : ' <span class="muted">(off)</span>'}</label>`).join('')}</div>` : '<span class="muted">Add a channel above first.</span>';
    if (s.weekly && Date.now() - preview.at > 60_000 && !preview.loading) loadPreview();
    return group('Weekly summary', row('Send a weekly summary', 'Once a week: the internet outages, the lowest uptime, the most packet loss and the alerts of the last 7 days, with the same numbers as the report.', sw('weekly'), 'weekly') +
      (s.weekly ? row('When', "In the server's time zone.", when, 'weeklyDay') +
        row('Send it to', '', to, 'weeklyChannels') +
        `<div class="srow"><div style="min-width:0"><div class="k">Preview</div><pre class="weekly-preview" data-weekly-preview>${esc(preview.text || 'Working it out…')}</pre></div>
          <div class="ctl"><button class="btn sm" data-weekly-send ${picked.length ? '' : 'disabled'}>Send now</button></div></div>` : ''));
  }
  async function loadPreview() {
    preview.loading = true;
    try {
      const p = await api.get('summary');
      preview = { text: `${p.title}
${p.body}`, at: Date.now(), loading: false };
    } catch (err) { preview = { text: 'Could not make the summary: ' + err.message, at: Date.now(), loading: false }; }
    const box = main.querySelector('[data-weekly-preview]');
    if (box) box.textContent = preview.text;
  }

  // ---------- This computer: the Windows app's own settings ----------
  let app = null, appErr = '', shown = '';
  // Redraws only when a setting differs from what the page shows: a redraw
  // while someone is clicking would swallow the click. The size of the data
  // grows all the time, so it is updated in place.
  const appKey = a => JSON.stringify(a && { ...a, dataBytes: 0 });
  const loadApp = async () => {
    try { app = await api.get('app'); appErr = ''; } catch (err) { if (!app) appErr = err.message; }
    if (sec !== 'computer') return;
    if (!typing() && appKey(app) + appErr !== shown) { render(); return; }
    const size = main.querySelector('[data-app-bytes]');
    if (size && app) size.textContent = fmtBytes(app.dataBytes);
  };
  const typing = () => main.contains(document.activeElement) && document.activeElement.matches('input[type=number], input:not([type])');
  const noSlash = u => u.replace(/\/$/, '');

  function computer() {
    if (!app) return `<h2>This computer</h2>${appErr ? `<p class="err-text">Could not load these settings: ${esc(appErr)}</p>` : '<p class="none">Loading…</p>'}`;
    const a = app;
    const mono = s => `<span class="mono">${esc(noSlash(s))}</span>`;
    const path = p => `<span class="path">${esc(p)}</span>`;
    const appSwitch = (key, label, disabled = false) => `<label class="switch"><input type="checkbox" data-app="${key}" aria-label="${esc(label)}" ${a[key] ? 'checked' : ''} ${disabled ? 'disabled' : ''}><span></span></label>`;
    const addrs = a.addresses || [];
    const reach = a.allDevices
      ? (addrs.length ? `On: your phone and other devices on your network open it at ${addrs.map(mono).join(' or ')}.` : 'On, but this PC has no address on a home or office network right now.')
      : 'Off: only this PC can open OmniPing. On: your phone can open it too.';
    const firewall = 'Windows asks once whether to let OmniPing through the firewall. OmniPing has no login, so only do this on a network you trust, like at home.';
    const wins = S.st.channels.filter(c => c.type === 'windows');
    const notes = group('Windows notifications', row('Alerts as Windows notifications',
      'Shows alerts in the corner of the screen and in the notification centre, also when no OmniPing page is open. ' +
        (wins.length ? `Set up as ${wins.map(c => `“${esc(c.name)}”`).join(', ')}; add it to a rule under <a href="#/alerts">Alerts</a>.` : 'Add the channel, then add it to a rule under Alerts.'),
      wins.length ? `<button class="btn sm" data-edit-ch="${esc(wins[0].id)}">${icon('edit')} Edit</button>` : `<button class="btn sm" data-new-ch="windows">${icon('plus')} Add channel</button>`));

    // Other devices see what there is, but only this PC can change it.
    if (!a.local) {
      return `<h2>This computer</h2><p class="intro">OmniPing runs as an app on a Windows PC, with its icon next to the clock.</p>
        <div class="tip st-note">${icon('note')}<span>These settings can only be changed on the computer OmniPing runs on.</span></div>` +
        group('Web address', row('Port', '', `<span class="mono" style="font-size:14px">${a.port}</span>`) +
          row('Other devices on your network', a.allDevices && addrs.length ? `Open OmniPing at ${addrs.map(mono).join(' or ')}.` : '', `<span class="chip">${a.allDevices ? 'On' : 'Off'}</span>`)) +
        notes;
    }
    return `<h2>This computer</h2><p class="intro">OmniPing runs as an app on this Windows PC, with its icon next to the clock. These settings are about the program itself, so they can only be changed on this PC.</p>` +
      group('Web address', row('Port', `The page is at ${mono(a.url)}. If the port is taken when OmniPing starts, it moves to the next free one and tells you.`,
        `<input class="input num-in" style="width:90px" type="number" min="1" max="65535" data-app-port value="${a.port}" aria-label="Port"><button class="btn sm" data-app-apply>Apply</button>`) +
        (a.movedFrom ? `<div class="srow note"><div class="tip"><b class="mark">!</b><span>Port ${a.movedFrom} was in use by another program when OmniPing started, so it moved to ${a.port}. Change it here if you’d rather use another.</span></div></div>` : '') +
        row('Other devices on your network', `${reach} ${firewall}`, appSwitch('allDevices', 'Other devices on your network'), 'app-allDevices')) +
      group('Start-up', row('Start with Windows', a.autostartSupported
        ? 'Starts quietly next to the clock when you sign in, so OmniPing keeps watching after a restart. Task Manager lists it under Startup apps.'
        : 'Not available for this copy of OmniPing.', appSwitch('autostart', 'Start with Windows', !a.autostartSupported), 'app-autostart') +
        row('Open the page when you start OmniPing yourself', 'Off: it only appears next to the clock, and you open the page from there. When it starts with Windows, it never opens the page.', appSwitch('openOnStart', 'Open the page when you start OmniPing yourself'), 'app-openOnStart')) +
      group('Files', row('Data folder', `Settings, history and alert logs: <span data-app-bytes>${fmtBytes(a.dataBytes)}</span> now. ${a.portable ? 'Portable: the data stays next to OmniPing.exe.' : a.canMove ? 'Moving it restarts OmniPing.' : ''}`,
        `${path(a.dataDir)}<button class="btn sm" data-app-open="data" aria-label="Open the data folder">Open</button>${a.canMove ? '<button class="btn sm" data-app-move aria-label="Move the data folder">Move…</button>' : ''}`) +
        (a.logFile ? row('Log', 'What OmniPing did and any errors, for when something goes wrong.', `${path(a.logFile)}<button class="btn sm" data-app-open="log" aria-label="Open the log">Open</button>`) : '')) +
      notes;
  }

  // Changes the port or who can connect: OmniPing opens the new address
  // first, and the page follows, because this one stops answering.
  const saveApp = async (change, key) => {
    try {
      const res = await api.put('app', change);
      if (res.url && new URL(res.url, location.href).origin !== location.origin) {
        toast(`Moving to ${noSlash(res.url)}…`, '', { timeout: 0 });
        location.href = res.url + (res.url.includes('#') ? '' : location.hash);
        return;
      }
      app = res.app;
      render();
      if (key) flash(key);
    } catch (err) { errorToast(err); render(); }
  };

  async function moveData() {
    let dir = await promptDialog('Move the data folder', {
      label: 'New data folder, for example D:\\OmniPing', ok: 'Restart and move',
      help: 'OmniPing restarts and moves its settings and history there. Use a new or empty folder.',
    });
    // Explorer's "Copy as path" puts quotes around it.
    dir = (dir || '').trim().replace(/^"(.*)"$/, '$1').trim();
    if (!dir) return;
    const started = S.st.info.started;
    try { await api.post('app/move', { dir }); } catch (err) { errorToast(err); return; }
    const closeNote = toast('OmniPing is restarting…', `It moves its data to ${dir}. This page reloads when it’s back.`, { timeout: 0 });
    const failed = await waitForRestart(started);
    if (!failed) return;
    closeNote();
    const why = failed[0].toUpperCase() + failed.slice(1).replace(/\.?$/, '.');
    toast('OmniPing could not restart', `${why} Your data moves to ${dir} the next time OmniPing starts.`, { kind: 'bad', timeout: 0 });
    loadApp();
  }

  function channelSummary(ch) {
    const c = ch.config || {};
    const parts = { ntfy: c.topic && ` · ${c.server || 'https://ntfy.sh'}/${c.topic}`, telegram: c.chatId && ` · chat ${c.chatId}`, email: c.to && ` · to ${c.to}`, webhook: c.url && ` · ${c.method || 'POST'} ${c.url}`, discord: c.url && ' · webhook set', gotify: c.server && ` · ${c.server}`, log: c.path && ` · ${c.path}`, script: c.command && ` · ${c.command}`, browser: ` · sound ${c.sound === 'off' ? 'off' : 'on'}`, pushover: c.user && ' · user key set' }[ch.type];
    return esc(parts || '');
  }

  const flash = key => {
    savedAt[key] = Date.now();
    const f = main.querySelector(`[data-saved="${key}"]`);
    if (f) { f.classList.add('on'); setTimeout(() => f.classList.remove('on'), 1500); }
  };
  const save = async (key, value) => {
    try { await api.put('settings', { [key]: value }); flash(key); } catch (err) { errorToast(err); render(); }
  };

  main.addEventListener('change', async e => {
    const t = e.target;
    if (t.matches('[data-weekly-ch]')) {
      save('weeklyChannels', [...main.querySelectorAll('[data-weekly-ch]')].filter(x => x.checked).map(x => x.dataset.weeklyCh));
      return;
    }
    if (t.matches('[data-key]')) {
      const key = t.dataset.key;
      const cur = settings()[key];
      let v = t.type === 'checkbox' ? t.checked : t.value;
      if (typeof cur === 'number') v = Math.round(+v);
      save(key, v);
      return;
    }
    if (t.matches('[data-col]')) {
      const cols = [...main.querySelectorAll('[data-col]')].filter(x => x.checked).map(x => x.dataset.col);
      save('columns', cols);
      return;
    }
    if (t.matches('[data-app]')) { saveApp({ [t.dataset.app]: t.checked }, 'app-' + t.dataset.app); return; }
    if (t.matches('[data-local="sound"]')) { setPref('sound', t.checked); return; }
    if (t.matches('[data-local="desktop"]')) {
      if (t.checked && Notification.permission !== 'granted') {
        const p = await Notification.requestPermission();
        if (p !== 'granted') { t.checked = false; toast('Notifications blocked', 'Allow notifications for this page in the browser settings.', { kind: 'warn' }); return; }
      }
      setPref('desktop', t.checked);
      return;
    }
    if (t.matches('[data-csv-range]')) {
      const to = Math.round(now());
      main.querySelector('[data-csv]').href = `api/export/csv?from=${to - +t.value}&to=${to}`;
    }
    if (t.matches('[data-restore]')) {
      const f = t.files[0];
      t.value = '';
      if (!f) return;
      let cfg;
      try { cfg = JSON.parse(await f.text()); } catch { toast('Not a backup', `${f.name} is not a settings backup (config.json).`, { kind: 'bad' }); return; }
      const n = Array.isArray(cfg.targets) ? cfg.targets.length : 0, r = Array.isArray(cfg.rules) ? cfg.rules.length : 0;
      if (!await confirmDialog(`Replace everything with ${f.name}? It has ${n} target${n === 1 ? '' : 's'} and ${r} alert rule${r === 1 ? '' : 's'}. The ${S.st.targets.length} targets and settings you have now are kept as config.before-restore.json.`, { title: 'Restore a backup', ok: 'Restore' })) return;
      try {
        const res = await api.post('import/config', cfg);
        toast('Backup restored', `${res.targets} targets, ${res.rules} alert rules, ${res.channels} channels`, { kind: 'good' });
      } catch (err) { errorToast(err); }
    }
  });
  main.addEventListener('click', async e => {
    if (e.target.closest('[data-test-sound]')) { chime('start'); return; }
    if (e.target.closest('[data-pw-set]')) { passwordDialog(); return; }
    if (e.target.closest('[data-pw-off]')) {
      if (!await confirmDialog('Turn the password off? Anyone who can reach OmniPing can then open it again.', { title: 'Turn the password off', ok: 'Turn off' })) return;
      try { await api.put('auth', { password: '' }); toast('Password off', 'OmniPing opens without a password again.'); loadAuth(); } catch (err) { errorToast(err); }
      return;
    }
    if (e.target.closest('[data-pw-out]')) {
      try { await api.post('logout'); } catch { /* signed out anyway */ }
      location.href = 'login';
      return;
    }
    if (e.target.closest('[data-weekly-send]')) {
      try {
        const r = await api.post('summary/send', {});
        toast('Summary sent', `To ${r.sent} channel${r.sent === 1 ? '' : 's'}. Check there that it arrived.`, { kind: 'good' });
      } catch (err) { errorToast(err); }
      return;
    }
    if (e.target.closest('[data-app-apply]')) {
      const v = +main.querySelector('[data-app-port]').value;
      if (!Number.isInteger(v) || v < 1 || v > 65535) { toast('Not a port', 'Use a number from 1 to 65535.', { kind: 'bad' }); return; }
      if (v !== app.port) saveApp({ port: v });
      return;
    }
    const ao = e.target.closest('[data-app-open]');
    if (ao) { api.post('app/open', { what: ao.dataset.appOpen }).catch(errorToast); return; }
    if (e.target.closest('[data-app-move]')) { moveData(); return; }
    if (e.target.closest('[data-import]')) { openAddTargets('list'); return; }
    if (e.target.closest('[data-move]')) { openMoveToGroup(); return; }
    if (e.target.closest('[data-autogroup]')) {
      try { const r = await api.post('groups/auto'); toast('Targets grouped', `${r.grouped} target${r.grouped === 1 ? '' : 's'} put in a group`, { kind: 'good', timeout: 3000 }); } catch (err) { errorToast(err); }
      return;
    }
    const gm = e.target.closest('[data-gmove]');
    if (gm) { moveGroup(gm.dataset.g, +gm.dataset.gmove); return; }
    const gr = e.target.closest('[data-grename]');
    if (gr) {
      const from = gr.dataset.grename;
      const to = await promptDialog('Rename group', { label: 'Name', value: from });
      if (to != null && to.trim() && to.trim() !== from) api.put('groups', { rename: { from, to: to.trim() } }).catch(errorToast);
      return;
    }
    const gu = e.target.closest('[data-gungroup]');
    if (gu) {
      const g = gu.dataset.gungroup;
      if (gu.dataset.empty || await confirmDialog(`Take the targets in ${g} out of the group? The targets stay; only the group goes.`, { title: 'Ungroup', ok: 'Ungroup' })) api.put('groups', { rename: { from: g, to: '' } }).catch(errorToast);
      return;
    }
    if (e.target.closest('[data-clear]')) {
      if (await confirmDialog(`Remove all ${S.st.targets.length} targets and their history? This cannot be undone.`, { title: 'Remove all targets', ok: 'Remove all', danger: true })) {
        api.post('targets/clear').then(() => toast('All targets removed', '', { timeout: 3000 })).catch(errorToast);
      }
      return;
    }
    const tc = e.target.closest('[data-test-ch]');
    if (tc) {
      const ch = S.st.channels.find(c => c.id === tc.dataset.testCh);
      tc.disabled = true;
      try {
        const r = await api.post('channels/test', ch);
        if (r.ok) toast('Test sent', `${ch.name}: check that it arrived.`, { kind: 'good' });
        else toast('Test failed', `${ch.name}: ${r.error}`, { kind: 'bad', timeout: 12000 });
      } catch (err) { errorToast(err); }
      tc.disabled = false;
      return;
    }
    const ec = e.target.closest('[data-edit-ch]');
    if (ec) { editChannel(S.st.channels.find(c => c.id === ec.dataset.editCh)); return; }
    const nc = e.target.closest('[data-new-ch]');
    if (nc) {
      const type = nc.dataset.newCh, def = CHANNEL_TYPES[type];
      const config = Object.fromEntries(def.fields.map(([k, , d]) => [k, d]));
      if (type === 'ntfy') config.topic = 'omniping-' + Math.random().toString(36).slice(2, 10);
      editChannel({ id: '', name: def.label, type, enabled: true, config });
    }
  });

  main.addEventListener('keydown', e => {
    if (e.key === 'Enter' && e.target.matches('[data-app-port]')) main.querySelector('[data-app-apply]').click();
  });

  offs.push(on('state', () => {
    if (typing()) return;
    render();
  }));
  offs.push(on('info', () => {
    if (sec === 'about' || sec === 'storage') render();
    if (sec === 'computer') loadApp();
  }));
  if (sec === 'general') loadAuth();
  // Start with Windows can also change in the tray menu or Task Manager.
  if (sec === 'computer') {
    loadApp();
    // Coming back to the page usually means a click is on its way; let it
    // land before looking for changes.
    const onFocus = () => setTimeout(loadApp, 800);
    window.addEventListener('focus', onFocus);
    offs.push(() => window.removeEventListener('focus', onFocus));
  }
  render();
  return { unmount() { offs.forEach(f => f()); } };
}

// After a move OmniPing restarts. The new copy has another start time, so
// reload as soon as that answers. Copying a large history can take a while:
// after a minute say so, and keep looking, less often. While the old copy
// still answers, it can say the restart failed; then this returns why.
async function waitForRestart(started) {
  for (let i = 0; ; i++) {
    await new Promise(r => setTimeout(r, i < 60 ? 1000 : 5000));
    try {
      const info = await (await fetch('api/info', { cache: 'no-store' })).json();
      if (info.started !== started) break;
      const app = await (await fetch('api/app', { cache: 'no-store' })).json();
      if (app.restartError) return app.restartError;
    } catch { /* not back yet */ }
    if (i === 60) toast('OmniPing isn’t back yet', 'It may still be copying the history, or showing a message next to the clock. This page reloads when it’s back.', { timeout: 0 });
  }
  location.reload();
  return '';
}

function editChannel(ch) {
  const def = CHANNEL_TYPES[ch.type];
  const field = ([k, label, , opts]) => {
    const v = ch.config?.[k] ?? '';
    const secret = /password|token|secret/i.test(k);
    if (opts) return `<div class="f"><label for="ch-${k}">${label}</label><select class="input" id="ch-${k}" data-cfg="${k}">${opts.map(o => `<option ${o === v ? 'selected' : ''}>${o}</option>`).join('')}</select></div>`;
    return `<div class="f"><label for="ch-${k}">${label}</label><input class="input ${secret || k === 'topic' || k === 'url' || k === 'server' ? 'mono' : ''}" id="ch-${k}" data-cfg="${k}" ${secret ? 'type="password" autocomplete="new-password"' : ''} value="${esc(v)}"></div>`;
  };
  const m = modal({
    title: ch.id ? `Edit ${ch.name}` : `Add ${def.label}`, size: 'md',
    body: `<div class="frow" style="grid-template-columns:1fr auto;align-items:end">
        <div class="f"><label for="ch-name">Name</label><input class="input" id="ch-name" value="${esc(ch.name)}"></div>
        <label class="check" style="height:34px"><input type="checkbox" id="ch-enabled" ${ch.enabled ? 'checked' : ''}> Enabled</label></div>
      ${def.fields.length ? `<div class="frow" style="grid-template-columns:1fr 1fr">${def.fields.map(field).join('')}</div>` : ''}
      ${def.help ? `<div class="tip">${icon('note')}<span>${esc(def.help)}</span></div>` : ''}
      <div data-result></div>`,
    footer: `${ch.id && ch.id !== 'browser' ? `<button class="btn danger" data-del>${icon('trash')} Delete</button>` : ''}<span class="spacer"></span><button class="btn" data-test>Send test</button><button class="btn" data-close>Cancel</button><button class="btn primary" data-save>Save</button>`,
  });
  const collect = () => {
    const config = {};
    m.root.querySelectorAll('[data-cfg]').forEach(i => { config[i.dataset.cfg] = i.value.trim(); });
    return { ...ch, name: m.root.querySelector('#ch-name').value.trim() || def.label, enabled: m.root.querySelector('#ch-enabled').checked, config };
  };
  const result = m.root.querySelector('[data-result]');
  m.root.querySelector('[data-test]').addEventListener('click', async e => {
    e.target.disabled = true;
    result.innerHTML = '<p class="none">Sending…</p>';
    try {
      const r = await api.post('channels/test', collect());
      result.innerHTML = r.ok ? `<div class="tip" style="color:var(--good)">${icon('check')}<span>Sent. Check that it arrived.</span></div>` : `<div class="tip" style="color:var(--bad)">${icon('x')}<span>${esc(r.error)}</span></div>`;
    } catch (err) { result.innerHTML = `<p class="err-text">${esc(err.message)}</p>`; }
    e.target.disabled = false;
  });
  m.root.querySelector('[data-save]').addEventListener('click', async () => {
    const c = collect();
    try {
      if (c.id) await api.put('channels/' + c.id, c); else await api.post('channels', c);
      m.close();
      toast('Channel saved', c.name + (ch.id ? '' : ' · add it to an alert rule under Alerts'), { kind: 'good', timeout: 5000 });
    } catch (err) { errorToast(err); }
  });
  const del = m.root.querySelector('[data-del]');
  if (del) del.addEventListener('click', async () => {
    if (!(await confirmDialog(`Delete the channel “${ch.name}”? Rules that use it stop sending to it.`, { ok: 'Delete', danger: true }))) return;
    try { await api.del('channels/' + ch.id); m.close(); } catch (err) { errorToast(err); }
  });
}
