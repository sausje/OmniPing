// Small wrapper around the server's JSON API.

async function call(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch('api/' + path, opts);
  // Signed out (a password was set, or the session ran out): to the sign-in page.
  if (res.status === 401 && !location.pathname.endsWith('/login')) { location.href = 'login'; return new Promise(() => {}); }
  let data = null;
  try { data = await res.json(); } catch { /* empty body */ }
  if (!res.ok) throw new Error((data && data.error) || `${res.status} ${res.statusText}`);
  return data;
}

export const api = {
  get: path => call('GET', path),
  post: (path, body = {}) => call('POST', path, body),
  put: (path, body = {}) => call('PUT', path, body),
  patch: (path, body = {}) => call('PATCH', path, body),
  del: path => call('DELETE', path),
};

/** Fetch samples for some targets between from and to, at most max points each. */
export async function fetchSamples(ids, from, to, max) {
  const q = new URLSearchParams({ ids: ids.length ? ids.join(',') : 'all', from: Math.round(from), to: Math.round(to), max: Math.round(max) });
  const res = await api.get('samples?' + q);
  // The server leaves out empty arrays.
  for (const s of res.series) {
    for (const k of ['t', 'r', 'n', 'lost', 'min', 'avg', 'max']) s[k] = s[k] || [];
  }
  return res;
}
