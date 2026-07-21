import {
  compactThread,
  forceCompleteTurn,
  interruptTurn,
  recoverThread,
  respondApproval as respondApprovalRPC,
} from "../../../../../shared/api/backendApi.js";
import {
  approvalIdentityKey,
  requireApprovalIdentity,
} from "../../../../../shared/api/support/approvalRequestId.js";
import { firstOptionalPresent } from "../../contractStoreModel.js";
import {
  activeThreadInterruptTarget,
  backendThreadIdForState,
} from "./clientStoreRuntimeThreadModel.js";
import {
  clockNowMillis,
  normalizeString,
  optionalUiObject,
} from "./clientStoreUtils.js";

const APPROVAL_SUBMIT_TIMEOUT_MS = 15_000;
const APPROVAL_SUBMIT_TIMEOUT_CODE = "APPROVAL_SUBMIT_TIMEOUT";
function approvalSubmitTimeoutError() {
  const error = new Error("审批提交超时");
  error.code = APPROVAL_SUBMIT_TIMEOUT_CODE;
  return error;
}
function approvalSubmitIsTimeout(error) {
  return error?.code === APPROVAL_SUBMIT_TIMEOUT_CODE;
}
async function respondApprovalWithinTimeout(params) {
  let timeoutId = 0;
  try {
    return await Promise.race([
      respondApprovalRPC(params),
      new Promise((_, reject) => {
        timeoutId = window.setTimeout(
          () => reject(approvalSubmitTimeoutError()),
          APPROVAL_SUBMIT_TIMEOUT_MS,
        );
      }),
    ]);
  } finally {
    window.clearTimeout(timeoutId);
  }
}
function approvalSubmitPatch(state, identityKey, identity, decision) {
  return {
    approvalSubmitByIdentity: {
      ...state.approvalSubmitByIdentity,
      [identityKey]: {
        ...identity,
        approved: decision,
        inFlight: true,
        startedAt: clockNowMillis(),
      },
    },
  };
}
function clearApprovalSubmitPatch(state, identityKey) {
  const current = state.approvalSubmitByIdentity || optionalUiObject();
  if (!current[identityKey]) return {};
  const next = { ...current };
  delete next[identityKey];
  return { approvalSubmitByIdentity: next };
}
function hasInterruptibleThreadAction(runtime) {
  return activeThreadInterruptTarget(runtime.get()).interruptible;
}
async function refreshActiveThreadStatusAction(runtime) {
  const threadId = backendThreadIdForState(
    runtime.get(),
    runtime.get().activeThreadId,
  );
  if (!threadId) return false;
  await runtime.get().syncThreadState(threadId);
  runtime.notifyAction("线程状态已刷新", "success", { threadId });
  return true;
}
function warnMissingApprovalRequest(runtime, item) {
  runtime.notifyAction("当前审批缺少完整身份，无法提交", "error");
  runtime.addWarning("error", "timeline.approval.identity_missing", {
    command: normalizeString(firstOptionalPresent(item?.command, item?.title)),
  });
}
function warnDuplicateApprovalSubmit(runtime, requestId, decision) {
  runtime.notifyAction("审批结果正在提交，请等待当前请求完成", "warning", {
    requestId,
  });
  runtime.addWarning("warn", "timeline.approval.respond_duplicate", {
    requestId,
    approved: decision,
  });
}
function approvalSubmitIsInFlight(runtime, identityKey) {
  return (
    runtime.get().approvalSubmitByIdentity?.[identityKey]?.inFlight === true
  );
}
function warnApprovalFailed(runtime, requestId, decision, _error) {
  runtime.notifyAction("审批提交失败，请重试。", "error", { requestId });
  runtime.addWarning("error", "timeline.approval.respond.failed", {
    requestId,
    approved: decision,
    error: "action failure; see Health diagnostic ID",
  });
}
async function respondApprovalAction(runtime, deps, item, approved) {
  if (typeof approved !== "boolean") {
    warnApprovalFailed(
      runtime,
      item?.requestId,
      undefined,
      new TypeError("approval decision must be a boolean"),
    );
    return false;
  }
  const decision = approved;
  let identity;
  try {
    identity = requireApprovalIdentity(item, "timeline approval");
  } catch {
    warnMissingApprovalRequest(runtime, item);
    return false;
  }
  const requestId = identity.requestId;
  const identityKey = approvalIdentityKey(identity);
  if (approvalSubmitIsInFlight(runtime, identityKey)) {
    warnDuplicateApprovalSubmit(runtime, requestId, decision);
    return false;
  }
  deps.recordApproval("start");
  runtime.set((state) =>
    approvalSubmitPatch(state, identityKey, identity, decision),
  );
  try {
    const result = await respondApprovalWithinTimeout({
      ...identity,
      approved: decision,
    });
    if (result !== null)
      throw new TypeError("approval/respond response must be null");
    deps.recordApproval("success");
    runtime.notifyAction("审批结果已提交", "success", { requestId });
    return true;
  } catch (error) {
    deps.recordApproval(approvalSubmitIsTimeout(error) ? "timeout" : "failure");
    warnApprovalFailed(runtime, requestId, decision, error);
    throw error;
  } finally {
    runtime.set((state) => clearApprovalSubmitPatch(state, identityKey));
  }
}

export function createActiveThreadActions(runtime, deps) {
  return {
    interruptActiveThread: () =>
      runtime.activeThreadRPC("thread.interrupt", interruptTurn),
    forceCompleteActiveThread: () =>
      runtime.activeThreadRPC("thread.force_complete", forceCompleteTurn),
    compactActiveThread: () =>
      runtime.activeThreadRPC("thread.compact", compactThread),
    recoverActiveThread: () => runtime.recoverActiveThreadRPC(recoverThread),
    hasActiveThreadActions: () =>
      Boolean(
        backendThreadIdForState(runtime.get(), runtime.get().activeThreadId),
      ),
    hasInterruptibleThreadAction: () => hasInterruptibleThreadAction(runtime),
    hasForceCompleteThreadAction: () => hasInterruptibleThreadAction(runtime),
    refreshActiveThreadStatus: () => refreshActiveThreadStatusAction(runtime),
    respondApproval: (item, approved) =>
      respondApprovalAction(runtime, deps, item, approved),
  };
}
