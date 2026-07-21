import {
  parseRuntimeTurnTerminal,
  runtimeTerminalFingerprint,
  runtimeTurnRefKey,
} from '../../runtimeAssistantTimeline.js';
import { publicErrorForRemoteTerminal } from '../../../../../shared/ui/publicError.js';
import { entryMatchesTurnRef } from './turnRef.js';

const MAX_TRACKED_TURN_TERMINALS = 64;
const MAX_TRACKED_TURN_TERMINAL_SCOPES = 8;
const RETIRED_TURN_FILTER_WORDS = 256;
const RETIRED_TURN_FILTER_BITS = RETIRED_TURN_FILTER_WORDS * 32;

function createTurnTerminalLedger(runtime) {
  return {
    assistantDeltaBuffers: runtime?.assistantDeltaBuffers || new Map(),
    turnTerminalStates: runtime?.turnTerminalStates || new Map(),
    observedTurnByThread: runtime?.observedTurnByThread || new Map(),
    retiredTurnRefs: runtime?.retiredTurnRefs || new Map(),
    retiredTurnFilter: runtime?.retiredTurnFilter || new Uint32Array(RETIRED_TURN_FILTER_WORDS),
  };
}

function assertTurnTerminalLedgerCapacity(runtime, scope) {
  if (!scope || scope === '.') {
    throw new Error('frontend-app: active chat CWD is required for turn terminal ledger');
  }
  if (!runtime.assistantEventLedgersByScope.has(scope)
    && runtime.assistantEventLedgersByScope.size >= MAX_TRACKED_TURN_TERMINAL_SCOPES) {
    throw new Error('frontend-app: turn terminal scope ledger capacity exhausted');
  }
}

function activateTurnTerminalLedger(runtime, scope) {
  assertTurnTerminalLedgerCapacity(runtime, scope);
  let ledger = runtime.assistantEventLedgersByScope.get(scope);
  if (!ledger) {
    ledger = createTurnTerminalLedger(runtime.assistantEventScope ? null : runtime);
    runtime.assistantEventLedgersByScope.set(scope, ledger);
  }
  runtime.assistantEventScope = scope;
  runtime.assistantDeltaBuffers = ledger.assistantDeltaBuffers;
  runtime.turnTerminalStates = ledger.turnTerminalStates;
  runtime.observedTurnByThread = ledger.observedTurnByThread;
  runtime.retiredTurnRefs = ledger.retiredTurnRefs;
  runtime.retiredTurnFilter = ledger.retiredTurnFilter;
  return ledger;
}

function retiredTurnHash(key, seed) {
  let hash = seed >>> 0;
  for (let index = 0; index < key.length; index += 1) {
    hash = Math.imul(hash ^ key.charCodeAt(index), 16777619) >>> 0;
  }
  return hash;
}

function retiredTurnFilterIndexes(key) {
  const first = retiredTurnHash(key, 2166136261);
  const second = retiredTurnHash(key, 2246822519) | 1;
  return [0, 1, 2, 3].map((offset) => (first + Math.imul(offset, second)) >>> 0)
    .map((hash) => hash % RETIRED_TURN_FILTER_BITS);
}

function rememberRetiredTurn(runtime, key) {
  for (const index of retiredTurnFilterIndexes(key)) {
    runtime.retiredTurnFilter[index >>> 5] |= (1 << (index & 31));
  }
}

function retiredTurnRemembered(runtime, key) {
  return retiredTurnFilterIndexes(key).every((index) => (
    runtime.retiredTurnFilter[index >>> 5] & (1 << (index & 31))
  ) !== 0);
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

function archivedTurnRef(runtime, turnRef) {
  const key = runtimeTurnRefKey(turnRef.threadId, turnRef.turnId);
  return runtime.retiredTurnRefs.has(key) || retiredTurnRemembered(runtime, key);
}

function terminalTimelineContainsTurn(runtime, deps, turnRef) {
  const timeline = runtime.get().timelinesByThread[turnRef.threadId] || deps.optionalUiArray();
  return timeline.some((item) => item.kind === 'turn_terminal' && item.turnId === turnRef.turnId);
}

function turnRefIsActive(runtime, deps, turnRef) {
  return deps.activeTurnIdForThread(runtime.get(), turnRef.threadId) === turnRef.turnId;
}

function observedTurnRefEvictable(runtime, deps, turnRef) {
  if (turnRefIsActive(runtime, deps, turnRef)) return false;
  return runtime.turnTerminalStates.get(runtimeTurnRefKey(turnRef.threadId, turnRef.turnId))?.status !== 'pending';
}

function retainRetiredTurnRef(runtime, deps, turnRef) {
  const key = runtimeTurnRefKey(turnRef.threadId, turnRef.turnId);
  if (runtime.retiredTurnRefs.has(key)) return true;
  rememberRetiredTurn(runtime, key);
  if (runtime.retiredTurnRefs.size >= MAX_TRACKED_TURN_TERMINALS) {
    for (const [retiredKey, retiredTurnRef] of runtime.retiredTurnRefs) {
      if (turnRefIsActive(runtime, deps, retiredTurnRef)) continue;
      runtime.retiredTurnRefs.delete(retiredKey);
      break;
    }
  }
  if (runtime.retiredTurnRefs.size >= MAX_TRACKED_TURN_TERMINALS) return false;
  runtime.retiredTurnRefs.set(key, turnRef);
  return true;
}

function retirePendingObservedTurnForActiveTurn(runtime, deps, observedTurnRef, activeTurnRef) {
  const observedKey = runtimeTurnRefKey(observedTurnRef.threadId, observedTurnRef.turnId);
  if (runtime.turnTerminalStates.get(observedKey)?.status !== 'pending') return false;
  if (!turnRefIsActive(runtime, deps, activeTurnRef)) return false;
  if (!retainRetiredTurnRef(runtime, deps, observedTurnRef)) return false;
  runtime.turnTerminalStates.delete(observedKey);
  runtime.observedTurnByThread.delete(observedTurnRef.threadId);
  return true;
}

function evictOldestInactiveObservedTurn(runtime, deps) {
  for (const [threadId, turnId] of runtime.observedTurnByThread) {
    const turnRef = { threadId, turnId };
    if (!observedTurnRefEvictable(runtime, deps, turnRef)) continue;
    if (!retainRetiredTurnRef(runtime, deps, turnRef)) return false;
    runtime.turnTerminalStates.delete(runtimeTurnRefKey(threadId, turnId));
    runtime.observedTurnByThread.delete(threadId);
    return true;
  }
  return false;
}

function rejectTerminalCacheCapacity(runtime, deps, event, method, turnRef) {
  emitRejectedTurnEvent(runtime, deps, event, method, {
    ...turnRef,
    reason: 'capacity',
  });
  return false;
}

function trackObservedTurn(runtime, deps, event, method, turnRef) {
  const observedTurnId = runtime.observedTurnByThread.get(turnRef.threadId);
  if (observedTurnId === turnRef.turnId) return true;
  if (!observedTurnId && runtime.observedTurnByThread.size >= MAX_TRACKED_TURN_TERMINALS
    && !evictOldestInactiveObservedTurn(runtime, deps)) {
    return rejectTerminalCacheCapacity(runtime, deps, event, method, turnRef);
  }
  if (observedTurnId) {
    const observedTurnRef = { threadId: turnRef.threadId, turnId: observedTurnId };
    if (!observedTurnRefEvictable(runtime, deps, observedTurnRef)) {
      if (!retirePendingObservedTurnForActiveTurn(runtime, deps, observedTurnRef, turnRef)) {
        return rejectTerminalCacheCapacity(runtime, deps, event, method, turnRef);
      }
    } else if (!retainRetiredTurnRef(runtime, deps, observedTurnRef)) {
      return rejectTerminalCacheCapacity(runtime, deps, event, method, turnRef);
    }
    if (runtime.observedTurnByThread.get(turnRef.threadId) === observedTurnId) {
      runtime.turnTerminalStates.delete(runtimeTurnRefKey(turnRef.threadId, observedTurnId));
      runtime.observedTurnByThread.delete(turnRef.threadId);
    }
  }
  runtime.observedTurnByThread.set(turnRef.threadId, turnRef.turnId);
  return true;
}

function turnEventRejected(runtime, method, turnRef, deps) {
  const key = runtimeTurnRefKey(turnRef.threadId, turnRef.turnId);
  if (runtime.turnTerminalStates.get(key)?.status === 'sealed'
    || archivedTurnRef(runtime, turnRef)
    || terminalTimelineContainsTurn(runtime, deps, turnRef)) {
    emitRejectedTurnEvent(runtime, deps, 'turn.event.late', method, turnRef);
    return true;
  }
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), turnRef.threadId);
  if (activeTurnId && activeTurnId !== turnRef.turnId) {
    emitRejectedTurnEvent(runtime, deps, 'turn.event.stale', method, turnRef);
    return true;
  }
  return !trackObservedTurn(runtime, deps, 'turn.event.cache_exhausted', method, turnRef);
}

function terminalTimelineItem(terminal) {
  const publicError = terminal.publicError ? publicErrorForRemoteTerminal(terminal.publicError) : null;
  return {
    id: `turn-terminal-${terminal.eventId}`,
    role: 'assistant',
    kind: 'turn_terminal',
    terminalOutcome: terminal.outcome,
    ...(terminal.terminationCause ? { terminationCause: terminal.terminationCause } : {}),
    ...(publicError ? { publicError } : {}),
    time: terminal.occurredAt,
    done: true,
    turnId: terminal.turnId,
  };
}

function terminalNotice(terminal, deps) {
  if (terminal.outcome === 'success') return deps.actionNotice('已收到回复', 'success');
  if (terminal.publicError) {
    return deps.actionNotice(`运行失败：${publicErrorForRemoteTerminal(terminal.publicError).message}`, 'error');
  }
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

function terminalConflictReason(existing, terminal) {
  if (existing.eventId === terminal.eventId) return 'event_id_content_mismatch';
  if (existing.fingerprint === runtimeTerminalFingerprint(terminal)) return 'content_replayed_with_new_event_id';
  return 'terminal_truth_conflict';
}

function cachePendingTurnTerminal(runtime, pendingTerminal, deps) {
  const { method, payload, terminal, threadId, key, fingerprint } = pendingTerminal;
  const existing = runtime.turnTerminalStates.get(key);
  if (existing) {
    if (existing.eventId === terminal.eventId && existing.fingerprint === fingerprint) return false;
    emitRejectedTurnEvent(runtime, deps, 'turn.terminal.conflict', method, {
      threadId,
      turnId: terminal.turnId,
      reason: terminalConflictReason(existing, terminal),
    });
    return false;
  }
  runtime.turnTerminalStates.set(key, {
    status: 'pending',
    method,
    payload,
    eventId: terminal.eventId,
    fingerprint,
    threadId,
    turnId: terminal.turnId,
  });
  return true;
}

function applyTurnTerminal(runtime, method, payload, deps) {
  const parsed = parseRuntimeTurnTerminal(payload);
  if (!parsed.value) {
    runtime.addWarning('error', 'turn.terminal.contract_invalid', {
      eventName: method,
      reason: parsed.error,
    });
    runtime.notifyAction('响应契约错误', 'error', { category: 'turn_terminal_contract' });
    return false;
  }
  const terminal = parsed.value;
  const threadId = runtime.bridgeThreadIdForPayload(terminal);
  if (!threadId) {
    runtime.addWarning('error', 'turn.terminal.thread_invalid', {
      eventName: method,
      threadId: terminal.threadId,
      turnId: terminal.turnId,
    });
    return false;
  }
  const key = runtimeTurnRefKey(threadId, terminal.turnId);
  const fingerprint = runtimeTerminalFingerprint(terminal);
  const terminalState = runtime.turnTerminalStates.get(key);
  if (terminalState) {
    if (terminalState.eventId === terminal.eventId && terminalState.fingerprint === fingerprint) return false;
    emitRejectedTurnEvent(runtime, deps, 'turn.terminal.conflict', method, {
      threadId,
      turnId: terminal.turnId,
      reason: terminalConflictReason(terminalState, terminal),
    });
    return false;
  }
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), threadId);
  const observedTurnId = runtime.observedTurnByThread.get(threadId);
  if ((observedTurnId && observedTurnId !== terminal.turnId)
    || (!observedTurnId && activeTurnId && activeTurnId !== terminal.turnId)
    || archivedTurnRef(runtime, { threadId, turnId: terminal.turnId })
    || terminalTimelineContainsTurn(runtime, deps, { threadId, turnId: terminal.turnId })) {
    emitRejectedTurnEvent(runtime, deps, 'turn.terminal.stale', method, {
      threadId,
      turnId: terminal.turnId,
    });
    return false;
  }
  if (!trackObservedTurn(runtime, deps, 'turn.terminal.cache_exhausted', method, {
    threadId,
    turnId: terminal.turnId,
  })) return false;
  const turnRef = { threadId, turnId: terminal.turnId };
  const timeline = runtime.get().timelinesByThread[threadId] || deps.optionalUiArray();
  if (!terminalPartialItemsAccepted(runtime, timeline, turnRef, terminal)) {
    cachePendingTurnTerminal(runtime, { method, payload, terminal, threadId, key, fingerprint }, deps);
    return false;
  }
  runtime.flushAssistantDeltasNow(turnRef);
  runtime.turnTerminalStates.set(key, {
    status: 'sealed',
    eventId: terminal.eventId,
    fingerprint,
  });
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
    runtime.addWarning('error', `turn.terminal.${terminal.outcome}`, {
      threadId,
      turnId: terminal.turnId,
      eventName: method,
    });
  }
  return true;
}

function replayPendingTurnTerminal(runtime, turnRef, deps) {
  const key = runtimeTurnRefKey(turnRef.threadId, turnRef.turnId);
  const pending = runtime.turnTerminalStates.get(key);
  if (pending?.status !== 'pending') return false;
  runtime.turnTerminalStates.delete(key);
  return applyTurnTerminal(runtime, pending.method, pending.payload, deps);
}

function clearTurnRuntimeForThread(runtime, threadId) {
  if (!threadId) return;
  const prefix = `${threadId}\u0000`;
  for (const key of runtime.assistantDeltaBuffers.keys()) {
    if (key.startsWith(prefix)) runtime.assistantDeltaBuffers.delete(key);
  }
  if (runtime.assistantDeltaBuffers.size === 0) runtime.clearAssistantDeltaFlushTimer();
  for (const key of runtime.turnTerminalStates.keys()) {
    if (key.startsWith(prefix)) runtime.turnTerminalStates.delete(key);
  }
  for (const key of runtime.retiredTurnRefs.keys()) {
    if (key.startsWith(prefix)) runtime.retiredTurnRefs.delete(key);
  }
  runtime.observedTurnByThread.delete(threadId);
}

function activateAssistantEventScope(runtime, scope) {
  runtime.assistantEventScopeEpoch += 1;
  runtime.clearAssistantDeltaFlushTimer();
  activateTurnTerminalLedger(runtime, scope);
}

function terminalCacheStats(runtime) {
  let terminalStates = 0;
  let observedTurns = 0;
  let retiredTurns = 0;
  for (const ledger of runtime.assistantEventLedgersByScope.values()) {
    terminalStates += ledger.turnTerminalStates.size;
    observedTurns += ledger.observedTurnByThread.size;
    retiredTurns += ledger.retiredTurnRefs.size;
  }
  return {
    capacity: MAX_TRACKED_TURN_TERMINALS,
    terminalStates: runtime.turnTerminalStates.size,
    observedTurns: runtime.observedTurnByThread.size,
    retiredTurns: runtime.retiredTurnRefs.size,
    scopeCapacity: MAX_TRACKED_TURN_TERMINAL_SCOPES,
    scopeCount: runtime.assistantEventLedgersByScope.size,
    totalTerminalStates: terminalStates,
    totalObservedTurns: observedTurns,
    totalRetiredTurns: retiredTurns,
  };
}

function reconcileObservedTurnWithActiveTurn(runtime, threadId, deps) {
  const activeTurnId = deps.activeTurnIdForThread(runtime.get(), threadId);
  if (!activeTurnId) return;
  trackObservedTurn(runtime, deps, 'turn.event.cache_exhausted', 'turn.active.reconcile', {
    threadId,
    turnId: activeTurnId,
  });
}

export function attachAssistantTurnLifecycle(runtime, deps) {
  Object.assign(runtime, {
    turnEventRejected: (method, turnRef) => turnEventRejected(runtime, method, turnRef, deps),
    replayPendingTurnTerminal: (turnRef) => replayPendingTurnTerminal(runtime, turnRef, deps),
    applyTurnTerminal: (method, payload) => applyTurnTerminal(runtime, method, payload, deps),
    clearTurnRuntimeForThread: (threadId) => clearTurnRuntimeForThread(runtime, threadId),
    assertAssistantEventScopeCapacity: (scope) => assertTurnTerminalLedgerCapacity(runtime, scope),
    activateAssistantEventScope: (scope) => activateAssistantEventScope(runtime, scope),
    getTurnTerminalCacheStats: () => terminalCacheStats(runtime),
    reconcileObservedTurnWithActiveTurn: (threadId) => reconcileObservedTurnWithActiveTurn(runtime, threadId, deps),
  });
}
