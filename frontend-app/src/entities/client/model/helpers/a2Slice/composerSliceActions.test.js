import { expect, it, vi } from 'vitest';
import { actionNotice, composerActionDeps } from '../a1/clientStoreSendModel.js';
import { createComposerActionSet } from './composerSliceActions.js';

function deferred() {
  let resolve;
  const promise = new Promise((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
}

function testRuntime(initialState) {
  let state = initialState;
  return {
    activeThreadRPC: vi.fn().mockResolvedValue(true),
    addWarning: vi.fn(),
    clearComposerDraft: vi.fn(),
    get: () => state,
    notifyAction: vi.fn(),
    pendingTurnStart: null,
    requireCwd: () => '/repo/app',
    set: (update) => {
      const patch = typeof update === 'function' ? update(state) : update;
      state = { ...state, ...patch };
    },
  };
}

function sendDependencies(startTurn) {
  const request = {
    cwd: '/repo/app',
    input: [{ type: 'text', text: 'Cancel this startup' }],
    capabilityPayload: { manualSkillSelection: false },
    previousThreadId: 'thread-1',
    provisionalThreadId: '',
    previousActiveThreadId: 'thread-1',
    previousDraft: 'Cancel this startup',
    previousAttachments: [{ path: '/tmp/cancel.txt', name: 'cancel.txt' }],
    previousComposerCapabilities: [],
  };
  return {
    actionNotice: (message, tone) => ({ message, tone }),
    createSendDraftRequest: () => request,
    createdThreadIdForSendRollback: vi.fn(),
    deleteProvisionalThreadAfterSendFailure: vi.fn(),
    freshThreadRetryRequest: vi.fn(),
    isCodexIdentityAutoResumeError: () => false,
    optimisticSendDraftState: (state) => ({ ...state, sending: true, draft: '', attachments: [] }),
    promotedDraftThreadState: vi.fn(),
    recoveryActionMessageFromRPCError: vi.fn(),
    resolveLaunchPreferences: vi.fn(),
    rollbackSendDraftState: vi.fn(),
    saveFailedSendDraftSnapshot: vi.fn(),
    sendRollbackRestoresVisibleComposer: vi.fn(),
    startNewDraftThread: vi.fn(),
    startTurnWithStoppedThreadRecovery: startTurn,
  };
}

it('wires the cancellation notice factory through the production send dependencies', () => {
  expect(composerActionDeps.send.actionNotice).toBe(actionNotice);
});

it('interrupts a cancelled pending start with the canonical local turn and restores the composer', async () => {
  const pendingStart = deferred();
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Cancel this startup', attachments: [] });
  const interruptActiveThread = vi.fn().mockResolvedValue(true);
  runtime.set({ interruptActiveThread });
  const deps = sendDependencies(vi.fn().mockReturnValue(pendingStart.promise));
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  const sending = sendDraft();
  expect(runtime.pendingTurnStart).toEqual(expect.objectContaining({ cancelled: false, threadId: 'thread-1' }));
  runtime.pendingTurnStart.cancelled = true;
  pendingStart.resolve({ turn_id: 'turn-local-pending' });

  await expect(sending).resolves.toBe(true);
  expect(interruptActiveThread).toHaveBeenCalledWith({
    activeTurnTarget: { threadId: 'thread-1', turnId: 'turn-local-pending' },
  });
  expect(runtime.get()).toEqual(expect.objectContaining({
    sending: false,
    draft: 'Cancel this startup',
    attachments: [{ path: '/tmp/cancel.txt', name: 'cancel.txt' }],
    actionNotice: { message: '本轮已取消', tone: 'info' },
    activeTurnByThread: {
      'thread-1': expect.objectContaining({ id: 'turn-local-pending', threadId: 'thread-1' }),
    },
  }));
  expect(runtime.pendingTurnStart).toBeNull();
});

it('does not roll back or delete a thread whose start succeeded when cancellation is unconfirmed', async () => {
  const pendingStart = deferred();
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Cancel this startup', attachments: [] });
  const interruptError = new Error('interrupt confirmation unavailable');
  runtime.set({ interruptActiveThread: vi.fn().mockRejectedValue(interruptError) });
  const deps = sendDependencies(vi.fn().mockReturnValue(pendingStart.promise));
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  const sending = sendDraft();
  runtime.pendingTurnStart.cancelled = true;
  pendingStart.resolve({ turn_id: 'turn-local-pending' });

  await expect(sending).rejects.toThrow('interrupt confirmation unavailable');
  expect(deps.rollbackSendDraftState).not.toHaveBeenCalled();
  expect(deps.deleteProvisionalThreadAfterSendFailure).not.toHaveBeenCalled();
  expect(deps.saveFailedSendDraftSnapshot).not.toHaveBeenCalled();
  expect(runtime.get().sending).toBe(false);
  expect(runtime.pendingTurnStart).toBeNull();
});
