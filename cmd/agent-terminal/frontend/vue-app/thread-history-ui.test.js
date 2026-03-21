// @ts-nocheck
import { describe, it, expect, vi } from 'vitest';
import { applyImmediateTimelineFromMessages } from './stores/thread-history-ui.js';

describe('thread-history-ui immediate hydration', () => {
  it('preserves internal worker report metadata from thread/messages history', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'main-agent',
      response: {
        messages: [{
          id: 1,
          role: 'user',
          content: '任务已完成',
          createdAt: '2026-03-10T12:00:00Z',
          metadata: {
            internal: true,
            sourceKind: 'agent',
            fromThreadId: 'worker-agent',
            toThreadId: 'main-agent',
            fromDisplay: '代码修复代理',
            toDisplay: '主控代理',
          },
        }],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['main-agent']).toEqual([{
      id: 'main-agent-history-1',
      kind: 'user',
      text: '任务已完成',
      ts: '2026-03-10T12:00:00Z',
      internal: true,
      sourceKind: 'agent',
      fromThreadId: 'worker-agent',
      toThreadId: 'main-agent',
      fromDisplay: '代码修复代理',
      toDisplay: '主控代理',
    }]);
  });

  it('normalizes thread/messages pages to chronological timeline order', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-1',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: '较新输出', createdAt: '2026-03-10T12:00:01Z' },
          { id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-1'].map((item) => item.kind)).toEqual(['user', 'assistant']);
    expect(state.timelinesByThread['thread-1'][0]).toEqual(expect.objectContaining({
      id: 'thread-1-history-1',
      text: '旧请求',
      ts: '2026-03-10T12:00:00Z',
    }));
    expect(state.timelinesByThread['thread-1'][1]).toEqual(expect.objectContaining({
      id: 'thread-1-history-2',
      text: '较新输出',
      ts: '2026-03-10T12:00:01Z',
    }));
  });

  it('skips immediate history apply when existing dialog timeline is newer and longer', () => {
    const existingTimeline = [{ id: 'thread-1-user', kind: 'user', text: '旧请求', ts: '2026-03-10T12:00:00Z' }, { id: 'thread-1-assistant', kind: 'assistant', text: '最新输出', ts: '2026-03-10T12:00:01Z' }, { id: 'thread-1-thinking', kind: 'thinking', text: '', ts: '2026-03-10T12:00:02Z' }];
    const state = { timelinesByThread: { 'thread-1': existingTimeline } };
    const logInfo = vi.fn();
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-1',
      response: { messages: [{ id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T12:00:00Z' }, { id: 2, role: 'assistant', content: '较旧输出', createdAt: '2026-03-10T12:00:01Z' }] },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo,
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-1']).toBe(existingTimeline);
    expect(logInfo).toHaveBeenCalledWith('thread', 'messages.load.local_timeline.skipped_stale', {
      thread_id: 'thread-1',
      existing_count: 3,
      incoming_count: 2,
      existing_dialog_count: 2,
      incoming_dialog_count: 2,
      existing_latest_dialog_ts: Date.parse('2026-03-10T12:00:01Z'),
      incoming_latest_dialog_ts: Date.parse('2026-03-10T12:00:01Z'),
    });
  });

  it('skips same-length history pages that would overwrite fresher waiting-state dialog', () => {
    const existingTimeline = [
      { id: 'thread-2-user', kind: 'user', text: '最新提示词', ts: '2026-03-10T12:00:00Z' },
      { id: 'thread-2-assistant', kind: 'assistant', text: '最新输出', ts: '2026-03-10T12:00:02Z' },
    ];
    const state = { timelinesByThread: { 'thread-2': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-2',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: '较旧输出', createdAt: '2026-03-10T12:00:01Z' },
          { id: 1, role: 'user', content: '最新提示词', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: () => ({ changed: true, items: [] }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-2']).toBe(existingTimeline);
  });
  it('applies incoming history page when existing live-patch entries have no ts but incoming has valid ts', () => {
    // live-patch 写入的条目没有 ts（或 ts=''），不应阻止拥有完整 ts 的 history page
    const existingTimeline = [
      { id: 'live-1', kind: 'user', text: '最新提示词', ts: '' },
      { id: 'live-2', kind: 'assistant', text: '等待中...', ts: '' },
    ];
    const state = { timelinesByThread: { 'thread-3': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-3',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: '等待中...', createdAt: '2026-03-10T12:00:01Z' },
          { id: 1, role: 'user', content: '最新提示词', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    // 应该接受 incoming（有完整 ts），不能因 length 相等就跳过
    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-3'][0].ts).toBe('2026-03-10T12:00:00Z');
    expect(state.timelinesByThread['thread-3'][1].ts).toBe('2026-03-10T12:00:01Z');
  });

  it('skips incoming history page when existing dialog has valid ts but incoming lacks ts', () => {
    const existingTimeline = [{ id: 'ts-1', kind: 'user', text: 'A', ts: '2026-03-10T12:00:00Z' }, { id: 'ts-2', kind: 'assistant', text: 'B', ts: '2026-03-10T12:00:01Z' }];
    const state = { timelinesByThread: { 'thread-5': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-5',
      response: { messages: [{ id: 2, role: 'assistant', content: 'B', createdAt: '' }, { id: 1, role: 'user', content: 'A', createdAt: '' }] },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: () => ({ changed: true, items: [] }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-5']).toBe(existingTimeline);
  });

  it('still skips incoming history page when both existing and incoming have no valid ts and lengths are equal', () => {
    // 两边都没有 ts → fallback 比长度，相等则跳过（保留本地）
    const existingTimeline = [
      { id: 'live-1', kind: 'user', text: 'A', ts: '' },
      { id: 'live-2', kind: 'assistant', text: 'B', ts: '' },
    ];
    const state = { timelinesByThread: { 'thread-4': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-4',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: 'B', createdAt: '' },
          { id: 1, role: 'user', content: 'A', createdAt: '' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: () => ({ changed: true, items: [] }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-4']).toBe(existingTimeline);
  });

  // ─── 回归: 防止 history hydration 回退到替换已充足的 runtime timeline ───

  it('[regression] still applies history when only history items exist (no runtime data)', () => {
    const historyTimeline = [
      { id: 'thread-h-history-1', kind: 'user', text: '旧请求', ts: '2026-03-10T11:00:00Z' },
    ];
    const state = { timelinesByThread: { 'thread-h': historyTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-h',
      response: {
        messages: [
          { id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T11:00:00Z' },
          { id: 2, role: 'assistant', content: '新回复', createdAt: '2026-03-10T12:00:01Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-h']).toHaveLength(2);
  });

  it('[regression] applies history on empty timeline (first load)', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-new',
      response: {
        messages: [
          { id: 1, role: 'user', content: 'Hello', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-new']).toHaveLength(1);
  });
});
