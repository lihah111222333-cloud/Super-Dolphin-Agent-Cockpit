const UI_LOCAL_STATE_KEYS = Object.freeze([
  'activeThreadId',
  'activeCmdThreadId',
  'pinnedThreadAtById',
  'archivedThreadAtById',
]);

const RUNTIME_STATE_KEYS = Object.freeze([
  'threads',
  'statuses',
  'interruptibleByThread',
  'viewPrefsChat',
  'viewPrefsCmd',
  'statusHeadersByThread',
  'statusDetailsByThread',
  'timelinesByThread',
  'diffTextByThread',
  'diffRevisionByThread',
  'tokenUsageByThread',
  'agentMetaById',
  'agentRuntimeById',
  'activityStatsByThread',
  'alertsByThread',
  'skillRevision',
]);

export const THREAD_STORE_UI_LOCAL_STATE_WHITELIST = UI_LOCAL_STATE_KEYS;
export const THREAD_STORE_RUNTIME_STATE_KEYS = RUNTIME_STATE_KEYS;

export const THREAD_STORE_STATE_WHITELIST = Object.freeze([
  ...UI_LOCAL_STATE_KEYS,
]);

const ALLOWED_STATE_KEYS = new Set(THREAD_STORE_STATE_WHITELIST);

function normalizeStateKeys(candidate) {
  if (!candidate || typeof candidate !== 'object') {
    return [];
  }
  return Object.keys(candidate);
}

export function getUnexpectedThreadStoreStateKeys(candidate) {
  const keys = normalizeStateKeys(candidate);
  return keys.filter((key) => !ALLOWED_STATE_KEYS.has(key));
}

export function assertThreadStoreStateWhitelist(candidate, context = 'thread-store') {
  const unexpected = getUnexpectedThreadStoreStateKeys(candidate);
  if (unexpected.length === 0) {
    return;
  }
  throw new Error(
    `[${context}] unexpected thread store state keys: ${unexpected.join(', ')}. `
      + `Only whitelist keys are allowed in JS store root state.`,
  );
}
