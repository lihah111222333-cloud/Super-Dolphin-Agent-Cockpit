import { firstOptionalPresent, normalizeOptionalTextField } from './contractStoreModel.js';
function optionalUiObject() {
  return {};
}

// @ts-check

function normalizeString(value) {
  return normalizeOptionalTextField(value);
}

export function normalizeThreadId(value) {
  return normalizeString(value);
}

export function isAgentRuntimeId(value) {
  return /^agent[_-]/i.test(normalizeThreadId(value));
}

export function isLaunchIntentId(value) {
  return /^launch[_-]/i.test(normalizeThreadId(value));
}

export function normalizeBackendThreadId(value) {
  const id = normalizeThreadId(value);
  if (!id || isLaunchIntentId(id)) return '';
  return id;
}

export function firstBackendThreadId(...values) {
  for (const value of values) {
    const id = normalizeBackendThreadId(value);
    if (id) return id;
  }
  return '';
}

export function firstRuntimeAgentId(...values) {
  for (const value of values) {
    const id = normalizeThreadId(value);
    if (id && isAgentRuntimeId(id)) return id;
  }
  return '';
}

export function normalizeThreadIdentity(raw = {}) {
  const thread = raw?.thread || optionalUiObject();
  const id = firstBackendThreadId(
    raw?.threadId,
    raw?.threadID,
    raw?.thread_id,
    raw?.codexThreadId,
    raw?.codex_thread_id,
    thread?.threadId,
    thread?.threadID,
    thread?.thread_id,
    thread?.codexThreadId,
    thread?.codex_thread_id,
    raw?.id,
    thread?.id,
    raw?.agentId,
    raw?.agent_id,
    thread?.agentId,
    thread?.agent_id,
  );
  const agentId = normalizeThreadId(
    firstOptionalPresent(
      raw?.agentId,
      raw?.agent_id,
      thread?.agentId,
      thread?.agent_id,
      firstRuntimeAgentId(raw?.id, thread?.id),
    ),
  );
  return {
    threadId: id,
    agentId,
    providerThreadId: normalizeThreadId(firstOptionalPresent(raw?.providerThreadId, raw?.provider_thread_id, thread?.providerThreadId, thread?.provider_thread_id)),
    sessionId: normalizeThreadId(firstOptionalPresent(raw?.sessionId, raw?.session_id, thread?.sessionId, thread?.session_id)),
  };
}
