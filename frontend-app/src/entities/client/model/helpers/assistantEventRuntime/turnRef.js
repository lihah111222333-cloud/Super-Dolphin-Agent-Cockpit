import { parseRuntimeTurnRef } from '../../runtimeAssistantTimeline.js';
import { normalizeThreadId } from '../threadIdentity.js';

export function entryMatchesTurnRef(entry, turnRef) {
  return !turnRef || (entry.threadId === turnRef.threadId && entry.turnId === turnRef.turnId);
}

export function canonicalTurnEventRef(runtime, method, payload) {
  const parsed = parseRuntimeTurnRef(payload);
  if (!parsed.value) {
    runtime.addWarning('error', 'turn.event.contract_invalid', { eventName: method, reason: parsed.error });
    return null;
  }
  const threadId = runtime.bridgeThreadIdForPayload(parsed.value);
  if (!threadId) {
    runtime.addWarning('error', 'turn.event.contract_invalid', { eventName: method, reason: 'thread_identity' });
    return null;
  }
  return { threadId, turnId: parsed.value.turnId };
}

export function relatedThreadTimelineKeys(state, threadId, deps) {
  const keys = new Set([threadId]);
  const addKey = (value) => {
    const id = normalizeThreadId(value);
    if (id) keys.add(id);
  };
  const matchedThread = (state.threads || deps.optionalUiArray()).find((thread) => (
    deps.threadMatchesIdentifier(thread, threadId)
  ));
  if (matchedThread) {
    addKey(matchedThread.id);
    addKey(matchedThread.agentId);
    addKey(matchedThread.providerThreadId);
    addKey(matchedThread.sessionId);
  }
  return Array.from(keys);
}
