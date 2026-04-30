// @ts-nocheck
// Phase 3.10b 单测：useThreadProgressProtocol
// 覆盖 readProgressLineCount / readDoneMarker · NotFound 静默 · 其它错误 logWarn · taskId 空 fallback。

import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock("./services/log.js", () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));
vi.mock("./services/api.js", () => ({ callAPI: vi.fn() }));

import { useThreadProgressProtocol } from './composables/useThreadProgressProtocol.js';
import { logWarn } from './services/log.js';

function makeCallAPI(routes = {}) {
  return vi.fn(async (method, params) => {
    if (typeof routes[method] === 'function') return routes[method](params);
    return null;
  });
}

beforeEach(() => { vi.clearAllMocks(); });

describe('useThreadProgressProtocol · readProgressLineCount', () => {
  it('返回非空行数（忽略空白行）', async () => {
    const callAPI = makeCallAPI({
      'ui/memory/shared-file/get': ({ path }) => {
        expect(path).toBe('_internal/progress/task_abc.md');
        return { path, content: '2026-04-30 step 1\n2026-04-30 step 2\n\n   \n2026-04-30 step 3\n' };
      },
    });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readProgressLineCount('task_abc')).toBe(3);
    expect(callAPI).toHaveBeenCalledTimes(1);
  });

  it('NotFound 静默返回 0（agent 还没写过）', async () => {
    const callAPI = vi.fn(async () => { throw new Error('shared file not found'); });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readProgressLineCount('task_abc')).toBe(0);
    expect(logWarn).not.toHaveBeenCalled();
  });

  it('其它错误返回 0 + logWarn（不破降级）', async () => {
    const callAPI = vi.fn(async () => { throw new Error('rpc transport broken'); });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readProgressLineCount('task_abc')).toBe(0);
    expect(logWarn).toHaveBeenCalledWith(
      'ui', 'thread_progress_protocol.progress_read_failed',
      expect.objectContaining({ task_id: 'task_abc' }),
    );
  });

  it('taskId 空白不调 RPC 直接返回 0', async () => {
    const callAPI = vi.fn();
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readProgressLineCount('   ')).toBe(0);
    expect(await protocol.readProgressLineCount(null)).toBe(0);
    expect(callAPI).not.toHaveBeenCalled();
  });
});

describe('useThreadProgressProtocol · readDoneMarker', () => {
  it('文件存在且内容非空 → true', async () => {
    const callAPI = makeCallAPI({
      'ui/memory/shared-file/get': ({ path }) => {
        expect(path).toBe('_internal/done/task_abc.md');
        return { path, content: 'done at 2026-04-30' };
      },
    });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readDoneMarker('task_abc')).toBe(true);
  });

  it('NotFound → false（静默）', async () => {
    const callAPI = vi.fn(async () => { throw new Error('not found'); });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readDoneMarker('task_abc')).toBe(false);
    expect(logWarn).not.toHaveBeenCalled();
  });

  it('内容仅空白 → false（防 agent 误写空文件触发误终止）', async () => {
    const callAPI = makeCallAPI({
      'ui/memory/shared-file/get': () => ({ content: '   \n   ' }),
    });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readDoneMarker('task_abc')).toBe(false);
  });

  it('其它错误 → false + logWarn', async () => {
    const callAPI = vi.fn(async () => { throw new Error('boom'); });
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readDoneMarker('task_abc')).toBe(false);
    expect(logWarn).toHaveBeenCalledWith(
      'ui', 'thread_progress_protocol.done_read_failed',
      expect.objectContaining({ task_id: 'task_abc' }),
    );
  });

  it('taskId 空白不调 RPC 返回 false', async () => {
    const callAPI = vi.fn();
    const protocol = useThreadProgressProtocol({ callAPIFn: callAPI });
    expect(await protocol.readDoneMarker('')).toBe(false);
    expect(callAPI).not.toHaveBeenCalled();
  });
});
