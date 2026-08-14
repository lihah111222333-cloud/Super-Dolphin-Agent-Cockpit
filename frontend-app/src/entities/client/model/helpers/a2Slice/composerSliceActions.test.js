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
    createLocalTurnID: vi.fn(() => 'turn-local-pending'),
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

it('pre-generates the local turn identity and carries it into turn/start', async () => {
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Start with a stable identity', attachments: [] });
  const startTurn = vi.fn().mockResolvedValue({ turn_id: 'turn-local-pending' });
  const deps = sendDependencies(startTurn);
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  await expect(sendDraft()).resolves.toBe(true);

  expect(deps.createLocalTurnID).toHaveBeenCalledTimes(1);
  expect(startTurn).toHaveBeenCalledWith(expect.objectContaining({
    threadId: 'thread-1',
    localTurnId: 'turn-local-pending',
  }));
});

it('carries a deferred Stop request identity into turn/start after a new thread becomes canonical', async () => {
  const launch = deferred();
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Stop before provider start', attachments: [] });
  const startTurn = vi.fn().mockResolvedValue({ turn_id: 'turn-local-pending' });
  const deps = sendDependencies(startTurn);
  const baseRequest = deps.createSendDraftRequest();
  deps.createSendDraftRequest = () => ({
    ...baseRequest, previousThreadId: '', provisionalThreadId: 'thread-provisional', previousActiveThreadId: '',
  });
  deps.startNewDraftThread.mockReturnValue(launch.promise);
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  const sending = sendDraft();
  runtime.pendingTurnStart.cancelled = true;
  runtime.pendingTurnStart.interruptRequested = true;
  runtime.pendingTurnStart.interruptRequestId = 'stop-before-provider-start';
  launch.resolve({ threadId: 'thread-canonical', identity: {}, launchPreferences: {} });

  await expect(sending).resolves.toBe(true);
  expect(startTurn).toHaveBeenCalledWith(expect.objectContaining({
    threadId: 'thread-canonical',
    localTurnId: 'turn-local-pending',
    preparingCancelRequestId: 'stop-before-provider-start',
  }));
});

it('does not duplicate a Stop already registered during turn/start prepare', async () => {
  const pendingStart = deferred();
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Stop after dispatch', attachments: [] });
  const interruptActiveThread = vi.fn().mockResolvedValue(true);
  runtime.set({ interruptActiveThread });
  const startTurn = vi.fn().mockReturnValue(pendingStart.promise);
  const deps = sendDependencies(startTurn);
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  const sending = sendDraft();
  expect(startTurn).toHaveBeenCalledWith(expect.not.objectContaining({ preparingCancelRequestId: expect.any(String) }));
  runtime.pendingTurnStart.cancelled = true;
  runtime.pendingTurnStart.interruptRequested = true;
  runtime.pendingTurnStart.interruptRequestId = 'stop-after-dispatch';
  pendingStart.resolve({ turn_id: 'turn-local-pending' });

  await expect(sending).resolves.toBe(true);
  expect(interruptActiveThread).not.toHaveBeenCalled();
});

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

it('keeps a canonical provisional thread after retryable registered cancellation delivery failure', async () => {
  const launch = deferred();
  const pendingStart = deferred();
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Cancel this startup', attachments: [] });
  const interruptActiveThread = vi.fn().mockResolvedValue(true);
  runtime.set({ interruptActiveThread });
  const deps = sendDependencies(vi.fn().mockReturnValue(pendingStart.promise));
  const baseRequest = deps.createSendDraftRequest();
  deps.createSendDraftRequest = () => ({
    ...baseRequest, previousThreadId: '', provisionalThreadId: 'thread-provisional', previousActiveThreadId: '',
  });
  deps.startNewDraftThread.mockReturnValue(launch.promise);
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  const sending = sendDraft();
  runtime.pendingTurnStart.cancelled = true;
  runtime.pendingTurnStart.interruptRequested = true;
  runtime.pendingTurnStart.interruptRequestId = 'stop-delivery-retryable';
  launch.resolve({ threadId: 'thread-canonical', identity: {}, launchPreferences: {} });
  await vi.waitFor(() => expect(deps.startTurnWithStoppedThreadRecovery).toHaveBeenCalledWith(expect.objectContaining({
    preparingCancelRequestId: 'stop-delivery-retryable',
  })));
  pendingStart.resolve({
    turn_id: 'turn-local-pending', interrupt_retryable: true, interrupt_retryable_code: 'REGISTERED_INTERRUPT_DELIVERY_RETRYABLE',
  });

  await expect(sending).resolves.toBe(true);
  expect(interruptActiveThread).toHaveBeenCalledWith({
    activeTurnTarget: { threadId: 'thread-canonical', turnId: 'turn-local-pending' },
    requestId: 'stop-delivery-retryable',
  });
  expect(deps.deleteProvisionalThreadAfterSendFailure).not.toHaveBeenCalled();
  expect(runtime.get().activeTurnByThread['thread-canonical']).toEqual(expect.objectContaining({ id: 'turn-local-pending' }));
  expect(runtime.notifyAction).toHaveBeenCalledWith('停止请求未送达，任务仍在运行，可再次停止', 'warning', { threadId: 'thread-canonical' });
  expect(runtime.addWarning).toHaveBeenCalledWith('warn', 'thread.interrupt.delivery_retryable', expect.objectContaining({ threadId: 'thread-canonical' }));
});

it('keeps canonical turn when turn/start reports a durable provider-id bind diagnostic', async () => {
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Start despite persistence diagnostic', attachments: [] });
  const deps = sendDependencies(vi.fn().mockResolvedValue({
    turn_id: 'turn-durable-diagnostic', start_diagnostic_code: 'TURN_DEDUPE_PROVIDER_ID_BIND_FAILED',
  }));
  const baseRequest = deps.createSendDraftRequest();
  deps.createSendDraftRequest = () => ({
    ...baseRequest, previousThreadId: '', provisionalThreadId: 'thread-provisional', previousActiveThreadId: '',
  });
  deps.startNewDraftThread.mockResolvedValue({ threadId: 'thread-canonical', identity: {}, launchPreferences: {} });
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  await expect(sendDraft()).resolves.toBe(true);
  expect(deps.deleteProvisionalThreadAfterSendFailure).not.toHaveBeenCalled();
  expect(runtime.get().activeTurnByThread['thread-canonical']).toEqual(expect.objectContaining({ id: 'turn-durable-diagnostic' }));
  expect(runtime.notifyAction).toHaveBeenCalledWith('任务已启动，但启动去重状态未持久化', 'warning', { threadId: 'thread-canonical' });
  expect(runtime.addWarning).toHaveBeenCalledWith('warn', 'thread.start.dedupe_provider_id_bind_failed', {
    threadId: 'thread-canonical', code: 'TURN_DEDUPE_PROVIDER_ID_BIND_FAILED',
  });
});

it('retries cancellation after a durable bind diagnostic keeps canonical turn', async () => {
  const pendingStart = deferred();
  const runtime = testRuntime({ activeTurnByThread: {}, draft: 'Cancel durable diagnostic startup', attachments: [] });
  const interruptActiveThread = vi.fn().mockResolvedValue(true);
  runtime.set({ interruptActiveThread });
  const deps = sendDependencies(vi.fn().mockReturnValue(pendingStart.promise));
  const baseRequest = deps.createSendDraftRequest();
  deps.createSendDraftRequest = () => ({
    ...baseRequest, previousThreadId: '', provisionalThreadId: 'thread-provisional', previousActiveThreadId: '',
  });
  deps.startNewDraftThread.mockResolvedValue({ threadId: 'thread-canonical', identity: {}, launchPreferences: {} });
  const sendDraft = createComposerActionSet(runtime, {
    attachment: {}, capability: {}, model: {}, modelProvider: {}, send: deps,
  }).sendDraft;

  const sending = sendDraft();
  runtime.pendingTurnStart.cancelled = true;
  pendingStart.resolve({
    turn_id: 'turn-durable-cancelled', start_diagnostic_code: 'TURN_DEDUPE_PROVIDER_ID_BIND_FAILED',
  });

  await expect(sending).resolves.toBe(true);
  expect(interruptActiveThread).toHaveBeenCalledWith({
    activeTurnTarget: { threadId: 'thread-canonical', turnId: 'turn-durable-cancelled' },
  });
  expect(deps.deleteProvisionalThreadAfterSendFailure).not.toHaveBeenCalled();
  expect(runtime.get().activeTurnByThread['thread-canonical']).toEqual(expect.objectContaining({ id: 'turn-durable-cancelled' }));
});
