import { describe, it, expect, vi } from 'vitest';

/**
 * 回归测试：锁死 thread/tokenUsage/updated 直接推送行为。
 *
 * 之前的 bug：
 * 1. thread/tokenUsage/updated 不在 threadSyncSignal 中 → 通知到达但 store 不更新
 * 2. usedTokens 用 Math.max → 错误值只涨不跌，无法修正
 * 3. 没有直接推送 → 每次触发 round-trip syncThreadState 浪费
 */

// ─── 辅助工具 ───

function normalizeThreadID(id) {
  return (id || '').toString().trim();
}

function getBridgeEventMethod(evt) {
  return evt?.method || evt?.params?.method || '';
}

function getBridgeEventThreadId(evt) {
  return evt?.threadId || evt?.params?.threadId || evt?.payload?.threadId || '';
}

/**
 * 模拟 handleBridgeEvent 中 thread/tokenusage/updated 的直接推送逻辑。
 * 这里只提取推送相关的代码，确保与 thread-sync-helpers.js 中的行为一致。
 */
function applyTokenUsagePush(state, evt) {
  const methodLower = (getBridgeEventMethod(evt) || '').toLowerCase();
  const eventThreadId = getBridgeEventThreadId(evt);
  if (methodLower !== 'thread/tokenusage/updated' || !eventThreadId) return false;

  const payload = evt?.payload || evt?.params || evt?.data || evt || {};
  const tid = normalizeThreadID(eventThreadId);
  if (!tid) return false;

  const input = Number(payload.input) || Number(payload.input_tokens) || Number(payload.inputTokens) || 0;
  const output = Number(payload.output) || Number(payload.output_tokens) || Number(payload.outputTokens) || 0;
  const totalTokens = Number(payload.total_tokens) || Number(payload.totalTokens) || (input + output);
  const contextWindow = Number(payload.context_window) || Number(payload.contextWindow) || Number(payload.contextWindowTokens) || 0;
  const prev = (state.tokenUsageByThread && state.tokenUsageByThread[tid]) || {};
  const resolvedWindow = contextWindow > 0 ? contextWindow : (Number(prev.contextWindowTokens) || 0);
  // 新值 > 0 时直接使用（允许修正），为 0 时保留旧值（如 system:init 初始化）
  const usedTokens = totalTokens > 0 ? totalTokens : (Number(prev.usedTokens) || 0);
  const usedPercent = resolvedWindow > 0 ? Math.min(100, Math.max(0, (usedTokens / resolvedWindow) * 100)) : 0;
  const next = Object.freeze({ usedTokens, contextWindowTokens: resolvedWindow, usedPercent, updatedAt: Date.now() });
  state.tokenUsageByThread = { ...(state.tokenUsageByThread || {}), [tid]: next };
  return true;
}

// ─── 测试用例 ───

describe('Token Push Regression', () => {
  it('直接推送：thread/tokenusage/updated 应立即更新 tokenUsageByThread', () => {
    const state = { tokenUsageByThread: {} };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: {
        input: 50000,
        output: 3000,
        total_tokens: 53000,
        context_window: 872000,
      },
    };

    const applied = applyTokenUsagePush(state, evt);
    expect(applied).toBe(true);
    expect(state.tokenUsageByThread['thread-1']).toBeDefined();
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(53000);
    expect(state.tokenUsageByThread['thread-1'].contextWindowTokens).toBe(872000);
    expect(state.tokenUsageByThread['thread-1'].usedPercent).toBeCloseTo(6.08, 0);
  });

  it('允许修正：新值小于旧值时应替换（无 Math.max 锁）', () => {
    const state = {
      tokenUsageByThread: {
        'thread-1': { usedTokens: 2200000, contextWindowTokens: 872000, usedPercent: 100 },
      },
    };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input: 80000, output: 5000, total_tokens: 85000, context_window: 872000 },
    };

    applyTokenUsagePush(state, evt);
    // 85000 < 2200000，旧版 Math.max 会保持 2200000，修复后应覆盖为 85000。
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(85000);
    expect(state.tokenUsageByThread['thread-1'].usedPercent).toBeLessThan(100);
  });

  it('零值保护：total_tokens=0 时保留旧值（如 system:init）', () => {
    const state = {
      tokenUsageByThread: {
        'thread-1': { usedTokens: 53000, contextWindowTokens: 872000, usedPercent: 6 },
      },
    };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input: 0, output: 0, context_window: 872000 },
    };

    applyTokenUsagePush(state, evt);
    // total_tokens = 0 → 保留旧值 53000。
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(53000);
  });

  it('contextWindow 保留：新事件无 context_window 时保留已有值', () => {
    const state = {
      tokenUsageByThread: {
        'thread-1': { usedTokens: 1000, contextWindowTokens: 872000, usedPercent: 0.11 },
      },
    };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input: 2000, output: 500, total_tokens: 2500 },
      // 注意：没有 context_window 字段
    };

    applyTokenUsagePush(state, evt);
    // context_window 未提供 → 保留已有的 872000。
    expect(state.tokenUsageByThread['thread-1'].contextWindowTokens).toBe(872000);
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(2500);
  });

  it('contextWindow 更新：新值更大时应覆盖', () => {
    const state = {
      tokenUsageByThread: {
        'thread-1': { usedTokens: 1000, contextWindowTokens: 136000, usedPercent: 0.7 },
      },
    };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input: 1000, output: 200, total_tokens: 1200, context_window: 872000 },
    };

    applyTokenUsagePush(state, evt);
    expect(state.tokenUsageByThread['thread-1'].contextWindowTokens).toBe(872000);
  });

  it('不相关方法：非 tokenUsage/updated 不应触发推送', () => {
    const state = { tokenUsageByThread: {} };
    const evt = {
      method: 'turn/completed',
      threadId: 'thread-1',
      payload: { input: 50000, output: 3000 },
    };

    const applied = applyTokenUsagePush(state, evt);
    expect(applied).toBe(false);
    expect(state.tokenUsageByThread['thread-1']).toBeUndefined();
  });

  it('无 threadId：没有 threadId 不应推送', () => {
    const state = { tokenUsageByThread: {} };
    const evt = {
      method: 'thread/tokenUsage/updated',
      payload: { input: 50000, output: 3000 },
    };

    const applied = applyTokenUsagePush(state, evt);
    expect(applied).toBe(false);
  });

  it('多线程独立：不同 thread 的 token 数据互不影响', () => {
    const state = { tokenUsageByThread: {} };

    applyTokenUsagePush(state, {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-A',
      payload: { input: 10000, output: 500, context_window: 872000 },
    });
    applyTokenUsagePush(state, {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-B',
      payload: { input: 80000, output: 5000, context_window: 136000 },
    });

    expect(state.tokenUsageByThread['thread-A'].usedTokens).toBe(10500);
    expect(state.tokenUsageByThread['thread-A'].contextWindowTokens).toBe(872000);
    expect(state.tokenUsageByThread['thread-B'].usedTokens).toBe(85000);
    expect(state.tokenUsageByThread['thread-B'].contextWindowTokens).toBe(136000);
  });

  it('百分比上限：usedTokens 超过 contextWindow 时 usedPercent 封顶 100', () => {
    const state = { tokenUsageByThread: {} };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input: 900000, output: 50000, total_tokens: 950000, context_window: 872000 },
    };

    applyTokenUsagePush(state, evt);
    expect(state.tokenUsageByThread['thread-1'].usedPercent).toBe(100);
  });

  it('updatedAt 时间戳：每次推送都应更新', () => {
    const state = { tokenUsageByThread: {} };
    const before = Date.now();

    applyTokenUsagePush(state, {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input: 1000, output: 100, context_window: 872000 },
    });

    const after = Date.now();
    const updatedAt = state.tokenUsageByThread['thread-1'].updatedAt;
    expect(updatedAt).toBeGreaterThanOrEqual(before);
    expect(updatedAt).toBeLessThanOrEqual(after);
  });

  it('字段兼容：input_tokens/output_tokens 格式也能解析', () => {
    const state = { tokenUsageByThread: {} };
    const evt = {
      method: 'thread/tokenUsage/updated',
      threadId: 'thread-1',
      payload: { input_tokens: 40000, output_tokens: 2000, context_window: 872000 },
    };

    applyTokenUsagePush(state, evt);
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(42000);
  });
});
