const BASE = '';

async function req(method, path, body) {
  const opts = { method, headers: {} };
  if (body instanceof FormData) {
    opts.body = body;
  } else if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(BASE + path, opts);
  let data;
  try { data = await resp.json(); } catch { data = { error: `HTTP ${resp.status}` }; }
  if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
  return data;
}

// ── Search ────────────────────────────────────────────────────────────────────

export async function search(file, params) {
  const form = new FormData();
  form.append('file', file);
  form.append('params', JSON.stringify(params));
  const resp = await fetch(BASE + '/api/search', { method: 'POST', body: form });
  let data;
  try { data = await resp.json(); } catch { data = { error: `HTTP ${resp.status}` }; }
  if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
  return data;
}

// ── Directories ───────────────────────────────────────────────────────────────

export const getDirs    = ()              => req('GET',    '/api/dirs');
export const addDir     = (path, recursive, extensions) =>
  req('POST', '/api/dirs', { path, recursive, extensions: extensions ?? [] });
export const deleteDir  = (id)            => req('DELETE', `/api/dirs/${id}`);
export const scanDir    = (id)            => req('POST',   `/api/dirs/${id}/scan`);
export const toggleDir  = (id, enabled)  => req('POST',   `/api/dirs/${id}/toggle`, { enabled });

// ── Stats & status ────────────────────────────────────────────────────────────

export const getStats   = ()  => req('GET', '/api/stats');
export const getStatus  = ()  => req('GET', '/api/status');
export const getJobs    = ()  => req('GET', '/api/jobs');

// ── Files ─────────────────────────────────────────────────────────────────────

export const deleteFile = (id) => req('DELETE', `/api/files/${id}`);
export const thumbUrl   = (id) => `/api/thumb/${id}`;
export const openUrl    = (id) => `/api/open/${id}`;

// ── Repair ────────────────────────────────────────────────────────────────────

export const getRepair    = ()  => req('GET',    '/api/repair');
export const startRepair  = ()  => req('POST',   '/api/repair');
export const cancelRepair = ()  => req('DELETE', '/api/repair');

// ── Config ────────────────────────────────────────────────────────────────────

export const getConfig  = ()     => req('GET', '/api/config');
export const putConfig  = (cfg)  => req('PUT', '/api/config', cfg);
