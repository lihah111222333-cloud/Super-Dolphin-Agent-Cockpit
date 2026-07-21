// @ts-check

import {
  validateSidebarStateResponse as validateCoreSidebarStateResponse,
  validateThreadRecoverResponse as validateCoreThreadRecoverResponse,
  validateUIStateResponse as validateCoreUIStateResponse,
} from './backendResponseValidatorsCore.js';
import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateStringFields,
} from './backendResponseValidatorShared.js';

const PROMPT_HISTORY_RESPONSE_KEYS = new Set(['entries', 'nextCursor', 'hasMore', 'nonce']);
const PROMPT_HISTORY_ENTRY_KEYS = new Set(['threadId', 'messageId', 'text', 'createdAt']);
const TOOLBRIDGE_TOOLS_RESPONSE_KEYS = new Set(['tools']);
const TOOLBRIDGE_TOOL_RESPONSE_KEYS = new Set(['serverName', 'toolName', 'displayName', 'description', 'enabled', 'disabledReason']);
const ACTIVITY_STATS_RESPONSE_KEYS = new Set(['lspCalls', 'commands', 'fileEdits', 'toolCalls']);
const UI_STATE_RESPONSE_KEYS = new Set([
  'threads',
  'agents',
  'active_turn',
  'recent_turns',
  'token_usage',
  'tokenUsage',
  'statuses',
  'interruptibleByThread',
  'statusHeadersByThread',
  'statusDetailsByThread',
  'tokenUsageByThread',
  'agentMetaById',
  'agentRuntimeById',
  'diffTextByThread',
  'diffRevisionByThread',
  'timelinesByThread',
  'activityStatsByThread',
  'alertsByThread',
  'unchanged',
  'activeThreadId',
  'activeCmdThreadId',
  'mainAgentId',
  'mainAgentState',
  'settings.showInjectedPromptInChat',
  'viewPrefs.chat',
  'viewPrefs.cmd',
  'threadPins.chat',
  'threadArchives.chat',
  'groups',
]);
const SIDEBAR_STATE_RESPONSE_KEYS = new Set([...UI_STATE_RESPONSE_KEYS, 'workspace']);
const SIDEBAR_REQUIRED_RESPONSE_KEYS = ['threads', 'agents', 'recent_turns', 'workspace', 'token_usage'];

/** @param {string} method @param {unknown} value @param {string} path */
function assertUIStateNonNegativeInteger(method, value, path) {
  if (!Number.isInteger(value)) {
    throw new TypeError(`${method} response ${path} must be an integer`);
  }
  if (/** @type {number} */ (value) < 0) {
    throw new TypeError(`${method} response ${path} must be a non-negative integer`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label @param {'string' | 'boolean'} valueType */
function validateUIStateTypedMap(method, value, label, valueType) {
  const map = assertResponseRecord(method, value, label);
  for (const [threadId, candidate] of Object.entries(map)) {
    if (!normalizeString(threadId)) {
      throw new TypeError(`${method} response ${label} thread id must be non-empty`);
    }
    if (typeof candidate !== valueType) {
      throw new TypeError(`${method} response ${label}.${threadId} must be a ${valueType}`);
    }
  }
}

/** @param {string} method @param {unknown} value */
function validateUIStateActivityMap(method, value) {
  const label = 'activityStatsByThread';
  const activityMap = assertResponseRecord(method, value, label);
  for (const [threadId, candidate] of Object.entries(activityMap)) {
    if (!normalizeString(threadId)) {
      throw new TypeError(`${method} response ${label} thread id must be non-empty`);
    }
    const activity = assertResponseRecord(method, candidate, `${label}.${threadId}`);
    assertOnlyResponseKeys(method, activity, ACTIVITY_STATS_RESPONSE_KEYS, `${label}.${threadId}`);
    for (const key of ['lspCalls', 'commands', 'fileEdits']) {
      assertUIStateNonNegativeInteger(method, activity[key], `${label}.${threadId}.${key}`);
    }
    if (!hasOwn(activity, 'toolCalls')) continue;
    const toolCalls = assertResponseRecord(method, activity.toolCalls, `${label}.${threadId}.toolCalls`);
    for (const [toolName, count] of Object.entries(toolCalls)) {
      if (!normalizeString(toolName)) {
        throw new TypeError(`${method} response ${label}.${threadId}.toolCalls tool name must be non-blank`);
      }
      assertUIStateNonNegativeInteger(method, count, `${label}.${threadId}.toolCalls.${toolName}`);
    }
  }
}

/** @param {string} method @param {unknown} value */
function validateUIStateAgentRuntimeMap(method, value) {
  const label = 'agentRuntimeById';
  const runtimeMap = assertResponseRecord(method, value, label);
  for (const [runtimeId, candidate] of Object.entries(runtimeMap)) {
    if (!normalizeString(runtimeId)) {
      throw new TypeError(`${method} response ${label} runtime id must be non-empty`);
    }
    assertResponseRecord(method, candidate, `${label}.${runtimeId}`);
  }
}

/** @param {string} method @param {Record<string, unknown>} value */
function validateStateMaps(method, value) {
  for (const key of ['statuses', 'statusHeadersByThread', 'statusDetailsByThread']) {
    if (hasOwn(value, key)) validateUIStateTypedMap(method, value[key], key, 'string');
  }
  if (hasOwn(value, 'interruptibleByThread')) {
    validateUIStateTypedMap(method, value.interruptibleByThread, 'interruptibleByThread', 'boolean');
  }
  if (hasOwn(value, 'activityStatsByThread')) {
    validateUIStateActivityMap(method, value.activityStatsByThread);
  }
  if (hasOwn(value, 'agentRuntimeById')) {
    validateUIStateAgentRuntimeMap(method, value.agentRuntimeById);
  }
}

/** @param {string} method @param {unknown} response */
function validateRuntimeUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, UI_STATE_RESPONSE_KEYS, 'body');
  const requiredSnapshotFields = [
    ['threads'],
    ['agents'],
    ['token_usage', 'tokenUsage'],
  ];
  const missingFields = requiredSnapshotFields
    .filter((aliases) => !aliases.some((key) => hasOwn(value, key)))
    .map((aliases) => aliases.join(' or '));
  validateCoreUIStateResponse(method, value);
  validateStateMaps(method, value);
  if (missingFields.length > 0) {
    throw new Error(`${method} response missing UI state snapshot fields; required: ${missingFields.join(', ')}`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateSidebarStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, SIDEBAR_STATE_RESPONSE_KEYS, 'body');
  const { activityStatsByThread, ...coreValue } = value;
  for (const requiredField of SIDEBAR_REQUIRED_RESPONSE_KEYS) {
    if (!hasOwn(value, requiredField)) {
      throw new TypeError(`${method} response ${requiredField} is required`);
    }
  }
  validateCoreSidebarStateResponse(method, coreValue);
  validateStateMaps(method, value);
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateThreadRecoverResponse(method, response) {
  const envelope = assertBackendResponseObject(method, response);
  const responseThread = assertResponseRecord(method, envelope.thread, 'thread');
  if (!normalizeString(responseThread.id)) {
    throw new TypeError(`${method} response thread.id must be a non-empty string`);
  }
  const value = validateCoreThreadRecoverResponse(method, response);
  const thread = assertResponseRecord(method, value.thread, 'thread');
  if (thread.status !== 'recovering') {
    throw new TypeError(`${method} response thread.status must be recovering`);
  }
  if (!normalizeString(value.mode)) {
    throw new TypeError(`${method} response mode must be a non-empty string`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateThreadPromptHistoryResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, PROMPT_HISTORY_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.entries)) throw new TypeError(`${method} response entries must be an array`);
  if (value.entries.length > 50) throw new TypeError(`${method} response entries must not exceed 50`);
  /** @type {unknown[]} */ (value.entries).forEach((candidate, index) => {
    const label = `entries[${index}]`;
    const entry = assertResponseRecord(method, candidate, label);
    assertOnlyResponseKeys(method, entry, PROMPT_HISTORY_ENTRY_KEYS, label);
    validateStringFields(method, entry, label, ['threadId', 'messageId', 'createdAt'], ['text']);
  });
  if (typeof value.nextCursor !== 'string') throw new TypeError(`${method} response nextCursor must be a string`);
  if (new TextEncoder().encode(value.nextCursor).byteLength > 2048) {
    throw new TypeError(`${method} response nextCursor exceeds 2048 bytes`);
  }
  if (typeof value.hasMore !== 'boolean') throw new TypeError(`${method} response hasMore must be a boolean`);
  if (value.hasMore && !value.nextCursor) {
    throw new TypeError(`${method} response nextCursor must be non-empty when hasMore is true`);
  }
  if (!value.hasMore && value.nextCursor !== '') {
    throw new TypeError(`${method} response nextCursor must be empty when hasMore is false`);
  }
  if (typeof value.nonce !== 'string' || !normalizeString(value.nonce)) throw new TypeError(`${method} response nonce must be a non-empty string`);
  if (new TextEncoder().encode(value.nonce).byteLength > 2048) {
    throw new TypeError(`${method} response nonce exceeds 2048 bytes`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateToolbridgeToolsListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, TOOLBRIDGE_TOOLS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.tools)) throw new TypeError(`${method} response tools must be an array`);
  /** @type {unknown[]} */ (value.tools).forEach((candidate, index) => {
    const label = `tools[${index}]`;
    const tool = assertResponseRecord(method, candidate, label);
    assertOnlyResponseKeys(method, tool, TOOLBRIDGE_TOOL_RESPONSE_KEYS, label);
    for (const key of ['serverName', 'toolName', 'displayName']) {
      if (!normalizeString(tool[key])) {
        throw new TypeError(`${method} response ${label}.${key} must be a non-empty string`);
      }
    }
    validateStringFields(method, tool, label, [], ['description', 'disabledReason']);
    if (typeof tool.enabled !== 'boolean') {
      throw new TypeError(`${method} response ${label}.enabled must be a boolean`);
    }
  });
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateCronListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const keys = new Set(['jobs', 'next_cursor', 'has_more']);
  if (Object.keys(value).length !== keys.size || [...keys].some((key) => !hasOwn(value, key))) {
    throw new Error(`${method} response must contain exactly jobs, next_cursor, has_more`);
  }
  assertOnlyResponseKeys(method, value, keys, 'body');
  if (!Array.isArray(value.jobs)) throw new TypeError(`${method} response jobs must be an array`);
  if (typeof value.next_cursor !== 'string') throw new TypeError(`${method} response next_cursor must be a string`);
  if (typeof value.has_more !== 'boolean') throw new TypeError(`${method} response has_more must be a boolean`);
  if (!value.has_more && value.next_cursor !== '') {
    throw new Error(`${method} response final page next_cursor must be empty`);
  }
  if (value.has_more && !value.next_cursor) {
    throw new Error(`${method} response next_cursor is required when has_more`);
  }
  return value;
}

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

/** @param {string} method @param {Record<string, unknown>} value @param {string} label @param {string} key */
function requireResponseString(method, value, label, key) {
  if (typeof value[key] !== 'string') {
    throw new TypeError(`${method} response ${label}.${key} must be a string`);
  }
}

/** @param {string} method @param {Record<string, unknown>} value @param {string} label @param {string} key */
function requireResponseIdentity(method, value, label, key) {
  requireResponseString(method, value, label, key);
  if (!normalizeString(value[key])) {
    throw new TypeError(`${method} response ${label}.${key} must be a non-empty string`);
  }
}

/** @param {string} method @param {Record<string, unknown>} value @param {string} label @param {string} key */
function requireResponseInteger(method, value, label, key) {
  if (!Number.isInteger(value[key])) {
    throw new TypeError(`${method} response ${label}.${key} must be an integer`);
  }
}

/** @param {string} method @param {Record<string, unknown>} value @param {string} label @param {string} key */
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

/** @param {string} method @param {Record<string, unknown>} value @param {string} label @param {readonly string[]} keys */
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
 * @param {unknown} response
 */
export function validateUIStateResponse(method, response) {
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
export {
  validateDashboardDagDetailResponse,
  validateDashboardDagRunResponse,
  validateDashboardDagRunsResponse,
  validateWorkflowTemplateDraftResponse,
  validateWorkflowTemplateResponse,
  validateWorkflowTemplateSaveResponse,
  validateWorkflowTemplatesListResponse,
};
