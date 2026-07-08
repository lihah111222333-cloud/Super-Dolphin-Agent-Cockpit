import { getSidebarState } from '../../../../../shared/api/backendApi.js';
import { normalizePath } from './clientStoreUtils.js';
import { actionNotice } from './clientStoreSendModel.js';

function refreshEntryIsCurrent(runtime, cwd, refreshEntry) {
  return !refreshEntry.cancelled && runtime.sidebarRefreshesByCwd.get(cwd) === refreshEntry;
}

function refreshSeqIsCurrent(runtime, cwd, seq) {
  return seq === runtime.sidebarRefreshSeq && normalizePath(runtime.currentChatCwd()) === cwd;
}

function maybeApplyCachedSidebar(runtime, cwd, options) {
  if (!options.clearSurface) return;
  const cachedSidebar = runtime.sidebarSnapshotsByCwd.get(cwd);
  runtime.clearChatSurfaceForCwdSwitch(cwd, {
    preserveActiveThreadId: options.preserveActiveThreadId === true,
  });
  if (!cachedSidebar) return;
  runtime.applySnapshot(cachedSidebar, {
    autoSelectThread: false,
    scopeCwd: cwd,
    preserveActiveThreadId: options.preserveActiveThreadId === true,
    preserveLiveBusyStatus: true,
  });
}

function chatSurfaceLoadedPatch(state, cwd) {
  return {
    chatSurfaceLoadingCwd: state.chatSurfaceLoadingCwd === cwd ? '' : state.chatSurfaceLoadingCwd,
  };
}

function chatSurfaceRefreshFailedPatch(state, cwd, error) {
  return {
    chatSurfaceLoadingCwd: state.chatSurfaceLoadingCwd === cwd ? '' : state.chatSurfaceLoadingCwd,
    actionNotice: actionNotice(`刷新会话列表失败：${error.message}`, 'error'),
  };
}

function applySidebarSnapshot(runtime, cwd, options, sidebar) {
  runtime.cacheSidebarSnapshot(cwd, sidebar);
  runtime.applySnapshot(sidebar, {
    autoSelectThread: false,
    scopeCwd: cwd,
    preserveActiveThreadId: options.preserveActiveThreadId === true,
    preserveLiveBusyStatus: true,
  });
  if (options.clearSurface) {
    runtime.set((state) => chatSurfaceLoadedPatch(state, cwd));
  }
}

function handleSidebarRefreshFailure(runtime, cwd, options, error) {
  if (options.clearSurface) {
    runtime.set((state) => chatSurfaceRefreshFailedPatch(state, cwd, error));
  }
  runtime.addWarning('error', 'thread.sidebar.refresh.failed', { cwd, error: error.message });
}

function performSidebarRefreshForCwd(runtime, cwd, options, refreshEntry) {
  const seq = ++runtime.sidebarRefreshSeq;
  maybeApplyCachedSidebar(runtime, cwd, options);
  return getSidebarState({ cwd })
    .then((sidebar) => {
      if (!refreshEntryIsCurrent(runtime, cwd, refreshEntry)) return;
      if (!refreshSeqIsCurrent(runtime, cwd, seq)) return;
      applySidebarSnapshot(runtime, cwd, options, sidebar);
    })
    .catch((error) => {
      if (!refreshEntryIsCurrent(runtime, cwd, refreshEntry)) return;
      if (!refreshSeqIsCurrent(runtime, cwd, seq)) return;
      handleSidebarRefreshFailure(runtime, cwd, options, error);
    });
}

function runSidebarRefreshEntry(runtime, cwd, refreshEntry, options) {
  refreshEntry.pending = false;
  refreshEntry.clearSurface = options.clearSurface === true;
  void performSidebarRefreshForCwd(runtime, cwd, options, refreshEntry)
    .finally(() => {
      if (!refreshEntryIsCurrent(runtime, cwd, refreshEntry)) return;
      if (refreshEntry.pending) {
        runSidebarRefreshEntry(runtime, cwd, refreshEntry, { preserveActiveThreadId: true });
        return;
      }
      runtime.sidebarRefreshesByCwd.delete(cwd);
    });
}

function createSidebarRefreshEntry(runtime, cwd, options) {
  const refreshEntry = {
    pending: false,
    cancelled: false,
    clearSurface: options.clearSurface === true,
  };
  runtime.sidebarRefreshesByCwd.set(cwd, refreshEntry);
  runSidebarRefreshEntry(runtime, cwd, refreshEntry, options);
}

function replaceSidebarRefreshEntry(runtime, cwd, existingRefresh, options) {
  existingRefresh.cancelled = true;
  createSidebarRefreshEntry(runtime, cwd, {
    ...options,
    clearSurface: true,
  });
}

function queueExistingSidebarRefresh(runtime, cwd, existingRefresh, options) {
  if (options.clearSurface === true && !existingRefresh.clearSurface) {
    replaceSidebarRefreshEntry(runtime, cwd, existingRefresh, options);
    return;
  }
  existingRefresh.pending = true;
}

function refreshSidebarSnapshotForCwdInBackground(runtime, cwdValue, options = {}) {
  const cwd = normalizePath(cwdValue);
  if (!cwd || cwd === '.') {
    throw new Error('frontend-app: cwd is required for project chat refresh');
  }
  const existingRefresh = runtime.sidebarRefreshesByCwd.get(cwd);
  if (existingRefresh) {
    queueExistingSidebarRefresh(runtime, cwd, existingRefresh, options);
    return;
  }
  createSidebarRefreshEntry(runtime, cwd, options);
}

function attachSidebarRuntime(runtime) {
  const refreshChatSurfaceForCwdInBackground = (cwdValue, options = {}) => {
    refreshSidebarSnapshotForCwdInBackground(runtime, cwdValue, {
      clearSurface: true,
      preserveActiveThreadId: options.preserveActiveThreadId === true,
    });
  };

  const refreshActiveChatSidebarInBackground = () => {
    const cwd = runtime.currentChatCwd();
    if (!cwd || cwd === '.') {
      runtime.addWarning('warn', 'thread.sidebar.refresh.skipped', { reason: 'missing_cwd' });
      return;
    }
    refreshSidebarSnapshotForCwdInBackground(runtime, cwd, { preserveActiveThreadId: true });
  };

  Object.assign(runtime, {
    refreshSidebarSnapshotForCwdInBackground: (cwdValue, options = {}) =>
      refreshSidebarSnapshotForCwdInBackground(runtime, cwdValue, options),
    refreshChatSurfaceForCwdInBackground,
    refreshActiveChatSidebarInBackground,
  });
}

export {
  attachSidebarRuntime,
  chatSurfaceLoadedPatch,
  chatSurfaceRefreshFailedPatch,
  refreshEntryIsCurrent,
  refreshSeqIsCurrent,
};
