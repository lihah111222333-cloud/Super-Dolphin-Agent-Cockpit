import { firstOptionalPresent } from '../../contractStoreModel.js';
import {
  buildForkKickoffInput,
  FORK_KICKOFF_PROMPT,
} from '../threadFork.js';
import { markForkKickoffFailedState } from '../../threadForkState.js';

function optionalUiArray() {
  return [];
}

function forkDraftSharedFilesLoadedState(latest, sourceThreadId, availableSharedFiles, mergeForkSharedFilesWithSelected) {
  if (latest.forkDraft.sourceThreadId !== sourceThreadId) return {};
  const selectedPaths = latest.forkDraft.sharedFilePaths || optionalUiArray();
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
}

function forkDraftSharedFilesFailedState(latest, sourceThreadId, message) {
  if (latest.forkDraft.sourceThreadId !== sourceThreadId) return {};
  return {
    forkDraft: {
      ...latest.forkDraft,
      loadingSharedFiles: false,
      error: `共享文件列表加载失败：${message}`,
    },
  };
}

function forkDraftSubmittingState(latest) {
  return {
    forkDraft: {
      ...latest.forkDraft,
      submitting: true,
      error: '',
      kickoffError: '',
    },
  };
}

function forkDraftSubmitFailedState(latest, actionNotice, message) {
  return {
    forkDraft: {
      ...latest.forkDraft,
      submitting: false,
      error: message,
    },
    actionNotice: actionNotice(`创建继承对话失败：${message}`, 'error'),
  };
}

function toggledForkSharedFileState(state, target) {
  const selected = new Set(state.forkDraft.sharedFilePaths || optionalUiArray());
  if (selected.has(target)) selected.delete(target);
  else selected.add(target);
  return {
    forkDraft: {
      ...state.forkDraft,
      sharedFilePaths: Array.from(selected),
    },
  };
}

async function loadForkDraftSharedFiles(runtime, deps, sourceThreadId) {
  const response = await deps.listSharedFiles();
  const availableSharedFiles = deps.normalizeForkSharedFiles(response);
  runtime.set((latest) => forkDraftSharedFilesLoadedState(
    latest,
    sourceThreadId,
    availableSharedFiles,
    deps.mergeForkSharedFilesWithSelected,
  ));
}

function reportForkDraftSharedFilesFailure(runtime, sourceThreadId, error) {
  const message = error.message || String(error);
  runtime.set((latest) => forkDraftSharedFilesFailedState(latest, sourceThreadId, message));
  runtime.addWarning('warn', 'thread.fork.shared_files.failed', { threadId: sourceThreadId, error: message });
}

async function loadForkKickoffContext(runtime, deps, sourceThreadId, draft) {
  const latest = runtime.get();
  const sourceThread = deps.forkSourceThread(latest, sourceThreadId);
  const provisionalName = draft.sourceTitle || deps.forkSourceTitle(sourceThread, sourceThreadId);
  const sharedFiles = await deps.loadForkSharedFiles(latest.forkDraft.sharedFilePaths);
  return { provisionalName, sharedFiles, sourceThread };
}

async function startForkThread(runtime, deps, sourceThreadId, draft) {
  const { provisionalName, sharedFiles, sourceThread } = await loadForkKickoffContext(
    runtime,
    deps,
    sourceThreadId,
    draft,
  );
  const input = buildForkKickoffInput(sharedFiles);
  const cwd = runtime.requireCwd('fork thread');
  const response = await deps.forkThread({ threadId: sourceThreadId });
  if (response.kickoffState !== 'created_only') {
    throw new Error(`thread/fork unsupported kickoff state ${response.kickoffState}`);
  }
  const identity = deps.normalizeThreadIdentity(response.thread);
  if (!identity.threadId) throw new Error('thread/fork response missing thread.id');
  runtime.set((current) => deps.addForkThreadState({
    state: current,
    threadId: identity.threadId,
    sourceThreadId,
    sourceThread,
    identity,
    provisionalName,
    kickoffText: FORK_KICKOFF_PROMPT,
  }));
  return {
    cwd,
    threadId: identity.threadId,
    input,
  };
}

async function sendForkKickoff(runtime, deps, cwd, threadId, input) {
  try {
    await deps.startTurn({
      cwd,
      threadId,
      input,
      manualSkillSelection: false,
    });
  }
  catch (kickoffError) {
    const message = kickoffError.message || String(kickoffError);
    runtime.set((current) => ({
      ...markForkKickoffFailedState(current, threadId, message),
      actionNotice: deps.actionNotice(`已创建继承对话，但开场消息发送失败：${message}`, 'warning'),
    }));
    runtime.addWarning('warn', 'thread.fork.kickoff.failed', { threadId, error: message });
  }
}

function createOpenForkDraftAction(runtime, deps) {
  return async (options = {}) => {
    const state = runtime.get();
    const sourceThreadId = deps.backendThreadIdForState(state, state.activeThreadId);
    if (!sourceThreadId) {
      runtime.notifyAction('当前没有可继承的后端会话', 'warning');
      return false;
    }
    const seedSharedFilePath = deps.normalizeString(firstOptionalPresent(options?.sharedFilePath, options?.seedSharedFilePath));
    const thread = deps.forkSourceThread(state, sourceThreadId);
    const sourceTitle = deps.forkSourceTitle(thread, sourceThreadId);
    const cachedFiles = deps.cachedForkSharedFiles(state);
    const sharedFilePaths = deps.initialForkSharedFilePaths(state, cachedFiles, seedSharedFilePath);
    runtime.set({
      forkDraft: {
        ...deps.emptyForkDraft(),
        open: true,
        sourceThreadId,
        sourceThreadName: deps.normalizeString(thread?.name),
        sourceTitle,
        availableSharedFiles: deps.mergeForkSharedFilesWithSelected(cachedFiles, sharedFilePaths),
        sharedFilePaths,
        loadingSharedFiles: true,
      },
    });

    try {
      await loadForkDraftSharedFiles(runtime, deps, sourceThreadId);
    }
    catch (error) {
      reportForkDraftSharedFilesFailure(runtime, sourceThreadId, error);
    }
    return true;
  };
}

function createToggleForkDraftSharedFileAction(runtime, deps) {
  return (path) => {
    const target = deps.normalizeString(path);
    if (!target) return false;
    runtime.set((state) => toggledForkSharedFileState(state, target));
    return true;
  };
}

function createSubmitForkThreadAction(runtime, deps) {
  return async () => {
    const state = runtime.get();
    const draft = state.forkDraft || deps.emptyForkDraft();
    const sourceThreadId = deps.backendThreadIdForState(state, draft.sourceThreadId);
    if (!draft.open || !sourceThreadId) throw new Error('fork thread: source thread is required');
    if (draft.submitting) return '';

    runtime.set(forkDraftSubmittingState);

    let newThreadId = '';
    try {
      const started = await startForkThread(runtime, deps, sourceThreadId, draft);
      newThreadId = started.threadId;
      await sendForkKickoff(runtime, deps, started.cwd, started.threadId, started.input);
      return newThreadId;
    }
    catch (error) {
      if (!newThreadId) {
        const message = error.message || String(error);
        runtime.set((latest) => forkDraftSubmitFailedState(latest, deps.actionNotice, message));
      }
      throw error;
    }
  };
}

function createForkActionSet(runtime, deps) {
  return {
    openForkDraft: createOpenForkDraftAction(runtime, deps),
    closeForkDraft: () => {
      runtime.set({ forkDraft: deps.emptyForkDraft() });
      return true;
    },
    toggleForkDraftSharedFile: createToggleForkDraftSharedFileAction(runtime, deps),
    submitForkThread: createSubmitForkThreadAction(runtime, deps),
  };
}

export { createForkActionSet };
