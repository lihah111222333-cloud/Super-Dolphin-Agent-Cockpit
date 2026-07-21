import {
  deleteThread as deleteThreadRPC,
  setPreference,
} from "../../../../../../shared/api/backendApi.js";
import { actionNotice } from "../clientStoreSendModel.js";
import { backendThreadIdForArchiveState } from "../clientStoreRuntimeThreadModel.js";
import { clockNowMillis, mapSidebarThreadCache } from "../clientStoreUtils.js";

function normalizedDeleteThreadIds(runtime, threadIds) {
  return [
    ...new Set(
      (Array.isArray(threadIds) ? threadIds : [])
        .map((threadId) =>
          backendThreadIdForArchiveState(runtime.get(), threadId),
        )
        .filter(Boolean),
    ),
  ];
}
async function deleteThreadsById(runtime, ids) {
  const deletedIds = [];
  const failedIds = [];
  const failureCauses = [];
  for (const id of ids) {
    try {
      await deleteThreadRPC({ threadId: id });
      deletedIds.push(id);
    } catch (error) {
      failedIds.push(id);
      failureCauses.push(error);
      runtime.addWarning("warn", "thread.delete.failed", {
        threadId: id,
        error: "action failure; see Health diagnostic ID",
      });
    }
  }
  return { deletedIds, failedIds, failureCauses };
}
function clearArchivedPreference(cwd, id) {
  return setPreference({ cwd, key: `archivedThreadAtById.${id}`, value: null });
}
function deletedThreadsNotice(deletedIds, failedIds) {
  return actionNotice(
    failedIds.length > 0
      ? `已删除 ${deletedIds.length} 个无用会话，${failedIds.length} 个失败`
      : `已删除 ${deletedIds.length} 个无用会话`,
    failedIds.length > 0 ? "warning" : "success",
  );
}
function deletedThreadsPatch(state, deletedIds, failedIds) {
  const deletedSet = new Set(deletedIds);
  const threadIsRetained = (thread) => !deletedSet.has(thread.id);
  return {
    activeThreadId: deletedSet.has(state.activeThreadId)
      ? ""
      : state.activeThreadId,
    threads: state.threads.filter(threadIsRetained),
    sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) =>
      threads.filter(threadIsRetained),
    ),
    actionNotice: deletedThreadsNotice(deletedIds, failedIds),
    lastListMutationTime: clockNowMillis(),
  };
}
async function deleteStaleThreadsAction(runtime, threadIds) {
  const ids = normalizedDeleteThreadIds(runtime, threadIds);
  if (ids.length === 0) return { deleted: 0, failed: 0 };
  const cwd = runtime.requireCwd("thread.delete");
  const { deletedIds, failedIds, failureCauses } = await deleteThreadsById(
    runtime,
    ids,
  );
  if (deletedIds.length > 0) {
    deletedIds.forEach((id) => runtime.clearTurnRuntimeForThread(id));
    runtime.set((state) => deletedThreadsPatch(state, deletedIds, failedIds));
    await Promise.all(deletedIds.map((id) => clearArchivedPreference(cwd, id)));
  } else {
    runtime.set({
      actionNotice: actionNotice(
        `删除无用会话失败：${failedIds.length} 个失败`,
        "error",
      ),
    });
  }
  if (failureCauses.length > 0)
    throw new AggregateError(
      failureCauses,
      `${failureCauses.length} thread delete action(s) failed`,
    );
  return { deleted: deletedIds.length, failed: failedIds.length };
}

export function createThreadDeleteActions(runtime) {
  return {
    deleteStaleThreads: (threadIds) =>
      deleteStaleThreadsAction(runtime, threadIds),
  };
}
