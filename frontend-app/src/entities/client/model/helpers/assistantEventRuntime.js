import {
  appendAssistantDeltaText,
  assistantDeltaBufferKey,
  mergeRuntimeAssistantCompletion,
  runtimeAssistantCompletion,
  runtimeAssistantFallbackId,
} from '../runtimeAssistantTimeline.js';
import { normalizeThreadId } from './threadIdentity.js';

function drainAssistantDeltaEntries(runtime, deps) {
  runtime.clearAssistantDeltaFlushTimer();
  if (runtime.assistantDeltaBuffers.size === 0) return null;
  const entries = Array.from(runtime.assistantDeltaBuffers.values());
  runtime.assistantDeltaBuffers.clear();
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
    kind: 'assistant',
    text: entry.delta,
    time: entry.timestamp || flushTime,
    done: false,
    optimistic: false,
    runtime: true,
  };
}

function timelineWithAssistantDelta(timeline, entry, flushTime) {
  let found = false;
  const nextTimeline = timeline.map((item) => {
    if (item.id !== entry.itemId) return item;
    found = true;
    return {
      ...item,
      role: 'assistant',
      text: appendAssistantDeltaText(item.text, entry.delta),
      done: false,
    };
  });
  if (found) return nextTimeline;
  return [...nextTimeline, bufferedAssistantTimelineItem(entry, flushTime)];
}

function assistantDeltaFlushPatch(state, flushed, deps) {
  const timelinesByThread = { ...state.timelinesByThread };
  for (const entry of flushed.entries) {
    const timeline = timelinesByThread[entry.threadId] || deps.optionalUiArray();
    timelinesByThread[entry.threadId] = timelineWithAssistantDelta(
      timeline,
      entry,
      flushed.flushTime,
    );
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

function flushAssistantDeltaBuffers(runtime, deps) {
  const flushed = drainAssistantDeltaEntries(runtime, deps);
  if (!flushed) return false;
  runtime.set((state) => assistantDeltaFlushPatch(state, flushed, deps));
  return true;
}

function scheduleAssistantDeltaFlush(runtime, deps) {
  if (runtime.assistantDeltaFlushTimer) return;
  runtime.assistantDeltaFlushTimer = setTimeout(() => {
    runtime.assistantDeltaFlushTimer = null;
    flushAssistantDeltaBuffers(runtime, deps);
  }, deps.ASSISTANT_DELTA_FLUSH_MS);
}

function assistantDeltaItemId(runtime, payload, deps) {
  return deps.normalizeString(payload.itemId || payload.item_id || payload.messageId || payload.message_id) ||
    runtimeAssistantFallbackId(payload, { normalizeThreadId, runtimeThreadIdentifier: deps.runtimeThreadIdentifier });
}

function setAssistantDeltaBuffer(runtime, method, entry) {
  const key = assistantDeltaBufferKey(entry.threadId, entry.itemId);
  const existing = runtime.assistantDeltaBuffers.get(key);
  runtime.assistantDeltaBuffers.set(key, {
    ...entry,
    method: existing?.method || method,
    delta: appendAssistantDeltaText(existing?.delta, entry.delta),
    timestamp: existing?.timestamp || entry.timestamp,
  });
}

function enqueueAssistantDelta(runtime, method, payload, deps) {
  const threadId = runtime.bridgeThreadIdForPayload(payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!threadId || delta === '') return false;
  setAssistantDeltaBuffer(runtime, method, {
    threadId,
    itemId: assistantDeltaItemId(runtime, payload, deps),
    delta,
    timestamp: deps.normalizeString(payload.timestamp) || deps.clockNowISO(),
  });
  scheduleAssistantDeltaFlush(runtime, deps);
  return true;
}

function enqueueReasoningDelta(runtime, method, payload, deps) {
  const threadId = runtime.bridgeThreadIdForPayload(payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!threadId || delta === '') return false;
  const turnId = deps.normalizeString(payload.turnId || payload.turn_id);
  if (!turnId) return false;
  setAssistantDeltaBuffer(runtime, method, {
    threadId,
    itemId: `thinking:${turnId}`,
    delta,
    timestamp: deps.normalizeString(payload.timestamp) || deps.clockNowISO(),
    kind: 'thinking',
    turnId,
  });
  scheduleAssistantDeltaFlush(runtime, deps);
  return true;
}

function commandOutputItemId(timeline) {
  let fallback = '';
  for (let i = timeline.length - 1; i >= 0; i--) {
    if (timeline[i].kind !== 'command') continue;
    if (timeline[i].done !== true) return timeline[i].id;
    if (!fallback) fallback = timeline[i].id;
  }
  return fallback;
}

function enqueueCommandOutputDelta(runtime, method, payload, deps) {
  const threadId = runtime.bridgeThreadIdForPayload(payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!threadId || delta === '') return false;
  const timeline = runtime.get().timelinesByThread[threadId] || deps.optionalUiArray();
  const itemId = commandOutputItemId(timeline);
  if (!itemId) return false;
  setAssistantDeltaBuffer(runtime, method, {
    threadId,
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
    item.role === 'assistant' ||
    item.kind === 'assistant' ||
    item.kind === 'thinking' ||
    item.kind === 'command'
  ) && item.done === false;
}

function finalizedAssistantTimeline(timeline) {
  let mutated = false;
  const items = timeline.map((item) => {
    if (!isOpenAssistantTimelineItem(item)) return item;
    mutated = true;
    return { ...item, done: true };
  });
  return { items, mutated };
}

function finalizeActiveAssistantMessages(runtime, threadId, deps) {
  if (!threadId) return false;
  runtime.set((state) => {
    let mutated = false;
    const timelinesByThread = { ...state.timelinesByThread };
    for (const key of relatedThreadTimelineKeys(state, threadId, deps)) {
      if (!deps.hasOwn(state.timelinesByThread, key)) continue;
      const result = finalizedAssistantTimeline(state.timelinesByThread[key]);
      mutated = mutated || result.mutated;
      timelinesByThread[key] = result.items;
    }
    return mutated ? { timelinesByThread } : {};
  });
  return true;
}

function applyAssistantCompletion(runtime, method, payload, deps) {
  const threadId = runtime.bridgeThreadIdForPayload(payload);
  const completion = runtimeAssistantCompletion(payload);
  if (!threadId || !completion) return false;
  runtime.set((state) => {
    const timelinesByThread = { ...state.timelinesByThread };
    const targetKeys = relatedThreadTimelineKeys(state, threadId, deps)
      .filter((key) => key === threadId || deps.hasOwn(state.timelinesByThread, key));
    for (const key of targetKeys) {
      timelinesByThread[key] = mergeRuntimeAssistantCompletion(
        state.timelinesByThread[key] || deps.optionalUiArray(),
        completion,
      );
    }
    return {
      timelinesByThread,
      actionNotice: deps.actionNotice('已收到回复', 'success'),
      activityEntries: [{
        id: `${method}-${deps.clockNowMillis()}`,
        method,
        threadId,
        timestamp: deps.clockNowISO(),
      }, ...state.activityEntries].slice(0, 120),
    };
  });
  return true;
}

function relatedThreadTimelineKeys(state, threadId, deps) {
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

export function attachAssistantEventRuntime(runtime, deps) {
  runtime.clearAssistantDeltaFlushTimer = () => {
    if (!runtime.assistantDeltaFlushTimer) return;
    clearTimeout(runtime.assistantDeltaFlushTimer);
    runtime.assistantDeltaFlushTimer = null;
  };
  Object.assign(runtime, {
    enqueueAssistantDelta: (method, payload) => enqueueAssistantDelta(runtime, method, payload, deps),
    enqueueReasoningDelta: (method, payload) => enqueueReasoningDelta(runtime, method, payload, deps),
    enqueueCommandOutputDelta: (method, payload) => enqueueCommandOutputDelta(runtime, method, payload, deps),
    finalizeActiveAssistantMessages: (threadId) => finalizeActiveAssistantMessages(runtime, threadId, deps),
    flushAssistantDeltasNow: () => flushAssistantDeltaBuffers(runtime, deps),
    applyAssistantCompletion: (method, payload) => applyAssistantCompletion(runtime, method, payload, deps),
  });
}
