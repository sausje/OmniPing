// Dialogs for adding, editing and removing targets, and for comments.

import { S, on, pref, setPref, settings, addrOf, offText, serverClockDiffers } from './store.js';
import { api } from './api.js';
import { fmtMs, fmtNum, fmtTime, fmtDateTime, fmtInterval, esc } from './fmt.js';
import { modal, icon, toast, errorToast, confirmDialog, promptDialog, el } from './ui.js';

const INTERVALS = [100, 250, 500, 1000, 2000, 2500, 5000, 10000, 30000, 60000, 300000];

/** Remember what was typed in the add box, to suggest it again. */
export function rememberEntry(text) {
  const h = pref('history', []).filter(x => x !== text);
  h.unshift(text);
  setPref('history', h.slice(0, 30));
}

export async function addTargets(text, group = '') {
  const res = await api.post('targets', { text, group });
  const n = res.added.length;
  if (n) toast(n === 1 ? 'Target added' : `${n} targets added`, res.skipped?.length ? `${res.skipped.length} already in the list` : '', { kind: 'good', timeout: 3000 });
  else if (res.skipped?.length) toast('Already in the list', res.skipped.join(', '), { kind: 'warn' });
  if (res.invalid?.length) toast(`${res.invalid.length} line${res.invalid.length === 1 ? '' : 's'} skipped`, `Not an IP address or name: ${res.invalid.slice(0, 3).join(', ')}`, { kind: 'warn', timeout: 9000 });
  return res;
}

function groupOptions() {
  return `<datalist id="op-groups">${S.st.groups.map(g => `<option value="${esc(g)}">`).join('')}</datalist>`;
}

function scanState() {
  if (S.scan && !S.scan.found) S.scan.found = [];
  return S.scan;
}

function guessStart() {
  const counts = {};
  for (const t of S.st.targets) {
    const m = (t.ip || '').match(/^(\d+\.\d+\.\d+)\.\d+$/);
    if (m && /^(10|192\.168|172\.(1[6-9]|2\d|3[01]))\./.test(t.ip)) counts[m[1]] = (counts[m[1]] || 0) + 1;
  }
  const best = Object.entries(counts).sort((a, b) => b[1] - a[1])[0];
  return best ? best[0] + '.1' : '192.168.1.1';
}

/** The "Add targets" dialog. tab: single, list or scan. */
export function openAddTargets(tab = 'single', preset = '') {
  const offs = [];
  const m = modal({
    title: 'Add targets',
    tabs: `<div class="tabs" role="tablist">
      <button data-tab="single" role="tab">Single target</button>
      <button data-tab="list" role="tab">List or file</button>
      <button data-tab="scan" role="tab">Scan address range</button></div>`,
    body: '<div data-pane></div>',
    footer: '<span class="muted" data-foot-note style="font-size:12.5px"></span><span class="spacer"></span><button class="btn" data-close>Close</button><button class="btn primary" data-go></button>',
    onClose: () => offs.forEach(f => f()),
  });
  const pane = m.body.querySelector('[data-pane]');
  const go = m.root.querySelector('[data-go]');
  const note = m.root.querySelector('[data-foot-note]');
  let mode = tab;

  function show(t) {
    mode = t;
    m.root.querySelectorAll('[data-tab]').forEach(b => { b.classList.toggle('on', b.dataset.tab === t); b.setAttribute('aria-selected', b.dataset.tab === t); });
    note.textContent = '';
    go.disabled = false;
    if (t === 'single') single();
    if (t === 'list') list();
    if (t === 'scan') scan();
  }
  m.root.querySelector('.tabs').addEventListener('click', e => { const b = e.target.closest('[data-tab]'); if (b) show(b.dataset.tab); });

  function single() {
    pane.innerHTML = `<form class="frow" data-form style="grid-template-columns:1.3fr 1fr 1fr">
        <div class="f"><label for="nt-host">IP address or name</label><input class="input mono" id="nt-host" placeholder="192.168.1.1 or google.com" autofocus value="${esc(preset)}"></div>
        <div class="f"><label for="nt-name">Alias (optional)</label><input class="input" id="nt-name" placeholder="Router"></div>
        <div class="f"><label for="nt-group">Group (optional)</label><input class="input" id="nt-group" list="op-groups" placeholder="${settings().autoGroup ? 'automatic' : 'LAN'}">${groupOptions()}</div>
        ${methodFields('nt', 0)}
        <div class="f"><label for="nt-iv">Ping interval</label><select class="input" id="nt-iv"><option value="0">Default (${fmtInterval(settings().intervalMs)})</option>${INTERVALS.map(v => `<option value="${v}">${fmtInterval(v)}</option>`).join('')}</select></div>
        <button hidden></button>
      </form>
      <div class="tip">${icon('note')}<span>You can also type an address and an alias together in the top bar, like <code>192.168.1.20 Raspberry Pi</code>, and press Enter. Add <code>:port</code> to time a TCP connection instead, like <code>192.168.1.10:3389</code>, or check a DNS server with <code>dns 1.1.1.1 example.com</code>.</span></div>`;
    go.textContent = 'Add target';
    wireMethod(pane, 'nt');
    pane.querySelector('[data-form]').addEventListener('submit', e => { e.preventDefault(); go.click(); });
    setTimeout(() => pane.querySelector('#nt-host').focus(), 30);
  }

  function list() {
    pane.innerHTML = `<div class="f"><label for="nt-list">One target per line: an address, then an optional alias</label>
        <textarea class="input" id="nt-list" rows="10" placeholder="192.168.1.1 Router&#10;192.168.1.10 NAS&#10;google.com&#10;8.8.8.8, Google DNS, Internet"></textarea>
        <span class="help">Lines can also be <code>address, alias, group</code>. Lines starting with # are ignored.</span></div>
      <div class="frow" style="grid-template-columns:auto 1fr;align-items:end">
        <div class="f"><label>Read from a file</label><label class="btn" style="width:max-content">${icon('upload')} Load from file…<input type="file" accept=".lst,.mpl,.txt,.csv,text/plain" hidden data-file></label></div>
        <div class="f"><label for="nt-lgroup">Group for lines without one</label><input class="input" id="nt-lgroup" list="op-groups" placeholder="${settings().autoGroup ? 'automatic' : 'optional'}">${groupOptions()}</div>
      </div>
      <div class="tip">${icon('note')}<span>MultiPing's <code>.lst</code> and <code>.mpl</code> files work here. In MultiPing, use File → Save target list, or copy the addresses from its window.</span></div>`;
    const ta = pane.querySelector('#nt-list');
    const count = () => {
      const n = ta.value.split('\n').map(l => l.trim()).filter(l => l && !/^(#|;|\/\/|<|\[[A-Za-z])/.test(l)).length;
      go.textContent = n ? `Add ${n} target${n === 1 ? '' : 's'}` : 'Add targets';
    };
    ta.addEventListener('input', count);
    pane.querySelector('[data-file]').addEventListener('change', async e => {
      const f = e.target.files[0];
      if (!f) return;
      ta.value = (ta.value ? ta.value.trimEnd() + '\n' : '') + await f.text();
      count();
    });
    count();
  }

  function scan() {
    const st = scanState();
    pane.innerHTML = `<div class="frow" style="grid-template-columns:repeat(4,1fr)">
        <div class="f"><label for="sc-start">First address</label><input class="input mono" id="sc-start" value="${esc(st?.start || guessStart())}"></div>
        <div class="f"><label for="sc-count">Number of addresses</label><input class="input mono" id="sc-count" type="number" min="1" max="4096" value="${st?.total || 254}"></div>
        <div class="f"><label for="sc-group">Add to group</label><input class="input" id="sc-group" list="op-groups" placeholder="optional">${groupOptions()}</div>
        <div class="f"><label for="sc-wait">Wait for reply</label><select class="input mono" id="sc-wait"><option value="500">500 ms</option><option value="1000" selected>1000 ms</option><option value="2000">2000 ms</option></select></div>
      </div>
      <label class="check"><input type="checkbox" id="sc-names" checked> Look up device names</label>
      <div data-progress></div>
      <div data-results></div>`;
    renderScan();
  }

  const chosen = new Set();
  function renderScan() {
    if (mode !== 'scan') return;
    const st = scanState();
    const prog = pane.querySelector('[data-progress]'), resBox = pane.querySelector('[data-results]');
    if (!st || (!st.running && !st.found?.length && !st.done)) {
      prog.innerHTML = '<div class="tip">' + icon('radar') + '<span>OmniPing pings every address in the range once and lists the ones that answer. Scanning 254 addresses takes about 10 seconds.</span></div>';
      resBox.innerHTML = '';
      go.textContent = 'Start scan';
      note.textContent = '';
      return;
    }
    const pct = st.total ? Math.round(st.done * 100 / st.total) : 0;
    prog.innerHTML = `<div style="display:flex;justify-content:space-between;font-size:12.5px;color:var(--muted);margin-bottom:7px">
        <span>${st.running ? `Scanning <span class="mono" style="color:var(--fg)">${esc(st.current || st.start)}</span> · ${st.done} of ${st.total}` : st.error === 'stopped' ? `Stopped after ${st.done} of ${st.total}` : `Scanned ${st.total} addresses`}</span>
        <span><b style="color:var(--fg)">${st.found.length}</b> replied · ${st.found.filter(f => !f.known).length} new</span></div>
      <div class="progress"><i style="width:${pct}%"></i></div>`;
    for (const f of st.found) if (!f.known && !chosen.has('x' + f.ip)) { chosen.add(f.ip); chosen.add('x' + f.ip); }
    resBox.innerHTML = st.found.length ? `<div class="disc-table"><table>
        <thead><tr><th style="width:40px"></th><th>Address</th><th>Name</th><th>Reply</th><th></th></tr></thead>
        <tbody>${st.found.map(f => `<tr class="${f.known ? 'have' : ''}"><td><input type="checkbox" data-ip="${f.ip}" ${f.known ? 'disabled' : chosen.has(f.ip) ? 'checked' : ''} aria-label="Add ${f.ip}"></td>
          <td class="m">${f.ip}</td><td>${f.name ? esc(f.name) : '<span class="dim">no name</span>'}</td><td class="m">${fmtMs(f.rtt)} ms</td><td>${f.known ? 'Already monitored' : ''}</td></tr>`).join('')}</tbody></table></div>` : '';
    const n = st.found.filter(f => !f.known && chosen.has(f.ip)).length;
    note.textContent = st.found.length ? `${n} new device${n === 1 ? '' : 's'} selected` : '';
    go.textContent = st.running ? 'Stop scan' : n ? `Add ${n} target${n === 1 ? '' : 's'}` : 'Scan again';
  }
  pane.addEventListener('change', e => {
    const cb = e.target.closest('[data-ip]');
    if (!cb) return;
    if (cb.checked) chosen.add(cb.dataset.ip); else chosen.delete(cb.dataset.ip);
    renderScan();
  });
  offs.push(on('discover', renderScan));

  go.addEventListener('click', async () => {
    try {
      if (mode === 'single') {
        let host = pane.querySelector('#nt-host').value.trim();
        if (!host) { pane.querySelector('#nt-host').focus(); return; }
        const how = methodValue(pane, 'nt');
        if (!how) return;
        if (how.port) host = /:/.test(host) ? `[${host.replace(/^\[|\]$/g, '')}]:${how.port}` : `${host}:${how.port}`;
        if (how.lookup) host = `dns ${host} ${how.lookup}`;
        const name = pane.querySelector('#nt-name').value.trim();
        const group = pane.querySelector('#nt-group').value.trim();
        const iv = +pane.querySelector('#nt-iv').value;
        const res = await addTargets(name ? `${host} ${name}` : host, group);
        if (iv && res.added[0]) await api.patch('targets/' + res.added[0], { intervalMs: iv });
        rememberEntry(name ? `${host} ${name}` : host);
        pane.querySelector('#nt-host').value = ''; pane.querySelector('#nt-name').value = '';
        pane.querySelector('#nt-host').focus();
      } else if (mode === 'list') {
        const text = pane.querySelector('#nt-list').value;
        if (!text.trim()) return;
        await addTargets(text, pane.querySelector('#nt-lgroup').value.trim());
        m.close();
      } else {
        const st = scanState();
        if (st && st.running) { await api.post('discover/stop'); return; }
        const n = st ? st.found.filter(f => !f.known && chosen.has(f.ip)) : [];
        if (st && !st.running && n.length) {
          const text = n.map(f => f.name ? `${f.ip} ${f.name.split('.')[0]}` : f.ip).join('\n');
          await addTargets(text, pane.querySelector('#sc-group').value.trim());
          m.close();
          return;
        }
        chosen.clear();
        S.scan = await api.post('discover', {
          start: pane.querySelector('#sc-start').value.trim(),
          count: +pane.querySelector('#sc-count').value,
          timeoutMs: +pane.querySelector('#sc-wait').value,
          names: pane.querySelector('#sc-names').checked,
        });
        renderScan();
      }
    } catch (err) { errorToast(err); }
  });

  if (S.scan == null) api.get('discover').then(s => { S.scan = s; renderScan(); }).catch(() => {});
  show(tab);
}

/** "Ping with" fields: ICMP, the time to open a TCP connection to a port,
 * or a DNS lookup of a name. */
function methodFields(p, port, lookup = '') {
  const m = lookup ? 'dns' : port ? 'tcp' : 'icmp';
  return `<div class="f"><label for="${p}-method">Ping with</label><select class="input" id="${p}-method">
      <option value="icmp" ${m === 'icmp' ? 'selected' : ''}>Ping (ICMP)</option><option value="tcp" ${m === 'tcp' ? 'selected' : ''}>TCP port</option><option value="dns" ${m === 'dns' ? 'selected' : ''}>DNS lookup</option></select></div>
    <div class="f" data-port-field ${m === 'tcp' ? '' : 'hidden'}><label for="${p}-port">Port</label><input class="input mono" id="${p}-port" type="number" min="1" max="65535" placeholder="443" value="${m === 'tcp' ? port : ''}"></div>
    <div class="f" data-lookup-field ${m === 'dns' ? '' : 'hidden'}><label for="${p}-lookup">Name to look up</label><input class="input mono" id="${p}-lookup" placeholder="example.com" value="${esc(lookup)}"></div>`;
}

function wireMethod(root, p) {
  const sel = root.querySelector(`#${p}-method`), port = root.querySelector('[data-port-field]'), lookup = root.querySelector('[data-lookup-field]');
  sel.addEventListener('change', () => {
    port.hidden = sel.value !== 'tcp';
    lookup.hidden = sel.value !== 'dns';
    if (!port.hidden) root.querySelector(`#${p}-port`).focus();
    if (!lookup.hidden) root.querySelector(`#${p}-lookup`).focus();
  });
}

/** What the target measures: { port, lookup }, or null (after a warning)
 * when the TCP port or the name to look up is missing. */
function methodValue(root, p) {
  const m = root.querySelector(`#${p}-method`).value;
  if (m === 'icmp') return { port: 0, lookup: '' };
  if (m === 'dns') {
    const inp = root.querySelector(`#${p}-lookup`), name = inp.value.trim().replace(/\.$/, '');
    if (name.length <= 253 && /^[^\s.]{1,63}(\.[^\s.]{1,63})*$/.test(name)) return { port: 0, lookup: name };
    toast('Which name?', 'Enter the name the DNS server should look up, like example.com.', { kind: 'warn' });
    inp.focus();
    return null;
  }
  const inp = root.querySelector(`#${p}-port`), v = +inp.value;
  if (Number.isInteger(v) && v >= 1 && v <= 65535) return { port: v, lookup: '' };
  toast('Which port?', 'Enter a port number from 1 to 65535, like 443 for a web server or 22 for SSH.', { kind: 'warn' });
  inp.focus();
  return null;
}

export function editTarget(t, focus = '') {
  const clock = () => { const o = S.st.info.utcOffset / 3600; return `UTC${o >= 0 ? '+' : ''}${o}`; };
  const m = modal({
    title: `Edit ${t.name}`, size: 'md',
    body: `<form class="frow" style="grid-template-columns:1fr 1fr">
        <div class="f"><label for="et-name">Alias</label><input class="input" id="et-name" value="${esc(t.name)}"></div>
        <div class="f"><label for="et-host">IP address or name</label><input class="input mono" id="et-host" value="${esc(t.host)}"></div>
        <div class="f"><label for="et-group">Group</label><input class="input" id="et-group" list="op-groups" value="${esc(t.group)}">${groupOptions()}</div>
        <div class="f"><label for="et-iv">Ping interval</label><select class="input" id="et-iv"><option value="0">Default (${fmtInterval(settings().intervalMs)})</option>${INTERVALS.map(v => `<option value="${v}" ${t.intervalMs === v ? 'selected' : ''}>${fmtInterval(v)}</option>`).join('')}${t.intervalMs && !INTERVALS.includes(t.intervalMs) ? `<option value="${t.intervalMs}" selected>${fmtInterval(t.intervalMs)}</option>` : ''}</select></div>
        ${methodFields('et', t.port || 0, t.lookup || '')}
        <div class="f off-times" style="grid-column:1 / -1"><label for="et-off-from">Expected off</label>
          <div class="off-list" data-off-list></div>
          <div class="off-add"><select class="input" id="et-off-days" aria-label="Days" style="width:auto"><option value="every">Every day</option><option value="weekdays">Weekdays</option><option value="weekends">Weekends</option></select>
            <input class="input mono" type="time" id="et-off-from" value="01:00" aria-label="From"><span class="muted">to</span><input class="input mono" type="time" id="et-off-to" value="08:00" aria-label="To">
            <button type="button" class="btn sm" data-off-add>${icon('plus')} Add</button></div>
          <div class="off-note">For a host that's off on purpose, like a PC at night. It keeps being pinged, but sends no alerts then, and the report leaves that time out.${serverClockDiffers() ? ` Times follow the server's clock (${clock()}), not this browser's; set <code>TZ</code> for Docker to change it.` : ''}</div></div>
        <label class="check"><input type="checkbox" id="et-paused" ${t.paused ? 'checked' : ''}> Paused</label>
        <label class="check"><input type="checkbox" id="et-shown" ${t.hidden ? '' : 'checked'}> Show its timeline graph</label>
        <button hidden></button>
      </form>
      <div class="tip">${icon('note')}<span>${t.dnsName && t.dnsName !== t.host ? `Name: <code>${esc(t.dnsName)}</code> · ` : ''}Pinging <code>${esc(t.ip || 'not resolved yet')}</code> · ${fmtNum(t.count || 0)} samples stored · added ${fmtDateTime(t.added, false)}</span></div>`,
    footer: `<button class="btn danger" data-del>${icon('trash')} Delete</button><span class="spacer"></span><button class="btn" data-close>Cancel</button><button class="btn primary" data-save>Save</button>`,
  });
  const v = id => m.root.querySelector(id);
  wireMethod(m.root, 'et');
  let off = [...(t.off || [])];
  const drawOff = () => {
    v('[data-off-list]').innerHTML = off.length ? off.map((o, i) => `<span class="tchip">${esc(offText(o))}<button type="button" data-off-rm="${i}" aria-label="Remove ${esc(offText(o))}">×</button></span>`).join('') : '<span class="muted">Never</span>';
  };
  drawOff();
  m.root.addEventListener('click', e => {
    const rm = e.target.closest('[data-off-rm]');
    if (rm) { off.splice(+rm.dataset.offRm, 1); drawOff(); return; }
    if (!e.target.closest('[data-off-add]')) return;
    const from = v('#et-off-from').value, to = v('#et-off-to').value;
    if (!from || !to) { toast('Which times?', 'Fill in when it goes off and when it comes back.', { kind: 'warn' }); return; }
    if (off.length >= 8) { toast('Up to 8', 'A target can have up to 8 expected-off times.', { kind: 'warn' }); return; }
    off.push({ days: v('#et-off-days').value, from, to });
    drawOff();
  });
  if (focus === 'off') setTimeout(() => { v('.off-times').scrollIntoView({ block: 'center' }); v('#et-off-from').focus(); }, 50);
  const save = async () => {
    const how = methodValue(m.root, 'et');
    if (!how) return;
    try {
      await api.patch('targets/' + t.id, {
        name: v('#et-name').value, host: v('#et-host').value.trim(), group: v('#et-group').value,
        intervalMs: +v('#et-iv').value, paused: v('#et-paused').checked, hidden: !v('#et-shown').checked, port: how.port, lookup: how.lookup, off,
      });
      m.close();
    } catch (err) { errorToast(err); }
  };
  m.root.querySelector('[data-save]').addEventListener('click', save);
  m.root.querySelector('form').addEventListener('submit', e => { e.preventDefault(); save(); });
  m.root.querySelector('[data-del]').addEventListener('click', async () => { m.close(); deleteTarget(t); });
}

/** Groups in display order: the saved order, then any only targets use. */
export function groupOrder() {
  const names = [...S.st.groups];
  for (const t of S.st.targets) if (t.group && !names.includes(t.group)) names.push(t.group);
  return names;
}

/** Move a group one place up (dir -1) or down (dir 1). */
export function moveGroup(name, dir) {
  const g = groupOrder();
  const i = g.indexOf(name), j = i + dir;
  if (i < 0 || j < 0 || j >= g.length) return;
  [g[i], g[j]] = [g[j], g[i]];
  api.put('groups', { groups: g }).catch(errorToast);
}

/** Dialog to move targets into a group (new or existing), or out of one. */
export function openMoveToGroup(ids = [], group = '') {
  const pick = new Set(ids);
  const groups = groupOrder();
  const byGroup = [...groups, ''].map(g => [g, S.st.targets.filter(t => (t.group || '') === g)]).filter(([, ts]) => ts.length);
  const m = modal({
    title: 'Move to a group', size: 'md',
    body: `<div class="f"><label for="mg-name">Group</label><input class="input" id="mg-name" list="op-groups" placeholder="Pick one below, or type a new name" value="${esc(group)}">${groupOptions()}
        <div class="mg-chips">${groups.map(g => `<button type="button" class="chip" data-g="${esc(g)}">${esc(g)}</button>`).join('')}</div></div>
      <div class="f"><label>Targets <span class="muted" style="font-weight:400">· tick the ones to move</span></label>
        <div class="mg-list">${byGroup.map(([g, ts]) => `<div class="mg-h">${esc(g || 'No group')}</div>${ts.map(t => `<label class="check"><input type="checkbox" data-id="${t.id}" ${pick.has(t.id) ? 'checked' : ''}><span>${esc(t.name)}</span><span class="dim mono">${esc(addrOf(t))}</span></label>`).join('')}`).join('')}</div></div>`,
    footer: '<span class="spacer"></span><button class="btn" data-close>Cancel</button><button class="btn primary" data-save></button>',
  });
  const name = m.root.querySelector('#mg-name'), save = m.root.querySelector('[data-save]');
  const label = () => {
    const n = pick.size, g = name.value.trim(), what = `${n} target${n === 1 ? '' : 's'}`;
    save.textContent = !n ? 'Tick targets to move' : g ? `Move ${what} to ${g}` : `Take ${what} out of their group`;
    save.disabled = !n;
    m.root.querySelectorAll('[data-g]').forEach(b => b.classList.toggle('accent', b.dataset.g === g));
  };
  m.root.addEventListener('change', e => {
    const cb = e.target.closest('[data-id]');
    if (cb) { if (cb.checked) pick.add(cb.dataset.id); else pick.delete(cb.dataset.id); label(); }
  });
  m.root.addEventListener('click', e => {
    const b = e.target.closest('[data-g]');
    if (b) { name.value = b.dataset.g; label(); }
  });
  name.addEventListener('input', label);
  save.addEventListener('click', async () => {
    try {
      const res = await api.post('targets/group', { ids: [...pick], group: name.value.trim() });
      toast(res.group ? `Moved to ${res.group}` : 'Taken out of their group', `${res.moved} target${res.moved === 1 ? '' : 's'}`, { kind: 'good', timeout: 3000 });
      m.close();
    } catch (err) { errorToast(err); }
  });
  label();
  setTimeout(() => (group ? m.root.querySelector('[data-id]') : name)?.focus(), 30);
}

export async function deleteTarget(t) {
  const ok = await confirmDialog(`Delete ${t.name} (${addrOf(t)})? Its ${fmtNum(t.count || 0)} stored samples and comments are deleted too.`, { title: 'Delete target', ok: 'Delete', danger: true });
  if (!ok) return;
  try {
    await api.del('targets/' + t.id);
    toast('Target deleted', t.name, { timeout: 3000 });
    if (location.hash.startsWith('#/target/' + t.id)) location.hash = '#/';
  } catch (err) { errorToast(err); }
}

export async function renameTarget(t) {
  const name = await promptDialog('Rename target', { label: `Alias for ${addrOf(t)}`, value: t.name });
  if (name == null) return;
  api.patch('targets/' + t.id, { name }).catch(errorToast);
}

export function addComment(targetId, t) {
  const tg = S.byId.get(targetId);
  const m = modal({
    title: 'Add comment', size: 'sm',
    body: `<form class="f"><label for="cm-text">At ${esc(fmtDateTime(t))}${tg ? ` on ${esc(tg.name)}` : ''}</label>
      <textarea class="input" id="cm-text" rows="3" placeholder="What happened? For example: moved the router, firmware update" style="font-family:var(--sans)" autofocus></textarea>
      <label class="check" style="margin-top:6px"><input type="checkbox" id="cm-all"> Show on every target's timeline</label></form>`,
    footer: '<span class="spacer"></span><button class="btn" data-close>Cancel</button><button class="btn primary" data-save>Add comment</button>',
  });
  m.root.querySelector('[data-save]').addEventListener('click', async () => {
    const text = m.root.querySelector('#cm-text').value.trim();
    if (!text) return;
    try {
      await api.post('comments', { target: m.root.querySelector('#cm-all').checked ? '' : targetId, t: Math.round(t), text });
      m.close();
    } catch (err) { errorToast(err); }
  });
}
