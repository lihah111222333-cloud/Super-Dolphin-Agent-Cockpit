import {
  beginTextClipboardWrite,
  resolveThreadIdentity,
} from "../../../../../shared/api/backendApi.js";
import { buildThreadCopyPayload } from "../threadCopyPayload.js";
import { backendThreadIdForState } from "./clientStoreRuntimeThreadModel.js";
import { writeThreadInfoClipboard } from "./clientStoreThreadClipboard.js";
import { DEFAULT_PROVIDER, optionalUiObject } from "./clientStoreUtils.js";

async function resolveThreadCopyIdentity(
  runtime,
  preparedClipboardWrite,
  cwd,
  threadId,
) {
  try {
    return await resolveThreadIdentity({ cwd, threadId });
  } catch (error) {
    preparedClipboardWrite?.cancel?.(error);
    runtime.notifyAction("复制失败：线程信息暂时不可用，请重试。", "warning", {
      threadId,
    });
    runtime.addWarning("warn", "thread.identity.resolve.failed", {
      threadId,
      error: "action failure; see Health diagnostic ID",
    });
    throw error;
  }
}

function validateThreadCopyIdentity(
  runtime,
  preparedClipboardWrite,
  identity,
  threadId,
) {
  if (identity && typeof identity === "object" && !Array.isArray(identity))
    return true;
  preparedClipboardWrite?.cancel?.();
  runtime.notifyAction(
    "复制失败：线程信息接口返回值不是 JSON 对象",
    "warning",
    { threadId },
  );
  runtime.addWarning("warn", "thread.identity.resolve.invalid", { threadId });
  throw new TypeError("thread/resolve response must be a JSON object");
}

async function copyActiveThreadInfoAction(runtime) {
  const state = runtime.get();
  const threadId = backendThreadIdForState(state, state.activeThreadId);
  if (!threadId) {
    runtime.notifyAction("当前没有可复制的后端线程", "warning");
    return false;
  }
  const preparedClipboardWrite = beginTextClipboardWrite();
  const thread =
    state.threads.find((item) => item.id === threadId) || optionalUiObject();
  const cwd = runtime.requireCwd("thread.copy");
  const identity = await resolveThreadCopyIdentity(
    runtime,
    preparedClipboardWrite,
    cwd,
    threadId,
  );
  if (
    !validateThreadCopyIdentity(
      runtime,
      preparedClipboardWrite,
      identity,
      threadId,
    )
  )
    return false;
  const threadConfig =
    state.threadConfigByThread[threadId] ||
    (await runtime.get().loadThreadConfig(threadId));
  const payload = buildThreadCopyPayload({
    state: runtime.get(),
    threadId,
    thread,
    identity,
    threadConfig,
    defaultProvider: DEFAULT_PROVIDER,
  });
  try {
    await writeThreadInfoClipboard(
      runtime,
      preparedClipboardWrite,
      JSON.stringify(payload, null, 2),
      threadId,
    );
    runtime.notifyAction("线程信息已复制", "success", { threadId });
    return true;
  } catch (error) {
    runtime.notifyAction("复制失败，请重试。", "warning", { threadId });
    runtime.addWarning("warn", "thread.copy.clipboard.failed", {
      threadId,
      error: "action failure; see Health diagnostic ID",
    });
    throw error;
  }
}

export function createThreadCopyActions(runtime) {
  return { copyActiveThreadInfo: () => copyActiveThreadInfoAction(runtime) };
}
