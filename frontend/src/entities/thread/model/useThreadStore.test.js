import { vi, describe, it, expect, beforeEach, afterEach } from 'vitest';
import { useThreadStore } from './useThreadStore';
import { useLogStore } from '../../log/model/useLogStore';

const mockBackend = vi.hoisted(() => ({
  compactThread: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadMessages: vi.fn(),
  getThreadState: vi.fn(),
  interruptTurn: vi.fn(),
  onBridgeEvent: vi.fn(),
  recoverThread: vi.fn(),
  renameThread: vi.fn(),
  setPreference: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('../../../shared/api/backendApi', () => ({
  compactThread: (...args) => mockBackend.compactThread(...args),
  getSidebarState: (...args) => mockBackend.getSidebarState(...args),
  getThreadMessages: (...args) => mockBackend.getThreadMessages(...args),
  getThreadState: (...args) => mockBackend.getThreadState(...args),
  interruptTurn: (...args) => mockBackend.interruptTurn(...args),
  onBridgeEvent: (...args) => mockBackend.onBridgeEvent(...args),
  recoverThread: (...args) => mockBackend.recoverThread(...args),
  renameThread: (...args) => mockBackend.renameThread(...args),
  setPreference: (...args) => mockBackend.setPreference(...args),
  startThread: (...args) => mockBackend.startThread(...args),
  startTurn: (...args) => mockBackend.startTurn(...args),
  registerBridgeLogStore: (...args) => mockBackend.registerBridgeLogStore(...args),
  sendFrontendLogBatch: (...args) => mockBackend.sendFrontendLogBatch(...args),
}));

describe('useThreadStore', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useThreadStore.getState().destroy();
    useThreadStore.setState({
      threads: [],
      statuses: {},
      timelinesByThread: {},
      tokenUsageByThread: {},
      diffTextByThread: {},
      activeThreadId: '',
      activeCmdThreadId: '',
    });
    useLogStore.setState({
      entries: [],
      bridgeQueue: [],
    });
  });

  afterEach(() => {
    useThreadStore.getState().destroy();
  });

  describe('snapshot hydration', () => {
    it('should pass explicit cwd to sidebar refresh', async () => {
      mockBackend.getSidebarState.mockResolvedValue({
        threads: [{ id: 'thread-1', name: 'Thread 1' }],
        activeThreadId: 'thread-1',
      });

      await useThreadStore.getState().refreshSidebarState('/repo/app');

      expect(mockBackend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
      expect(useThreadStore.getState().threads).toEqual([{ id: 'thread-1', name: 'Thread 1' }]);
    });

    it('should hydrate threads, statuses, timelines, and tokens from thread state sync', async () => {
      const mockSnapshot = {
        threads: [{ id: 'thread-1', name: 'Test Thread' }],
        statuses: { 'thread-1': 'thinking' },
        timelinesByThread: {
          'thread-1': [{ id: 'msg-1', kind: 'assistant', text: 'Hello', ts: '2026-05-29T00:00:00.000Z' }]
        },
        tokenUsageByThread: {
          'thread-1': { usedTokens: 500, contextWindowTokens: 128000, usedPercent: 0.39 }
        },
        diffTextByThread: {
          'thread-1': 'some diff'
        },
        activeThreadId: 'thread-1'
      };

      mockBackend.getThreadState.mockResolvedValue(mockSnapshot);
      mockBackend.getThreadMessages.mockResolvedValue({ messages: [] });

      await useThreadStore.getState().syncThreadState('thread-1');

      const state = useThreadStore.getState();
      expect(state.threads).toEqual(mockSnapshot.threads);
      expect(state.statuses['thread-1']).toBe('thinking');
      expect(state.tokenUsageByThread['thread-1']).toEqual(mockSnapshot.tokenUsageByThread['thread-1']);
      expect(state.diffTextByThread['thread-1']).toBe('some diff');
    });

    it('should map sidebar token_usage to the active thread token bucket', async () => {
      mockBackend.getSidebarState.mockResolvedValue({
        threads: [{ id: 'thread-1', name: 'Thread 1' }],
        activeThreadId: 'thread-1',
        token_usage: {
          usedTokens: 2048,
          contextWindowTokens: 128000,
          usedPercent: 1.6,
        },
      });

      await useThreadStore.getState().refreshSidebarState('/repo/app');

      expect(useThreadStore.getState().tokenUsageByThread['thread-1']).toEqual({
        usedTokens: 2048,
        contextWindowTokens: 128000,
        usedPercent: 1.6,
      });
    });

    it('should remember cwd and use it when selecting a thread for state and token sync', async () => {
      mockBackend.getThreadState.mockResolvedValue({
        threads: [{ id: 'thread-1', name: 'Thread 1' }],
        activeThreadId: 'thread-1',
        token_usage: {
          usedTokens: 4096,
          contextWindowTokens: 128000,
          usedPercent: 3.2,
        },
      });
      mockBackend.getThreadMessages.mockResolvedValue({ messages: [] });

      useThreadStore.getState().setActiveThread('thread-1', '/repo/app');

      await vi.waitFor(() => {
        expect(mockBackend.getThreadState).toHaveBeenCalledWith({
          cwd: '/repo/app',
          threadId: 'thread-1',
          includeDiff: false,
        });
      });
      expect(useThreadStore.getState().tokenUsageByThread['thread-1']).toEqual({
        usedTokens: 4096,
        contextWindowTokens: 128000,
        usedPercent: 3.2,
      });
    });
  });

  describe('active selection preservation', () => {
    it('should preserve existing active selection if snapshot does not provide active selection', async () => {
      useThreadStore.setState({
        activeThreadId: 'thread-existing',
        activeCmdThreadId: 'cmd-existing'
      });

      const mockSnapshot = {
        threads: [{ id: 'thread-existing', name: 'Existing' }],
        // activeThreadId and activeCmdThreadId are not provided or falsy
      };

      mockBackend.getThreadState.mockResolvedValue(mockSnapshot);
      mockBackend.getThreadMessages.mockResolvedValue({ messages: [] });

      await useThreadStore.getState().syncThreadState('thread-existing');

      const state = useThreadStore.getState();
      expect(state.activeThreadId).toBe('thread-existing');
      expect(state.activeCmdThreadId).toBe('cmd-existing');
    });

    it('should keep a newly started thread active even if backend snapshots still report an old active thread', async () => {
      mockBackend.startThread.mockResolvedValue({ threadId: 'thread-new' });
      mockBackend.getSidebarState.mockResolvedValue({
        activeThreadId: 'thread-old',
        threads: [
          { id: 'thread-old', name: 'Old stale thread' },
          { id: 'thread-new', name: 'New thread' },
        ],
      });
      mockBackend.getThreadState.mockResolvedValue({
        activeThreadId: 'thread-old',
        threads: [
          { id: 'thread-old', name: 'Old stale thread' },
          { id: 'thread-new', name: 'New thread' },
        ],
      });
      mockBackend.getThreadMessages.mockResolvedValue({ messages: [] });

      const threadId = await useThreadStore.getState().startThread('/repo/app', {
        provider: 'codex',
        name: 'New thread',
      });

      expect(threadId).toBe('thread-new');
      expect(useThreadStore.getState().activeThreadId).toBe('thread-new');
    });
  });

  describe('history hydration', () => {
    it('hydrates thread/messages in chronological order', async () => {
      mockBackend.getThreadMessages.mockResolvedValue({
        messages: [
          {
            id: 2,
            role: 'assistant',
            content: 'reply',
            createdAt: '2026-05-29T10:00:02.000Z',
          },
          {
            id: 1,
            role: 'user',
            content: 'prompt',
            createdAt: '2026-05-29T10:00:00.000Z',
          },
        ],
      });

      await useThreadStore.getState().loadMessages('thread-1');

      expect(useThreadStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ id: 1, kind: 'user', text: 'prompt' }),
        expect.objectContaining({ id: 2, kind: 'assistant', text: 'reply' }),
      ]);
    });

    it('reloads persisted messages when a thread patch reports turn completion', async () => {
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });
      mockBackend.getThreadState.mockResolvedValue({
        threads: [{ id: 'thread-1', name: 'Thread 1' }],
        activeThreadId: 'thread-old',
        timelinesByThread: {
          'thread-1': [{ id: 'turn-end', kind: 'turn_end', status: 'completed' }],
        },
      });
      mockBackend.getThreadMessages.mockResolvedValue({
        messages: [
          { id: 1, role: 'user', content: 'prompt', createdAt: '2026-05-29T10:00:00.000Z' },
          { id: 2, role: 'assistant', content: 'reply', createdAt: '2026-05-29T10:00:02.000Z' },
        ],
      });
      useThreadStore.setState({
        currentCwd: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1' }],
      });

      useThreadStore.getState().initialize();
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          source: 'turn/completed',
          timelineItems: [{ id: 'turn-end', kind: 'turn_end', status: 'completed' }],
        },
      });

      await vi.waitFor(() => {
        expect(mockBackend.getThreadState).toHaveBeenCalledWith({
          cwd: '/repo/app',
          threadId: 'thread-1',
          includeDiff: false,
        });
        expect(useThreadStore.getState().timelinesByThread['thread-1']).toEqual([
          expect.objectContaining({ kind: 'user', text: 'prompt' }),
          expect.objectContaining({ kind: 'assistant', text: 'reply' }),
        ]);
      });
    });
  });

  describe('optimistic user messages', () => {
    it('should append optimistic user message and roll it back on failure', async () => {
      const threadId = 'thread-1';
      useThreadStore.setState({
        timelinesByThread: { [threadId]: [] }
      });

      mockBackend.startTurn.mockRejectedValue(new Error('API Error'));

      const sendPromise = useThreadStore.getState().sendMessage(threadId, 'hello', 'cwd');

      // Timeline should contain the optimistic message immediately
      let timeline = useThreadStore.getState().timelinesByThread[threadId];
      expect(timeline.length).toBe(1);
      expect(timeline[0].kind).toBe('user');
      expect(timeline[0].text).toBe('hello');
      expect(timeline[0].id).toContain('-optimistic-');

      await expect(sendPromise).rejects.toThrow('API Error');

      // After failure, it should roll back
      timeline = useThreadStore.getState().timelinesByThread[threadId];
      expect(timeline.length).toBe(0);

      // Verify log store captured the error
      const logState = useLogStore.getState();
      expect(logState.entries.some(e => e.event === 'thread.send.failed')).toBe(true);
    });

    it('should clean up optimistic items when a new snapshot with real items is merged', async () => {
      const threadId = 'thread-1';
      mockBackend.startTurn.mockResolvedValue({ ok: true });

      await useThreadStore.getState().sendMessage(threadId, 'hello', 'cwd', ['/tmp/a.txt']);

      expect(mockBackend.startTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: 'cwd',
        threadId,
        input: [
          { type: 'text', text: 'hello' },
          { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
        ],
      }));

      const stateWithOptimistic = useThreadStore.getState();
      expect(stateWithOptimistic.timelinesByThread[threadId].length).toBe(1);
      const optId = stateWithOptimistic.timelinesByThread[threadId][0].id;

      // Simulate bridge event payload with real items
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });

      useThreadStore.getState().initialize();

      // Trigger ui/thread/patch event
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId,
          timelineItems: [
            { id: 'msg-real-1', kind: 'user', text: 'hello', ts: new Date().toISOString() }
          ]
        }
      });

      // The optimistic item (with optId) should be stripped because of -optimistic- check in applySnapshot
      const finalTimeline = useThreadStore.getState().timelinesByThread[threadId];
      expect(finalTimeline.length).toBe(1);
      expect(finalTimeline[0].id).toBe('msg-real-1');
      expect(finalTimeline.some(i => i.id === optId)).toBe(false);
    });
  });

  describe('sequence gap warning/repair logic', () => {
    it('debounces high-frequency sidebar changed bridge events into one refresh', async () => {
      vi.useFakeTimers();
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });
      mockBackend.getSidebarState.mockResolvedValue({ threads: [], activeThreadId: '' });
      useThreadStore.setState({ currentCwd: '/repo/app' });

      useThreadStore.getState().initialize();
      bridgeCallback({ method: 'ui/sidebar/changed', payload: {} });
      bridgeCallback({ method: 'ui/sidebar/changed', payload: {} });
      bridgeCallback({ method: 'ui/sidebar/changed', payload: {} });

      expect(mockBackend.getSidebarState).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(90);

      expect(mockBackend.getSidebarState).toHaveBeenCalledTimes(1);
      expect(mockBackend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
      vi.useRealTimers();
    });

    it('should handle sequential patches correctly without warning', () => {
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });

      useThreadStore.getState().initialize();

      // Send patch 1
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: 1,
          timelineItems: [{ id: 'msg-1', kind: 'user', text: 'p1' }]
        }
      });

      // Send patch 2
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: 2,
          timelineItems: [{ id: 'msg-2', kind: 'user', text: 'p2' }]
        }
      });

      const logState = useLogStore.getState();
      const hasGapWarning = logState.entries.some(e => e.event === 'thread.patch.sequence_gap');
      const hasStaleWarning = logState.entries.some(e => e.event === 'thread.patch.stale');

      expect(hasGapWarning).toBe(false);
      expect(hasStaleWarning).toBe(false);
      expect(mockBackend.getThreadState).not.toHaveBeenCalled();
    });

    it('should warn and trigger sync repair on sequence gap', () => {
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });

      useThreadStore.getState().initialize();

      // Send patch 1
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: 1,
          timelineItems: [{ id: 'msg-1', kind: 'user', text: 'p1' }]
        }
      });

      // Send patch 3 (gap!)
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: 3,
          timelineItems: [{ id: 'msg-3', kind: 'user', text: 'p3' }]
        }
      });

      const logState = useLogStore.getState();
      const gapWarning = logState.entries.find(e => e.event === 'thread.patch.sequence_gap');

      expect(gapWarning).toBeDefined();
      expect(gapWarning.fields.sequence).toBe(3);
      expect(gapWarning.fields.prevSequence).toBe(1);

      // Verify sync repair was triggered
      expect(mockBackend.getThreadState).toHaveBeenCalledWith(expect.objectContaining({ threadId: 'thread-1' }));
    });

    it('should ignore stale sequence patches', () => {
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });

      useThreadStore.getState().initialize();

      // Send patch 2
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: 2,
          timelineItems: [{ id: 'msg-2', kind: 'user', text: 'p2' }]
        }
      });

      // Send patch 1 (stale!)
      bridgeCallback({
        method: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: 1,
          timelineItems: [{ id: 'msg-1', kind: 'user', text: 'p1' }]
        }
      });

      const logState = useLogStore.getState();
      const staleWarning = logState.entries.find(e => e.event === 'thread.patch.stale');

      expect(staleWarning).toBeDefined();
      expect(staleWarning.fields.sequence).toBe(1);
      expect(staleWarning.fields.prevSequence).toBe(2);

      // Timeline should not have applied the stale patch's items
      const timeline = useThreadStore.getState().timelinesByThread['thread-1'] || [];
      expect(timeline.some(i => i.id === 'msg-1')).toBe(false);
    });
  });
});
