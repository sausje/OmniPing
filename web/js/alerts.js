// Alert rules: conditions, actions and the targets they watch, plus a feed
// of what fired and whether each notification was delivered.

import { S, on, now, addrOf } from './store.js';
import { api } from './api.js';
import { fmtWhen, fmtDuration, fmtMs, esc } from './fmt.js';
import { icon, confirmDialog, toast, errorToast } from './ui.js';

const COND_DEFAULTS = {
  loss: { type: 'loss', on: false, value: 20, samples: 30 },
  latency: { type: 'latency', on: false, value: 200, samples: 10 },
  noreply: { type: 'noreply', on: false, value: 30, samples: 1 },
  ipchange: { type: 'ipchange', on: false, value: 0, samples: 1 },
};
const WHEN = [['start', 'Starts'], ['each', 'Every sample'], ['end', 'Ends'], ['ok', 'While OK']];
export const CHANNEL_ICON = { browser: 'sound', windows: 'desktop', ntfy: 'push', telegram: 'chat', discord: 'chat', pushover: 'push', gotify: 'push', webhook: 'hook', email: 'mail', log: 'file', script: 'run' };

function blankRule(targetId) {
  const t = targetId && S.byId.get(targetId);
  return {
    id: '', name: t ? `Watch ${t.name}` : 'New alert', enabled: true, match: 'any',
    conds: [{ ...COND_DEFAULTS.loss, on: true }, { ...COND_DEFAULTS.latency }, { ...COND_DEFAULTS.noreply, on: true }, { ...COND_DEFAULTS.ipchange }],
    actions: S.st.channels.filter(c => c.enabled).slice(0, 3).map(c => ({ channel: c.id, when: ['start', 'end'], repeatMin: 0 })),
    scope: t ? { all: false, targets: [t.id], groups: [] } : { all: true, targets: [], groups: [] },
    together: false, message: '', messageEnd: '',
  };
}

// An outage rule: fires once when every internet target stops answering.
// Push channels only get the "it's over" message, because while the
// internet is down they cannot reach a phone anyway. Channels on this
// computer (pages, Windows notifications, the log) get both.
function outageRule() {
  const group = S.st.groups.find(g => /internet|wan|online|extern/i.test(g));
  return {
    ...blankRule(), name: 'Internet down', together: true,
    conds: [{ ...COND_DEFAULTS.loss, on: true, value: 100, samples: 5 }, { ...COND_DEFAULTS.latency }, { ...COND_DEFAULTS.noreply }, { ...COND_DEFAULTS.ipchange }],
    actions: S.st.channels.filter(c => c.enabled).map(c => ({ channel: c.id, when: ['browser', 'windows', 'log'].includes(c.type) ? ['start', 'end'] : ['end'], repeatMin: 0 })),
    scope: { all: false, targets: [], groups: group ? [group] : [] },
    messageEnd: 'The internet was down for {duration} ({since} – {until}). It’s back up now.',
  };
}

function normalize(r) {
  const d = JSON.parse(JSON.stringify(r));
  d.conds = Object.keys(COND_DEFAULTS).map(k => d.conds.find(c => c.type === k) || { ...COND_DEFAULTS[k] });
  const sc = d.scope || {};
  d.scope = { all: !!sc.all, targets: sc.targets || [], groups: sc.groups || [], exceptTargets: sc.exceptTargets || [], exceptGroups: sc.exceptGroups || [] };
  d.actions = d.actions || [];
  d.together = !!d.together;
  d.message = d.message || '';
  d.messageEnd = d.messageEnd || '';
  return d;
}

// Names the targets of an active alert: one target, or a together rule's.
export function alertTargets(a) {
  if (a.target) return S.byId.get(a.target)?.name || '?';
  return (a.targets || []).map(id => S.byId.get(id)?.name).filter(Boolean).join(', ') || 'its targets';
}

// The targets a rule watches; with withExceptions false, before its exceptions.
function scopeTargets(scope, withExceptions = true) {
  const inc = scope.all ? S.st.targets : S.st.targets.filter(t => scope.targets.includes(t.id) || (t.group && scope.groups.includes(t.group)));
  if (!withExceptions) return inc;
  const xt = scope.exceptTargets || [], xg = scope.exceptGroups || [];
  return inc.filter(t => !xt.includes(t.id) && !(t.group && xg.includes(t.group)));
}

export function mount(root, arg, arg2) {
  const offs = [];
  let draft = null, dirty = false, selected = null;

  root.innerHTML = `<div class="al-layout">
    <aside class="al-side"><h2>Alert rules</h2><div data-rules></div><button class="newrule" data-new>+ New alert rule</button><button class="newrule" data-new-outage title="Alerts once when all your internet targets stop answering, and tells you how long it lasted">+ Internet outage rule</button></aside>
    <main class="al-main" data-editor></main>
    <aside class="al-feed"><h2>Recent activity</h2><div class="evlist" data-feed></div></aside>
  </div>`;
  const q = s => root.querySelector(s);

  function pick(id, targetId) {
    if (id === 'new' || id === 'new-outage') { selected = null; draft = normalize(id === 'new' ? blankRule(targetId) : outageRule()); dirty = true; }
    else {
      const r = S.st.rules.find(x => x.id === id) || S.st.rules[0];
      selected = r ? r.id : null;
      draft = r ? normalize(r) : null;
      dirty = false;
    }
    list(); editor();
  }

  function list() {
    const box = q('[data-rules]');
    box.innerHTML = S.st.rules.map(r => {
      const firing = S.active.filter(a => a.rule === r.id).length;
      const chans = r.actions.map(a => S.st.channels.find(c => c.id === a.channel)?.name).filter(Boolean);
      const n = scopeTargets(r.scope).length;
      const chip = !r.enabled ? '<span class="chip">Off</span>' : firing ? `<span class="chip bad"><b>${firing}</b>&nbsp;firing</span>` : '<span class="chip good">OK</span>';
      return `<button class="rule ${r.id === selected ? 'on' : ''}" data-rule="${r.id}"><span class="n">${esc(r.name)}</span><span class="m">${n} target${n === 1 ? '' : 's'}${chans.length ? ' · ' + esc(chans.join(', ')) : ' · no actions'}</span>${chip}</button>`;
    }).join('') + (draft && !draft.id ? `<button class="rule on"><span class="n">${esc(draft.name)}</span><span class="m">not saved yet</span><span class="chip accent">New</span></button>` : '');
  }

  function editor() {
    const box = q('[data-editor]');
    if (!draft) {
      box.innerHTML = `<div class="empty"><h3>No alert rules</h3><p>A rule watches targets for packet loss, high latency or no reply, and tells you through the channels you choose.</p><button class="btn primary" data-new>${icon('plus')} New alert rule</button></div>`;
      return;
    }
    const d = draft;
    const c = type => d.conds.find(x => x.type === type);
    const condRow = (type, html) => `<div class="cond ${c(type).on ? '' : 'off'}"><label class="check"><input type="checkbox" data-cond-on="${type}" ${c(type).on ? 'checked' : ''}></label>${html}</div>`;
    const num = (type, key, w = 70) => `<input class="input num-in" style="width:${w}px" type="number" min="0" step="any" data-cond="${type}" data-key="${key}" value="${c(type)[key]}">`;
    const chans = S.st.channels;
    const hasPush = chans.some(ch => ch.enabled && !['browser', 'log', 'script'].includes(ch.type));
    const targetOpts = S.st.targets.filter(t => !d.scope.targets.includes(t.id)).map(t => `<option value="t:${t.id}">${esc(t.name)} (${esc(addrOf(t))})</option>`).join('');
    const groupOpts = S.st.groups.filter(g => !d.scope.groups.includes(g)).map(g => `<option value="g:${esc(g)}">Group: ${esc(g)}</option>`).join('');
    // Exceptions can be any target or group the rule would otherwise watch.
    const included = scopeTargets(d.scope, false);
    const watched = scopeTargets(d.scope).length;
    const exTargetOpts = included.filter(t => !d.scope.exceptTargets.includes(t.id) && !(t.group && d.scope.exceptGroups.includes(t.group))).map(t => `<option value="t:${t.id}">${esc(t.name)} (${esc(addrOf(t))})</option>`).join('');
    const exGroupOpts = [...new Set(included.map(t => t.group).filter(Boolean))].filter(g => !d.scope.exceptGroups.includes(g) && !d.scope.groups.includes(g)).map(g => `<option value="g:${esc(g)}">Group: ${esc(g)}</option>`).join('');
    const hasExceptions = d.scope.exceptTargets.length + d.scope.exceptGroups.length > 0;
    const firing = S.active.filter(a => a.rule === d.id);

    box.innerHTML = `
      <div class="titlerow">
        <input class="input name-in" data-name value="${esc(d.name)}" aria-label="Rule name">
        <label class="switch" title="Enabled"><input type="checkbox" data-enabled ${d.enabled ? 'checked' : ''}><span></span></label><span class="muted" style="font-size:12.5px">${d.enabled ? 'Enabled' : 'Off'}</span>
        <span class="spacer"></span>
        ${d.id ? `<button class="btn" data-dup>Duplicate</button><button class="btn" data-test ${dirty ? 'disabled title="Save first"' : ''}>Send test alert</button><button class="btn danger" data-del>${icon('trash')}</button>` : ''}
        ${dirty && d.id ? '<button class="btn" data-discard>Discard</button>' : ''}
        <button class="btn primary" data-save ${dirty ? '' : 'disabled'}>${d.id ? 'Save changes' : 'Create rule'}</button>
      </div>
      ${firing.length ? `<div class="banner" style="border-radius:8px;border:0">${icon('bell')} Firing now for ${firing.map(a => esc(alertTargets(a)) + ' (' + fmtDuration(now() - a.since) + ')').join(', ')}</div>` : ''}

      <section class="sec">
        <div class="sec-h"><h3>Conditions</h3><span class="muted">Fire when</span>
          <select class="input" data-match style="height:28px;width:auto"><option value="any" ${d.match !== 'all' ? 'selected' : ''}>any</option><option value="all" ${d.match === 'all' ? 'selected' : ''}>all</option></select>
          <span class="muted">${d.together ? 'of these is true for <b>every</b> target below at the same time' : 'of these is true for a target'}</span></div>
        ${condRow('loss', `Packet loss is at least ${num('loss', 'value')} % over the last ${num('loss', 'samples')} samples`)}
        ${condRow('latency', `Average latency is at least ${num('latency', 'value', 84)} ms over the last ${num('latency', 'samples')} samples`)}
        ${condRow('noreply', `No reply at all for ${num('noreply', 'value')} seconds`)}
        ${condRow('ipchange', `The address behind a name changes <span class="chip">dynamic DNS</span>`)}
      </section>

      <section class="sec">
        <div class="sec-h"><h3>Actions</h3><span class="muted">Each action picks when it runs: when the alert starts, on every sample while it lasts, when it ends, or on every sample while all is well.</span></div>
        <div class="acts-wrap"><table class="acts">
          <thead><tr><th style="width:240px">Send to</th><th>Runs when the alert…</th><th style="width:150px">Repeat at most</th><th style="width:40px"></th></tr></thead>
          <tbody>${d.actions.map((a, i) => `<tr>
            <td><select class="input" data-act-ch="${i}" style="width:100%">${chans.map(ch => `<option value="${ch.id}" ${ch.id === a.channel ? 'selected' : ''}>${esc(ch.name)}${ch.enabled ? '' : ' (off)'}</option>`).join('')}</select></td>
            <td><span class="when">${WHEN.map(([k, l]) => `<button type="button" class="${a.when.includes(k) ? 'on' : ''}" data-act-when="${i}" data-k="${k}">${l}</button>`).join('')}</span></td>
            <td>${a.when.includes('each') || a.when.includes('ok') ? `<span class="field"><input class="input num-in" style="width:64px" type="number" min="0" data-act-rep="${i}" value="${a.repeatMin || 0}"><span class="lbl">min</span></span>` : '<span class="dim">—</span>'}</td>
            <td><button class="btn ghost sm icon" data-act-del="${i}" title="Remove action">${icon('x')}</button></td></tr>`).join('') || '<tr><td colspan="4" class="dim">No actions yet: the rule will only show up in the activity feed.</td></tr>'}</tbody>
        </table></div>
        <div class="addrow"><button class="btn sm" data-act-add>${icon('plus')} Add action</button>
          ${hasPush ? '' : `<span class="muted" style="font-size:12.5px;margin-left:10px">For notifications on your phone or PC, <a href="#/settings/notifications">set up ntfy, Telegram or email</a> first.</span>`}</div>
      </section>

      <section class="sec">
        <div class="sec-h"><h3>Applies to</h3><span class="muted">One rule can watch many targets, and a target can have many rules.</span></div>
        <div class="targets-pick">
          <label class="check"><input type="checkbox" data-all ${d.scope.all ? 'checked' : ''}> All targets, including ones added later</label>
          ${d.scope.all ? '' : `
            ${d.scope.groups.map(g => `<span class="tchip">${icon('tag')} ${esc(g)}<button data-rm-g="${esc(g)}" aria-label="Remove">×</button></span>`).join('')}
            ${d.scope.targets.map(id => { const t = S.byId.get(id); return t ? `<span class="tchip"><span class="sd good" style="width:7px;height:7px"></span>${esc(t.name)}<button data-rm-t="${id}" aria-label="Remove">×</button></span>` : ''; }).join('')}
            ${targetOpts || groupOpts ? `<select class="input" data-add-scope style="width:auto;height:28px"><option value="">+ Add targets or groups</option>${groupOpts}${targetOpts}</select>` : ''}`}
        </div>
        ${included.length || hasExceptions ? `<div class="targets-pick except">
          <span class="ex-l">Except</span>
          ${d.scope.exceptGroups.map(g => `<span class="tchip ex">${icon('tag')} ${esc(g)}<button data-rm-xg="${esc(g)}" aria-label="Remove exception">×</button></span>`).join('')}
          ${d.scope.exceptTargets.map(id => { const t = S.byId.get(id); return t ? `<span class="tchip ex"><span class="sd" style="width:7px;height:7px"></span>${esc(t.name)}<button data-rm-xt="${id}" aria-label="Remove exception">×</button></span>` : ''; }).join('')}
          ${exTargetOpts || exGroupOpts ? `<select class="input" data-add-except style="width:auto;height:28px"><option value="">${hasExceptions ? '+ Add another exception' : '+ Add an exception'}</option>${exGroupOpts}${exTargetOpts}</select>` : ''}
          <span class="spacer"></span><span class="muted ex-n">Watches ${watched} of ${S.st.targets.length} target${S.st.targets.length === 1 ? '' : 's'}</span>
        </div>` : ''}
        <label class="check together"><input type="checkbox" data-together ${d.together ? 'checked' : ''}><span><b>Treat these targets as one.</b> Alert once when all of them have a problem at the same time, for example when the internet is down, and end as soon as one answers again. Meanwhile, other alerts for these targets are held back, so an outage sends one message instead of one per target.</span></label>
      </section>

      <section class="sec">
        <div class="sec-h"><h3>Message</h3><span class="muted">Optional. Leave empty for the standard text.</span></div>
        <div class="card-b msgs">
          <label for="msg-start">When it starts</label><textarea class="input" id="msg-start" data-message rows="2" placeholder="${d.together ? '{target}: {reason}' : '{target} ({ip}): {reason}'}">${esc(d.message)}</textarea>
          <label for="msg-end">When it ends</label><textarea class="input" id="msg-end" data-message-end rows="2" placeholder="${d.together ? '{rule} lasted {duration}, from {since} to {until}.' : '{target} recovered after {duration}.'}">${esc(d.messageEnd)}</textarea>
          <div class="dim" style="font-size:12px">Placeholders: {target} {ip} {host} {group} {rule} {state} {reason} {time} {duration} {since} {until}</div></div>
      </section>`;
  }

  function feed() {
    const box = q('[data-feed]');
    const list = S.events.slice(0, 60);
    if (!list.length) { box.innerHTML = '<p class="none">Nothing has fired yet.</p>'; return; }
    box.innerHTML = list.map(e => `<div class="ev"><i style="background:var(--${e.kind === 'end' ? 'good' : e.kind === 'test' || e.kind === 'trace' ? 'accent' : e.kind === 'ipchange' ? 'warn' : 'bad'})"></i><div>
      <div class="t">${esc(fmtWhen(e.t, now()))}</div>
      <div class="w">${e.target ? `<a href="#/target/${e.target}" style="color:inherit;text-decoration:none">${esc(e.title)}</a>` : esc(e.title)}</div>
      <div class="d">${esc(e.ruleName)}${e.kind === 'test' ? ' · test' : ''}</div>
      ${e.kind === 'trace' ? traceBlock(e) : ''}
      ${e.held ? `<div class="sent"><span class="held">Not sent: “${esc(e.held)}” was firing</span></div>` : ''}
      ${e.sent?.length ? `<div class="sent">${e.sent.map(s => `<span class="${s.state}" title="${esc(s.err || '')}">${esc(s.name)} ${s.state === 'ok' ? '✓' : s.state === 'failed' ? '✗ ' + esc(s.err || '') : s.state === 'retrying' ? '↻ retrying' : '…'}</span>`).join('')}</div>` : ''}</div></div>`).join('');
  }

  const change = () => { dirty = true; editor(); list(); };

  root.addEventListener('click', async e => {
    const r = e.target.closest('[data-rule]');
    if (r) {
      if (dirty && !(await confirmDialog('Discard your changes to this rule?', { ok: 'Discard', danger: true }))) return;
      location.hash = '#/alerts/' + r.dataset.rule; return;
    }
    const nw = e.target.closest('[data-new], [data-new-outage]');
    if (nw) {
      if (dirty && !(await confirmDialog('Discard your changes to this rule?', { ok: 'Discard', danger: true }))) return;
      location.hash = nw.matches('[data-new]') ? '#/alerts/new' : '#/alerts/new-outage'; return;
    }
    if (!draft) return;
    const w = e.target.closest('[data-act-when]');
    if (w) {
      const a = draft.actions[+w.dataset.actWhen], k = w.dataset.k;
      a.when = a.when.includes(k) ? a.when.filter(x => x !== k) : [...a.when, k];
      change(); return;
    }
    const del = e.target.closest('[data-act-del]');
    if (del) { draft.actions.splice(+del.dataset.actDel, 1); change(); return; }
    if (e.target.closest('[data-act-add]')) {
      const used = new Set(draft.actions.map(a => a.channel));
      const ch = S.st.channels.find(c => !used.has(c.id)) || S.st.channels[0];
      if (ch) { draft.actions.push({ channel: ch.id, when: ['start', 'end'], repeatMin: 0 }); change(); }
      return;
    }
    const rg = e.target.closest('[data-rm-g]');
    if (rg) { draft.scope.groups = draft.scope.groups.filter(g => g !== rg.dataset.rmG); change(); return; }
    const rt = e.target.closest('[data-rm-t]');
    if (rt) { draft.scope.targets = draft.scope.targets.filter(t => t !== rt.dataset.rmT); change(); return; }
    const xg = e.target.closest('[data-rm-xg]');
    if (xg) { draft.scope.exceptGroups = draft.scope.exceptGroups.filter(g => g !== xg.dataset.rmXg); change(); return; }
    const xt = e.target.closest('[data-rm-xt]');
    if (xt) { draft.scope.exceptTargets = draft.scope.exceptTargets.filter(t => t !== xt.dataset.rmXt); change(); return; }
    if (e.target.closest('[data-discard]')) { pick(draft.id); return; }
    if (e.target.closest('[data-dup]')) { const copy = normalize(draft); copy.id = ''; copy.name += ' (copy)'; draft = copy; selected = null; dirty = true; list(); editor(); return; }
    if (e.target.closest('[data-del]')) {
      if (!(await confirmDialog(`Delete the rule “${draft.name}”? Its past alerts stay in the activity feed.`, { title: 'Delete rule', ok: 'Delete', danger: true }))) return;
      try { await api.del('rules/' + draft.id); dirty = false; location.hash = '#/alerts'; pick(null); } catch (err) { errorToast(err); }
      return;
    }
    if (e.target.closest('[data-test]')) {
      try { await api.post(`rules/${draft.id}/test`); toast('Test sent', 'Check the activity feed for delivery results.', { timeout: 4000 }); } catch (err) { errorToast(err); }
      return;
    }
    if (e.target.closest('[data-save]')) {
      try {
        const body = { ...draft, conds: draft.conds.map(c => ({ ...c, value: +c.value || 0, samples: Math.max(1, +c.samples || 1) })) };
        const saved = draft.id ? await api.put('rules/' + draft.id, body) : await api.post('rules', body);
        dirty = false;
        toast('Rule saved', saved.name, { kind: 'good', timeout: 2500 });
        if (location.hash !== '#/alerts/' + saved.id) location.hash = '#/alerts/' + saved.id;
        else pick(saved.id);
      } catch (err) { errorToast(err); }
    }
  });
  root.addEventListener('input', e => {
    if (!draft) return;
    const t = e.target;
    if (t.matches('[data-name]')) { draft.name = t.value; dirty = true; list(); q('[data-save]').disabled = false; return; }
    if (t.matches('[data-message]')) { draft.message = t.value; dirty = true; q('[data-save]').disabled = false; return; }
    if (t.matches('[data-message-end]')) { draft.messageEnd = t.value; dirty = true; q('[data-save]').disabled = false; return; }
    if (t.matches('[data-cond]')) { draft.conds.find(c => c.type === t.dataset.cond)[t.dataset.key] = t.value; dirty = true; q('[data-save]').disabled = false; return; }
    if (t.matches('[data-act-rep]')) { draft.actions[+t.dataset.actRep].repeatMin = +t.value || 0; dirty = true; q('[data-save]').disabled = false; }
  });
  root.addEventListener('change', e => {
    if (!draft) return;
    const t = e.target;
    if (t.matches('[data-enabled]')) { draft.enabled = t.checked; change(); }
    else if (t.matches('[data-match]')) { draft.match = t.value; change(); }
    else if (t.matches('[data-cond-on]')) { draft.conds.find(c => c.type === t.dataset.condOn).on = t.checked; change(); }
    else if (t.matches('[data-act-ch]')) { draft.actions[+t.dataset.actCh].channel = t.value; change(); }
    else if (t.matches('[data-all]')) { draft.scope.all = t.checked; change(); }
    else if (t.matches('[data-together]')) { draft.together = t.checked; change(); }
    else if (t.matches('[data-add-scope]') && t.value) {
      const [kind, v] = [t.value.slice(0, 1), t.value.slice(2)];
      if (kind === 'g') draft.scope.groups.push(v); else draft.scope.targets.push(v);
      change();
    }
    else if (t.matches('[data-add-except]') && t.value) {
      const [kind, v] = [t.value.slice(0, 1), t.value.slice(2)];
      if (kind === 'g') draft.scope.exceptGroups.push(v); else draft.scope.exceptTargets.push(v);
      change();
    }
  });

  offs.push(on('state', () => {
    if (!dirty) pick(selected || (draft && draft.id) || null);
    else list();
  }));
  offs.push(on('active', () => { list(); if (!dirty) editor(); }));
  offs.push(on('event', feed));
  offs.push(on('events', feed));

  pick(arg === 'new' || arg === 'new-outage' ? arg : arg || null, arg2);
  feed();
  return { unmount() { offs.forEach(f => f()); } };
}

/** A traceroute from the alert history: its verdict, and the hops to open. */
export function traceBlock(e) {
  const hops = e.trace || [];
  const rows = hops.map(h => `<div><span class="dim">${h.ttl}</span><span>${h.addr ? esc(h.addr) : '<span class="dim">no answer</span>'}${h.name ? ` <span class="muted">${esc(h.name)}</span>` : ''}</span><span class="muted">${h.rtt >= 0 ? esc(fmtMs(h.rtt)) + ' ms' : ''}</span></div>`).join('');
  const verdict = (e.text || '').split(/\r?\n/)[0];
  return `<div class="trace-v">${esc(verdict)}</div>
    <details class="trace"><summary>${hops.length} hop${hops.length === 1 ? '' : 's'}</summary><div class="hops">${rows}</div></details>`;
}
