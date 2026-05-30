// Log and Tracing Zustand Store
import { create } from 'zustand';
import { registerBridgeLogStore, sendFrontendLogBatch } from '../../../shared/api/backendApi';

const MAX_LOG_ENTRIES = 600;
const MAX_BRIDGE_QUEUE = 240;
const BATCH_SIZE = 24;

export const useLogStore = create((set, get) => {
  let flushTimeout = null;

  const queueForBridge = (entry) => {
    // Only send debug, warn, and error to the backend. info stays local.
    if (entry.level === 'info') return;

    set((state) => {
      const nextQueue = [...state.bridgeQueue, entry];
      if (nextQueue.length > MAX_BRIDGE_QUEUE) {
        nextQueue.shift(); // Drop oldest if limit is hit
      }
      return { bridgeQueue: nextQueue };
    });

    triggerFlush();
  };

  const triggerFlush = () => {
    if (flushTimeout) return;
    flushTimeout = setTimeout(async () => {
      flushTimeout = null;
      await get().flushBridgeQueue();
    }, 1000);
  };

  return {
    entries: [],
    bridgeQueue: [],
    filters: {
      level: '',
      method: '',
      threadId: '',
      operationId: '',
    },

    log: (level, event, fields = {}) => {
      const entry = {
        timestamp: new Date().toISOString(),
        level,
        event,
        fields,
      };

      set((state) => {
        const nextEntries = [...state.entries, entry];
        if (nextEntries.length > MAX_LOG_ENTRIES) {
          nextEntries.shift();
        }
        return { entries: nextEntries };
      });

      queueForBridge(entry);
    },

    info: (event, fields) => get().log('info', event, fields),
    debug: (event, fields) => get().log('debug', event, fields),
    warn: (event, fields) => get().log('warn', event, fields),
    error: (event, fields) => get().log('error', event, fields),

    setFilter: (patch) => {
      set((state) => ({
        filters: { ...state.filters, ...patch },
      }));
    },

    clearFilters: () => {
      set({
        filters: { level: '', method: '', threadId: '', operationId: '' },
      });
    },

    flushBridgeQueue: async () => {
      const { bridgeQueue } = get();
      if (bridgeQueue.length === 0) return;

      const batch = bridgeQueue.slice(0, BATCH_SIZE);
      try {
        await sendFrontendLogBatch(batch);
        // Remove successfully sent logs
        set((state) => ({
          bridgeQueue: state.bridgeQueue.slice(batch.length),
        }));
      } catch (err) {
        console.error('Failed to flush log batch to Wails backend', err);
      }

      // If there are more logs, schedule another flush
      if (get().bridgeQueue.length > 0) {
        triggerFlush();
      }
    },

    exportLogBundle: () => {
      const { entries, filters } = get();
      return JSON.stringify({
        exported_at: new Date().toISOString(),
        log_level: filters.level || 'all',
        entries,
      }, null, 2);
    },

    destroy: () => {
      if (flushTimeout) {
        clearTimeout(flushTimeout);
        flushTimeout = null;
      }
    },
  };
});

// Register with Wails bridge so bridge logs feed into this store automatically
registerBridgeLogStore({
  info: (event, fields) => useLogStore.getState().info(event, fields),
  debug: (event, fields) => useLogStore.getState().debug(event, fields),
  warn: (event, fields) => useLogStore.getState().warn(event, fields),
  error: (event, fields) => useLogStore.getState().error(event, fields),
});
