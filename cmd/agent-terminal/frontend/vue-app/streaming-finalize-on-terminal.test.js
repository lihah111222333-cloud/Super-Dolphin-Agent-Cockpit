// @ts-nocheck
// 回归测试：所有 turn 终结信号都把 done:false 的 assistant streaming bubble
// 标记 streamingFinalized:true，让 ChatTimeline 切到真 markdown 分支，
// 不再卡在 <pre> 占位。
//
// 覆盖 b096194 引入但缺失的注入路径测试，并在 baseline turn/completed 之外
// 新增 4 个终结信号：turn/interrupted、agent/stopped、thread/stopped、agent/failed。
// 任一信号丢失时，<pre> 流式占位会卡到下一个 turn —— 这是用户已观察到的回归现象。

import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn(),
}));

import { useThreadStore } from './stores/threads.js';

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},
    pinnedThreadAtById: {},
    archivedThreadAtById: {},
    threads: [],
    statuses: {},
    interruptibleByThread: {},
    viewPrefsChat: null,
    viewPrefsCmd: null,
    statusHeadersByThread: {},
    statusDetailsByThread: {},
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: {},
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
  });
}

function seedActiveStreamingBubble(store, threadId, text = 'partial markdown') {
  store.state.activeThreadId = threadId;
  store.state.threads = [{ id: threadId, name: threadId, state: 'responding' }];
  store.state.timelinesByThread = {
    [threadId]: [
      { id: `${threadId}-user-1`, kind: 'user', text: 'hi', ts: '2026-05-15T00:00:00Z' },
      { id: `${threadId}-stream-1`, kind: 'assistant', text, done: false, ts: '2026-05-15T00:00:01Z' },
    ],
  };
}

function assistantBubble(store, threadId) {
  const tl = store.state.timelinesByThread[threadId] || [];
  return tl.find((it) => it?.kind === 'assistant');
}

describe('streaming bubble finalize on terminal signals', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async () => ({}));
    const store = useThreadStore();
    resetThreadStore(store);
  });

  // ─── Baseline：b096194 引入但漏测的 turn/completed 注入路径 ───

  it('turn/completed marks the active streaming bubble streamingFinalized:true', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    expect(assistantBubble(store, threadId).streamingFinalized).toBe(true);
  });

  // ─── 新扩 4 个终结信号 ───

  it('turn/interrupted marks the active streaming bubble streamingFinalized:true', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    store.handleBridgeEvent({ method: 'turn/interrupted', payload: { threadId, reason: 'user' } });
    expect(assistantBubble(store, threadId).streamingFinalized).toBe(true);
  });

  it('agent/stopped marks the active streaming bubble streamingFinalized:true', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    store.handleBridgeEvent({ method: 'agent/stopped', payload: { threadId, agentId: 'agent-A' } });
    expect(assistantBubble(store, threadId).streamingFinalized).toBe(true);
  });

  it('thread/stopped marks the active streaming bubble streamingFinalized:true', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    store.handleBridgeEvent({ method: 'thread/stopped', payload: { threadId, status: 'stopped' } });
    expect(assistantBubble(store, threadId).streamingFinalized).toBe(true);
  });

  it('agent/failed marks the active streaming bubble streamingFinalized:true', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    store.handleBridgeEvent({ method: 'agent/failed', payload: { threadId, agentId: 'agent-A', error: 'crash' } });
    expect(assistantBubble(store, threadId).streamingFinalized).toBe(true);
  });

  // ─── 反例：跨 thread 不污染 ───

  it('terminal signal for thread B does not finalize bubble in active thread A', () => {
    const store = useThreadStore();
    const threadA = 'thread-A';
    const threadB = 'thread-B';
    seedActiveStreamingBubble(store, threadA);
    // 事件 payload 指向 B，A 是 active
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId: threadB } });
    expect(assistantBubble(store, threadA).streamingFinalized).toBeUndefined();
  });

  // ─── 反例：已 finalized 的 bubble 不重复 mutation（保持引用稳定）───

  it('already-finalized bubble is not re-mutated (timelinesByThread reference stable)', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    // 第一次终结信号 → mutate 一次，引用替换
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    const refAfterFirst = store.state.timelinesByThread;
    // 再发一次终结信号 → bubble 已 finalized，mutated=false，引用不变
    store.handleBridgeEvent({ method: 'turn/interrupted', payload: { threadId } });
    expect(store.state.timelinesByThread).toBe(refAfterFirst);
  });

  // ─── 反例：非终结 method 不注入 ───

  it('turn/output/delta is NOT a terminal signal and does not finalize the bubble', () => {
    const store = useThreadStore();
    const threadId = 'thread-A';
    seedActiveStreamingBubble(store, threadId);
    store.handleBridgeEvent({ method: 'turn/output/delta', payload: { threadId, delta: ' more' } });
    expect(assistantBubble(store, threadId).streamingFinalized).toBeUndefined();
  });
});
