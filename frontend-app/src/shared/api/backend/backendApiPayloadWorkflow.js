import { RPC_METHODS } from "./backendRpcMethods.js";
import {
  assertPlainObject,
  hasOwn,
  normalizeOptionalLimit,
  normalizeOptionalString,
  normalizeString,
  requireBoolean,
  requireCwd,
  requireKey,
} from "./backendApiCommon.js";
import {
  dashboardDagApplyOpsPayload,
  dashboardDagCreateAndStartPayload,
  dashboardDagDispatchNodePayload,
  dashboardDagRunsPayload,
  dashboardDagStartPayload,
  dashboardDagTerminatePayload,
  dashboardDagsPayload,
  optionalInteger,
  requireNumber,
} from "./backendApiPayloadWorkflowDag.js";
import {
  codeFilePayload,
  codeProjectsPayload,
  cronIdPayload,
  cronJobConfigPayload,
  cronJobEnabledPayload,
  cronJobMaxAttemptsPayload,
  cronJobMutationPayload,
  cronJobSkillsPayload,
  cronListPayload,
  cronListRunsPayload,
  cronSetEnabledPayload,
  optionalCodeInteger,
} from "./backendApiPayloadWorkflowCron.js";
import {
  memoryConsolidationPayload,
  personalizationProfilePayload,
  promptDeletePayload,
  promptDraftKeyPayload,
  promptIntentCommitPayload,
  promptIntentDiscardPayload,
  promptIntentDraftPayload,
  promptIntentDryRunPayload,
  promptIntentEnableGlobal,
  promptIntentRawInput,
  promptIntentSourceFields,
  promptMatchWhen,
  promptProviderFields,
  promptSectionPayload,
  promptWritePayload,
} from "./backendApiPayloadWorkflowPrompts.js";

/** @param {unknown} params */
function dashboardWorkflowMaterialWritePayload(params) {
  const method = RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE;
  const payload = assertPlainObject(method, params);
  const path = normalizeString(payload.path);
  const content = typeof payload.content === "string" ? payload.content : "";
  if (!path) throw new Error(`${method}: path is required`);
  if (!content.trim()) throw new Error(`${method}: content is required`);
  return { path, content };
}

/** @param {unknown} params */
function lspPromptHintWritePayload(params) {
  const method = RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE;
  const payload = requireCwd(method, params);
  if (!hasOwn(payload, "hint")) throw new Error(`${method}: hint is required`);
  return { cwd: payload.cwd, hint: normalizeOptionalString(payload.hint) };
}

/** @param {unknown} params */
function videoApiKeyPayload(params) {
  const method = RPC_METHODS.UI_VIDEO_SET_API_KEY;
  const payload = assertPlainObject(method, params);
  const apiKey = normalizeString(payload.apiKey);
  if (!apiKey) throw new Error(`${method}: apiKey is required`);
  return { apiKey };
}

/** @param {unknown} params */
function builtinToolWritePayload(params) {
  const method = RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE;
  const payload = requireBoolean(
    method,
    requireKey(method, requireCwd(method, params), "id"),
    "enabled",
  );
  return { cwd: payload.cwd, id: payload.id, enabled: payload.enabled };
}

/** @param {unknown} [params] */
function dashboardLogsPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.DASHBOARD_LOGS, params);
  return Object.fromEntries(
    Object.entries({
      source: normalizeString(payload.source),
      category: normalizeString(payload.category),
      keyword: normalizeString(payload.keyword),
      level: normalizeString(payload.level),
      logger: normalizeString(payload.logger),
      component: normalizeString(payload.component),
      agentId: normalizeString(payload.agentId || payload.agent_id),
      threadId: normalizeString(payload.threadId || payload.thread_id),
      eventType: normalizeString(payload.eventType || payload.event_type),
      toolName: normalizeString(payload.toolName || payload.tool_name),
      limit: normalizeOptionalLimit(RPC_METHODS.DASHBOARD_LOGS, payload),
    }).filter(([, value]) => value !== undefined && value !== ""),
  );
}

export {
  dashboardDagStartPayload,
  dashboardDagCreateAndStartPayload,
  dashboardWorkflowMaterialWritePayload,
  dashboardDagDispatchNodePayload,
  optionalInteger,
  requireNumber,
  dashboardDagsPayload,
  dashboardDagRunsPayload,
  dashboardDagTerminatePayload,
  dashboardDagApplyOpsPayload,
  cronIdPayload,
  cronSetEnabledPayload,
  cronListRunsPayload,
  cronListPayload,
  cronJobMutationPayload,
  cronJobConfigPayload,
  cronJobSkillsPayload,
  cronJobEnabledPayload,
  cronJobMaxAttemptsPayload,
  codeProjectsPayload,
  optionalCodeInteger,
  codeFilePayload,
  promptWritePayload,
  promptMatchWhen,
  promptDeletePayload,
  promptIntentDraftPayload,
  promptIntentRawInput,
  promptIntentEnableGlobal,
  promptIntentSourceFields,
  promptProviderFields,
  memoryConsolidationPayload,
  promptDraftKeyPayload,
  promptIntentCommitPayload,
  promptIntentDiscardPayload,
  promptIntentDryRunPayload,
  personalizationProfilePayload,
  promptSectionPayload,
  lspPromptHintWritePayload,
  videoApiKeyPayload,
  builtinToolWritePayload,
  dashboardLogsPayload,
  hasOwn,
};
