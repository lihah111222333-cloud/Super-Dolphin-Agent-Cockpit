import { RPC_METHODS } from "../backendApi.js";
import {
  builtinToolsResponse,
  codeSaveResponse,
  dashboardLogsResponse,
  dashboardPageResponse,
  frontendIngestResponse,
  modelProviderRegistryResponse,
  okResponse,
  openWindowResponse,
  projectsStateResponse,
  runtimeConfigResponse,
  sidebarStateResponse,
  videoApiKeyStatusResponse,
  windowBootstrapResponse,
} from "./backendApi.contractResponse.testSupport.js";
import { dashboardPromptResponse } from "./backendApi.opsPromptMemory.responses.js";
import {
  promptIntentDraftResponse,
  promptWireItem,
} from "./backendApi.opsPromptMemory.testSupport.js";
import {
  threadCompactResponse,
  threadConfigResponse,
  threadRecoverResponse,
} from "./backendApi.threadState.testSupport.js";
import {
  cronJobResponse,
  cronRunResponse,
  dashboardDagNode,
  dashboardDagRun,
  dashboardDagSummary,
} from "./backendApi.workflow.testSupport.js";

function cronResponse(method) {
  if (method === RPC_METHODS.CRONJOB_LIST)
    return { jobs: [], next_cursor: "", has_more: false };
  if (
    [
      RPC_METHODS.CRONJOB_GET,
      RPC_METHODS.CRONJOB_CREATE,
      RPC_METHODS.CRONJOB_UPDATE,
      RPC_METHODS.CRONJOB_RUN_ONCE,
    ].includes(method)
  )
    return cronJobResponse();
  if (method === RPC_METHODS.CRONJOB_DELETE)
    return { deleted: true, id: "job-1" };
  if (method === RPC_METHODS.CRONJOB_SET_ENABLED)
    return { id: "job-1", enabled: true };
  if (method === RPC_METHODS.CRONJOB_LIST_RUNS)
    return { runs: [cronRunResponse()] };
}

function configurationResponse(method) {
  if (method === RPC_METHODS.TOOLBRIDGE_TOOLS_LIST) return { tools: [] };
  if (method === RPC_METHODS.THREAD_PROMPT_HISTORY)
    return { entries: [], nextCursor: "", hasMore: false, nonce: "nonce-1" };
  if (method === RPC_METHODS.CONFIG_READ) return runtimeConfigResponse();
  if (
    method === RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ ||
    method === RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE
  )
    return builtinToolsResponse();
  if (method === RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET)
    return windowBootstrapResponse();
  if (method === RPC_METHODS.UI_SIDEBAR_GET) return sidebarStateResponse();
  if (method === RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST)
    return frontendIngestResponse();
  if (method === RPC_METHODS.UI_OPEN_NEW_WINDOW) return openWindowResponse();
  if (method === RPC_METHODS.UI_CODE_SAVE) return codeSaveResponse();
  if (
    [
      RPC_METHODS.UI_PROJECTS_GET,
      RPC_METHODS.UI_PROJECTS_SET_ACTIVE,
      RPC_METHODS.UI_PROJECTS_ADD,
      RPC_METHODS.UI_PROJECTS_REMOVE,
    ].includes(method)
  )
    return projectsStateResponse();
  if (
    method === RPC_METHODS.UI_PREFERENCES_SET ||
    method === RPC_METHODS.UI_VIDEO_SET_API_KEY
  )
    return okResponse();
  if (method === RPC_METHODS.UI_DASHBOARD_GET) return dashboardPageResponse();
  if (method === RPC_METHODS.UI_VIDEO_GET_API_KEY)
    return videoApiKeyStatusResponse();
  if (method === RPC_METHODS.DASHBOARD_LOGS) return dashboardLogsResponse();
  if (method === RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ)
    return {
      hint: "effective prompt",
      defaultHint: "default prompt",
      overrideHint: "custom prompt",
      usingDefault: false,
    };
  if (method === RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE)
    return {
      hint: "custom prompt",
      defaultHint: "default prompt",
      overrideHint: "custom prompt",
      usingDefault: false,
    };
}

function dashboardResponse(method) {
  if (method === RPC_METHODS.DASHBOARD_SHARED_FILES)
    return {
      files: [],
      finalOutputRefs: [],
      sharedFileRetention: {
        items: [],
        protectedCount: 0,
        cleanupCandidateCount: 0,
      },
    };
  if (
    [
      RPC_METHODS.MODEL_PROVIDERS_APPLY,
      RPC_METHODS.MODEL_PROVIDERS_LIST,
      RPC_METHODS.MODEL_PROVIDERS_SAVE,
    ].includes(method)
  )
    return modelProviderRegistryResponse();
  if (
    [
      RPC_METHODS.OBSERVABILITY_ERROR_LIST,
      RPC_METHODS.OBSERVABILITY_RECENT_LIST,
      RPC_METHODS.OBSERVABILITY_SLOW_LIST,
      RPC_METHODS.OBSERVABILITY_THREAD_RECENT,
      RPC_METHODS.OBSERVABILITY_TRACE_GET,
    ].includes(method)
  )
    return { source: "memory", events: [] };
  if (method === RPC_METHODS.UI_MEMORY_GET)
    return { overview: {}, private: { entries: [] }, team: { entries: [] } };
  if (method === RPC_METHODS.UI_STATE_GET)
    return { threads: [], agents: [], token_usage: {} };
  if (method === RPC_METHODS.UI_SHARED_FILE_GET)
    return { path: "reports/final.md", content: "" };
  if (method === RPC_METHODS.THREAD_MESSAGES)
    return { messages: [], total: 0, hasMore: false, nextBefore: "" };
  if (method === RPC_METHODS.THREAD_RESOLVE) return { id: "thread-2" };
}

function threadAndWorkflowResponse(method) {
  if (
    [
      RPC_METHODS.THREAD_ARCHIVE,
      RPC_METHODS.THREAD_UNARCHIVE,
      RPC_METHODS.THREAD_DELETE,
      RPC_METHODS.THREAD_NAME_SET,
      RPC_METHODS.APPROVAL_RESPOND,
    ].includes(method)
  )
    return null;
  if (
    method === RPC_METHODS.THREAD_CONFIG_GET ||
    method === RPC_METHODS.THREAD_CONFIG_SET
  )
    return threadConfigResponse();
  if (method === RPC_METHODS.THREAD_COMPACT_START)
    return threadCompactResponse();
  if (method === RPC_METHODS.THREAD_RECOVER) return threadRecoverResponse();
  if (method === RPC_METHODS.THREAD_START)
    return { threadId: "thread-123", status: "running" };
  if (method === RPC_METHODS.TURN_START) return { turn_id: "turn-1" };
  if (method === RPC_METHODS.TURN_FORCE_COMPLETE)
    return { ok: true, forceCompleted: true };
  if (method === RPC_METHODS.DASHBOARD_DAG_START) return { runKey: "run-1" };
  if (method === RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START)
    return { dagKey: "dag-created", runKey: "run-created" };
  if (method === RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE)
    return {
      node: dashboardDagNode({ assigned_to: "codex-runner" }),
      wakeup_id: 88,
      enqueued: true,
    };
  if (method === RPC_METHODS.DASHBOARD_DAG_TERMINATE) return {};
  if (method === RPC_METHODS.DASHBOARD_DAG_APPLY_OPS) return { newVersion: 12 };
  if (method === RPC_METHODS.DASHBOARD_DAG_DETAIL)
    return { dag: dashboardDagSummary(), nodes: [dashboardDagNode()] };
  if (method === RPC_METHODS.DASHBOARD_DAG_RUNS)
    return { runs: [dashboardDagRun()] };
  if (method === RPC_METHODS.DASHBOARD_DAG_RUN)
    return { run: dashboardDagRun(), nodes: [dashboardDagNode()] };
  if (method === RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE)
    return { path: "reports/workflows/uploads/dag-1/material.md" };
}

function promptAndMemoryResponse(method) {
  if (
    [
      RPC_METHODS.UI_MEMORY_ENTRY_GET,
      RPC_METHODS.UI_MEMORY_ENTRY_UPSERT,
      RPC_METHODS.UI_MEMORY_ENTRY_MERGE,
    ].includes(method)
  )
    return {
      target: "private",
      path: "feedback/tdd.md",
      name: "tdd-rule",
      type: "feedback",
      content: "规则",
    };
  if (
    method === RPC_METHODS.UI_MEMORY_ENTRY_DELETE ||
    method === RPC_METHODS.UI_SHARED_FILE_DELETE
  )
    return { deleted: true };
  if (method === RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT)
    return { ok: true, enabled: true };
  if (method === RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE)
    return { ignored: true, key: "private:a.md|team:b.md" };
  if (
    method === RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START ||
    method === RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS
  )
    return { jobId: "memory-job-1", status: "running" };
  if (method === RPC_METHODS.PROMPT_ASSETS_LIST)
    return { prompts: [promptWireItem()] };
  if (method === RPC_METHODS.DASHBOARD_PROMPTS)
    return dashboardPromptResponse();
  if (
    method === RPC_METHODS.PROMPTS_GET ||
    method === RPC_METHODS.PROMPTS_WRITE
  )
    return { prompt: promptWireItem() };
  if (method === RPC_METHODS.PROMPTS_DELETE) return { ok: true };
  if (method === RPC_METHODS.PROMPT_INTENTS_DRAFT)
    return promptIntentDraftResponse();
  if (method === RPC_METHODS.PROMPT_INTENTS_COMMIT)
    return {
      draft_key: "intent/expert/review",
      prompt_key: "main/reviewer",
      kind: "expert",
      status: "enabled",
    };
  if (method === RPC_METHODS.PROMPT_INTENTS_DISCARD)
    return { draft_key: "intent/expert/review", status: "rejected" };
  if (method === RPC_METHODS.PROMPT_INTENTS_DRY_RUN)
    return {
      would_use: true,
      action: "launch_agent",
      target: "main/reviewer",
      reasons: ["matched"],
      disclaimer: "",
    };
  if (
    method === RPC_METHODS.PERSONALIZATION_PROFILE_GET ||
    method === RPC_METHODS.PERSONALIZATION_PROFILE_SAVE
  )
    return {
      profile: {
        displayName: "小海",
        role: "后端工程师",
        background: "熟悉 Go",
        customInstructions: "回答要直接",
      },
    };
}

export function guardedBackendResponse(method) {
  for (const resolver of [
    cronResponse,
    configurationResponse,
    dashboardResponse,
    threadAndWorkflowResponse,
    promptAndMemoryResponse,
  ]) {
    const response = resolver(method);
    if (response !== undefined) return response;
  }
  return { ok: true };
}
