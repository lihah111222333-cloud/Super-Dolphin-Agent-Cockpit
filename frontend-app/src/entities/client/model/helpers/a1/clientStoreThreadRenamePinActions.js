import {
  renameThread as renameThreadRPC,
  setPreference,
} from "../../../../../shared/api/backendApi.js";
import { actionNotice } from "./clientStoreSendModel.js";
import {
  backendThreadIdForArchiveState,
  backendThreadIdForState,
} from "./clientStoreRuntimeThreadModel.js";
import {
  THREAD_PINS_CHAT_PREF_KEY,
  clockNowMillis,
  mapSidebarThreadCache,
  normalizeString,
  normalizeTimestampMap,
} from "./clientStoreUtils.js";
import { applyThreadRename } from "../threadListMutations.js";

function nextPinnedThreadMap(currentMap, id, pinned) {
  const nextMap = { ...currentMap };
  if (pinned) {
    delete nextMap[id];
    return nextMap;
  }
  nextMap[id] = clockNowMillis();
  return nextMap;
}
function pinnedThreadView(thread, id, pinned, nextMap) {
  if (thread.id !== id) return thread;
  return { ...thread, pinned: !pinned, pinnedAt: nextMap[id] || 0 };
}
function threadPinPatch(state, id, pinned, nextMap) {
  const applyPin = (thread) => pinnedThreadView(thread, id, pinned, nextMap);
  return {
    pinnedThreadAtById: nextMap,
    threads: state.threads.map(applyPin),
    sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) =>
      threads.map(applyPin),
    ),
    actionNotice: actionNotice(
      pinned ? "会话已取消置顶" : "会话已置顶",
      "success",
    ),
  };
}
function renamedThreadPatch(state, id, nextName) {
  return {
    threads: applyThreadRename(state.threads, id, nextName),
    sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) =>
      applyThreadRename(threads, id, nextName),
    ),
    actionNotice: actionNotice("线程已重命名", "success"),
  };
}
async function renameThreadAction(runtime, threadId, name) {
  const id = backendThreadIdForState(runtime.get(), threadId);
  const nextName = normalizeString(name);
  if (!id || !nextName) return false;
  try {
    await renameThreadRPC({ threadId: id, name: nextName });
    runtime.set((state) => renamedThreadPatch(state, id, nextName));
    return true;
  } catch (error) {
    runtime.notifyRPCFailure("重命名会话", "thread.rename.failed", error, {
      threadId: id,
    });
    throw error;
  }
}
async function toggleThreadPinAction(runtime, threadId) {
  const id = backendThreadIdForArchiveState(runtime.get(), threadId);
  if (!id) return false;
  const cwd = runtime.requireCwd("thread.pin");
  const currentMap = normalizeTimestampMap(runtime.get().pinnedThreadAtById);
  const pinned = currentMap[id] > 0;
  const nextMap = nextPinnedThreadMap(currentMap, id, pinned);
  try {
    await setPreference({
      cwd,
      key: THREAD_PINS_CHAT_PREF_KEY,
      value: nextMap,
    });
    runtime.set((state) => threadPinPatch(state, id, pinned, nextMap));
    return true;
  } catch (error) {
    runtime.notifyRPCFailure(
      pinned ? "取消置顶会话" : "置顶会话",
      "thread.pin.failed",
      error,
      { threadId: id },
    );
    throw error;
  }
}

export function createThreadRenamePinActions(runtime) {
  return {
    renameThread: (threadId, name) =>
      renameThreadAction(runtime, threadId, name),
    toggleThreadPin: (threadId) => toggleThreadPinAction(runtime, threadId),
  };
}
