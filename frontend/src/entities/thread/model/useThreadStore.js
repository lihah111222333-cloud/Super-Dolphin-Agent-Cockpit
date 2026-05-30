// Thread Zustand Store
import { create } from 'zustand';
import {
  compactThread as compactThreadRPC,
  getSidebarState,
  getThreadMessages,
  getThreadState,
  interruptTurn as interruptTurnRPC,
  onBridgeEvent,
  recoverThread as recoverThreadRPC,
  renameThread as renameThreadRPC,
  setPreference,
  startThread as startThreadRPC,
  startTurn,
} from '../../../shared/api/backendApi';
import { useLogStore } from '../../log/model/useLogStore';

const normalizeThreadID = (id) => (id || '').toString().trim();

function basename(path) {
  const value = (path || '').toString().trim();
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function normalizeAttachmentPath(item) {
  if (typeof item === 'string') return item.trim();
  if (item && typeof item === 'object') return (item.path || item.url || '').toString().trim();
  return '';
}

function attachmentToInputItem(item) {
  if (item && typeof item === 'object' && (item.kind || '').toString().trim() === 'image') {
    const path = (item.path || '').toString().trim();
    const previewUrl = (item.previewUrl || item.url || '').toString().trim();
    if (path) {
      const payload = { type: 'localImage', path };
      if (previewUrl.toLowerCase().startsWith('data:image/')) payload.url = previewUrl;
      return payload;
    }
    if (previewUrl) return { type: 'image', url: previewUrl };
    return null;
  }

  const path = normalizeAttachmentPath(item);
  if (!path) return null;
  return { type: 'mention', name: basename(path), path };
}

function buildTurnInput(text, attachments = []) {
  const input = [];
  const trimmedText = (text || '').toString().trim();
  if (trimmedText) input.push({ type: 'text', text });
  if (Array.isArray(attachments)) {
    for (const attachment of attachments) {
      const item = attachmentToInputItem(attachment);
      if (item) input.push(item);
    }
  }
  return input;
}

function normalizeTokenUsage(value) {
  if (!value || typeof value !== 'object') return null;
  return {
    usedTokens: value.usedTokens ?? value.used_tokens ?? value.totalTokens ?? value.total_tokens ?? 0,
    contextWindowTokens: value.contextWindowTokens ?? value.context_window_tokens ?? value.contextWindow ?? value.context_window ?? 0,
    usedPercent: value.usedPercent ?? value.used_percent ?? 0,
  };
}

function timestampValue(value) {
  const time = new Date(value || '').getTime();
  return Number.isFinite(time) ? time : 0;
}

function isTurnCompletedSource(value) {
  const source = (value || '').toString().trim().toLowerCase();
  return source === 'turn/completed' || source === 'turn.completed' || source === 'turn/end' || source === 'turn_end';
}

function hasTerminalTimelineItem(items) {
  return Array.isArray(items) && items.some((item) => {
    const kind = (item?.kind || '').toString().trim().toLowerCase();
    return kind === 'turn_end' || kind === 'turn_interrupted';
  });
}

export const useThreadStore = create((set, get) => {
  let bridgeUnsubscribe = null;
  let syncDebounceTimer = null;
  let streamingSyncDebounceTimer = null;
  const sequencesByThread = new Map();

  const rememberCwd = (cwd) => {
    const finalCwd = (cwd || '').toString().trim();
    if (finalCwd && finalCwd !== '.' && finalCwd !== get().currentCwd) {
      set({ currentCwd: finalCwd });
    }
    return finalCwd && finalCwd !== '.' ? finalCwd : get().currentCwd;
  };

  const getCwdParams = (cwd) => {
    const finalCwd = rememberCwd(cwd) || '';
    return finalCwd ? { cwd: finalCwd } : {};
  };

  const scheduleSidebarRefresh = () => {
    if (syncDebounceTimer) clearTimeout(syncDebounceTimer);
    syncDebounceTimer = setTimeout(() => {
      syncDebounceTimer = null;
      get().refreshSidebarState(get().currentCwd);
    }, 75);
  };

  const applySnapshot = (snapshot, options = {}) => {
    if (!snapshot || typeof snapshot !== 'object') return;
    const preferredActiveThreadId = normalizeThreadID(options.preferredActiveThreadId);

    set((state) => {
      const nextThreads = snapshot.threads || state.threads;
      const snapshotActiveThreadId = normalizeThreadID(snapshot.activeThreadId || snapshot.active_thread_id);
      const currentActiveThreadId = normalizeThreadID(state.activeThreadId);
      const currentActiveStillVisible = Boolean(
        currentActiveThreadId && nextThreads.some((thread) => normalizeThreadID(thread.id || thread.threadId) === currentActiveThreadId),
      );
      const nextActiveThreadId = preferredActiveThreadId
        || (currentActiveStillVisible ? currentActiveThreadId : '')
        || snapshotActiveThreadId
        || currentActiveThreadId
        || normalizeThreadID(nextThreads[0]?.id || nextThreads[0]?.threadId);

      // Merge timeline items
      const nextTimelines = { ...state.timelinesByThread };
      if (snapshot.timelinesByThread) {
        for (const [tid, items] of Object.entries(snapshot.timelinesByThread)) {
          // Merge old optimistic with new incoming items
          const oldItems = state.timelinesByThread[tid] || [];
          const newItems = Array.isArray(items) ? items : [];

          if (newItems.length === 0 && oldItems.length > 0) continue;

          // Deduplicate and merge
          const newIds = new Set(newItems.map((i) => i.id).filter(Boolean));
          const localItems = oldItems.filter((i) => {
            if (newIds.has(i.id)) return false;
            // Strip optimistic items once real items come in
            if (i.id && i.id.includes('-optimistic-')) return false;
            return true;
          });

          nextTimelines[tid] = [...newItems, ...localItems].sort((a, b) => {
            return (a.ts || '') < (b.ts || '') ? -1 : (a.ts || '') > (b.ts || '') ? 1 : 0;
          });
        }
      }

      // Merge statuses
      const nextStatuses = { ...state.statuses };
      if (snapshot.statuses) {
        Object.assign(nextStatuses, snapshot.statuses);
      }

      // Merge token usage
      const nextTokenUsage = { ...state.tokenUsageByThread };
      if (snapshot.tokenUsageByThread) {
        Object.assign(nextTokenUsage, snapshot.tokenUsageByThread);
      }
      const sidebarTokenUsage = normalizeTokenUsage(snapshot.token_usage || snapshot.tokenUsage);
      if (sidebarTokenUsage && nextActiveThreadId) {
        nextTokenUsage[nextActiveThreadId] = sidebarTokenUsage;
      }

      // Merge diff text
      const nextDiffText = { ...state.diffTextByThread };
      if (snapshot.diffTextByThread) {
        Object.assign(nextDiffText, snapshot.diffTextByThread);
      }

      return {
        threads: nextThreads,
        timelinesByThread: nextTimelines,
        statuses: nextStatuses,
        tokenUsageByThread: nextTokenUsage,
        diffTextByThread: nextDiffText,
        activeThreadId: nextActiveThreadId,
        activeCmdThreadId: snapshot.activeCmdThreadId || state.activeCmdThreadId,
      };
    });
  };

  const handleBridgeEventInternal = (evt) => {
    const method = (evt?.method || evt?.type || '').toString().toLowerCase();
    const payload = evt?.payload || evt?.params || evt?.data || {};
    const threadId = normalizeThreadID(payload.threadId || payload.thread_id || evt?.threadId);

    // Delta stream token / content mapping
    if (method === 'item/agentmessage/delta' && threadId) {
      const activeId = get().activeThreadId;
      if (threadId === activeId) {
        const delta = payload.delta || '';
        if (delta) {
          set((state) => {
            const timeline = [...(state.timelinesByThread[activeId] || [])];
            let lastIndex = -1;
            for (let i = timeline.length - 1; i >= 0; i--) {
              if (timeline[i].kind === 'assistant' && !timeline[i].done && !timeline[i].streamingFinalized) {
                lastIndex = i;
                break;
              }
            }
            if (lastIndex >= 0) {
              timeline[lastIndex] = {
                ...timeline[lastIndex],
                text: (timeline[lastIndex].text || '') + delta,
              };
            } else {
              timeline.push({
                id: `stream-${Date.now()}-streaming`,
                kind: 'assistant',
                text: delta,
                done: false,
                ts: new Date().toISOString(),
              });
            }
            return {
              timelinesByThread: {
                ...state.timelinesByThread,
                [activeId]: timeline,
              },
            };
          });
        }
      }
    }

    // Token Updates push
    if (method === 'thread/tokenusage/updated' && threadId) {
      const input = Number(payload.input || payload.inputTokens) || 0;
      const output = Number(payload.output || payload.outputTokens) || 0;
      const total = Number(payload.totalTokens || payload.total_tokens) || (input + output);
      const limit = Number(payload.contextWindow || payload.context_window) || 128000;
      const pct = limit > 0 ? Math.min(100, (total / limit) * 100) : 0;

      set((state) => ({
        tokenUsageByThread: {
          ...state.tokenUsageByThread,
          [threadId]: {
            usedTokens: total,
            contextWindowTokens: limit,
            usedPercent: pct,
          },
        },
      }));
    }

    // Main status terminal finalize streams
    const term = ['turn/completed', 'turn.completed', 'turn/end', 'turn_end', 'turn/interrupted', 'agent/stopped', 'thread/stopped', 'agent/failed'];
    if (term.includes(method) && threadId) {
      set((state) => {
        const timeline = [...(state.timelinesByThread[threadId] || [])];
        let changed = false;
        const next = timeline.map((it) => {
          if (it.kind === 'assistant' && !it.streamingFinalized) {
            changed = true;
            return { ...it, streamingFinalized: true, done: true };
          }
          return it;
        });
        if (changed) {
          return {
            timelinesByThread: {
              ...state.timelinesByThread,
              [threadId]: next,
            },
          };
        }
        return {};
      });

      // Reload final state
      get().syncThreadState(threadId, get().currentCwd);
    }

    // Sidebar trigger
    if (method === 'ui/sidebar/changed') {
      scheduleSidebarRefresh();
    }

    // Live patches
    if (method === 'ui/thread/patch' && threadId) {
      const sequence = Number(payload.sequence || 0);
      const prevSequence = sequencesByThread.get(threadId) || 0;

      if (sequence > 0) {
        if (prevSequence > 0 && sequence <= prevSequence) {
          useLogStore.getState().warn('thread.patch.stale', { threadId, sequence, prevSequence });
          return;
        }

        const isGap = prevSequence > 0 && sequence !== prevSequence + 1;
        if (isGap) {
          useLogStore.getState().warn('thread.patch.sequence_gap', { threadId, sequence, prevSequence });
          // Repair by syncing thread state
          get().syncThreadState(threadId, get().currentCwd);
        }
        sequencesByThread.set(threadId, sequence);
      }

      const timelineItems = payload.timelineItems || payload.timeline_items;
      if (timelineItems) {
        applySnapshot({ timelinesByThread: { [threadId]: timelineItems } });
      }
      if (isTurnCompletedSource(payload.source || payload.Source) || hasTerminalTimelineItem(timelineItems)) {
        get().syncThreadState(threadId, get().currentCwd);
      }
    }
  };

  return {
    threads: [],
    statuses: {},
    timelinesByThread: {},
    tokenUsageByThread: {},
    diffTextByThread: {},
    activeThreadId: '',
    activeCmdThreadId: '',
    currentCwd: '',

    initialize: () => {
      if (bridgeUnsubscribe) return;
      bridgeUnsubscribe = onBridgeEvent((evt) => {
        handleBridgeEventInternal(evt);
      });
    },

    destroy: () => {
      if (bridgeUnsubscribe) {
        bridgeUnsubscribe();
        bridgeUnsubscribe = null;
      }
      if (syncDebounceTimer) {
        clearTimeout(syncDebounceTimer);
        syncDebounceTimer = null;
      }
      if (streamingSyncDebounceTimer) {
        clearTimeout(streamingSyncDebounceTimer);
        streamingSyncDebounceTimer = null;
      }
      sequencesByThread.clear();
    },

    setActiveThread: (id, cwd) => {
      const params = getCwdParams(cwd);
      set({ activeThreadId: normalizeThreadID(id) });
      if (id) {
        get().syncThreadState(id, params.cwd);
      }
    },

    setActiveCmdThread: (id, cwd) => {
      const params = getCwdParams(cwd);
      set({ activeCmdThreadId: normalizeThreadID(id) });
      if (id) {
        get().syncThreadState(id, params.cwd);
      }
    },

    refreshSidebarState: async (cwd) => {
      try {
        const params = getCwdParams(cwd);
        const sidebar = await getSidebarState(params);
        applySnapshot(sidebar);
      } catch (error) {
        useLogStore.getState().error('thread.sidebar.refresh_failed', { error: error.message });
      }
    },

    syncThreadState: async (threadId, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        const params = { ...getCwdParams(cwd), threadId: tid, includeDiff: false };
        const snapshot = await getThreadState(params);
        applySnapshot(snapshot, { preferredActiveThreadId: tid });

        // Fetch complete message timeline logs
        await get().loadMessages(tid);
      } catch (error) {
        useLogStore.getState().error('thread.sync_state.failed', { threadId: tid, error: error.message });
      }
    },

    loadMessages: async (threadId) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        const res = await getThreadMessages({ threadId: tid, limit: 300 });
        if (res && Array.isArray(res.messages)) {
          const items = res.messages.map((m) => ({
            id: m.id || `msg-${Math.random()}`,
            kind: m.role === 'user' ? 'user' : 'assistant',
            text: m.content || '',
            ts: m.createdAt || new Date().toISOString(),
            done: true,
            streamingFinalized: true,
          })).sort((a, b) => timestampValue(a.ts) - timestampValue(b.ts));
          set((state) => ({
            timelinesByThread: {
              ...state.timelinesByThread,
              [tid]: items,
            },
          }));
        }
      } catch (error) {
        useLogStore.getState().error('thread.load_messages.failed', { threadId: tid, error: error.message });
      }
    },

    startThread: async (cwd, options = {}) => {
      try {
        const params = { ...getCwdParams(cwd), deferSpawn: true, ...options };
        const thread = await startThreadRPC(params);
        const threadId = normalizeThreadID(thread.threadId || thread.id);
        if (threadId) {
          await get().refreshSidebarState(cwd);
          set({ activeThreadId: threadId });
          await get().syncThreadState(threadId, cwd);
          return threadId;
        }
      } catch (error) {
        useLogStore.getState().error('thread.start.failed', { error: error.message });
      }
      return '';
    },

    sendMessage: async (threadId, input, cwd, attachments = []) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      const turnInput = buildTurnInput(input, attachments);
      if (turnInput.length === 0) return;

      // Inject Optimistic user item
      const optimisticId = `user-optimistic-${Date.now()}`;
      const optimisticItem = {
        id: optimisticId,
        kind: 'user',
        text: input,
        attachments: Array.isArray(attachments) && attachments.length > 0
          ? attachments.map((item) => (typeof item === 'string' ? item : { ...item }))
          : undefined,
        ts: new Date().toISOString(),
        done: true,
        streamingFinalized: true,
      };

      set((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          [tid]: [...(state.timelinesByThread[tid] || []), optimisticItem],
        },
      }));

      try {
        await startTurn({
          ...getCwdParams(cwd),
          threadId: tid,
          input: turnInput,
          manualSkillSelection: false,
        });
      } catch (error) {
        // Rollback optimistic
        set((state) => ({
          timelinesByThread: {
            ...state.timelinesByThread,
            [tid]: (state.timelinesByThread[tid] || []).filter((i) => i.id !== optimisticId),
          },
        }));
        useLogStore.getState().error('thread.send.failed', { threadId: tid, error: error.message });
        throw error;
      }
    },

    interruptTurn: async (threadId, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        await interruptTurnRPC({ ...getCwdParams(cwd), threadId: tid });
      } catch (error) {
        useLogStore.getState().error('thread.interrupt.failed', { threadId: tid, error: error.message });
      }
    },

    compactThread: async (threadId, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        await compactThreadRPC({ ...getCwdParams(cwd), threadId: tid });
      } catch (error) {
        useLogStore.getState().error('thread.compact.failed', { threadId: tid, error: error.message });
      }
    },

    recoverThread: async (threadId, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        await recoverThreadRPC({ ...getCwdParams(cwd), threadId: tid });
      } catch (error) {
        useLogStore.getState().error('thread.recover.failed', { threadId: tid, error: error.message });
      }
    },

    renameThread: async (threadId, newName, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        await renameThreadRPC({ ...getCwdParams(cwd), threadId: tid, name: newName });
        await get().refreshSidebarState(cwd);
      } catch (error) {
        useLogStore.getState().error('thread.rename.failed', { threadId: tid, error: error.message });
      }
    },

    setThreadPinned: async (threadId, pinned, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        await setPreference({
          ...getCwdParams(cwd),
          key: `pinnedThreadAtById.${tid}`,
          value: pinned ? new Date().toISOString() : null,
        });
        await get().refreshSidebarState(cwd);
      } catch (error) {
        useLogStore.getState().error('thread.pin.failed', { threadId: tid, error: error.message });
      }
    },

    setThreadArchived: async (threadId, archived, cwd) => {
      const tid = normalizeThreadID(threadId);
      if (!tid) return;
      try {
        await setPreference({
          ...getCwdParams(cwd),
          key: `archivedThreadAtById.${tid}`,
          value: archived ? new Date().toISOString() : null,
        });
        await get().refreshSidebarState(cwd);
      } catch (error) {
        useLogStore.getState().error('thread.archive.failed', { threadId: tid, error: error.message });
      }
    },
  };
});
