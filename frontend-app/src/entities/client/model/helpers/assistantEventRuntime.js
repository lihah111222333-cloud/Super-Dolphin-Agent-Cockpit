import {
  appendAssistantDeltaText,
  assistantDeltaBufferKey,
  mergeRuntimeAssistantCompletion,
  parseRuntimeTurnRef,
  parseRuntimeTurnTerminal,
  runtimeTerminalFingerprint,
  runtimeAssistantCompletion,
  runtimeAssistantFallbackId,
  runtimeTurnRefKey,
} from '../runtimeAssistantTimeline.js';
import { normalizeThreadId } from './threadIdentity.js';

const MAX_PENDING_TURN_TERMINALS = 64;

function entryMatchesTurnRef(entry, turnRef) {
  return !turnRef || (entry.threadId === turnRef.threadId && entry.turnId === turnRef.turnId);
}

function drainAssistantDeltaEntries(runtime, deps, turnRef) {
  if (runtime.assistantDeltaBuffers.size === 0) return null;
  const entries = [];
  for (const [key, entry] of runtime.assistantDeltaBuffers.entries()) {
    if (!entryMatchesTurnRef(entry, turnRef)) continue;
    entries.push(entry);
    runtime.assistantDeltaBuffers.delete(key);
  }
  if (entries.length === 0) return null;
  if (runtime.assistantDeltaBuffers.size === 0) runtime.clearAssistantDeltaFlushTimer();
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

function flushAssistantDeltaBuffers(runtime, deps, turnRef) {
  const flushed = drainAssistantDeltaEntries(runtime, deps, turnRef);
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
  const key = assistantDeltaBufferKey(entry.threadId, entry.itemId, entry.turnId);
  const existing = runtime.assistantDeltaBuffers.get(key);
  runtime.assistantDeltaBuffers.set(key, {
    ...entry,
    method: existing?.method || method,
    delta: appendAssistantDeltaText(existing?.delta, entry.delta),
    timestamp: existing?.timestamp || entry.timestamp,
  });
}

function emitRejectedTurnEvent(runtime, deps, event, method, rejection) {
  const { threadId, turnId, reason = '' } = rejection;
  runtime.addWarning('error', event, {
    eventName: method,
    threadId,
    turnId,
    ...(reason ? { reason } : {}),
  });
  deps.emitFrontendTraceEvent({
    phase: 'frontend.turn_event.rejected',
    method: event,
    thread_id: threadId,
    turn_id: turnId,
    status: 'error',
    ...(reason ? { error: reason } : {}),
    metadata: { event_name: method },
  });
}

function canonicalTurnEventRef(runtime, method, payload) {
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

function turnEventRejected(runtime, method, turnRef, deps) {
  const key = runtimeTurnRefKey(turnRef.threadId, turnRef.turnId);
  if (runtime.sealedTurnTerminals.has(key) || runtime.retiredTurnRefs.has(key)) {
    emitRejectedTurnEvent(runtime, deps, 'turn.event.late', method, turnRef);
    return true;
  }
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), turnRef.threadId);
  if (activeTurnId && activeTurnId !== turnRef.turnId) {
    emitRejectedTurnEvent(runtime, deps, 'turn.event.stale', method, turnRef);
    return true;
  }
  const observedTurnId = runtime.observedTurnByThread.get(turnRef.threadId);
  if (observedTurnId && observedTurnId !== turnRef.turnId) {
    runtime.retiredTurnRefs.add(runtimeTurnRefKey(turnRef.threadId, observedTurnId));
  }
  runtime.observedTurnByThread.set(turnRef.threadId, turnRef.turnId);
  return false;
}

function reconcileObservedTurnWithActiveTurn(runtime, threadId, deps) {
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), threadId);
  if (!activeTurnId) return;
  const observedTurnId = runtime.observedTurnByThread.get(threadId);
  if (observedTurnId === activeTurnId) return;
  if (observedTurnId) {
    runtime.retiredTurnRefs.add(runtimeTurnRefKey(threadId, observedTurnId));
  }
  runtime.observedTurnByThread.set(threadId, activeTurnId);
}

function enqueueAssistantDelta(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!turnRef || delta === '' || turnEventRejected(runtime, method, turnRef, deps)) return false;
  setAssistantDeltaBuffer(runtime, method, {
    ...turnRef,
    itemId: assistantDeltaItemId(runtime, payload, deps),
    delta,
    timestamp: deps.normalizeString(payload.timestamp) || deps.clockNowISO(),
  });
  replayPendingTurnTerminal(runtime, turnRef, deps);
  scheduleAssistantDeltaFlush(runtime, deps);
  return true;
}

function enqueueReasoningDelta(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!turnRef || delta === '' || turnEventRejected(runtime, method, turnRef, deps)) return false;
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

function commandOutputItemId(timeline, turnId) {
  let fallback = '';
  for (let i = timeline.length - 1; i >= 0; i--) {
    if (timeline[i].kind !== 'command' || timeline[i].turnId !== turnId) continue;
    if (timeline[i].done !== true) return timeline[i].id;
    if (!fallback) fallback = timeline[i].id;
  }
  return fallback;
}

function enqueueCommandOutputDelta(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  const delta = deps.extractDeltaText(payload.delta ?? payload.text ?? payload.content);
  if (!turnRef || delta === '' || turnEventRejected(runtime, method, turnRef, deps)) return false;
  const timeline = runtime.get().timelinesByThread[turnRef.threadId] || deps.optionalUiArray();
  const itemId = commandOutputItemId(timeline, turnRef.turnId);
  if (!itemId) {
    runtime.addWarning('error', 'turn.event.contract_invalid', { eventName: method, reason: 'command_item_turn_ref' });
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
    item.role === 'assistant' ||
    item.kind === 'assistant' ||
    item.kind === 'thinking' ||
    item.kind === 'command'
  ) && (item.done === false || item.status === 'running');
}

function finalizedAssistantTimeline(timeline, turnId) {
  let mutated = false;
  const items = timeline.map((item) => {
    if (item.turnId !== turnId || !isOpenAssistantTimelineItem(item)) return item;
    mutated = true;
    return { ...item, done: true };
  });
  return { items, mutated };
}

function finalizeActiveAssistantMessages(runtime, turnRef, deps) {
  if (!turnRef?.threadId || !turnRef?.turnId) return false;
  runtime.set((state) => {
    let mutated = false;
    const timelinesByThread = { ...state.timelinesByThread };
    for (const key of relatedThreadTimelineKeys(state, turnRef.threadId, deps)) {
      if (!deps.hasOwn(state.timelinesByThread, key)) continue;
      const result = finalizedAssistantTimeline(state.timelinesByThread[key], turnRef.turnId);
      mutated = mutated || result.mutated;
      timelinesByThread[key] = result.items;
    }
    return mutated ? { timelinesByThread } : {};
  });
  return true;
}

function applyAssistantCompletion(runtime, method, payload, deps) {
  const turnRef = canonicalTurnEventRef(runtime, method, payload);
  if (!turnRef || turnEventRejected(runtime, method, turnRef, deps)) return false;
  const completion = runtimeAssistantCompletion(payload);
  if (!completion) return false;
  runtime.flushAssistantDeltasNow(turnRef);
  runtime.set((state) => {
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
      activityEntries: [{
        id: `${method}-${deps.clockNowMillis()}`,
        method,
        threadId: turnRef.threadId,
        timestamp: deps.clockNowISO(),
      }, ...state.activityEntries].slice(0, 120),
    };
  });
  return true;
}

function terminalTimelineItem(terminal) {
  return {
    id: `turn-terminal-${terminal.eventId}`,
    role: 'assistant',
    kind: 'turn_terminal',
    terminalOutcome: terminal.outcome,
    ...(terminal.terminationCause ? { terminationCause: terminal.terminationCause } : {}),
    ...(terminal.publicError ? { publicError: terminal.publicError } : {}),
    time: terminal.occurredAt,
    done: true,
    turnId: terminal.turnId,
  };
}

function terminalNotice(terminal, deps) {
  if (terminal.outcome === 'success') return deps.actionNotice('已收到回复', 'success');
  if (terminal.publicError) return deps.actionNotice(`运行失败：${terminal.publicError.message}`, 'error');
  if (terminal.outcome === 'cancelled') return deps.actionNotice('本轮已取消', 'info');
  return deps.actionNotice('本轮已中断', 'info');
}

function terminalPartialItemsAccepted(runtime, timeline, turnRef, terminal) {
  if (!terminal.partialItemIds) return true;
  const bufferedItemIds = new Set();
  for (const entry of runtime.assistantDeltaBuffers.values()) {
    if (entryMatchesTurnRef(entry, turnRef)) bufferedItemIds.add(entry.itemId);
  }
  return terminal.partialItemIds.every((itemId) => timeline.some((item) => (
    item.id === itemId && item.turnId === terminal.turnId
  )) || bufferedItemIds.has(itemId));
}

function replayPendingTurnTerminal(runtime, turnRef, deps) {
  const key = runtimeTurnRefKey(turnRef.threadId, turnRef.turnId);
  const pending = runtime.pendingTurnTerminals.get(key);
  if (!pending) return false;
  runtime.pendingTurnTerminals.delete(key);
  return applyTurnTerminal(runtime, pending.method, pending.payload, deps);
}

function terminalConflictReason(existing, terminal) {
  if (existing.eventId === terminal.eventId) return 'event_id_content_mismatch';
  if (existing.fingerprint === runtimeTerminalFingerprint(terminal)) return 'content_replayed_with_new_event_id';
  return 'terminal_truth_conflict';
}

function cachePendingTurnTerminal(runtime, pendingTerminal, deps) {
  const { method, payload, terminal, threadId, key, fingerprint } = pendingTerminal;
  const existing = runtime.pendingTurnTerminals.get(key);
  if (existing) {
    if (existing.eventId === terminal.eventId && existing.fingerprint === fingerprint) return false;
    emitRejectedTurnEvent(runtime, deps, 'turn.terminal.conflict', method, {
      threadId,
      turnId: terminal.turnId,
      reason: terminalConflictReason(existing, terminal),
    });
    return false;
  }
  if (runtime.pendingTurnTerminals.size >= MAX_PENDING_TURN_TERMINALS) {
    const [evictedKey, evicted] = runtime.pendingTurnTerminals.entries().next().value;
    runtime.pendingTurnTerminals.delete(evictedKey);
    runtime.retiredTurnRefs.add(evictedKey);
    emitRejectedTurnEvent(runtime, deps, 'turn.terminal.pending_evicted', evicted.method, {
      threadId: evicted.threadId,
      turnId: evicted.turnId,
      reason: 'capacity',
    });
  }
  runtime.pendingTurnTerminals.set(key, {
    method,
    payload,
    eventId: terminal.eventId,
    fingerprint,
    threadId,
    turnId: terminal.turnId,
  });
  runtime.observedTurnByThread.set(threadId, terminal.turnId);
  return true;
}

function applyTurnTerminal(runtime, method, payload, deps) {
  const parsed = parseRuntimeTurnTerminal(payload);
  if (!parsed.value) {
    runtime.addWarning('error', 'turn.terminal.contract_invalid', { eventName: method, reason: parsed.error });
    runtime.notifyAction('响应契约错误', 'error', { category: 'turn_terminal_contract' });
    return false;
  }
  const terminal = parsed.value;
  const threadId = runtime.bridgeThreadIdForPayload(terminal);
  if (!threadId) {
    runtime.addWarning('error', 'turn.terminal.thread_invalid', { eventName: method, threadId: terminal.threadId, turnId: terminal.turnId });
    return false;
  }
  const key = runtimeTurnRefKey(threadId, terminal.turnId);
  const fingerprint = runtimeTerminalFingerprint(terminal);
  const sealed = runtime.sealedTurnTerminals.get(key);
  if (sealed) {
    if (sealed.eventId === terminal.eventId && sealed.fingerprint === fingerprint) return false;
    emitRejectedTurnEvent(
      runtime,
      deps,
      'turn.terminal.conflict',
      method,
      { threadId, turnId: terminal.turnId, reason: terminalConflictReason(sealed, terminal) },
    );
    return false;
  }
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), threadId);
  const observedTurnId = runtime.observedTurnByThread.get(threadId);
  if ((observedTurnId && observedTurnId !== terminal.turnId)
    || (!observedTurnId && activeTurnId && activeTurnId !== terminal.turnId)
    || runtime.retiredTurnRefs.has(key)) {
    emitRejectedTurnEvent(runtime, deps, 'turn.terminal.stale', method, { threadId, turnId: terminal.turnId });
    return false;
  }
  if (runtime.pendingTurnTerminals.has(key)) {
    cachePendingTurnTerminal(runtime, { method, payload, terminal, threadId, key, fingerprint }, deps);
    return false;
  }
  const turnRef = { threadId, turnId: terminal.turnId };
  const timeline = runtime.get().timelinesByThread[threadId] || deps.optionalUiArray();
  if (!terminalPartialItemsAccepted(runtime, timeline, turnRef, terminal)) {
    cachePendingTurnTerminal(runtime, { method, payload, terminal, threadId, key, fingerprint }, deps);
    return false;
  }
  runtime.observedTurnByThread.set(threadId, terminal.turnId);
  runtime.flushAssistantDeltasNow(turnRef);
  runtime.sealedTurnTerminals.set(key, { eventId: terminal.eventId, fingerprint });
  runtime.finalizeActiveAssistantMessages(turnRef);
  runtime.set((state) => ({
    timelinesByThread: {
      ...state.timelinesByThread,
      [threadId]: [...(state.timelinesByThread[threadId] || deps.optionalUiArray()), terminalTimelineItem(terminal)],
    },
    actionNotice: terminalNotice(terminal, deps),
    activityEntries: [{
      id: `${method}-${terminal.eventId}`,
      method,
      threadId,
      timestamp: terminal.occurredAt,
    }, ...state.activityEntries].slice(0, 120),
  }));
  if (terminal.outcome !== 'success' && terminal.terminationCause !== 'user_request') {
    runtime.addWarning('error', `turn.terminal.${terminal.outcome}`, { threadId, turnId: terminal.turnId, eventName: method });
  }
  return true;
}

export function clearTurnRuntimeForThread(runtime, threadId) {
  if (!threadId) return;
  const prefix = `${threadId}\u0000`;
  for (const key of runtime.assistantDeltaBuffers.keys()) {
    if (key.startsWith(prefix)) runtime.assistantDeltaBuffers.delete(key);
  }
  if (runtime.assistantDeltaBuffers.size === 0) runtime.clearAssistantDeltaFlushTimer();
  for (const key of runtime.pendingTurnTerminals.keys()) {
    if (key.startsWith(prefix)) runtime.pendingTurnTerminals.delete(key);
  }
  for (const key of runtime.sealedTurnTerminals.keys()) {
    if (key.startsWith(prefix)) runtime.sealedTurnTerminals.delete(key);
  }
  for (const key of runtime.retiredTurnRefs) {
    if (key.startsWith(prefix)) runtime.retiredTurnRefs.delete(key);
  }
  runtime.observedTurnByThread.delete(threadId);
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
    clearTurnRuntimeForThread: (threadId) => clearTurnRuntimeForThread(runtime, threadId),
    reconcileObservedTurnWithActiveTurn: (threadId) => reconcileObservedTurnWithActiveTurn(runtime, threadId, deps),
    finalizeActiveAssistantMessages: (turnRef) => finalizeActiveAssistantMessages(runtime, turnRef, deps),
    flushAssistantDeltasNow: (turnRef) => flushAssistantDeltaBuffers(runtime, deps, turnRef),
    applyAssistantCompletion: (method, payload) => applyAssistantCompletion(runtime, method, payload, deps),
    applyTurnTerminal: (method, payload) => applyTurnTerminal(runtime, method, payload, deps),
  });
}
