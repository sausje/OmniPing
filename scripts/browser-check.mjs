// Opens every page of OmniPing in a headless browser and fails when one of
// them shows an error or throws one in the console. Go tests don't run the
// page, so this is what catches a broken page before a release.
//
//   go build -o omniping . && node scripts/browser-check.mjs ./omniping
//
// Needs Node 22 or later and Chrome, Chromium or Edge. Set CHROME to the
// browser's path if it isn't found by itself. No npm packages: it speaks the
// DevTools protocol directly.
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const bin = resolve(process.argv[2] || './omniping');
const port = Number(process.env.PORT || 9581);
const devtools = port + 1;
const base = `http://127.0.0.1:${port}/`;
const sleep = ms => new Promise(r => setTimeout(r, ms));
const temp = mkdtempSync(join(tmpdir(), 'omniping-check-'));
const procs = [];

function findBrowser() {
  if (process.env.CHROME) return process.env.CHROME;
  const files = [
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  ];
  for (const f of files) if (existsSync(f)) return f;
  for (const name of ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser']) {
    const r = spawnSync('which', [name], { encoding: 'utf8' });
    if (r.status === 0 && r.stdout.trim()) return r.stdout.trim();
  }
  throw new Error('No Chrome, Chromium or Edge found; set CHROME to its path.');
}

async function waitFor(url, what) {
  for (let i = 0; i < 100; i++) {
    try { const r = await fetch(url); if (r.ok) return r; } catch { /* not yet */ }
    await sleep(150);
  }
  throw new Error(`${what} did not start`);
}

async function api(method, path, body) {
  const r = await fetch(base + path, { method, headers: { 'Content-Type': 'application/json' }, body: body && JSON.stringify(body) });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${await r.text()}`);
  return r.json();
}

// The DevTools protocol over the browser's WebSocket.
async function connect(wsUrl) {
  const ws = new WebSocket(wsUrl);
  await new Promise((ok, fail) => { ws.onopen = ok; ws.onerror = () => fail(new Error('DevTools connection failed')); });
  let seq = 0;
  const pending = new Map(), listeners = [];
  ws.onmessage = e => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } else if (m.method) listeners.forEach(f => f(m));
  };
  return {
    send: (method, params = {}) => new Promise(r => { const id = ++seq; pending.set(id, r); ws.send(JSON.stringify({ id, method, params })); }),
    on: f => listeners.push(f),
    close: () => ws.close(),
  };
}

async function main() {
  // A server with a few targets, a group and the default alert rule.
  procs.push(spawn(bin, ['-data', join(temp, 'data'), '-addr', `127.0.0.1:${port}`], { stdio: 'ignore' }));
  await waitFor(base + 'healthz', 'OmniPing');
  await api('POST', 'api/targets', { text: `127.0.0.1:${port}, Itself, Home\n127.0.0.1, Loopback, Home\n192.0.2.1, Nowhere` });
  await sleep(2500); // a few samples, so graphs have something to draw
  const st = await api('GET', 'api/state');
  const target = st.targets[0]?.id, rule = st.rules?.[0]?.id;

  const routes = ['#/', '#/timelines', `#/target/${target}`, `#/pop/${target}`, '#/alerts', ...(rule ? [`#/alerts/${rule}`] : []), '#/report',
    ...['general', 'display', 'packet', 'storage', 'notifications', 'data', 'computer', 'about'].map(s => `#/settings/${s}`)];

  procs.push(spawn(findBrowser(), ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${devtools}`, `--user-data-dir=${join(temp, 'browser')}`, 'about:blank'], { stdio: 'ignore' }));
  const list = await (await waitFor(`http://127.0.0.1:${devtools}/json/list`, 'The browser')).json();
  const cdp = await connect(list.find(t => t.type === 'page').webSocketDebuggerUrl);
  await cdp.send('Runtime.enable');
  await cdp.send('Log.enable');
  await cdp.send('Page.enable');

  let errors = [];
  cdp.on(m => {
    if (m.method === 'Runtime.exceptionThrown') {
      const d = m.params.exceptionDetails;
      errors.push(d.exception?.description?.split('\n')[0] || d.text);
    } else if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') {
      errors.push('console.error: ' + m.params.args.map(a => a.value ?? a.description ?? '').join(' ').split('\n')[0]);
    } else if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error') {
      errors.push(`${m.params.entry.text} ${m.params.entry.url || ''}`.trim());
    }
  });

  const failures = [];
  const sizes = [['desktop', 1440, 900, false], ['phone', 390, 844, true]];
  for (const [size, width, height, mobile] of sizes) {
    await cdp.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile });
    for (const r of routes) {
      errors = [];
      await cdp.send('Page.navigate', { url: base + r });
      await sleep(1500);
      const res = await cdp.send('Runtime.evaluate', {
        expression: `({ text: document.body.innerText.slice(0, 4000), shown: !!document.querySelector('#app')?.children.length })`,
        returnByValue: true,
      });
      const page = res.result?.result?.value || {};
      // In this plain server mode the app settings answer 404 by design.
      const found = errors.filter(e => !(e.includes('/api/app') && e.includes('404')));
      if (!page.shown) found.push('nothing was drawn');
      if (/could not be shown/i.test(page.text || '')) found.push('the page says it could not be shown: ' + page.text.match(/could not be shown[^\n]*\n?[^\n]*/i)[0].replace(/\s+/g, ' '));
      const line = `${size.padEnd(7)} ${r}`;
      if (found.length) { failures.push(`${line}\n    ${[...new Set(found)].join('\n    ')}`); console.log(`FAIL ${line}`); } else console.log(`ok   ${line}`);
    }
  }
  cdp.close();
  if (failures.length) {
    console.log(`\n${failures.length} page${failures.length === 1 ? '' : 's'} failed:\n${failures.join('\n')}`);
    process.exitCode = 1;
  } else console.log(`\nAll ${routes.length * sizes.length} pages opened without errors.`);
}

try {
  await main();
} catch (err) {
  console.error(err.message || err);
  process.exitCode = 1;
} finally {
  for (const p of procs) p.kill();
  await sleep(500);
  try { rmSync(temp, { recursive: true, force: true }); } catch { /* still in use on Windows */ }
}
