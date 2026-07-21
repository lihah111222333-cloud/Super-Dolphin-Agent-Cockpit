import { describe, expect, it } from 'vitest';
import {
  createSendDraftRequest,
  freshThreadRetryRequest,
  optimisticSendDraftState,
  promotedDraftThreadState,
  rollbackSendDraftState,
} from './helpers/a1/clientStoreSendModel.js';

function emptySendState() {
  return {
    activeThreadId: '',
    activityThreadAtById: {},
    attachments: [],
    composerCapabilities: [],
    draft: '请处理附件',
    sidebarThreadsByProject: {},
    threadTimelineReadyByThread: {},
    threads: [],
    timelinesByThread: {},
  };
}

describe('clientStoreSendModel', () => {
  it('keeps one provisional identity from optimistic insert through real thread promotion', () => {
    const state = emptySendState();
    const request = createSendDraftRequest(state, '/workspace');
    const optimistic = optimisticSendDraftState(state, request);
    const promoted = promotedDraftThreadState(optimistic, request, {
      identity: { agentId: 'agent-1', providerThreadId: 'provider-1', sessionId: 'session-1' },
      launchPreferences: { provider: 'codex' },
      threadId: 'thread-1',
    });

    expect(request.provisionalThreadId).toBe(request.launchIntentId);
    expect(optimistic.timelinesByThread[request.provisionalThreadId]).toHaveLength(1);
    expect(promoted.activeThreadId).toBe('thread-1');
    expect(promoted.timelinesByThread[request.provisionalThreadId]).toBeUndefined();
    expect(promoted.timelinesByThread['thread-1']).toHaveLength(1);
  });

  it('uses a fresh launch identity for retry and removes the failed provisional state', () => {
    const state = emptySendState();
    const request = createSendDraftRequest(state, '/workspace');
    const retry = freshThreadRetryRequest(request);
    const optimistic = optimisticSendDraftState(state, retry);
    const rollback = rollbackSendDraftState(optimistic, retry, new Error('failed'));

    expect(retry.launchIntentId).not.toBe(request.launchIntentId);
    expect(retry.provisionalThreadId).toBe(retry.launchIntentId);
    expect(rollback.timelinesByThread[retry.provisionalThreadId]).toBeUndefined();
    expect(rollback.draft).toBe(request.previousDraft);
  });
});
