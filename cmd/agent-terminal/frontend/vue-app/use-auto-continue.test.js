// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive, nextTick, ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(), logWarn: vi.fn(), logDebug: vi.fn(), logError: vi.fn(),
}));
vi.mock('./composables/useContextUsageThresholds.js', () => {
  const r = ref([70, 85, 95]);
  return {
    useContextUsageThresholds: () => r,
    loadContextUsageThresholds: vi.fn().mockResolvedValue([70, 85, 95]),
    saveContextUsageThresholds: vi.fn(),
    isValidThresholds: () => true,
    _resetContextUsageThresholdsForTest: () => { r.value = [70, 85, 95]; },
  };
});

const { logInfo } = await import('./services/log.js');
const { useAutoContinue } = await import('./composables/useAutoContinue.js');

function makeStore(initial = {}) {
  return {
    state: reactive({
      tokenUsageByThread: { ...(initial.tokenUsageByThread || {}) },
      statuses: { ...(initial.statuses || {}) },
      agentRuntimeById: { ...(initial.agentRuntimeById || {}) },
    }),
  };
}

let stopFn = () => {};
beforeEach(() => { vi.mocked(logInfo).mockReset(); });
afterEach(() => { stopFn(); vi.restoreAllMocks(); });

describe('useAutoContinue · token level signals', () => {
  it('emits token_critical when task thread crosses normal -> critical', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.tokenUsageByThread = { t1: { usedPercent: 96 } };
    await nextTick();

    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.signal', {
      source_thread_id: 't1', task_id: 'task-A', kind: 'token_critical', level: 'critical',
    });
    expect(logInfo).toHaveBeenCalledTimes(1);
  });

  it('does NOT emit when non-task thread crosses into critical', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: {} }, // no taskId
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();

    expect(logInfo).not.toHaveBeenCalled();
  });

  it('does NOT re-emit when already critical (critical -> critical)', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 96 } }, // primed as critical
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.tokenUsageByThread = { t1: { usedPercent: 98 } }; // still critical
    await nextTick();

    expect(logInfo).not.toHaveBeenCalled();
  });

  it('re-emits after dropping out of critical and crossing back', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 96 } },
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.tokenUsageByThread = { t1: { usedPercent: 75 } }; // warn
    await nextTick();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } }; // critical again
    await nextTick();

    expect(logInfo).toHaveBeenCalledTimes(1);
    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.signal', expect.objectContaining({
      source_thread_id: 't1', kind: 'token_critical',
    }));
  });

  it('does NOT emit on initial prime even if already critical', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 99 } },
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));
    await nextTick();

    expect(logInfo).not.toHaveBeenCalled();
  });
});

describe('useAutoContinue · status signals', () => {
  it('emits status_error when task thread goes idle -> error', async () => {
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.statuses = { t1: 'error' };
    await nextTick();

    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.signal', {
      source_thread_id: 't1', task_id: 'task-A', kind: 'status_error', status: 'error',
    });
    expect(logInfo).toHaveBeenCalledTimes(1);
  });

  it('does NOT re-emit when error -> error', async () => {
    const store = makeStore({
      statuses: { t1: 'error' }, // primed
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.statuses = { t1: 'error' };
    await nextTick();

    expect(logInfo).not.toHaveBeenCalled();
  });

  it('does NOT emit when non-task thread goes to error', async () => {
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: {} }, // no taskId
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.statuses = { t1: 'error' };
    await nextTick();

    expect(logInfo).not.toHaveBeenCalled();
  });

  it('emits again on transient: error -> idle -> error', async () => {
    const store = makeStore({
      statuses: { t1: 'error' },
      agentRuntimeById: { t1: { taskId: 'task-A' } },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.statuses = { t1: 'idle' };
    await nextTick();
    store.state.statuses = { t1: 'error' };
    await nextTick();

    expect(logInfo).toHaveBeenCalledTimes(1);
    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.signal', expect.objectContaining({
      source_thread_id: 't1', kind: 'status_error',
    }));
  });
});

describe('useAutoContinue · multi-thread', () => {
  it('emits per-thread independently', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 }, t2: { usedPercent: 50 } },
      agentRuntimeById: {
        t1: { taskId: 'task-A' },
        t2: { taskId: 'task-B' },
      },
    });
    ({ stop: stopFn } = useAutoContinue({ threadStore: store }));

    store.state.tokenUsageByThread = {
      t1: { usedPercent: 99 },
      t2: { usedPercent: 99 },
    };
    await nextTick();

    expect(logInfo).toHaveBeenCalledTimes(2);
    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.signal', expect.objectContaining({ source_thread_id: 't1', task_id: 'task-A' }));
    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.signal', expect.objectContaining({ source_thread_id: 't2', task_id: 'task-B' }));
  });
});
