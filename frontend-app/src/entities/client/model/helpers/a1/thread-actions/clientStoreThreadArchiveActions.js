import {
  archiveThread as archiveThreadRPC,
  setPreference,
  unarchiveThread as unarchiveThreadRPC,
} from "../../../../../../shared/api/backendApi.js";
import {
  archiveThreadFailureState,
  archiveThreadOptimisticState,
} from "../../threadListMutations.js";
import { actionNotice } from "../clientStoreSendModel.js";
import { backendThreadIdForArchiveState } from "../clientStoreRuntimeThreadModel.js";
import { clockNowMillis } from "../clientStoreUtils.js";

function threadArchiveLoadingClearedPatch(state, id) {
  return {
    threadArchiveLoadingByThread: {
      ...state.threadArchiveLoadingByThread,
      [id]: false,
    },
  };
}
async function writeThreadArchivePreference(cwd, id, archivedAt) {
  await setPreference({
    cwd,
    key: `archivedThreadAtById.${id}`,
    value: archivedAt > 0 ? archivedAt : null,
  });
}
async function applyThreadArchiveRPC(id, archived) {
  if (archived) {
    await archiveThreadRPC({ threadId: id });
    return;
  }
  await unarchiveThreadRPC({ threadId: id });
}
function archiveFailureNotice(archived, _message) {
  const action = archived ? "归档" : "恢复";
  return actionNotice(`${action}会话失败，请重试。`, "error");
}
function archivePreferenceFailureNotice(archived, _message) {
  const action = archived ? "归档" : "恢复";
  return actionNotice(`${action}偏好保存失败，请重试。`, "error");
}
async function archiveThreadAction(runtime, threadId, archived) {
  const id = backendThreadIdForArchiveState(runtime.get(), threadId);
  if (!id) return false;
  const cwd = runtime.requireCwd("thread.archive");
  if (runtime.get().threadArchiveLoadingByThread?.[id]) return false;
  const originalThreads = runtime.get().threads;
  const originalActiveThreadId = runtime.get().activeThreadId;
  const archivedAt = archived ? clockNowMillis() : 0;
  runtime.set((state) =>
    archiveThreadOptimisticState(state, {
      id,
      archived,
      archivedAt,
      timestamp: clockNowMillis(),
    }),
  );
  try {
    await applyThreadArchiveRPC(id, archived);
  } catch (error) {
    const message = error?.message || String(error);
    runtime.set((state) =>
      archiveThreadFailureState(state, {
        id,
        originalThreads,
        originalActiveThreadId,
        actionNotice: archiveFailureNotice(archived, message),
      }),
    );
    runtime.addWarning(
      "error",
      `thread.${archived ? "archive" : "unarchive"}.failed`,
      { threadId: id, error: "action failure; see Health diagnostic ID" },
    );
    throw error;
  }
  runtime.set((state) => threadArchiveLoadingClearedPatch(state, id));
  try {
    await writeThreadArchivePreference(cwd, id, archivedAt);
  } catch (error) {
    const message = error?.message || String(error);
    runtime.set({
      actionNotice: archivePreferenceFailureNotice(archived, message),
    });
    runtime.addWarning(
      "error",
      `thread.${archived ? "archive" : "unarchive"}.preference.failed`,
      { threadId: id, error: "action failure; see Health diagnostic ID" },
    );
    throw error;
  }
  runtime.set({
    actionNotice: actionNotice(
      archived ? "线程已归档" : "线程已恢复到列表",
      "success",
    ),
  });
  return true;
}

export function createThreadArchiveActions(runtime) {
  return {
    archiveThread: (threadId, archived) =>
      archiveThreadAction(runtime, threadId, archived),
  };
}
