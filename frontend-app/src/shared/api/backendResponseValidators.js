// @ts-check

import {
  parseDashboardPromptsResponse,
  parseMemoryAutoDreamIntentResponse,
  parseMemoryConsolidationJobResponse,
  parseMemoryEntryDeleteResponse,
  parseMemoryEntryDetailResponse,
  parseMemorySimilarityIgnoreResponse,
  parseMemorySnapshotResponse,
  parseModelProviderRegistryResponse,
  parseObservabilityResultResponse,
  parsePersonalizationProfileResponse,
  parsePromptAssetsResponse,
  parsePromptDetailResponse,
  parsePromptIntentCommitResponse,
  parsePromptIntentDiscardResponse,
  parsePromptIntentDraftResponse,
  parsePromptIntentDryRunResponse,
  parseSharedFileDeleteResponse,
  parseSharedFileDetailResponse,
  parseSharedFilesDashboardResponse,
  parseWorkflowMaterialWriteResponse,
} from './backendSchemas.js';

import {
  validateAppUpdateInstallResponse,
  validateBuiltinToolsResponse,
  validateCodeSaveResponse,
  validateDashboardDagCreateAndStartResponse,
  validateDashboardDagStartResponse,
  validateDashboardLogsResponse,
  validateDashboardPageResponse,
  validateFrontendIngestResponse,
  validateLspPromptHintResponse,
  validateNullResponse,
  validateOKResponse,
  validateOpenWindowResponse,
  validateProjectsStateResponse,
  validateRuntimeConfigResponse,
  validateSkillReadResponse,
  validateThreadCompactResponse,
  validateThreadConfigResponse,
  validateThreadForkResponse,
  validateThreadMessagesResponse,
  validateThreadResolveResponse,
  validateThreadStartResponse,
  validateTurnForceCompleteResponse,
  validateTurnStartResponse,
  validateVideoAPIKeyStatusResponse,
  validateWindowBootstrapResponse,
} from './backendResponseValidatorsCore.js';
import {
  validateCronListResponse,
  validateSidebarStateResponse as validateRuntimeSidebarStateResponse,
  validateThreadPromptHistoryResponse,
  validateThreadRecoverResponse,
  validateToolbridgeToolsListResponse,
  validateUIStateResponse as validateRuntimeUIStateResponse,
} from './backendResponseValidatorsRuntime.js';
import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateStringFields,
} from './backendResponseValidatorShared.js';

const MODEL_PROVIDER_REGISTRY_RESPONSE_KEYS = new Set(['activeVendorId', 'vendors']);
const MODEL_PROVIDER_VENDOR_KEYS = new Set(['id', 'label', 'enabled', 'baseURL', 'envKey', 'codexModelProvider', 'defaultModel', 'codexHome', 'codexInstanceKey', 'budget', 'tokenPool', 'configured', 'maskedEnv', 'envStatus']);
const MODEL_PROVIDER_BUDGET_KEYS = new Set(['dailyUsd', 'monthlyUsd']);
const MODEL_PROVIDER_TOKEN_POOL_KEYS = new Set(['priority', 'fallbackVendorId']);
const DASHBOARD_DAG_DETAIL_RESPONSE_KEYS = new Set(['dag', 'nodes']);
const DASHBOARD_DAG_SUMMARY_KEYS = new Set(['id', 'dag_key', 'version', 'title', 'description', 'status', 'created_by', 'metadata', 'trigger', 'cron_expr', 'next_run_at', 'schedule_enabled', 'started_at', 'finished_at', 'created_at', 'updated_at']);
const DASHBOARD_DAG_NODE_KEYS = new Set([
  'id', 'dag_key', 'node_key', 'title', 'node_type', 'assigned_to', 'depends_on',
  'reads', 'writes', 'status', 'command_ref', 'config', 'result', 'started_at',
  'finished_at', 'created_at', 'updated_at', 'active_turn_id', 'active_wakeup_id',
  'last_event_at', 'spawning_thread_id', 'executor', 'failure_class', 'last_wakeup_at',
  'artifact_links', 'next_action',
]);
const DASHBOARD_DAG_RUNS_RESPONSE_KEYS = new Set(['runs']);
const DASHBOARD_DAG_RUN_RESPONSE_KEYS = new Set(['run', 'nodes']);
const DASHBOARD_DAG_RUN_KEYS = new Set([
  'id', 'run_key', 'dag_key', 'dag_version_snapshot', 'trigger_source', 'status',
  'started_at', 'finished_at', 'events', 'budget_used', 'budget_limit', 'metadata',
  'created_at', 'updated_at', 'derived_state', 'blocked_reason', 'next_action',
  'artifact_count', 'recovery_actions',
]);
const WORKFLOW_RECOVERY_ACTION_KEYS = new Set(['action', 'label', 'enabled', 'reason', 'policy']);
const WORKFLOW_ARTIFACT_LINK_KEYS = new Set(['kind', 'label', 'path', 'url', 'node_key']);
const WORKFLOW_TEMPLATES_LIST_RESPONSE_KEYS = new Set(['templates']);
const WORKFLOW_TEMPLATE_RESPONSE_KEYS = new Set(['template']);
const WORKFLOW_TEMPLATE_DRAFT_RESPONSE_KEYS = new Set(['draft']);
const WORKFLOW_TEMPLATE_SAVE_RESPONSE_KEYS = new Set(['template']);
const WORKFLOW_TEMPLATE_SUMMARY_KEYS = new Set(['id', 'version', 'title', 'description', 'category', 'business_flow', 'output_types', 'tags', 'estimated_nodes', 'requires_review', 'supports_schedule', 'final_node_key', 'trust', 'compatibility', 'available_versions']);
const WORKFLOW_TEMPLATE_KEYS = new Set(['id', 'version', 'title', 'description', 'category', 'business_flow', 'output_types', 'tags', 'estimated_nodes', 'requires_review', 'supports_schedule', 'trust', 'compatibility', 'ui_schema', 'dag_template', 'validation', 'final_output']);
const WORKFLOW_LOCALIZED_TEXT_KEYS = new Set(['zh', 'en']);
const WORKFLOW_TRUST_KEYS = new Set(['level', 'source']);
const WORKFLOW_COMPATIBILITY_KEYS = new Set(['runtime', 'node_types', 'required_capabilities']);
const WORKFLOW_UI_FIELD_KEYS = new Set(['key', 'type', 'required', 'label', 'placeholder', 'help', 'options']);
const WORKFLOW_UI_OPTION_KEYS = new Set(['value', 'label']);
const WORKFLOW_DAG_TEMPLATE_KEYS = new Set(['dag_key_template', 'title_template', 'description_template', 'trigger', 'final_node_key', 'nodes']);
const WORKFLOW_NODE_TEMPLATE_KEYS = new Set(['node_key', 'title', 'node_type', 'assigned_to', 'depends_on', 'config']);
const WORKFLOW_VALIDATION_KEYS = new Set(['sharedfile_prefix', 'sharedfile_prefixes', 'require_review_before_final', 'require_final_node_key']);
const WORKFLOW_FINAL_OUTPUT_KEYS = new Set(['node_key', 'kind', 'path_template']);
const WORKFLOW_DAG_DRAFT_KEYS = new Set(['template_id', 'template_version', 'dag_key', 'title', 'description', 'trigger', 'final_node_key', 'review_node_key', 'nodes', 'final_output', 'metadata']);

/** @param {string} method @param {Record<string, any>} value @param {string} label @param {string} key */
function requireResponseString(method, value, label, key) {
  if (typeof value[key] !== 'string') {
    throw new TypeError(`${method} response ${label}.${key} must be a string`);
  }
}

/** @param {string} method @param {Record<string, any>} value @param {string} label @param {string} key */
function requireResponseIdentity(method, value, label, key) {
  requireResponseString(method, value, label, key);
  if (!normalizeString(value[key])) {
    throw new TypeError(`${method} response ${label}.${key} must be a non-empty string`);
  }
}

/** @param {string} method @param {Record<string, any>} value @param {string} label @param {string} key */
function requireResponseInteger(method, value, label, key) {
  if (!Number.isInteger(value[key])) {
    throw new TypeError(`${method} response ${label}.${key} must be an integer`);
  }
}

/** @param {string} method @param {Record<string, any>} value @param {string} label @param {string} key */
function requireResponseBoolean(method, value, label, key) {
  if (typeof value[key] !== 'boolean') {
    throw new TypeError(`${method} response ${label}.${key} must be a boolean`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label */
function validateResponseStringArray(method, value, label) {
  if (!Array.isArray(value) || value.some((item) => typeof item !== 'string')) {
    throw new TypeError(`${method} response ${label} must be an array of strings`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label */
function validateNullableResponseStringArray(method, value, label) {
  if (value === null) return;
  validateResponseStringArray(method, value, label);
}

/** @param {string} method @param {Record<string, any>} value @param {string} label @param {readonly string[]} keys */
function validateOptionalResponseStrings(method, value, label, keys) {
  for (const key of keys) {
    if (hasOwn(value, key)) requireResponseString(method, value, label, key);
  }
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowArtifactLink(method, response, label) {
  const link = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, link, WORKFLOW_ARTIFACT_LINK_KEYS, label);
  validateOptionalResponseStrings(method, link, label, ['kind', 'label', 'path', 'url', 'node_key']);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowRecoveryAction(method, response, label) {
  const action = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, action, WORKFLOW_RECOVERY_ACTION_KEYS, label);
  requireResponseIdentity(method, action, label, 'action');
  requireResponseBoolean(method, action, label, 'enabled');
  validateOptionalResponseStrings(method, action, label, ['label', 'reason', 'policy']);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateDashboardDagSummary(method, response, label) {
  const dag = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, dag, DASHBOARD_DAG_SUMMARY_KEYS, label);
  for (const key of ['id', 'version']) requireResponseInteger(method, dag, label, key);
  for (const key of ['dag_key']) requireResponseIdentity(method, dag, label, key);
  for (const key of ['title', 'status', 'created_at', 'updated_at']) requireResponseString(method, dag, label, key);
  requireResponseBoolean(method, dag, label, 'schedule_enabled');
  validateOptionalResponseStrings(method, dag, label, ['description', 'created_by', 'trigger', 'cron_expr', 'next_run_at', 'started_at', 'finished_at']);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateDashboardDagNode(method, response, label) {
  const node = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, node, DASHBOARD_DAG_NODE_KEYS, label);
  requireResponseInteger(method, node, label, 'id');
  for (const key of ['dag_key', 'node_key']) requireResponseIdentity(method, node, label, key);
  for (const key of ['title', 'status', 'created_at', 'updated_at']) requireResponseString(method, node, label, key);
  validateOptionalResponseStrings(method, node, label, ['node_type', 'assigned_to', 'command_ref', 'started_at', 'finished_at', 'active_turn_id', 'last_event_at', 'spawning_thread_id', 'executor', 'failure_class', 'last_wakeup_at', 'next_action']);
  for (const key of ['depends_on', 'reads', 'writes']) {
    if (hasOwn(node, key)) validateResponseStringArray(method, node[key], `${label}.${key}`);
  }
  if (hasOwn(node, 'active_wakeup_id')) requireResponseInteger(method, node, label, 'active_wakeup_id');
  if (hasOwn(node, 'artifact_links')) {
    if (!Array.isArray(node.artifact_links)) throw new TypeError(`${method} response ${label}.artifact_links must be an array`);
    /** @type {unknown[]} */ (node.artifact_links).forEach((link, index) => validateWorkflowArtifactLink(method, link, `${label}.artifact_links[${index}]`));
  }
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateDashboardDagRun(method, response, label) {
  const run = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, run, DASHBOARD_DAG_RUN_KEYS, label);
  for (const key of ['id', 'dag_version_snapshot', 'budget_used']) requireResponseInteger(method, run, label, key);
  for (const key of ['run_key', 'dag_key']) requireResponseIdentity(method, run, label, key);
  for (const key of ['status', 'started_at', 'created_at', 'updated_at']) requireResponseString(method, run, label, key);
  validateOptionalResponseStrings(method, run, label, ['trigger_source', 'finished_at', 'derived_state', 'blocked_reason', 'next_action']);
  for (const key of ['budget_limit', 'artifact_count']) {
    if (hasOwn(run, key)) requireResponseInteger(method, run, label, key);
  }
  if (hasOwn(run, 'recovery_actions')) {
    if (!Array.isArray(run.recovery_actions)) throw new TypeError(`${method} response ${label}.recovery_actions must be an array`);
    /** @type {unknown[]} */ (run.recovery_actions).forEach((action, index) => validateWorkflowRecoveryAction(method, action, `${label}.recovery_actions[${index}]`));
  }
}

/** @param {string} method @param {unknown} response */
function validateDashboardDagDetailResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_DAG_DETAIL_RESPONSE_KEYS, 'body');
  validateDashboardDagSummary(method, value.dag, 'body.dag');
  if (!Array.isArray(value.nodes)) throw new TypeError(`${method} response body.nodes must be an array`);
  /** @type {unknown[]} */ (value.nodes).forEach((node, index) => validateDashboardDagNode(method, node, `body.nodes[${index}]`));
  return value;
}

/** @param {string} method @param {unknown} response */
function validateDashboardDagRunsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_DAG_RUNS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.runs)) throw new TypeError(`${method} response body.runs must be an array`);
  /** @type {unknown[]} */ (value.runs).forEach((run, index) => validateDashboardDagRun(method, run, `body.runs[${index}]`));
  return value;
}

/** @param {string} method @param {unknown} response */
function validateDashboardDagRunResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_DAG_RUN_RESPONSE_KEYS, 'body');
  validateDashboardDagRun(method, value.run, 'body.run');
  if (hasOwn(value, 'nodes')) {
    if (!Array.isArray(value.nodes)) throw new TypeError(`${method} response body.nodes must be an array`);
    /** @type {unknown[]} */ (value.nodes).forEach((node, index) => validateDashboardDagNode(method, node, `body.nodes[${index}]`));
  }
  return value;
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowLocalizedText(method, response, label) {
  const text = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, text, WORKFLOW_LOCALIZED_TEXT_KEYS, label);
  requireResponseString(method, text, label, 'zh');
  validateOptionalResponseStrings(method, text, label, ['en']);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTrust(method, response, label) {
  const trust = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, trust, WORKFLOW_TRUST_KEYS, label);
  for (const key of ['level', 'source']) requireResponseString(method, trust, label, key);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowCompatibility(method, response, label) {
  const compatibility = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, compatibility, WORKFLOW_COMPATIBILITY_KEYS, label);
  requireResponseString(method, compatibility, label, 'runtime');
  validateResponseStringArray(method, compatibility.node_types, `${label}.node_types`);
  validateResponseStringArray(method, compatibility.required_capabilities, `${label}.required_capabilities`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowUIOption(method, response, label) {
  const option = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, option, WORKFLOW_UI_OPTION_KEYS, label);
  requireResponseString(method, option, label, 'value');
  validateWorkflowLocalizedText(method, option.label, `${label}.label`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowUIField(method, response, label) {
  const field = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, field, WORKFLOW_UI_FIELD_KEYS, label);
  for (const key of ['key', 'type']) requireResponseString(method, field, label, key);
  requireResponseBoolean(method, field, label, 'required');
  for (const key of ['label', 'placeholder', 'help']) validateWorkflowLocalizedText(method, field[key], `${label}.${key}`);
  if (hasOwn(field, 'options')) {
    if (!Array.isArray(field.options)) throw new TypeError(`${method} response ${label}.options must be an array`);
    /** @type {unknown[]} */ (field.options).forEach((option, index) => validateWorkflowUIOption(method, option, `${label}.options[${index}]`));
  }
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowNodeTemplate(method, response, label) {
  const node = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, node, WORKFLOW_NODE_TEMPLATE_KEYS, label);
  for (const key of ['node_key', 'title', 'node_type', 'assigned_to']) requireResponseString(method, node, label, key);
  validateResponseStringArray(method, node.depends_on, `${label}.depends_on`);
  assertResponseRecord(method, node.config, `${label}.config`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowFinalOutput(method, response, label) {
  const output = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, output, WORKFLOW_FINAL_OUTPUT_KEYS, label);
  for (const key of ['node_key', 'kind', 'path_template']) requireResponseString(method, output, label, key);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowValidationRule(method, response, label) {
  const rule = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, rule, WORKFLOW_VALIDATION_KEYS, label);
  for (const key of ['require_review_before_final', 'require_final_node_key']) requireResponseBoolean(method, rule, label, key);
  validateOptionalResponseStrings(method, rule, label, ['sharedfile_prefix']);
  if (hasOwn(rule, 'sharedfile_prefixes')) validateResponseStringArray(method, rule.sharedfile_prefixes, `${label}.sharedfile_prefixes`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowDagTemplate(method, response, label) {
  const dag = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, dag, WORKFLOW_DAG_TEMPLATE_KEYS, label);
  for (const key of ['dag_key_template', 'title_template', 'description_template', 'trigger', 'final_node_key']) requireResponseString(method, dag, label, key);
  if (!Array.isArray(dag.nodes)) throw new TypeError(`${method} response ${label}.nodes must be an array`);
  /** @type {unknown[]} */ (dag.nodes).forEach((node, index) => validateWorkflowNodeTemplate(method, node, `${label}.nodes[${index}]`));
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTemplateSummary(method, response, label) {
  const template = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, template, WORKFLOW_TEMPLATE_SUMMARY_KEYS, label);
  requireResponseIdentity(method, template, label, 'id');
  for (const key of ['version', 'estimated_nodes']) requireResponseInteger(method, template, label, key);
  for (const key of ['category', 'business_flow', 'final_node_key']) requireResponseString(method, template, label, key);
  for (const key of ['requires_review', 'supports_schedule']) requireResponseBoolean(method, template, label, key);
  validateWorkflowLocalizedText(method, template.title, `${label}.title`);
  validateWorkflowLocalizedText(method, template.description, `${label}.description`);
  validateResponseStringArray(method, template.output_types, `${label}.output_types`);
  validateNullableResponseStringArray(method, template.tags, `${label}.tags`);
  validateWorkflowTrust(method, template.trust, `${label}.trust`);
  validateWorkflowCompatibility(method, template.compatibility, `${label}.compatibility`);
  if (!Array.isArray(template.available_versions) || /** @type {unknown[]} */ (template.available_versions).some((version) => !Number.isInteger(version))) {
    throw new TypeError(`${method} response ${label}.available_versions must be an array of integers`);
  }
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTemplate(method, response, label) {
  const template = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, template, WORKFLOW_TEMPLATE_KEYS, label);
  requireResponseIdentity(method, template, label, 'id');
  for (const key of ['version', 'estimated_nodes']) requireResponseInteger(method, template, label, key);
  for (const key of ['category', 'business_flow']) requireResponseString(method, template, label, key);
  for (const key of ['requires_review', 'supports_schedule']) requireResponseBoolean(method, template, label, key);
  validateWorkflowLocalizedText(method, template.title, `${label}.title`);
  validateWorkflowLocalizedText(method, template.description, `${label}.description`);
  validateResponseStringArray(method, template.output_types, `${label}.output_types`);
  validateNullableResponseStringArray(method, template.tags, `${label}.tags`);
  validateWorkflowTrust(method, template.trust, `${label}.trust`);
  validateWorkflowCompatibility(method, template.compatibility, `${label}.compatibility`);
  if (!Array.isArray(template.ui_schema)) throw new TypeError(`${method} response ${label}.ui_schema must be an array`);
  /** @type {unknown[]} */ (template.ui_schema).forEach((field, index) => validateWorkflowUIField(method, field, `${label}.ui_schema[${index}]`));
  validateWorkflowDagTemplate(method, template.dag_template, `${label}.dag_template`);
  validateWorkflowValidationRule(method, template.validation, `${label}.validation`);
  validateWorkflowFinalOutput(method, template.final_output, `${label}.final_output`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTemplateDraft(method, response, label) {
  const draft = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, draft, WORKFLOW_DAG_DRAFT_KEYS, label);
  for (const key of ['template_id', 'dag_key']) requireResponseIdentity(method, draft, label, key);
  requireResponseInteger(method, draft, label, 'template_version');
  for (const key of ['title', 'description', 'trigger', 'final_node_key', 'review_node_key']) requireResponseString(method, draft, label, key);
  if (!Array.isArray(draft.nodes)) throw new TypeError(`${method} response ${label}.nodes must be an array`);
  /** @type {unknown[]} */ (draft.nodes).forEach((node, index) => validateWorkflowNodeTemplate(method, node, `${label}.nodes[${index}]`));
  validateWorkflowFinalOutput(method, draft.final_output, `${label}.final_output`);
  assertResponseRecord(method, draft.metadata, `${label}.metadata`);
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplatesListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATES_LIST_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.templates)) throw new TypeError(`${method} response body.templates must be an array`);
  /** @type {unknown[]} */ (value.templates).forEach((template, index) => validateWorkflowTemplateSummary(method, template, `body.templates[${index}]`));
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateSidebarStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (['threads', 'agents', 'workspace', 'token_usage'].every((key) => hasOwn(value, key))) {
    return validateRuntimeSidebarStateResponse(method, value);
  }
  validateRuntimeUIStateResponse(method, { unchanged: true, ...value });
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  let runtimeValue = value;
  if (!hasOwn(value, 'token_usage') && hasOwn(value, 'tokenUsage')) {
    const { tokenUsage, ...rest } = value;
    runtimeValue = { ...rest, token_usage: tokenUsage };
  }
  validateRuntimeUIStateResponse(method, runtimeValue);
  const requiredSnapshotFields = [
    ['threads'],
    ['agents'],
    ['token_usage', 'tokenUsage'],
  ];
  const missingFields = requiredSnapshotFields
    .filter((aliases) => !aliases.some((key) => hasOwn(value, key)))
    .map((aliases) => aliases.join(' or '));
  if (missingFields.length > 0) {
    throw new Error(`${method} response missing UI state snapshot fields; required: ${missingFields.join(', ')}`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATE_RESPONSE_KEYS, 'body');
  validateWorkflowTemplate(method, value.template, 'body.template');
  return value;
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplateDraftResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATE_DRAFT_RESPONSE_KEYS, 'body');
  validateWorkflowTemplateDraft(method, value.draft, 'body.draft');
  return value;
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplateSaveResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATE_SAVE_RESPONSE_KEYS, 'body');
  validateWorkflowTemplateSummary(method, value.template, 'body.template');
  return value;
}

const MCP_SERVER_LIST_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'mcpServers', 'mcp_servers']);
const MCP_SERVER_STATUS_RESPONSE_KEYS = new Set(['enabled']);
const MCP_SERVER_CONTROL_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'serverName', 'server_name', 'added', 'enabled']);

/**
 * @param {string} method
 * @param {any} response
 */
function validateMCPServerListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MCP_SERVER_LIST_RESPONSE_KEYS, 'body');
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) {
    throw new Error(`${method} response configPath must be a non-empty string`);
  }
  const servers = value.mcpServers || value.mcp_servers;
  if (!servers || typeof servers !== 'object' || Array.isArray(servers)) {
    throw new TypeError(`${method} response mcpServers must be an object`);
  }
  for (const [serverName, server] of Object.entries(servers)) {
    const normalizedName = normalizeString(serverName);
    if (!normalizedName) {
      throw new Error(`${method} response mcpServers must not include an empty server name`);
    }
    if (!server || typeof server !== 'object' || Array.isArray(server)) {
      throw new TypeError(`${method} response mcpServers.${normalizedName} must be an object`);
    }
    assertOnlyResponseKeys(method, server, MCP_SERVER_STATUS_RESPONSE_KEYS, `mcpServers.${normalizedName}`);
    if (typeof server.enabled !== 'boolean') {
      throw new TypeError(`${method} response mcpServers.${normalizedName}.enabled must be a boolean`);
    }
  }
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 * @param {Record<string, { serverName: string, enabled: boolean }>} controlSpecs
 */
function validateMCPServerControlResponse(method, response, controlSpecs) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MCP_SERVER_CONTROL_RESPONSE_KEYS, 'body');
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) {
    throw new Error(`${method} response configPath must be a non-empty string`);
  }
  const spec = controlSpecs[method];
  const serverName = normalizeString(value.serverName || value.server_name);
  if (!spec || serverName !== spec.serverName) {
    throw new Error(`${method} response serverName must be ${spec?.serverName || 'a known MCP server'}`);
  }
  if (value.enabled !== spec.enabled) {
    throw new TypeError(`${method} response enabled must be ${spec.enabled}`);
  }
  if (hasOwn(value, 'added') && typeof value.added !== 'boolean') {
    throw new TypeError(`${method} response added must be a boolean`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 * @param {(response: unknown) => unknown} parser
 */
function validateSchemaResponse(method, response, parser) {
  try {
    return parser(response);
  }
  catch (error) {
    throw new TypeError(`${method} response ${error.message || 'schema is invalid'}`, { cause: error });
  }
}

/** @type {(method: string, response: unknown) => unknown} */
const validateObservabilityResultResponse = (method, response) => validateSchemaResponse(method, response, parseObservabilityResultResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemorySnapshotResponse = (method, response) => validateSchemaResponse(method, response, parseMemorySnapshotResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSharedFilesDashboardResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFilesDashboardResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSharedFileDetailResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFileDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateParsedModelProviderRegistryResponse = (method, response) => validateSchemaResponse(method, response, parseModelProviderRegistryResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemoryEntryDetailResponse = (method, response) => validateSchemaResponse(method, response, parseMemoryEntryDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemoryEntryDeleteResponse = (method, response) => validateSchemaResponse(method, response, parseMemoryEntryDeleteResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemoryAutoDreamIntentResponse = (method, response) => validateSchemaResponse(method, response, parseMemoryAutoDreamIntentResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemorySimilarityIgnoreResponse = (method, response) => validateSchemaResponse(method, response, parseMemorySimilarityIgnoreResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemoryConsolidationJobResponse = (method, response) => validateSchemaResponse(method, response, parseMemoryConsolidationJobResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSharedFileDeleteResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFileDeleteResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateWorkflowMaterialWriteResponse = (method, response) => validateSchemaResponse(method, response, parseWorkflowMaterialWriteResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePromptAssetsResponse = (method, response) => validateSchemaResponse(method, response, parsePromptAssetsResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateDashboardPromptsResponse = (method, response) => validateSchemaResponse(method, response, parseDashboardPromptsResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePromptDetailResponse = (method, response) => validateSchemaResponse(method, response, parsePromptDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePromptIntentDraftResponse = (method, response) => validateSchemaResponse(method, response, parsePromptIntentDraftResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePromptIntentCommitResponse = (method, response) => validateSchemaResponse(method, response, parsePromptIntentCommitResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePromptIntentDiscardResponse = (method, response) => validateSchemaResponse(method, response, parsePromptIntentDiscardResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePromptIntentDryRunResponse = (method, response) => validateSchemaResponse(method, response, parsePromptIntentDryRunResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validatePersonalizationProfileResponse = (method, response) => validateSchemaResponse(method, response, parsePersonalizationProfileResponse);
/** @param {string} method @param {unknown} response @param {number} index */
function validateSavedModelProviderVendor(method, response, index) {
  const label = `body.vendors[${index}]`;
  const vendor = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, vendor, MODEL_PROVIDER_VENDOR_KEYS, label);
  if (hasOwn(vendor, 'budget')) {
    const budget = assertResponseRecord(method, vendor.budget, `${label}.budget`);
    assertOnlyResponseKeys(method, budget, MODEL_PROVIDER_BUDGET_KEYS, `${label}.budget`);
  }
  if (hasOwn(vendor, 'tokenPool')) {
    const tokenPool = assertResponseRecord(method, vendor.tokenPool, `${label}.tokenPool`);
    assertOnlyResponseKeys(method, tokenPool, MODEL_PROVIDER_TOKEN_POOL_KEYS, `${label}.tokenPool`);
  }
}

/** @type {(method: string, response: unknown) => unknown} */
const validateSavedModelProviderRegistryResponse = (method, response) => {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MODEL_PROVIDER_REGISTRY_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.vendors)) {
    throw new TypeError(`${method} response model provider registry body.vendors must be an array`);
  }
  /** @type {unknown[]} */
  const vendors = value.vendors;
  vendors.forEach((vendor, index) => {
    validateSavedModelProviderVendor(method, vendor, index);
  });
  return validateParsedModelProviderRegistryResponse(method, value);
};

/** @param {string} method @param {any} value @param {string} label */
function assertResponseArray(method, value, label) {
  if (!Array.isArray(value)) {
    throw new TypeError(`${method} response ${label} must be an array`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {any} value
 * @param {string} label
 * @param {{ stringKeys: string[], integerKeys: string[], booleanKeys?: string[] }} fields
 */
function validateRequiredFields(method, value, label, fields) {
  const { stringKeys, integerKeys, booleanKeys = [] } = fields;
  validateStringFields(method, value, label, stringKeys, []);
  for (const key of integerKeys) {
    if (!Number.isInteger(value[key])) {
      throw new TypeError(`${method} response ${label}.${key} must be an integer`);
    }
  }
  for (const key of booleanKeys) {
    if (typeof value[key] !== 'boolean') {
      throw new TypeError(`${method} response ${label}.${key} must be a boolean`);
    }
  }
}

/** @param {string} method @param {any} value @param {string} label */
function validateStringArray(method, value, label) {
  const items = assertResponseArray(method, value, label);
  items.forEach((item, index) => {
    if (typeof item !== 'string') {
      throw new TypeError(`${method} response ${label}[${index}] must be a string`);
    }
  });
}

/** @param {string} method @param {any} response */
function validateSkillFilesResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['dir', 'files']), 'body');
  validateStringFields(method, value, 'body', ['dir'], []);
  const files = assertResponseArray(method, value.files, 'body.files');
  files.forEach((raw, index) => {
    const label = `body.files[${index}]`;
    const file = assertResponseRecord(method, raw, label);
    assertOnlyResponseKeys(method, file, new Set(['name', 'path', 'size', 'is_main']), label);
    validateRequiredFields(method, file, label, {
      stringKeys: ['name', 'path'], integerKeys: ['size'], booleanKeys: ['is_main'],
    });
    if (file.size < 0) {
      throw new TypeError(`${method} response ${label}.size must be non-negative`);
    }
  });
  return value;
}

/** @param {string} method @param {any} response @param {string} label */
function validateSkillImportItem(method, response, label) {
  const item = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, item, new Set(['name', 'dir', 'skill_file', 'source', 'files', 'bytes']), label);
  validateRequiredFields(method, item, label, {
    stringKeys: ['name', 'dir', 'skill_file', 'source'], integerKeys: ['files', 'bytes'],
  });
}

/** @param {string} method @param {any} response @param {string} label */
function validateSkillMirrorReport(method, response, label) {
  const report = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, report, new Set(['published', 'skipped', 'deleted', 'conflicts']), label);
  for (const key of ['published', 'skipped', 'deleted', 'conflicts']) {
    if (!hasOwn(report, key)) continue;
    const items = assertResponseArray(method, report[key], `${label}.${key}`);
    items.forEach((raw, index) => {
      const itemLabel = `${label}.${key}[${index}]`;
      const item = assertResponseRecord(method, raw, itemLabel);
      assertOnlyResponseKeys(method, item, new Set(['target_id', 'provider', 'scope', 'relative_mirror_path', 'canonical_id', 'old_hash', 'new_hash', 'conflict_kind', 'error']), itemLabel);
      validateStringFields(method, item, itemLabel, ['target_id'], ['provider', 'scope', 'relative_mirror_path', 'canonical_id', 'old_hash', 'new_hash', 'conflict_kind', 'error']);
    });
  }
}

/** @param {string} method @param {any} response */
function validateSkillImportResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['requested', 'imported', 'failures', 'skill', 'mirror_publish']), 'body');
  validateRequiredFields(method, value, 'body', { stringKeys: [], integerKeys: ['requested'] });
  const imported = assertResponseArray(method, value.imported, 'body.imported');
  imported.forEach((item, index) => validateSkillImportItem(method, item, `body.imported[${index}]`));
  if (hasOwn(value, 'failures')) {
    assertResponseArray(method, value.failures, 'body.failures').forEach((raw, index) => {
      const label = `body.failures[${index}]`;
      const failure = assertResponseRecord(method, raw, label);
      assertOnlyResponseKeys(method, failure, new Set(['source', 'error']), label);
      validateStringFields(method, failure, label, ['source', 'error'], []);
    });
  }
  if (hasOwn(value, 'skill')) validateSkillImportItem(method, value.skill, 'body.skill');
  if (imported.length > 0 && !hasOwn(value, 'mirror_publish')) {
    throw new TypeError(`${method} response body.mirror_publish is required when skills were imported`);
  }
  if (hasOwn(value, 'mirror_publish')) validateSkillMirrorReport(method, value.mirror_publish, 'body.mirror_publish');
  return value;
}

/** @param {string} method @param {any} response */
function validateSkillSummarySuggestionResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['description']), 'body');
  validateStringFields(method, value, 'body', ['description'], []);
  return value;
}

/** @param {string} method @param {any} response @param {string} label */
function validateSkillResolutionSource(method, response, label) {
  const source = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, source, new Set(['scope', 'canonical_id', 'personal_type', 'content_hash', 'canonical_hash', 'path', 'skill_file']), label);
  validateStringFields(method, source, label, ['scope', 'canonical_id'], ['personal_type', 'content_hash', 'canonical_hash', 'path', 'skill_file']);
}

/** @param {string} method @param {any} response @param {string} label */
function validateSkillResolutionListItem(method, response, label) {
  const item = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, item, new Set(['conflict_id', 'kind', 'scope', 'personal_type', 'name', 'available_actions', 'provider_entries', 'sources']), label);
  validateStringFields(method, item, label, ['conflict_id', 'kind', 'name'], ['scope', 'personal_type']);
  validateStringArray(method, item.available_actions, `${label}.available_actions`);
  if (hasOwn(item, 'provider_entries')) {
    assertResponseArray(method, item.provider_entries, `${label}.provider_entries`).forEach((raw, index) => {
      const entryLabel = `${label}.provider_entries[${index}]`;
      const entry = assertResponseRecord(method, raw, entryLabel);
      assertOnlyResponseKeys(method, entry, new Set(['provider', 'source_path', 'target_path', 'source_hash', 'target_hash', 'target_id', 'source_path_id']), entryLabel);
      validateStringFields(method, entry, entryLabel, ['provider'], ['source_path', 'target_path', 'source_hash', 'target_hash', 'target_id', 'source_path_id']);
    });
  }
  if (hasOwn(item, 'sources')) {
    assertResponseArray(method, item.sources, `${label}.sources`).forEach((raw, index) => validateSkillResolutionSource(method, raw, `${label}.sources[${index}]`));
  }
}

/** @param {string} method @param {any} response */
function validateSkillResolutionListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['items']), 'body');
  assertResponseArray(method, value.items, 'body.items').forEach((item, index) => validateSkillResolutionListItem(method, item, `body.items[${index}]`));
  return value;
}

/** @param {string} method @param {any} response */
function validateSkillResolutionPreviewResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['conflict_id', 'kind', 'items']), 'body');
  validateStringFields(method, value, 'body', ['conflict_id', 'kind'], []);
  assertResponseArray(method, value.items, 'body.items').forEach((raw, index) => {
    const label = `body.items[${index}]`;
    const item = assertResponseRecord(method, raw, label);
    assertOnlyResponseKeys(method, item, new Set(['action', 'provider', 'preview_id', 'source_provider', 'source_path_id', 'source_path', 'target_path', 'source_hash', 'target_hash', 'preview_hash', 'backup_path', 'confirm_delete_mirror_hash', 'diff']), label);
    validateStringFields(method, item, label, ['action'], ['provider', 'preview_id', 'source_provider', 'source_path_id', 'source_path', 'target_path', 'source_hash', 'target_hash', 'preview_hash', 'backup_path', 'confirm_delete_mirror_hash', 'diff']);
  });
  return value;
}

/** @param {string} method @param {any} response */
function validateSkillResolutionApplyResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['action', 'name', 'resultingHash', 'partialFailure', 'followUpAction']), 'body');
  validateRequiredFields(method, value, 'body', {
    stringKeys: ['action', 'name', 'resultingHash', 'followUpAction'],
    integerKeys: [],
    booleanKeys: ['partialFailure'],
  });
  return value;
}

/** @param {string} method @param {any} response */
function validateSkillToolsListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['tools']), 'body');
  assertResponseArray(method, value.tools, 'body.tools').forEach((raw, index) => {
    const label = `body.tools[${index}]`;
    const item = assertResponseRecord(method, raw, label);
    assertOnlyResponseKeys(method, item, new Set(['id', 'cwd', 'methodName', 'description', 'enabled', 'createdAt', 'updatedAt']), label);
    validateRequiredFields(method, item, label, {
      stringKeys: ['cwd', 'methodName', 'description', 'createdAt', 'updatedAt'],
      integerKeys: ['id'],
      booleanKeys: ['enabled'],
    });
  });
  return value;
}

/** @param {string} method @param {any} response @param {string} label */
function validateDatasourceDocument(method, response, label) {
  const document = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, document, new Set(['documentId', 'sourcePath', 'fileName', 'extension', 'sizeBytes', 'contentHash', 'chunkCount', 'totalChars', 'status', 'errorMessage', 'createdAt', 'updatedAt']), label);
  validateRequiredFields(method, document, label, {
    stringKeys: ['sourcePath', 'fileName', 'extension', 'contentHash', 'status', 'errorMessage', 'createdAt', 'updatedAt'],
    integerKeys: ['documentId', 'sizeBytes', 'chunkCount', 'totalChars'],
  });
  return document;
}

/** @param {string} method @param {any} response @param {string} label @param {number} [documentId] */
function validateDatasourceChunk(method, response, label, documentId) {
  const chunk = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, chunk, new Set(['id', 'documentId', 'chunkIndex', 'content', 'charCount', 'byteCount', 'embeddingModel', 'embeddingDim', 'tokenCount', 'createdAt']), label);
  validateRequiredFields(method, chunk, label, {
    stringKeys: ['content', 'embeddingModel', 'createdAt'],
    integerKeys: ['id', 'documentId', 'chunkIndex', 'charCount', 'byteCount', 'embeddingDim', 'tokenCount'],
  });
  if (documentId !== undefined && chunk.documentId !== documentId) {
    throw new TypeError(`${method} response ${label}.documentId must match body.document.documentId`);
  }
}

/** @param {string} method @param {any} response */
function validateDatasourceDocumentsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['documents']), 'body');
  assertResponseArray(method, value.documents, 'body.documents').forEach((item, index) => validateDatasourceDocument(method, item, `body.documents[${index}]`));
  return value;
}

/** @param {string} method @param {any} value */
function validateDatasourcePageFields(method, value) {
  validateRequiredFields(method, value, 'body', {
    stringKeys: [], integerKeys: ['nextCursor'], booleanKeys: ['hasMore'],
  });
  const chunks = assertResponseArray(method, value.chunks, 'body.chunks');
  return chunks;
}

/** @param {string} method @param {any} response */
function validateDatasourceDetailResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['document', 'chunks', 'hasMore', 'nextCursor']), 'body');
  const document = validateDatasourceDocument(method, value.document, 'body.document');
  validateDatasourcePageFields(method, value).forEach((item, index) => validateDatasourceChunk(method, item, `body.chunks[${index}]`, document.documentId));
  return value;
}

/** @param {string} method @param {any} response */
function validateDatasourceChunksResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['chunks', 'hasMore', 'nextCursor']), 'body');
  validateDatasourcePageFields(method, value).forEach((item, index) => validateDatasourceChunk(method, item, `body.chunks[${index}]`));
  return value;
}

/** @param {string} method @param {any} response */
function validateDatasourceDocumentResponse(method, response) {
  return validateDatasourceDocument(method, response, 'body');
}

/** @param {Record<string, string>} methods */
export function createBackendResponseValidators(methods) {
  const controlSpecs = Object.freeze({
    [methods.MCP_SERVER_SQLITE_START]: { serverName: 'sqlite', enabled: true },
    [methods.MCP_SERVER_SQLITE_STOP]: { serverName: 'sqlite', enabled: false },
    [methods.MCP_SERVER_PLAYWRIGHT_START]: { serverName: 'playwright', enabled: true },
    [methods.MCP_SERVER_PLAYWRIGHT_STOP]: { serverName: 'playwright', enabled: false },
  });
  /** @type {(method: string, response: unknown) => unknown} */
  const validateControlResponse = (method, response) => validateMCPServerControlResponse(method, response, controlSpecs);

  /** @type {(method: string, response: unknown) => unknown} */
  const validateModelProviderRegistryResponse = (method, response) => {
    if (method === methods.MODEL_PROVIDERS_SAVE) {
      return validateSavedModelProviderRegistryResponse(method, response);
    }
    if (method === methods.MODEL_PROVIDERS_APPLY || method === methods.MODEL_PROVIDERS_LIST) {
      return validateParsedModelProviderRegistryResponse(method, response);
    }
    throw new Error(`${method} response validator is not registered for model provider registry`);
  };

  return Object.freeze({
    [methods.APP_UPDATE_INSTALL]: validateAppUpdateInstallResponse,
    [methods.APP_UPDATE_INSTALL_LATEST]: validateAppUpdateInstallResponse,
    [methods.CONFIG_READ]: validateRuntimeConfigResponse,
    [methods.CONFIG_BUILTIN_TOOLS_READ]: validateBuiltinToolsResponse,
    [methods.CONFIG_BUILTIN_TOOLS_WRITE]: validateBuiltinToolsResponse,
    [methods.CONFIG_LSP_PROMPT_HINT_READ]: validateLspPromptHintResponse,
    [methods.CONFIG_LSP_PROMPT_HINT_WRITE]: validateLspPromptHintResponse,
    [methods.DASHBOARD_SHARED_FILES]: validateSharedFilesDashboardResponse,
    [methods.CRONJOB_LIST]: validateCronListResponse,
    [methods.DASHBOARD_WORKFLOW_MATERIAL_WRITE]: validateWorkflowMaterialWriteResponse,
    [methods.DASHBOARD_PROMPTS]: validateDashboardPromptsResponse,
    [methods.MCP_SERVER_LIST]: validateMCPServerListResponse,
    [methods.TOOLBRIDGE_TOOLS_LIST]: validateToolbridgeToolsListResponse,
    [methods.MCP_SERVER_SQLITE_START]: validateControlResponse,
    [methods.MCP_SERVER_SQLITE_STOP]: validateControlResponse,
    [methods.MCP_SERVER_PLAYWRIGHT_START]: validateControlResponse,
    [methods.MCP_SERVER_PLAYWRIGHT_STOP]: validateControlResponse,
    [methods.MODEL_PROVIDERS_APPLY]: validateModelProviderRegistryResponse,
    [methods.MODEL_PROVIDERS_LIST]: validateModelProviderRegistryResponse,
    [methods.MODEL_PROVIDERS_SAVE]: validateModelProviderRegistryResponse,
    [methods.OBSERVABILITY_ERROR_LIST]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_FRONTEND_INGEST]: validateFrontendIngestResponse,
    [methods.OBSERVABILITY_RECENT_LIST]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_SLOW_LIST]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_THREAD_RECENT]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_TRACE_GET]: validateObservabilityResultResponse,
    [methods.UI_CODE_SAVE]: validateCodeSaveResponse,
    [methods.UI_DASHBOARD_GET]: validateDashboardPageResponse,
    [methods.UI_OPEN_NEW_WINDOW]: validateOpenWindowResponse,
    [methods.UI_PREFERENCES_SET]: validateOKResponse,
    [methods.UI_PROJECTS_ADD]: validateProjectsStateResponse,
    [methods.UI_PROJECTS_GET]: validateProjectsStateResponse,
    [methods.UI_PROJECTS_REMOVE]: validateProjectsStateResponse,
    [methods.UI_PROJECTS_SET_ACTIVE]: validateProjectsStateResponse,
    [methods.UI_VIDEO_GET_API_KEY]: validateVideoAPIKeyStatusResponse,
    [methods.UI_VIDEO_SET_API_KEY]: validateOKResponse,
    [methods.UI_WINDOW_BOOTSTRAP_GET]: validateWindowBootstrapResponse,
    [methods.UI_SIDEBAR_GET]: validateSidebarStateResponse,
    [methods.UI_STATE_GET]: validateUIStateResponse,
    [methods.UI_MEMORY_GET]: validateMemorySnapshotResponse,
    [methods.UI_MEMORY_ENTRY_GET]: validateMemoryEntryDetailResponse,
    [methods.UI_MEMORY_ENTRY_UPSERT]: validateMemoryEntryDetailResponse,
    [methods.UI_MEMORY_ENTRY_DELETE]: validateMemoryEntryDeleteResponse,
    [methods.UI_MEMORY_AUTO_DREAM_SET_INTENT]: validateMemoryAutoDreamIntentResponse,
    [methods.UI_MEMORY_ENTRY_MERGE]: validateMemoryEntryDetailResponse,
    [methods.UI_MEMORY_SIMILARITY_IGNORE]: validateMemorySimilarityIgnoreResponse,
    [methods.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START]: validateMemoryConsolidationJobResponse,
    [methods.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS]: validateMemoryConsolidationJobResponse,
    [methods.UI_SHARED_FILE_GET]: validateSharedFileDetailResponse,
    [methods.UI_SHARED_FILE_DELETE]: validateSharedFileDeleteResponse,
    [methods.PROMPT_ASSETS_LIST]: validatePromptAssetsResponse,
    [methods.PROMPTS_GET]: validatePromptDetailResponse,
    [methods.PROMPTS_WRITE]: validatePromptDetailResponse,
    [methods.PROMPTS_DELETE]: validateOKResponse,
    [methods.PROMPT_INTENTS_DRAFT]: validatePromptIntentDraftResponse,
    [methods.PROMPT_INTENTS_COMMIT]: validatePromptIntentCommitResponse,
    [methods.PROMPT_INTENTS_DISCARD]: validatePromptIntentDiscardResponse,
    [methods.PROMPT_INTENTS_DRY_RUN]: validatePromptIntentDryRunResponse,
    [methods.PERSONALIZATION_PROFILE_GET]: validatePersonalizationProfileResponse,
    [methods.PERSONALIZATION_PROFILE_SAVE]: validatePersonalizationProfileResponse,
    [methods.SKILLS_LOCAL_READ]: validateSkillReadResponse,
    [methods.SKILLS_LOCAL_LIST_FILES]: validateSkillFilesResponse,
    [methods.SKILLS_LOCAL_IMPORT_DIR]: validateSkillImportResponse,
    [methods.SKILLS_SUMMARY_SUGGEST]: validateSkillSummarySuggestionResponse,
    [methods.SKILLS_RESOLUTION_LIST]: validateSkillResolutionListResponse,
    [methods.SKILLS_RESOLUTION_PREVIEW]: validateSkillResolutionPreviewResponse,
    [methods.SKILLS_RESOLUTION_APPLY]: validateSkillResolutionApplyResponse,
    [methods.SKILL_TOOLS_LIST]: validateSkillToolsListResponse,
    [methods.DATASOURCE_V2_LIST]: validateDatasourceDocumentsResponse,
    [methods.DATASOURCE_V2_GET]: validateDatasourceDetailResponse,
    [methods.DATASOURCE_V2_LIST_CHUNKS]: validateDatasourceChunksResponse,
    [methods.DATASOURCE_V2_UPDATE]: validateDatasourceDocumentResponse,
    [methods.THREAD_ARCHIVE]: validateNullResponse,
    [methods.THREAD_UNARCHIVE]: validateNullResponse,
    [methods.THREAD_DELETE]: validateNullResponse,
    [methods.THREAD_CONFIG_GET]: validateThreadConfigResponse,
    [methods.THREAD_CONFIG_SET]: validateThreadConfigResponse,
    [methods.THREAD_COMPACT_START]: validateThreadCompactResponse,
    [methods.THREAD_FORK]: validateThreadForkResponse,
    [methods.THREAD_NAME_SET]: validateNullResponse,
    [methods.THREAD_RECOVER]: validateThreadRecoverResponse,
    [methods.THREAD_START]: validateThreadStartResponse,
    [methods.THREAD_MESSAGES]: validateThreadMessagesResponse,
    [methods.THREAD_PROMPT_HISTORY]: validateThreadPromptHistoryResponse,
    [methods.THREAD_RESOLVE]: validateThreadResolveResponse,
    [methods.APPROVAL_RESPOND]: validateNullResponse,
    [methods.TURN_START]: validateTurnStartResponse,
    [methods.TURN_FORCE_COMPLETE]: validateTurnForceCompleteResponse,
    [methods.DASHBOARD_LOGS]: validateDashboardLogsResponse,
    [methods.DASHBOARD_DAG_DETAIL]: validateDashboardDagDetailResponse,
    [methods.DASHBOARD_DAG_RUNS]: validateDashboardDagRunsResponse,
    [methods.DASHBOARD_DAG_RUN]: validateDashboardDagRunResponse,
    [methods.DASHBOARD_DAG_START]: validateDashboardDagStartResponse,
    [methods.DASHBOARD_DAG_CREATE_AND_START]: validateDashboardDagCreateAndStartResponse,
    [methods.WORKFLOW_TEMPLATES_LIST]: validateWorkflowTemplatesListResponse,
    [methods.WORKFLOW_TEMPLATES_GET]: validateWorkflowTemplateResponse,
    [methods.WORKFLOW_TEMPLATES_RENDER_DAG]: validateWorkflowTemplateDraftResponse,
    [methods.WORKFLOW_TEMPLATES_SAVE]: validateWorkflowTemplateSaveResponse,
  });
}
