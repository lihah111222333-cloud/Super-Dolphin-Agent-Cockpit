import {
  buildSeedInstructionsFromSummary,
  extractTimelineSummary,
  FORK_KICKOFF_PROMPT,
} from './threadFork.js';

/*
 * fork slice 从当前对话创建继承会话。
 * 它只把 timeline 摘要和选中的 shared files 带进新 thread，不伪造回复。
 */

export function createForkSlice(runtime, deps) {
  const {
    actionNotice,
    addForkThreadState,
    backendThreadIdForState,
    cachedForkSharedFiles,
    createLaunchIntentId,
    emptyForkDraft,
    forkSourceThread,
    forkSourceTitle,
    initialForkSharedFilePaths,
    listSharedFiles,
    loadForkSharedFiles,
    mergeForkSharedFilesWithSelected,
    normalizeForkSharedFiles,
    normalizeString,
    normalizeThreadIdentity,
    resolveLaunchPreferences,
    startThread,
    startTurn,
  } = deps;

  return {
    openForkDraft: async (options = {}) => {
      const state = runtime.get();
      const sourceThreadId = backendThreadIdForState(state, state.activeThreadId);
      if (!sourceThreadId) {
        runtime.notifyAction('当前没有可继承的后端会话', 'warning');
        return false;
      }
      const seedSharedFilePath = normalizeString(options?.sharedFilePath || options?.seedSharedFilePath);
      const thread = forkSourceThread(state, sourceThreadId);
      const sourceTitle = forkSourceTitle(thread, sourceThreadId);
      const cachedFiles = cachedForkSharedFiles(state);
      const sharedFilePaths = initialForkSharedFilePaths(state, cachedFiles, seedSharedFilePath);
      runtime.set({
        forkDraft: {
          ...emptyForkDraft(),
          open: true,
          sourceThreadId,
          sourceThreadName: normalizeString(thread?.name),
          sourceTitle,
          availableSharedFiles: mergeForkSharedFilesWithSelected(cachedFiles, sharedFilePaths),
          sharedFilePaths,
          loadingSharedFiles: true,
        },
      });

      try {
        const response = await listSharedFiles();
        const availableSharedFiles = normalizeForkSharedFiles(response);
        runtime.set((latest) => {
          if (latest.forkDraft.sourceThreadId !== sourceThreadId) return {};
          const selectedPaths = latest.forkDraft.sharedFilePaths || [];
          const selected = new Set(selectedPaths);
          const mergedSharedFiles = mergeForkSharedFilesWithSelected(availableSharedFiles, selectedPaths);
          return {
            forkDraft: {
              ...latest.forkDraft,
              availableSharedFiles: mergedSharedFiles,
              sharedFilePaths: mergedSharedFiles
                .map((file) => file.path)
                .filter((path) => selected.has(path)),
              loadingSharedFiles: false,
              error: '',
            },
          };
        });
      }
      catch (error) {
        const message = error.message || String(error);
        runtime.set((latest) => {
          if (latest.forkDraft.sourceThreadId !== sourceThreadId) return {};
          return {
            forkDraft: {
              ...latest.forkDraft,
              loadingSharedFiles: false,
              error: `共享文件列表加载失败：${message}`,
            },
          };
        });
        runtime.addWarning('warn', 'thread.fork.shared_files.failed', { threadId: sourceThreadId, error: message });
      }
      return true;
    },

    closeForkDraft: () => {
      runtime.set({ forkDraft: emptyForkDraft() });
      return true;
    },

    toggleForkDraftSharedFile: (path) => {
      const target = normalizeString(path);
      if (!target) return false;
      runtime.set((state) => {
        const selected = new Set(state.forkDraft.sharedFilePaths || []);
        if (selected.has(target)) selected.delete(target);
        else selected.add(target);
        return {
          forkDraft: {
            ...state.forkDraft,
            sharedFilePaths: Array.from(selected),
          },
        };
      });
      return true;
    },

    submitForkThread: async () => {
      const state = runtime.get();
      const draft = state.forkDraft || emptyForkDraft();
      const sourceThreadId = backendThreadIdForState(state, draft.sourceThreadId);
      if (!draft.open || !sourceThreadId) throw new Error('fork thread: source thread is required');
      if (draft.submitting) return '';

      runtime.set((latest) => ({
        forkDraft: {
          ...latest.forkDraft,
          submitting: true,
          error: '',
          kickoffError: '',
        },
      }));

      let newThreadId = '';
      try {
        const latest = runtime.get();
        const sourceThread = forkSourceThread(latest, sourceThreadId);
        const sourceTitle = draft.sourceTitle || forkSourceTitle(sourceThread, sourceThreadId);
        const summary = extractTimelineSummary(latest.timelinesByThread?.[sourceThreadId] || []);
        const sharedFiles = await loadForkSharedFiles(latest.forkDraft.sharedFilePaths);
        if (!summary && sharedFiles.length === 0) {
          throw new Error('当前会话没有可用上下文，且未选择共享文件，无法创建继承对话。');
        }
        const baseInstructions = buildSeedInstructionsFromSummary(summary, {
          sourceTitle,
          sharedFiles,
        });
        const cwd = runtime.requireCwd('fork thread');
        const launchPreferences = await resolveLaunchPreferences(cwd);
        const response = await startThread({
          cwd,
          name: sourceTitle,
          ...launchPreferences,
          deferSpawn: true,
          launchIntentId: createLaunchIntentId(),
          baseInstructions,
        });
        const identity = normalizeThreadIdentity(response);
        if (!identity.threadId) throw new Error('thread/start response missing threadId');
        newThreadId = identity.threadId;
        runtime.set((current) => addForkThreadState(current, newThreadId, identity, launchPreferences, sourceTitle, FORK_KICKOFF_PROMPT));

        try {
          await startTurn({
            cwd,
            threadId: newThreadId,
            input: [{ type: 'text', text: FORK_KICKOFF_PROMPT }],
            manualSkillSelection: false,
          });
        }
        catch (kickoffError) {
          const message = kickoffError.message || String(kickoffError);
          runtime.set({
            actionNotice: actionNotice(`已创建继承对话，但开场消息发送失败：${message}`, 'warning'),
          });
          runtime.addWarning('warn', 'thread.fork.kickoff.failed', { threadId: newThreadId, error: message });
        }
        return newThreadId;
      }
      catch (error) {
        if (!newThreadId) {
          const message = error.message || String(error);
          runtime.set((latest) => ({
            forkDraft: {
              ...latest.forkDraft,
              submitting: false,
              error: message,
            },
            actionNotice: actionNotice(`创建继承对话失败：${message}`, 'error'),
          }));
        }
        throw error;
      }
    },


  };
}
