// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
} from '../shared.js';
const DASHBOARD_DAG_DISPATCH_RESPONSE_KEYS = new Set(['node', 'wakeup_id', 'enqueued']);
const DASHBOARD_DAG_APPLY_OPS_RESPONSE_KEYS = new Set(['newVersion']);
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

/** @param {string} method @param {Record<string, unknown>} value @param {string} label @param {string} key @returns {number} */
function requireResponseInteger(method, value, label, key) {
  const candidate = value[key];
  if (typeof candidate !== 'number' || !Number.isInteger(candidate)) {
    throw new TypeError(`${method} response ${label}.${key} must be an integer`);
  }
  return candidate;
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

/** @param {string} method @param {unknown} response @param {unknown} request */
function validateDashboardDagDispatchNodeResponse(method, response, request) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_DAG_DISPATCH_RESPONSE_KEYS, 'body');
  validateDashboardDagNode(method, value.node, 'body.node');
  const node = assertResponseRecord(method, value.node, 'body.node');
  requireResponseBoolean(method, value, 'body', 'enqueued');
  if (value.enqueued) {
    const wakeupID = requireResponseInteger(method, value, 'body', 'wakeup_id');
    if (wakeupID <= 0) throw new TypeError(`${method} response body.wakeup_id must be a positive integer when enqueued is true`);
  } else if (hasOwn(value, 'wakeup_id')) {
    throw new TypeError(`${method} response body.wakeup_id must be omitted when enqueued is false`);
  }
  const payload = assertResponseRecord(method, request, 'request');
  const expectedDagKey = normalizeString(payload.dagKey);
  const expectedNodeKey = normalizeString(payload.nodeKey);
  const expectedAssignedTo = normalizeString(payload.assignedTo);
  if (!expectedDagKey || !expectedNodeKey || !expectedAssignedTo) {
    throw new TypeError(`${method} request dagKey, nodeKey, and assignedTo must be non-empty strings`);
  }
  if (node.dag_key !== expectedDagKey) {
    throw new TypeError(`${method} response body.node.dag_key must match request.dagKey`);
  }
  if (node.node_key !== expectedNodeKey) {
    throw new TypeError(`${method} response body.node.node_key must match request.nodeKey`);
  }
  if (node.assigned_to !== expectedAssignedTo) {
    throw new TypeError(`${method} response body.node.assigned_to must match request.assignedTo`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateDashboardDagTerminateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(), 'body');
  return value;
}

/** @param {string} method @param {unknown} response @param {unknown} request */
function validateDashboardDagApplyOpsResponse(method, response, request) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_DAG_APPLY_OPS_RESPONSE_KEYS, 'body');
  const newVersion = requireResponseInteger(method, value, 'body', 'newVersion');
  const payload = assertResponseRecord(method, request, 'request');
  const baseVersion = payload.baseVersion;
  if (typeof baseVersion !== 'number' || !Number.isInteger(baseVersion)) {
    throw new TypeError(`${method} request baseVersion must be an integer`);
  }
  if (newVersion <= baseVersion) {
    throw new TypeError(`${method} response body.newVersion must be greater than request.baseVersion`);
  }
  return value;
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

export {
  requireResponseString,
  requireResponseIdentity,
  requireResponseInteger,
  requireResponseBoolean,
  validateResponseStringArray,
  validateNullableResponseStringArray,
  validateOptionalResponseStrings,
  validateDashboardDagApplyOpsResponse,
  validateDashboardDagDetailResponse,
  validateDashboardDagDispatchNodeResponse,
  validateDashboardDagRunResponse,
  validateDashboardDagRunsResponse,
  validateDashboardDagTerminateResponse,
  validateWorkflowArtifactLink,
  validateWorkflowRecoveryAction,
};
