const UI_LOCAL_STATE_KEYS = Object.freeze([
  'activeThreadId',
  'activeCmdThreadId',
  'pinnedThreadAtById',
  'archivedThreadAtById',
  // Surfaced when thread/start reports the caller's prompt_key is stale
  // (template was deleted / disabled). startThread sets a notice string;
  // UI components consume it to render a one-shot toast and reset to ''.
  'promptStaleNotice',
  // Local-only send gate. Backend snapshots may still report idle after a
  // turn/start failure, so this must not live in runtime snapshot state.
  'sendBlockedNoticesByThread',
  // Temporary send hold for accepted sends whose follow-up UI/runtime sync
  // failed. A successful runtime refresh clears this automatically.
  'sendHoldNoticesByThread',
]);

const RUNTIME_STATE_KEYS = Object.freeze([
  'threads',
  'statuses',
  'interruptibleByThread',
  'viewPrefsChat',
  'viewPrefsCmd',
  'statusHeadersByThread',
  'statusDetailsByThread',
  'overlayTextByThread',
  'overlayTypeByThread',
  'overlayPriorityByThread',
  'timelinesByThread',
  'diffTextByThread',
  'diffRevisionByThread',
  'tokenUsageByThread',
  'agentMetaById',
  'agentRuntimeById',
  'mainAgentId',
  'mainAgentState',
  'partial',
  'activityStatsByThread',
  'alertsByThread',
  'skillRevision',
  // 「新建继承对话」kickoff message text，供 timeline selector 隐藏这条 user 消息
  // 让 agent 视觉上主动开场。进程级 in-memory，刷新页面后 kickoff 自然显示成历史消息。
  'kickoffByThread',
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
