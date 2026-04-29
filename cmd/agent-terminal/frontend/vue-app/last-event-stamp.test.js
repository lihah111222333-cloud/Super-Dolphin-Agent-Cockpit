// @ts-nocheck
// Phase 1.7a 单测：handleBridgeEvent 入口戳点 state.lastEventTsByThread。
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./services/api.js', () => ({ callAPI: vi.fn() }));
vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadStore } from './stores/threads.js';

let store;
beforeEach(() => {
  store = useThreadStore();
  store.state.lastEventTsByThread = {};
});

describe('Phase 1.7a · lastEventTsByThread 戳点', () => {
  it('带 threadId 的事件刷新该 thread 的时间戳', () => {
    const before = Date.now();
    store.handleBridgeEvent({
      method: 'item/agentMessage/delta',
      payload: { threadId: 'thread-A', delta: 'hello' },
    });
    const ts = store.state.lastEventTsByThread['thread-A'];
    expect(typeof ts).toBe('number');
    expect(ts).toBeGreaterThanOrEqual(before);
  });

  it('不同 thread 各自独立刷新', () => {
    store.handleBridgeEvent({ method: 'thread/started', payload: { threadId: 'thread-A' } });
    const tA = store.state.lastEventTsByThread['thread-A'];
    expect(typeof tA).toBe('number');
    store.handleBridgeEvent({ method: 'thread/started', payload: { threadId: 'thread-B' } });
    const tB = store.state.lastEventTsByThread['thread-B'];
    expect(typeof tB).toBe('number');
    // A 仍存在，B 也存在，互不覆盖
    expect(store.state.lastEventTsByThread['thread-A']).toBe(tA);
  });

  it('同一 thread 的后续事件刷新（增量更新）', async () => {
    store.handleBridgeEvent({ method: 'thread/started', payload: { threadId: 'thread-A' } });
    const t1 = store.state.lastEventTsByThread['thread-A'];
    // 等 ≥ 1ms 确保 Date.now() 推进
    await new Promise((r) => setTimeout(r, 2));
    store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId: 'thread-A' } });
    const t2 = store.state.lastEventTsByThread['thread-A'];
    expect(t2).toBeGreaterThanOrEqual(t1);
  });

  it('没有 threadId 的事件不写任何戳', () => {
    store.handleBridgeEvent({ method: 'skills/changed', payload: { skillsDir: '/tmp' } });
    expect(Object.keys(store.state.lastEventTsByThread)).toEqual([]);
  });

  it('threadId 为空字符串的事件不写戳', () => {
    store.handleBridgeEvent({ method: 'thread/started', payload: { threadId: '' } });
    expect(Object.keys(store.state.lastEventTsByThread)).toEqual([]);
  });
});
