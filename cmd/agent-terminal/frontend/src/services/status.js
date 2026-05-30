export const BACKEND_THREAD_STATUSES = Object.freeze([
  'idle',
  'starting',
  'thinking',
  'responding',
  'running',
  'editing',
  'waiting',
  'syncing',
  'archived',
  'error',
]);

const BACKEND_THREAD_STATUS_SET = new Set(BACKEND_THREAD_STATUSES);
const THREAD_ERROR_STATUS_SET = new Set(['error', 'failed', 'rejected']);
const THREAD_ACTIVE_STATUS_SET = new Set([
  'starting',
  'thinking',
  'responding',
  'running',
  'editing',
  'waiting',
  'syncing',
]);

export function normalizeStatus(state) {
  const s = (state || '').toString().toLowerCase().trim();
  if (!s) return 'idle';
  if (THREAD_ERROR_STATUS_SET.has(s)) return 'error';
  if (BACKEND_THREAD_STATUS_SET.has(s)) return s;
  return 'idle';
}

export function isThreadErrorStatus(state) {
  const s = (state || '').toString().toLowerCase().trim();
  return THREAD_ERROR_STATUS_SET.has(s);
}

export function isThreadActiveStatus(state) {
  return THREAD_ACTIVE_STATUS_SET.has(normalizeStatus(state));
}
