import { sessionApi } from "../../../../../shared/api/sessionApi.js";
import {
  createDashboardCommandRequest,
  createdThreadIdForSendRollback,
  deleteProvisionalThreadAfterSendFailure,
  optimisticSendDraftState,
  promotedDraftThreadState,
  rollbackSendDraftState,
  saveFailedSendDraftSnapshot,
  sendRollbackRestoresVisibleComposer,
  startNewDraftThread,
} from "./clientStoreSendModel.js";
import { resolveLaunchPreferences } from "./clientStoreUtils.js";

function promotedDashboardCommandState(state, request, started) {
  return {
    ...promotedDraftThreadState(state, request, started),
    activePage: "chat",
  };
}

function rollbackDashboardCommandState(state, request, error, createdThreadId) {
  return {
    ...rollbackSendDraftState(state, request, error, { createdThreadId }),
    activePage: "commands",
  };
}

async function runDashboardCommandAction(runtime, card) {
  const cwd = runtime.requireCwd("dashboard command");
  const request = createDashboardCommandRequest(runtime.get(), cwd, card);
  if (!request) return false;
  runtime.set((state) => ({
    ...optimisticSendDraftState(state, request),
    activePage: "chat",
  }));
  let threadId = request.previousThreadId;
  try {
    if (!threadId) {
      const started = await startNewDraftThread(request, (launchCwd) =>
        resolveLaunchPreferences(
          launchCwd,
          runtime.addWarning,
          runtime.getPreference,
        ),
      );
      threadId = started.threadId;
      runtime.set((state) =>
        promotedDashboardCommandState(state, request, started),
      );
    }
    await sessionApi.startTurn({
      cwd: request.cwd,
      threadId,
      input: request.input,
      manualSkillSelection: false,
    });
    runtime.clearComposerDraft(
      { ...runtime.get(), activeThreadId: request.previousActiveThreadId },
      request.previousActiveThreadId,
    );
    runtime.clearComposerDraft(runtime.get(), request.provisionalThreadId);
    runtime.clearComposerDraft(runtime.get(), threadId);
    runtime.set({ sending: false });
    return true;
  } catch (error) {
    const rollbackState = runtime.get();
    const createdThreadId = createdThreadIdForSendRollback(
      rollbackState,
      request,
      threadId,
    );
    const shouldCacheFailedDraft = !sendRollbackRestoresVisibleComposer(
      rollbackState,
      request,
      createdThreadId,
    );
    runtime.set((state) =>
      rollbackDashboardCommandState(state, request, error, createdThreadId),
    );
    if (shouldCacheFailedDraft) saveFailedSendDraftSnapshot(runtime, request);
    await deleteProvisionalThreadAfterSendFailure(
      createdThreadId,
      runtime.addWarning,
    );
    runtime.addWarning("error", "dashboard.command.send.failed", {
      error: "action failure; see Health diagnostic ID",
    });
    throw error;
  }
}

export function createDashboardCommandActions(runtime) {
  return {
    runDashboardCommand: (card) => runDashboardCommandAction(runtime, card),
  };
}
