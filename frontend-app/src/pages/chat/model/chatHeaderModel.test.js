import { describe, expect, it } from 'vitest';
import { chatHeaderFeedbackForStore } from './chatHeaderModel.js';

describe('chatHeaderFeedbackForStore', () => {
  it('prefers explicit action notices', () => {
    expect(chatHeaderFeedbackForStore({
      actionNotice: { message: 'Saved', tone: 'success' },
      bootstrapStatus: 'ready',
      error: '',
    })).toEqual({ message: 'Saved', tone: 'success' });
  });

  it('projects canonical pending without requiring a resolved thread record', () => {
    expect(chatHeaderFeedbackForStore({
      activeThreadId: 'thread-1',
      bootstrapStatus: 'ready',
      threadRecoveryPendingByThread: { 'thread-1': true },
    })).toEqual({
      message: '正在恢复',
      tone: 'info',
      recoveryRequesting: true,
    });

    expect(chatHeaderFeedbackForStore({
      activeThreadId: 'thread-2',
      bootstrapStatus: 'ready',
      threadRecoveryPendingByThread: { 'thread-1': true },
    })).toBeNull();
  });

  it('projects canonical recovery pending when the active identity is an alias', () => {
    expect(chatHeaderFeedbackForStore({
      activeThreadId: 'agent-1',
      bootstrapStatus: 'ready',
      threads: [{ id: 'thread-1', agentId: 'agent-1' }],
      threadRecoveryPendingByThread: { 'thread-1': true },
    })).toEqual({
      message: '正在恢复',
      tone: 'info',
      recoveryRequesting: true,
    });
  });

  it('preserves accepted wording without claiming recovery completed', () => {
    const feedback = chatHeaderFeedbackForStore({
      activeThreadId: 'thread-1',
      bootstrapStatus: 'ready',
      actionNotice: { message: '恢复请求已接受，正在恢复', tone: 'success', threadId: 'thread-1' },
    });

    expect(feedback).toEqual(expect.objectContaining({ message: '恢复请求已接受，正在恢复', tone: 'success' }));
    expect(feedback.message).not.toContain('已恢复完成');
  });

  it('keeps bootstrap recovery ahead of stale action notices', () => {
    expect(chatHeaderFeedbackForStore({
      actionNotice: { message: 'Saved', tone: 'success' },
      bootstrapStatus: 'failed',
      error: 'network',
    })).toEqual({
      message: 'network',
      tone: 'error',
      bootstrapRecovery: true,
      retrying: false,
    });
  });

  it('reports backend connection failures', () => {
    expect(chatHeaderFeedbackForStore({ bootstrapStatus: 'failed', error: 'offline' })).toEqual({
      message: 'offline',
      tone: 'error',
      bootstrapRecovery: true,
      retrying: false,
    });
  });

  it('does not duplicate an already canonical backend failure label', () => {
    expect(chatHeaderFeedbackForStore({
      bootstrapStatus: 'failed',
      error: '连接后端失败，请重试。',
    })).toEqual({
      message: '连接后端失败，请重试。',
      tone: 'error',
      bootstrapRecovery: true,
      retrying: false,
    });
  });

  it('marks loading with an existing bootstrap error as retrying', () => {
    expect(chatHeaderFeedbackForStore({ bootstrapStatus: 'loading', error: 'offline' })).toEqual({
      message: 'offline',
      tone: 'error',
      bootstrapRecovery: true,
      retrying: true,
    });
  });

  it('returns null when there is no feedback', () => {
    expect(chatHeaderFeedbackForStore({ bootstrapStatus: 'ready' })).toBeNull();
  });

  it('uses the initialization failure fallback when bootstrap has no display message', () => {
    expect(chatHeaderFeedbackForStore({ bootstrapStatus: 'failed', error: '' })).toEqual({
      message: '应用初始化失败，请重试。',
      tone: 'error',
      bootstrapRecovery: true,
      retrying: false,
    });
  });
});
