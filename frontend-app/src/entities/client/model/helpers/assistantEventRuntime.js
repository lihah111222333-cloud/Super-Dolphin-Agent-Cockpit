import {
  appendAssistantDeltaText,
  assistantDeltaBufferKey,
  mergeRuntimeAssistantCompletion,
  parseRuntimeTurnTerminal,
  runtimeTerminalFingerprint,
  runtimeAssistantCompletion,
  runtimeAssistantFallbackId,
  runtimeTurnId,
  runtimeTurnRefKey,
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
    turnId: entry.turnId,
  };
}

function timelineWithAssistantDelta(timeline, entry, flushTime) {
  let found = false;
  const nextTimeline = timeline.map((item) => {
    if (item.id !== entry.itemId || (entry.turnId && item.turnId && item.turnId !== entry.turnId)) return item;
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
  const key = assistantDeltaBufferKey(entry.threadId, entry.itemId, entry.turnId);
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
  const turnId = deps.normalizeString(payload.turnId || payload.turn_id);
  if (turnId && runtime.sealedTurnTerminals.has(runtimeTurnRefKey(threadId, turnId))) {
    runtime.addWarning('error', 'turn.event.late', { threadId, turnId, eventName: method });
    return false;
  }
  setAssistantDeltaBuffer(runtime, method, {
    threadId,
    turnId,
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
  ) && (item.done === false || item.status === 'running');
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
  const turnId = runtimeTurnId(payload);
  if (threadId && turnId && runtime.sealedTurnTerminals.has(runtimeTurnRefKey(threadId, turnId))) {
    runtime.addWarning('warn', 'turn.event.late', {
      threadId,
      turnId,
      eventName: method,
    });
    return false;
  }
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

function terminalPartialItemsAccepted(timeline, terminal) {
  if (!terminal.partialItemIds) return true;
  return terminal.partialItemIds.every((itemId) => timeline.some((item) => (
    item.id === itemId && item.turnId === terminal.turnId
  )));
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
    if (sealed.eventId === terminal.eventId || sealed.fingerprint === fingerprint) return false;
    runtime.addWarning('error', 'turn.terminal.conflict', {
      threadId,
      turnId: terminal.turnId,
      eventName: method,
    });
    return false;
  }
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), threadId);
  if (activeTurnId && activeTurnId !== terminal.turnId) {
    runtime.addWarning('warn', 'turn.terminal.stale', {
      threadId,
      turnId: terminal.turnId,
      activeTurnId,
      eventName: method,
    });
    return false;
  }
  runtime.flushAssistantDeltasNow();
  const timeline = runtime.get().timelinesByThread[threadId] || deps.optionalUiArray();
  if (!terminalPartialItemsAccepted(timeline, terminal)) {
    runtime.addWarning('error', 'turn.terminal.contract_invalid', { eventName: method, threadId, turnId: terminal.turnId, reason: 'partial_item_reference' });
    runtime.notifyAction('响应契约错误', 'error', { category: 'turn_terminal_contract', threadId });
    return false;
  }
  runtime.sealedTurnTerminals.set(key, { eventId: terminal.eventId, fingerprint });
  runtime.finalizeActiveAssistantMessages(threadId);
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
    applyTurnTerminal: (method, payload) => applyTurnTerminal(runtime, method, payload, deps),
  });
}
