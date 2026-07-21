import {
  appendAssistantDeltaText,
  assistantDeltaBufferKey,
  mergeRuntimeAssistantCompletion,
  runtimeAssistantCompletion,
  runtimeAssistantFallbackId,
} from '../../runtimeAssistantTimeline.js';
import { canonicalTurnEventRef, relatedThreadTimelineKeys } from './turnRef.js';
import { normalizeThreadId } from '../threadIdentity.js';

function drainAssistantDeltaEntries(runtime, deps, turnRef) {
  if (runtime.assistantDeltaBuffers.size === 0) return null;
  const entries = [];
  for (const [key, entry] of runtime.assistantDeltaBuffers.entries()) {
    if (turnRef && (entry.threadId !== turnRef.threadId || entry.turnId !== turnRef.turnId)) continue;
    entries.push(entry);
    runtime.assistantDeltaBuffers.delete(key);
  }
  if (runtime.assistantDeltaBuffers.size === 0) runtime.clearAssistantDeltaFlushTimer();
  if (entries.length === 0) return null;
  return {
    entries,
    flushTime: deps.clockNowISO(),
    flushId: deps.clockNowMillis(),
  };
}

function bufferedAssistantTimelineItem(entry, flushTime) {
  if (entry.kind === 'thinking') {
    return {
      id: entry.itemId,
      role: 'assistant',
      kind: 'thinking',
      text: entry.delta,
      time: entry.timestamp || flushTime,
      done: false,
      turnId: entry.turnId,
    };
  }
  return {
    id: entry.itemId,
    role: 'assistant',
    kind: entry.kind || 'assistant',
    text: entry.delta,
    time: entry.timestamp || flushTime,
    done: false,
    optimistic: false,
    runtime: true,
    turnId: entry.turnId,
  };
}

function timelineWithAssistantDelta(timeline, entry, flushTime) {
  let found = false;
  const nextTimeline = timeline.map((item) => {
    if (item.id !== entry.itemId || item.turnId !== entry.turnId) return item;
    found = true;
    return {
      ...item,
      role: 'assistant',
      text: appendAssistantDeltaText(item.text, entry.delta),
      done: false,
      turnId: entry.turnId || item.turnId,
    };
  });
  if (found) return nextTimeline;
  return [...nextTimeline, bufferedAssistantTimelineItem(entry, flushTime)];
}

function assistantDeltaFlushPatch(state, flushed, deps) {
  const timelinesByThread = { ...state.timelinesByThread };
  for (const entry of flushed.entries) {
    const timeline = timelinesByThread[entry.threadId] || deps.optionalUiArray();
    timelinesByThread[entry.threadId] = timelineWithAssistantDelta(timeline, entry, flushed.flushTime);
  }
  return {
    timelinesByThread,
    activityEntries: [
      ...flushed.entries.map((entry, index) => ({
        id: `${entry.method}-${flushed.flushId}-${index}`,
        method: entry.method,
        threadId: entry.threadId,
        timestamp: flushed.flushTime,
      })),
      ...state.activityEntries,
    ].slice(0, 120),
  };
}

function flushAssistantDeltaBuffers(runtime, deps, turnRef) {
  const flushed = drainAssistantDeltaEntries(runtime, deps, turnRef);
  if (!flushed) return false;
  runtime.set((state) => assistantDeltaFlushPatch(state, flushed, deps));
  return true;
}

function scheduleAssistantDeltaFlush(runtime, deps) {
  if (runtime.assistantDeltaFlushTimer) return;
  const scopeEpoch = runtime.assistantEventScopeEpoch;
  runtime.assistantDeltaFlushTimer = setTimeout(() => {
    if (scopeEpoch !== runtime.assistantEventScopeEpoch) return;
    runtime.assistantDeltaFlushTimer = null;
    flushAssistantDeltaBuffers(runtime, deps);
  }, deps.ASSISTANT_DELTA_FLUSH_MS);
}

function assistantDeltaItemId(runtime, payload, deps) {
  return deps.normalizeString(payload.itemId || payload.item_id || payload.messageId || payload.message_id)
    || runtimeAssistantFallbackId(payload, { normalizeThreadId, runtimeThreadIdentifier: deps.runtimeThreadIdentifier });
}

function setAssistantDeltaBuffer(runtime, method, entry) {
  const key = assistantDeltaBufferKey(entry.threadId, entry.itemId, entry.turnId);
  const existing = runtime.assistantDeltaBuffers.get(key);
  runtime.assistantDeltaBuffers.set(key, {
    ...entry,
    method: existing?.method || method,
    delta: appendAssistantDeltaText(existing?.delta, entry.delta),
    timestamp: existing?.timestamp || entry.timestamp,
  });
}

function commandOutputItemId(timeline, turnId) {
  let fallback = '';
  for (let i = timeline.length - 1; i >= 0; i--) {
    if (timeline[i].kind !== 'command' || timeline[i].turnId !== turnId) continue;
    if (timeline[i].done !== true) return timeline[i].id;
    if (!fallback) fallback = timeline[i].id;
  }
  return fallback;
}

function enqueueAssistantDelta(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!turnRef || delta === '' || runtime.turnEventRejected(method, turnRef)) return false;
  setAssistantDeltaBuffer(runtime, method, {
    ...turnRef,
    itemId: assistantDeltaItemId(runtime, payload, deps),
    delta,
    timestamp: deps.normalizeString(payload.timestamp) || deps.clockNowISO(),
  });
  runtime.replayPendingTurnTerminal(turnRef);
  scheduleAssistantDeltaFlush(runtime, deps);
  return true;
}

function enqueueReasoningDelta(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!turnRef || delta === '' || runtime.turnEventRejected(method, turnRef)) return false;
  setAssistantDeltaBuffer(runtime, method, {
    ...turnRef,
    itemId: `thinking:${turnRef.turnId}`,
    delta,
    timestamp: deps.normalizeString(payload.timestamp) || deps.clockNowISO(),
    kind: 'thinking',
  });
  scheduleAssistantDeltaFlush(runtime, deps);
  return true;
}

function enqueueCommandOutputDelta(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!turnRef || delta === '' || runtime.turnEventRejected(method, turnRef)) return false;
  const timeline = runtime.get().timelinesByThread[turnRef.threadId] || deps.optionalUiArray();
  const itemId = commandOutputItemId(timeline, turnRef.turnId);
  if (!itemId) {
    runtime.addWarning('error', 'turn.event.contract_invalid', {
      eventName: method,
      reason: 'command_item_turn_ref',
    });
    return false;
  }
  setAssistantDeltaBuffer(runtime, method, {
    ...turnRef,
    itemId,
    delta,
    timestamp: deps.normalizeString(payload.timestamp) || deps.clockNowISO(),
    kind: 'command',
  });
  scheduleAssistantDeltaFlush(runtime, deps);
  return true;
}

function isOpenAssistantTimelineItem(item) {
  return (
    item.role === 'assistant'
    || item.kind === 'assistant'
    || item.kind === 'thinking'
    || item.kind === 'command'
  ) && (item.done === false || item.status === 'running');
}

function finalizeAssistantTimeline(timeline, turnRef) {
  let mutated = false;
  const nextTimeline = timeline.map((item) => {
    if (item.turnId !== turnRef.turnId || !isOpenAssistantTimelineItem(item)) return item;
    mutated = true;
    return { ...item, done: true };
  });
  return { mutated, nextTimeline };
}

function finalizeActiveAssistantMessages(runtime, turnRef, deps) {
  if (!turnRef?.threadId || !turnRef?.turnId) return false;
  runtime.set((state) => {
    let mutated = false;
    const timelinesByThread = { ...state.timelinesByThread };
    for (const key of relatedThreadTimelineKeys(state, turnRef.threadId, deps)) {
      if (!deps.hasOwn(state.timelinesByThread, key)) continue;
      const finalized = finalizeAssistantTimeline(state.timelinesByThread[key], turnRef);
      timelinesByThread[key] = finalized.nextTimeline;
      mutated ||= finalized.mutated;
    }
    return mutated ? { timelinesByThread } : {};
  });
  return true;
}

function assistantCompletionActivityEntry(method, turnRef, deps) {
  return {
    id: `${method}-${deps.clockNowMillis()}`,
    method,
    threadId: turnRef.threadId,
    timestamp: deps.clockNowISO(),
  };
}

function assistantCompletionPatch(state, turnRef, completion, method, deps) {
  const timelinesByThread = { ...state.timelinesByThread };
  const targetKeys = relatedThreadTimelineKeys(state, turnRef.threadId, deps)
    .filter((key) => key === turnRef.threadId || deps.hasOwn(state.timelinesByThread, key));
  for (const key of targetKeys) {
    timelinesByThread[key] = mergeRuntimeAssistantCompletion(
      state.timelinesByThread[key] || deps.optionalUiArray(),
      completion,
    );
  }
  return {
    timelinesByThread,
    activityEntries: [assistantCompletionActivityEntry(method, turnRef, deps), ...state.activityEntries].slice(0, 120),
  };
}

function applyAssistantCompletion(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  if (!turnRef || runtime.turnEventRejected(method, turnRef)) return false;
  const completion = runtimeAssistantCompletion(payload);
  if (!completion) return false;
  runtime.flushAssistantDeltasNow(turnRef);
  runtime.set((state) => assistantCompletionPatch(state, turnRef, completion, method, deps));
  runtime.replayPendingTurnTerminal(turnRef);
  return true;
}

export function attachAssistantDeltaRuntime(runtime, deps) {
  Object.assign(runtime, {
    enqueueAssistantDelta: (method, payload) => enqueueAssistantDelta(runtime, method, payload, deps),
    enqueueReasoningDelta: (method, payload) => enqueueReasoningDelta(runtime, method, payload, deps),
    enqueueCommandOutputDelta: (method, payload) => enqueueCommandOutputDelta(runtime, method, payload, deps),
    flushAssistantDeltasNow: (turnRef) => flushAssistantDeltaBuffers(runtime, deps, turnRef),
    finalizeActiveAssistantMessages: (turnRef) => finalizeActiveAssistantMessages(runtime, turnRef, deps),
    applyAssistantCompletion: (method, payload) => applyAssistantCompletion(runtime, method, payload, deps),
  });
}
