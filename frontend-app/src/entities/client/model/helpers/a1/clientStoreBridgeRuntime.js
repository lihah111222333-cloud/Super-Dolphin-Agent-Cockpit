
import { firstOptionalPresent, optionalTextField } from '../../contractStoreModel.js';
import { emitFrontendTraceEvent } from '../../../../../shared/api/backendApi.js';
import { bridgePatchData, bridgePatchState } from '../../bridgePatchState.js';
import { bridgeRevisionKey } from '../bridgeRevision.js';
import { isAssistantMessageDeltaEvent } from '../../runtimeAssistantTimeline.js';
import { normalizeBackendThreadId, normalizeThreadId, isAgentRuntimeId } from '../threadIdentity.js';
import { normalizeTokenUsage, threadActivityTimestamp, shouldFloatThreadPatch } from '../threadActivityMetrics.js';
import {
  BRIDGE_PATCH_SLOW_MS,
  clockNowMillis,
  normalizeString,
  optionalUiObject,
} from './clientStoreUtils.js';
import {
  compareSequence,
} from './clientStoreSendModel.js';
import {
  runtimePayloadCwd,
  runtimeThreadIdentifier,
  threadMatchesIdentifier,
} from './clientStoreRuntimeThreadModel.js';
import { normalizeThread } from './clientStoreThreadModel.js';
import {
  mergeRuntimeResultEntries,
  runtimeResultEntriesFromTimelineItems,
} from './clientStoreSnapshotModel.js';

function tokenUsageByThreadPatch(state, threadId, usage) {
  return {
    tokenUsageByThread: {
      ...state.tokenUsageByThread,
      [threadId]: usage,
    },
  };
}

function bridgePatchFromPayload(method, payload, threadId) {
  return {
    ...bridgePatchData(method, payload, threadId, {
      normalizeThread,
      runtimeResultEntriesFromTimelineItems,
    }),
    promoteForActivity: shouldFloatThreadPatch(payload),
  };
}

function emitSlowBridgePatchTrace(method, payload, threadId, durationMs) {
  if (durationMs < BRIDGE_PATCH_SLOW_MS) return;
  emitFrontendTraceEvent({
    phase: 'frontend.patch.apply.slow',
    method,
    thread_id: threadId,
    agent_id: normalizeString(payload.agentId || payload.agent_id || payload.agentRuntime?.agentId || payload.agent_runtime?.agent_id),
    turn_id: normalizeString(payload.turnId || payload.turn_id || payload.activeTurn?.id || payload.active_turn?.id),
    duration_ms: durationMs,
    status: 'ok',
  });
}

function attachBridgeIdentityRuntime(runtime) {
  const { get, addWarning, currentChatCwd } = runtime;

  const bridgeThreadIdForPayload = (payload) => {
    const identifier = runtimeThreadIdentifier(payload);
    const id = normalizeThreadId(identifier);
    if (!id) return '';
    const payloadCwd = runtimePayloadCwd(payload);
    const activeCwd = currentChatCwd();
    if (payloadCwd && activeCwd && payloadCwd !== activeCwd) {
      addWarning('warn', 'thread.patch.cwd_mismatch', { threadId: id, payloadCwd, activeCwd });
      return '';
    }

    const state = get();
    const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
    if (matchedThread) return matchedThread.archived ? '' : normalizeBackendThreadId(matchedThread.id);

    const fallback = normalizeBackendThreadId(id);
    if (!fallback) return '';
    if (fallback === normalizeBackendThreadId(state.activeThreadId)) return fallback;

    const eventAgentId = normalizeThreadId(
      payload.agentId ||
      payload.agent_id ||
      payload.agentRuntime?.agentId ||
      payload.agent_runtime?.agentId ||
      payload.agent_runtime?.agent_id
    );
    if (eventAgentId && eventAgentId === normalizeThreadId(state.activeThreadId)) {
      return fallback;
    }

    if (payloadCwd && (!activeCwd || payloadCwd === activeCwd)) return fallback;

    if (isAgentRuntimeId(id)) return '';

    addWarning('warn', 'thread.patch.unknown_thread', { threadId: fallback, activeCwd });
    return '';
  };


  Object.assign(runtime, { bridgeThreadIdForPayload });
}

function attachBridgePatchRuntime(runtime) {
  /*
   * ui/thread/patch 是实时线程状态入口。
   * 先确认 thread/cwd 属于当前页面，再按 sequence 跳过旧事件。
   */
  const { set, bridgeThreadIdForPayload, reconcileObservedTurnWithActiveTurn } = runtime;
  const { sequencesByThread, patchGenerationsByThread } = runtime;

  const applyBridgePatch = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    if (!threadId) return;

    const generation = normalizeString(payload.generation || payload.epoch);
    if (generation) {
      const previousGeneration = patchGenerationsByThread.get(threadId) || optionalTextField();
      if (previousGeneration && compareSequence(generation, previousGeneration) < 0) {
        return;
      }
      if (!previousGeneration || compareSequence(generation, previousGeneration) > 0) {
        patchGenerationsByThread.set(threadId, generation);
      }
    }

    const sequence = normalizeString(payload.sequence);
    const sequenceKey = generation ? `${threadId}::${generation}` : threadId;
    const previousSequence = sequencesByThread.get(sequenceKey) || optionalTextField();
    if (sequence) {
      if (previousSequence && compareSequence(sequence, previousSequence) <= 0) {
        return;
      }
      sequencesByThread.set(sequenceKey, sequence);
    }

      const patchStart = clockNowMillis();
      try {
        const patch = bridgePatchFromPayload(method, payload, threadId);
        set((state) => bridgePatchState(state, patch, {
          mergeRuntimeResultEntries,
          threadActivityTimestamp,
          threadMatchesIdentifier,
        }));
        reconcileObservedTurnWithActiveTurn(threadId);
      }
    finally {
      const durationMs = clockNowMillis() - patchStart;
      emitSlowBridgePatchTrace(method, payload, threadId, durationMs);
    }
  };


  Object.assign(runtime, { applyBridgePatch });
}

function attachBridgeEventRuntime(runtime) {
  /*
   * bridge event handler 只负责分流：刷新标记、thread patch、delta、结束事件。
   * 结束事件先刷完 delta，再把未完成的 timeline item 标记完成。
   */
  const {
    set,
    addWarning,
    refreshActiveChatSidebarInBackground,
    applyBridgePatch,
    enqueueAssistantDelta,
    enqueueReasoningDelta,
    enqueueCommandOutputDelta,
    flushAssistantDeltasNow,
    applyAssistantCompletion,
    applyTurnTerminal,
    bridgeThreadIdForPayload,
    notifyAction,
  } = runtime;

  const handleBridgeEvent = (evt) => {
    const method = normalizeString(firstOptionalPresent(evt?.method, evt?.type));
    const eventName = method.toLowerCase();
    const payload = firstOptionalPresent(evt?.payload, evt?.params, evt?.data) || optionalUiObject();
    if (!method) {
      addWarning('error', 'bridge.event.method_missing', {
        eventKeys: evt && typeof evt === 'object' ? Object.keys(evt) : [],
        payloadKeys: payload && typeof payload === 'object' && !Array.isArray(payload) ? Object.keys(payload) : [],
      });
      return;
    }

    const revisionKey = bridgeRevisionKey(eventName, payload);
    if (revisionKey) {
      set((state) => ({ [revisionKey]: state[revisionKey] + 1 }));
      return;
    }
    if (eventName === 'ui/sidebar/changed') {
      refreshActiveChatSidebarInBackground();
      return;
    }
    if (method === 'ui/thread/patch') {
      flushAssistantDeltasNow();
      applyBridgePatch(method, payload);
      return;
    }
    if (isAssistantMessageDeltaEvent(eventName, payload)) {
      enqueueAssistantDelta(method, payload);
      return;
    }
    if (eventName === 'item/reasoning/textdelta' || eventName === 'item/reasoning/text_delta') {
      enqueueReasoningDelta(method, payload);
      return;
    }
    if (eventName === 'item/commandexecution/outputdelta' || eventName === 'item/command_execution/output_delta') {
      enqueueCommandOutputDelta(method, payload);
      return;
    }
    if (eventName === 'item/completed') {
      applyAssistantCompletion(method, payload);
      return;
    }
    if (eventName === 'turn/terminal') {
      applyTurnTerminal(method, payload);
      return;
    }
    if (eventName === 'agent/failed' || eventName === 'turn/completed' || eventName === 'turn/interrupted' || eventName === 'agent/stopped' || eventName === 'thread/stopped') {
      addWarning('error', 'turn.terminal.contract_invalid', { eventName: method, reason: 'legacy_terminal_event' });
      notifyAction('响应契约错误', 'error', { category: 'turn_terminal_contract' });
      return;
    }
    if (eventName === 'thread/tokenusage/updated') {
      const threadId = bridgeThreadIdForPayload(payload);
      const usage = normalizeTokenUsage(payload);
      if (threadId && usage) {
        set((state) => tokenUsageByThreadPatch(state, threadId, usage));
      }
      return;
    }
    if (eventName === 'bridge.event.parse_failed') {
      addWarning('error', method, bridgeParseFailureWarningFields(payload));
      return;
    }
    if (eventName === 'rpc.failed' || eventName.endsWith('/failed') || eventName.endsWith('.failed')) {
      addWarning('error', method, payload);
    }
  };

  Object.assign(runtime, { handleBridgeEvent });
}

function bridgeParseFailureWarningFields(payload = {}) {
  const out = {};
  const eventName = normalizeString(payload.eventName || payload.event_name);
  if (eventName) out.eventName = eventName;
  const error = normalizeString(payload.error || payload.message);
  if (error) out.error = error;
  const rawLen = Number(payload.rawLen ?? payload.raw_len);
  if (Number.isFinite(rawLen) && rawLen >= 0) out.rawLen = rawLen;
  return out;
}


export {
  attachBridgeEventRuntime,
  attachBridgeIdentityRuntime,
  attachBridgePatchRuntime,
  bridgePatchFromPayload,
  bridgeParseFailureWarningFields,
  emitSlowBridgePatchTrace,
  tokenUsageByThreadPatch,
};
