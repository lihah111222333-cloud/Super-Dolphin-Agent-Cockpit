import { create } from 'zustand';
import {
  compactThread,
  getProjects,
  getSidebarState,
  getThreadMessages,
  getThreadState,
  getWindowBootstrap,
  interruptTurn,
  onBridgeEvent,
  readConfig,
  recoverThread,
  registerBridgeLogStore,
  renameThread as renameThreadRPC,
  selectFiles,
  setPreference,
  startThread,
  startTurn,
} from '../../../shared/api/backendApi.js';

const DEFAULT_PROVIDER = 'codex';
const MAX_WARNING_ENTRIES = 300;

function normalizeString(value) {
  return (value || '').toString().trim();
}

function normalizePath(value) {
  const path = normalizeString(value);
  if (!path) return '';
  if (path !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(path)) {
    return path.replace(/[\\/]+$/, '');
  }
  return path;
}

function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function normalizeThreadId(value) {
  return normalizeString(value);
}

function normalizeThread(raw) {
  const id = normalizeThreadId(raw?.id || raw?.threadId || raw?.thread_id || raw?.agent_id);
  return {
    id,
    name: normalizeString(raw?.name || raw?.title || raw?.displayName || raw?.summary) || '新对话',
    provider: normalizeString(raw?.provider || raw?.agentKey || raw?.agent_key) || DEFAULT_PROVIDER,
    status: normalizeString(raw?.status || raw?.state) || '等待指示',
    lastMessage: normalizeString(raw?.lastMessage || raw?.last_message || raw?.preview),
    updatedAt: normalizeString(raw?.updatedAt || raw?.updated_at || raw?.createdAt || raw?.created_at),
    pinned: Boolean(raw?.pinned || raw?.isPinned),
    archived: Boolean(raw?.archived || raw?.isArchived),
  };
}

function normalizeTokenUsage(value) {
  if (!value || typeof value !== 'object') return null;
  const usedTokens = Number(value.usedTokens ?? value.used_tokens ?? value.totalTokens ?? value.total_tokens ?? 0) || 0;
  const contextWindowTokens = Number(value.contextWindowTokens ?? value.context_window_tokens ?? value.contextWindow ?? value.context_window ?? 0) || 0;
  const usedPercent = Number(value.usedPercent ?? value.used_percent ?? (contextWindowTokens > 0 ? (usedTokens / contextWindowTokens) * 100 : 0)) || 0;
  return { usedTokens, contextWindowTokens, usedPercent };
}

function normalizeTimelineItem(item) {
  const role = normalizeString(item?.role || item?.kind || item?.type).toLowerCase();
  const normalizedRole = role.includes('user') ? 'user' : 'assistant';
  return {
    id: normalizeString(item?.id || item?.messageId || item?.message_id) || `${normalizedRole}-${Date.now()}`,
    role: normalizedRole,
    text: normalizeString(item?.text || item?.content || item?.message || item?.delta),
    time: normalizeString(item?.time || item?.ts || item?.createdAt || item?.created_at) || new Date().toISOString(),
    done: item?.done !== false,
  };
}

function normalizeAttachment(value) {
  if (typeof value === 'string') {
    const path = normalizeString(value);
    return path ? { path, name: basename(path) } : null;
  }
  if (!value || typeof value !== 'object') return null;
  const path = normalizeString(value.path || value.url);
  if (!path) return null;
  return {
    path,
    name: normalizeString(value.name) || basename(path),
    kind: normalizeString(value.kind),
    previewUrl: normalizeString(value.previewUrl || value.url),
  };
}

function attachmentToInputItem(item) {
  const attachment = normalizeAttachment(item);
  if (!attachment) return null;
  if (attachment.kind === 'image') {
    const payload = { type: 'localImage', path: attachment.path };
    if (attachment.previewUrl.toLowerCase().startsWith('data:image/')) {
      payload.url = attachment.previewUrl;
    }
    return payload;
  }
  return { type: 'mention', name: attachment.name || basename(attachment.path), path: attachment.path };
}

function buildTurnInput(text, attachments) {
  const items = [];
  const message = normalizeString(text);
  if (message) items.push({ type: 'text', text: message });
  for (const attachment of attachments || []) {
    const item = attachmentToInputItem(attachment);
    if (item) items.push(item);
  }
  return items;
}

function createLaunchIntentId() {
  const id = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `launch_${id}`;
}

function compareSequence(left, right) {
  try {
    const a = BigInt(normalizeString(left) || '0');
    const b = BigInt(normalizeString(right) || '0');
    if (a === b) return 0;
    return a < b ? -1 : 1;
  } catch {
    return 0;
  }
}

function isNextSequence(previous, next) {
  try {
    return BigInt(normalizeString(next)) === BigInt(normalizeString(previous)) + 1n;
  } catch {
    return true;
  }
}

const baseState = {
  bootstrapStatus: 'idle',
  error: '',
  cwd: '',
  activeProject: '',
  projects: [],
  provider: DEFAULT_PROVIDER,
  permission: '完全访问权限',
  activePage: 'chat',
  threads: [],
  statuses: {},
  activeThreadId: '',
  timelinesByThread: {},
  tokenUsageByThread: {},
  diffTextByThread: {},
  activityEntries: [],
  warningEntries: [],
  draft: '',
  attachments: [],
  sending: false,
  rightPanelWidth: 520,
};

function stateWithPatch(patch = {}) {
  return {
    ...baseState,
    ...patch,
  };
}

export const useClientStore = create((set, get) => {
  let bridgeUnsubscribe = null;
  const sequencesByThread = new Map();

  const addWarning = (level, event, fields = {}) => {
    if (level !== 'warn' && level !== 'error') return;
    const entry = {
      id: `${event}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      timestamp: new Date().toISOString(),
      level,
      event,
      fields,
    };
    set((state) => ({
      warningEntries: [entry, ...state.warningEntries].slice(0, MAX_WARNING_ENTRIES),
    }));
  };

  const requireCwd = (reason) => {
    const cwd = normalizePath(get().activeProject) || normalizePath(get().cwd);
    if (!cwd || cwd === '.') {
      const error = new Error(`frontend-app: cwd is required for ${reason}`);
      addWarning('error', 'missing.cwd', { reason });
      throw error;
    }
    return cwd;
  };

  const applyProjects = (payload, fallbackCwd) => {
    const projects = Array.isArray(payload?.projects)
      ? payload.projects.map(normalizePath).filter(Boolean)
      : [];
    const active = normalizePath(payload?.active || payload?.activeProject || fallbackCwd);
    set({
      projects,
      activeProject: active || normalizePath(fallbackCwd),
    });
  };

  const applySnapshot = (payload = {}, options = {}) => {
    const preferredActiveThreadId = normalizeThreadId(options.preferredActiveThreadId);
    set((state) => {
      const nextThreads = Array.isArray(payload.threads)
        ? payload.threads.map(normalizeThread).filter((thread) => thread.id)
        : state.threads;
      const snapshotActive = normalizeThreadId(payload.activeThreadId || payload.active_thread_id);
      const activeThreadId = preferredActiveThreadId || snapshotActive || state.activeThreadId || nextThreads[0]?.id || '';

      const timelinesByThread = { ...state.timelinesByThread };
      const incomingTimelines = payload.timelinesByThread || payload.timelines_by_thread;
      if (incomingTimelines && typeof incomingTimelines === 'object') {
        for (const [threadId, items] of Object.entries(incomingTimelines)) {
          if (Array.isArray(items)) {
            timelinesByThread[threadId] = items.map(normalizeTimelineItem);
          }
        }
      }

      const tokenUsageByThread = { ...state.tokenUsageByThread };
      const incomingTokens = payload.tokenUsageByThread || payload.token_usage_by_thread;
      if (incomingTokens && typeof incomingTokens === 'object') {
        for (const [threadId, usage] of Object.entries(incomingTokens)) {
          const normalized = normalizeTokenUsage(usage);
          if (normalized) tokenUsageByThread[threadId] = normalized;
        }
      }
      const activeTokenUsage = normalizeTokenUsage(payload.tokenUsage || payload.token_usage);
      if (activeTokenUsage && activeThreadId) {
        tokenUsageByThread[activeThreadId] = activeTokenUsage;
      }

      const diffTextByThread = { ...state.diffTextByThread };
      const incomingDiff = payload.diffTextByThread || payload.diff_text_by_thread;
      if (incomingDiff && typeof incomingDiff === 'object') {
        Object.assign(diffTextByThread, incomingDiff);
      }
      if (activeThreadId && typeof payload.diffText === 'string') {
        diffTextByThread[activeThreadId] = payload.diffText;
      }

      return {
        activeThreadId,
        threads: nextThreads,
        timelinesByThread,
        tokenUsageByThread,
        diffTextByThread,
        statuses: {
          ...state.statuses,
          ...(payload.statuses || {}),
        },
      };
    });
  };

  const loadThreadMessages = async (threadId) => {
    const id = normalizeThreadId(threadId);
    if (!id) return;
    try {
      const res = await getThreadMessages({ threadId: id, limit: 300 });
      if (!Array.isArray(res?.messages) || res.messages.length === 0) return;
      set((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          [id]: res.messages.map((message) => normalizeTimelineItem({
            id: message.id,
            role: message.role,
            text: message.content,
            createdAt: message.createdAt || message.created_at,
          })),
        },
      }));
    } catch (error) {
      addWarning('error', 'thread.messages.failed', { threadId: id, error: error.message });
    }
  };

  const applyBridgePatch = (method, payload) => {
    const threadId = normalizeThreadId(payload.threadId || payload.thread_id || payload.agent_id);
    if (!threadId) return;

    const sequence = normalizeString(payload.sequence);
    const previousSequence = sequencesByThread.get(threadId) || '';
    if (sequence) {
      if (previousSequence && compareSequence(sequence, previousSequence) <= 0) {
        addWarning('warn', 'thread.patch.stale', { threadId, sequence, previousSequence });
        return;
      }
      if (previousSequence && !isNextSequence(previousSequence, sequence)) {
        addWarning('warn', 'thread.patch.gap', { threadId, sequence, previousSequence });
      }
      sequencesByThread.set(threadId, sequence);
    }

    const timelineItems = payload.timelineItems || payload.timeline_items;
    const tokenUsage = normalizeTokenUsage(payload.tokenUsage || payload.token_usage);
    const diffText = typeof payload.diffText === 'string' ? payload.diffText : payload.diff_text;

    set((state) => {
      const timelinesByThread = { ...state.timelinesByThread };
      if (Array.isArray(timelineItems)) {
        timelinesByThread[threadId] = timelineItems.map(normalizeTimelineItem);
      }

      const tokenUsageByThread = { ...state.tokenUsageByThread };
      if (tokenUsage) tokenUsageByThread[threadId] = tokenUsage;

      const diffTextByThread = { ...state.diffTextByThread };
      if (typeof diffText === 'string') diffTextByThread[threadId] = diffText;

      return {
        timelinesByThread,
        tokenUsageByThread,
        diffTextByThread,
        activityEntries: [{
          id: `${method}-${Date.now()}`,
          method,
          threadId,
          timestamp: new Date().toISOString(),
        }, ...state.activityEntries].slice(0, 120),
      };
    });
  };

  const handleBridgeEvent = (evt) => {
    const method = normalizeString(evt?.method || evt?.type);
    const payload = evt?.payload || evt?.params || evt?.data || {};
    if (!method) return;

    if (method === 'ui/thread/patch') {
      applyBridgePatch(method, payload);
      return;
    }
    if (method === 'thread/tokenUsage/updated' || method === 'thread/tokenusage/updated') {
      const threadId = normalizeThreadId(payload.threadId || payload.thread_id);
      const usage = normalizeTokenUsage(payload);
      if (threadId && usage) {
        set((state) => ({
          tokenUsageByThread: {
            ...state.tokenUsageByThread,
            [threadId]: usage,
          },
        }));
      }
      return;
    }
    if (method === 'rpc.failed' || method.endsWith('/failed')) {
      addWarning('error', method, payload);
    }
  };

  const activeThreadRPC = async (action, rpc) => {
    const threadId = normalizeThreadId(get().activeThreadId);
    if (!threadId) return false;
    const cwd = requireCwd(action);
    try {
      await rpc({ cwd, threadId });
      return true;
    } catch (error) {
      addWarning('error', `${action}.failed`, { threadId, error: error.message });
      throw error;
    }
  };

  return {
    ...baseState,

    initializeEvents: () => {
      if (bridgeUnsubscribe) return;
      bridgeUnsubscribe = onBridgeEvent(handleBridgeEvent);
    },

    destroy: () => {
      if (bridgeUnsubscribe) {
        bridgeUnsubscribe();
        bridgeUnsubscribe = null;
      }
      sequencesByThread.clear();
    },

    bootstrap: async () => {
      set({ bootstrapStatus: 'loading', error: '' });
      get().initializeEvents();
      try {
        const config = await readConfig();
        const cwd = normalizePath(config?.cwd);
        if (!cwd || cwd === '.') {
          throw new Error('frontend-app bootstrap cwd is required');
        }
        await getWindowBootstrap();
        set({ cwd, activeProject: cwd });
        const projects = await getProjects({ cwd });
        applyProjects(projects, cwd);
        const sidebar = await getSidebarState({ cwd });
        applySnapshot(sidebar);
        const activeThreadId = normalizeThreadId(useClientStore.getState().activeThreadId);
        if (activeThreadId) {
          await get().syncThreadState(activeThreadId);
        }
        set({ bootstrapStatus: 'ready' });
      } catch (error) {
        set({ bootstrapStatus: 'failed', error: error.message });
        addWarning('error', 'app.bootstrap.failed', { error: error.message });
        throw error;
      }
    },

    syncThreadState: async (threadId) => {
      const id = normalizeThreadId(threadId);
      if (!id) return;
      const cwd = requireCwd('thread.sync');
      const snapshot = await getThreadState({ cwd, threadId: id, includeDiff: true });
      applySnapshot(snapshot, { preferredActiveThreadId: id });
      await loadThreadMessages(id);
    },

    setActivePage: (activePage) => set({ activePage }),
    setDraft: (draft) => set({ draft }),
    setPermission: (permission) => set({ permission }),
    setRightPanelWidth: (rightPanelWidth) => set({ rightPanelWidth }),

    setActiveThread: async (threadId) => {
      const id = normalizeThreadId(threadId);
      set({ activeThreadId: id });
      if (id) await get().syncThreadState(id);
    },

    newThread: () => {
      set({ activeThreadId: '', draft: '', attachments: [] });
    },

    selectFilesForComposer: async () => {
      try {
        const picked = await selectFiles();
        const attachments = (Array.isArray(picked) ? picked : [])
          .map(normalizeAttachment)
          .filter(Boolean);
        set((state) => ({
          attachments: [...state.attachments, ...attachments],
        }));
        return attachments;
      } catch (error) {
        addWarning('error', 'attachments.select.failed', { error: error.message });
        throw error;
      }
    },

    removeAttachment: (path) => {
      const target = normalizeString(path);
      set((state) => ({
        attachments: state.attachments.filter((item) => item.path !== target),
      }));
    },

    sendDraft: async () => {
      const cwd = requireCwd('send message');
      const text = normalizeString(get().draft);
      const attachments = get().attachments.map(normalizeAttachment).filter(Boolean);
      const input = buildTurnInput(text, attachments);
      if (input.length === 0) return false;

      const previousDraft = get().draft;
      const previousAttachments = get().attachments;
      const previousThreadId = normalizeThreadId(get().activeThreadId);
      const launchIntentId = createLaunchIntentId();
      const provisionalThreadId = previousThreadId || launchIntentId;
      const optimisticItem = {
        id: `user-${launchIntentId}`,
        role: 'user',
        text,
        attachments,
        time: new Date().toISOString(),
        done: true,
      };

      set((state) => ({
        sending: true,
        error: '',
        draft: '',
        attachments: [],
        activeThreadId: provisionalThreadId,
        timelinesByThread: {
          ...state.timelinesByThread,
          [provisionalThreadId]: [
            ...(state.timelinesByThread[provisionalThreadId] || []),
            optimisticItem,
          ],
        },
      }));

      try {
        let threadId = previousThreadId;
        if (!threadId) {
          const thread = await startThread({
            cwd,
            name: text.slice(0, 40),
            provider: get().provider || DEFAULT_PROVIDER,
            deferSpawn: true,
            launchIntentId,
            optimisticUserMessage: text,
            skipInitialRuntimeSync: true,
          });
          threadId = normalizeThreadId(thread?.threadId || thread?.id);
          if (!threadId) throw new Error('thread/start response missing threadId');
          set((state) => {
            const provisionalTimeline = state.timelinesByThread[provisionalThreadId] || [];
            const timelinesByThread = { ...state.timelinesByThread };
            delete timelinesByThread[provisionalThreadId];
            timelinesByThread[threadId] = provisionalTimeline;
            return {
              activeThreadId: threadId,
              timelinesByThread,
              threads: [
                { id: threadId, name: text.slice(0, 40) || '新对话', provider: get().provider || DEFAULT_PROVIDER, status: '工作中' },
                ...state.threads.filter((item) => item.id !== threadId),
              ],
            };
          });
        }

        await startTurn({
          cwd,
          threadId,
          input,
          manualSkillSelection: false,
        });
        set({ sending: false });
        return true;
      } catch (error) {
        set((state) => {
          const timelinesByThread = { ...state.timelinesByThread };
          const activeTimeline = timelinesByThread[state.activeThreadId] || [];
          timelinesByThread[state.activeThreadId] = activeTimeline.filter((item) => item.id !== optimisticItem.id);
          if (!previousThreadId) {
            delete timelinesByThread[provisionalThreadId];
          }
          return {
            sending: false,
            draft: previousDraft,
            attachments: previousAttachments,
            activeThreadId: previousThreadId,
            timelinesByThread,
            error: error.message,
          };
        });
        addWarning('error', 'thread.send.failed', { error: error.message });
        throw error;
      }
    },

    interruptActiveThread: () => activeThreadRPC('thread.interrupt', interruptTurn),
    compactActiveThread: () => activeThreadRPC('thread.compact', compactThread),
    recoverActiveThread: () => activeThreadRPC('thread.recover', recoverThread),

    renameThread: async (threadId, name) => {
      const id = normalizeThreadId(threadId);
      const nextName = normalizeString(name);
      if (!id || !nextName) return false;
      const cwd = requireCwd('thread.rename');
      await renameThreadRPC({ cwd, threadId: id, name: nextName });
      set((state) => ({
        threads: state.threads.map((thread) => (thread.id === id ? { ...thread, name: nextName } : thread)),
      }));
      return true;
    },

    archiveThread: async (threadId, archived) => {
      const id = normalizeThreadId(threadId);
      if (!id) return false;
      const cwd = requireCwd('thread.archive');
      await setPreference({
        cwd,
        key: `archivedThreadAtById.${id}`,
        value: archived ? new Date().toISOString() : null,
      });
      set((state) => ({
        threads: state.threads.map((thread) => (thread.id === id ? { ...thread, archived: Boolean(archived) } : thread)),
      }));
      return true;
    },

    addWarning,
  };
});

export function resetClientStoreForTests(patch = {}) {
  useClientStore.getState().destroy();
  useClientStore.setState(stateWithPatch(patch));
}

registerBridgeLogStore({
  info: () => {},
  debug: () => {},
  warn: (event, fields) => useClientStore.getState().addWarning('warn', event, fields),
  error: (event, fields) => useClientStore.getState().addWarning('error', event, fields),
});
