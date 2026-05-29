import { vi, describe, it, expect, beforeEach, afterEach } from 'vitest';
import { useLogStore } from './useLogStore';

const mockBackend = vi.hoisted(() => ({
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('../../../shared/api/backendApi', () => ({
  registerBridgeLogStore: (...args) => mockBackend.registerBridgeLogStore(...args),
  sendFrontendLogBatch: (...args) => mockBackend.sendFrontendLogBatch(...args),
}));

describe('useLogStore', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    useLogStore.getState().destroy();
    useLogStore.setState({
      entries: [],
      bridgeQueue: [],
      filters: {
        level: '',
        method: '',
        threadId: '',
        operationId: '',
      },
    });
  });

  afterEach(() => {
    useLogStore.getState().destroy();
    vi.useRealTimers();
  });

  describe('logging levels and limits', () => {
    it('should add entries for all levels and queue non-info entries for bridge', () => {
      const store = useLogStore.getState();

      store.info('evt.info', { a: 1 });
      store.debug('evt.debug', { b: 2 });
      store.warn('evt.warn', { c: 3 });
      store.error('evt.error', { d: 4 });

      const state = useLogStore.getState();
      expect(state.entries.length).toBe(4);
      expect(state.entries[0].level).toBe('info');
      expect(state.entries[1].level).toBe('debug');
      expect(state.entries[2].level).toBe('warn');
      expect(state.entries[3].level).toBe('error');

      // Only debug, warn, error should be in bridgeQueue
      expect(state.bridgeQueue.length).toBe(3);
      expect(state.bridgeQueue[0].event).toBe('evt.debug');
      expect(state.bridgeQueue[1].event).toBe('evt.warn');
      expect(state.bridgeQueue[2].event).toBe('evt.error');
    });

    it('should cap entries at MAX_LOG_ENTRIES', () => {
      const store = useLogStore.getState();

      // MAX_LOG_ENTRIES is 600, log 605 times
      for (let i = 0; i < 605; i++) {
        store.info(`event-${i}`);
      }

      const state = useLogStore.getState();
      expect(state.entries.length).toBe(600);
      expect(state.entries[0].event).toBe('event-5');
      expect(state.entries[599].event).toBe('event-604');
    });

    it('should cap bridgeQueue at MAX_BRIDGE_QUEUE', () => {
      const store = useLogStore.getState();

      // MAX_BRIDGE_QUEUE is 240, log debug 250 times
      for (let i = 0; i < 250; i++) {
        store.debug(`debug-${i}`);
      }

      const state = useLogStore.getState();
      expect(state.bridgeQueue.length).toBe(240);
      expect(state.bridgeQueue[0].event).toBe('debug-10');
      expect(state.bridgeQueue[239].event).toBe('debug-249');
    });
  });

  describe('filters', () => {
    it('should set and clear filters', () => {
      const store = useLogStore.getState();

      store.setFilter({ level: 'error', threadId: 't-1' });
      expect(useLogStore.getState().filters).toEqual({
        level: 'error',
        method: '',
        threadId: 't-1',
        operationId: '',
      });

      store.clearFilters();
      expect(useLogStore.getState().filters).toEqual({
        level: '',
        method: '',
        threadId: '',
        operationId: '',
      });
    });
  });

  describe('batch queue flushing', () => {
    it('should batch flush logs to backend via timer', async () => {
      mockBackend.sendFrontendLogBatch.mockResolvedValue({});
      const store = useLogStore.getState();

      // Log 30 debug events (BATCH_SIZE is 24)
      for (let i = 0; i < 30; i++) {
        store.debug(`debug-${i}`);
      }

      expect(mockBackend.sendFrontendLogBatch).not.toHaveBeenCalled();

      // Advance timers by 1000ms to trigger first batch flush
      await vi.advanceTimersByTimeAsync(1000);

      expect(mockBackend.sendFrontendLogBatch).toHaveBeenCalledTimes(1);
      // The first call should contain the first 24 logs
      const firstBatch = mockBackend.sendFrontendLogBatch.mock.calls[0][0];
      expect(firstBatch.length).toBe(24);
      expect(firstBatch[0].event).toBe('debug-0');
      expect(firstBatch[23].event).toBe('debug-23');

      // The remaining 6 logs should still be in the queue
      let state = useLogStore.getState();
      expect(state.bridgeQueue.length).toBe(6);

      // Advance timers by 1000ms again to trigger second batch flush
      await vi.advanceTimersByTimeAsync(1000);

      expect(mockBackend.sendFrontendLogBatch).toHaveBeenCalledTimes(2);
      const secondBatch = mockBackend.sendFrontendLogBatch.mock.calls[1][0];
      expect(secondBatch.length).toBe(6);
      expect(secondBatch[0].event).toBe('debug-24');

      state = useLogStore.getState();
      expect(state.bridgeQueue.length).toBe(0);
    });

    it('should keep logs in queue if flush backend call fails', async () => {
      mockBackend.sendFrontendLogBatch.mockRejectedValue(new Error('Flush error'));
      const store = useLogStore.getState();

      store.debug('debug-fail');

      await vi.advanceTimersByTimeAsync(1000);

      expect(mockBackend.sendFrontendLogBatch).toHaveBeenCalledTimes(1);
      const state = useLogStore.getState();
      expect(state.bridgeQueue.length).toBe(1); // not removed
    });
  });

  describe('exporting logs', () => {
    it('should export log bundle JSON string', () => {
      const store = useLogStore.getState();
      store.info('evt-1', { foo: 'bar' });
      store.setFilter({ level: 'info' });

      const bundleStr = store.exportLogBundle();
      const bundle = JSON.parse(bundleStr);

      expect(bundle).toHaveProperty('exported_at');
      expect(bundle.log_level).toBe('info');
      expect(bundle.entries.length).toBe(1);
      expect(bundle.entries[0].event).toBe('evt-1');
      expect(bundle.entries[0].fields).toEqual({ foo: 'bar' });
    });
  });
});
