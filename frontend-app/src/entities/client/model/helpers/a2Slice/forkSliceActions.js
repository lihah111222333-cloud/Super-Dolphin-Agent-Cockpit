import { firstOptionalPresent } from '../../contractStoreModel.js';
import { recordFrontendHealth } from '../../../../../shared/diagnostics/frontendHealthStore.js';
import { diagnosticIdFactoryForError, publicErrorForAction } from '../../../../../shared/ui/publicError.js';
import {
  buildForkKickoffInput,
  FORK_KICKOFF_PROMPT,
} from '../threadFork.js';
import { markForkKickoffFailedState } from '../../threadForkState.js';

const FORK_SHARED_FILES_FAILURE_MESSAGE = '共享文件列表暂时不可用，请稍后重试。';
const FORK_SUBMIT_FAILURE_MESSAGE = '创建继承对话失败，请稍后重试。';
const FORK_KICKOFF_FAILURE_MESSAGE = '已创建继承对话，但开场消息暂时无法发送。';

function recordForkFailure(actionId, error) {
  const publicError = publicErrorForAction(actionId, {
    diagnosticIdFactory: diagnosticIdFactoryForError(error),
  });
  recordFrontendHealth({ actionId, publicError });
}

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

function forkDraftSharedFilesFailedState(latest, sourceThreadId) {
  if (latest.forkDraft.sourceThreadId !== sourceThreadId) return {};
  return {
    forkDraft: {
      ...latest.forkDraft,
      loadingSharedFiles: false,
      error: FORK_SHARED_FILES_FAILURE_MESSAGE,
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

function forkDraftSubmitFailedState(latest, actionNotice) {
  return {
    forkDraft: {
      ...latest.forkDraft,
      submitting: false,
      error: FORK_SUBMIT_FAILURE_MESSAGE,
    },
    actionNotice: actionNotice(FORK_SUBMIT_FAILURE_MESSAGE, 'error'),
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
  runtime.set((latest) => forkDraftSharedFilesFailedState(latest, sourceThreadId));
  runtime.addWarning('warn', 'thread.fork.shared_files.failed', { threadId: sourceThreadId, error });
  recordForkFailure('thread.fork.open', error);
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
    runtime.set((current) => ({
      ...markForkKickoffFailedState(current, threadId, FORK_KICKOFF_FAILURE_MESSAGE),
      actionNotice: deps.actionNotice(FORK_KICKOFF_FAILURE_MESSAGE, 'warning'),
    }));
    runtime.addWarning('warn', 'thread.fork.kickoff.failed', { threadId, error: kickoffError });
    recordForkFailure('thread.fork.submit', kickoffError);
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
        runtime.set((latest) => forkDraftSubmitFailedState(latest, deps.actionNotice));
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
