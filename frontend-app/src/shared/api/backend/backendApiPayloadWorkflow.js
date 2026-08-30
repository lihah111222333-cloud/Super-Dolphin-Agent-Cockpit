import { RPC_METHODS } from './backendRpcMethods.js';
import {
  objectPrototype,
  assertPlainObject,
  normalizeString,
  optionalPayloadObject,
  requireCwd,
  requireKey,
  cleanObject,
  requireBoolean,
  normalizeOptionalLimit,
} from './backendApiCommon.js';

/** @typedef {Record<string, unknown>} WorkflowPayload */

/** @param {unknown} params */
function dashboardDagStartPayload(params) {
  // 误判防护：dashboardDagStartPayload 要求 dagKey，避免 DAG start 空目标。
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_START, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_START, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    triggerSource: normalizeString(payload.triggerSource),
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

/** @param {unknown} params */
function dashboardDagCreateAndStartPayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START;
  const payload = requireKey(method, requireKey(method, assertPlainObject(method, params), 'dagKey'), 'title');
  if (!Array.isArray(payload.nodes) || payload.nodes.length === 0) {
    throw new Error(`${method}: nodes must be a non-empty array`);
  }
  if (payload.metadata != null && (typeof payload.metadata !== 'object' || Array.isArray(payload.metadata))) {
    throw new Error(`${method}: metadata must be an object`);
  }
  return cleanObject({
    dagKey: payload.dagKey,
    title: payload.title,
    description: normalizeString(payload.description),
    finalNodeKey: normalizeString(payload.finalNodeKey || payload.final_node_key),
    metadata: optionalPayloadObject(payload.metadata),
    nodes: payload.nodes,
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

/** @param {unknown} params */
function dashboardWorkflowMaterialWritePayload(params) {
  const method = RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE;
  const payload = assertPlainObject(method, params);
  const path = normalizeString(payload.path);
  const content = typeof payload.content === 'string' ? payload.content : '';
  if (!path) throw new Error(`${method}: path is required`);
  if (!content.trim()) throw new Error(`${method}: content is required`);
  return { path, content };
}

/** @param {unknown} params */
function dashboardDagDispatchNodePayload(params) {
  const payload = requireNumber(
    RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE,
    requireKey(
      RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, params), 'dagKey'),
      'nodeKey',
    ),
    'runId',
  );
  const assignedTo = normalizeString(payload.assignedTo || payload.assigned_to);
  if (!assignedTo) throw new Error(`${RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE}: assignedTo is required`);
  return {
    dagKey: payload.dagKey,
    runId: payload.runId,
    nodeKey: payload.nodeKey,
    assignedTo,
  };
}

/** @param {unknown} value @returns {number | undefined} */
function optionalInteger(value) {
  if (value === undefined || value === null || value === '') return undefined;
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return undefined;
  return Math.trunc(parsed);
}

/** @param {string} method @param {unknown} params @param {string} key */
function requireNumber(method, params, key) {
  // 误判防护：requireNumber 阻断缺失或非数字的 backend RPC 参数。
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, key) || payload[key] === null || payload[key] === '') {
    throw new Error(`${method}: ${key} is required`);
  }
  const value = Number(payload[key]);
  if (!Number.isFinite(value)) {
    throw new Error(`${method}: ${key} must be a number`);
  }
  return { ...payload, [key]: value };
}

/** @param {unknown} params */
function dashboardDagsPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.DASHBOARD_DAGS, params);
  return cleanObject({
    keyword: normalizeString(payload.keyword),
    status: normalizeString(payload.status),
    limit: optionalInteger(payload.limit),
  });
}

/** @param {unknown} params */
function dashboardDagRunsPayload(params) {
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_RUNS, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_RUNS, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    status: normalizeString(payload.status),
    limit: optionalInteger(payload.limit),
  });
}

/** @param {unknown} params */
function dashboardDagTerminatePayload(params) {
  const payload = requireKey(
    RPC_METHODS.DASHBOARD_DAG_TERMINATE,
    requireKey(RPC_METHODS.DASHBOARD_DAG_TERMINATE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_TERMINATE, params), 'dagKey'),
    'runKey',
  );
  return cleanObject({
    dagKey: payload.dagKey,
    runKey: payload.runKey,
    reason: normalizeString(payload.reason),
  });
}

/** @param {unknown} params */
function dashboardDagApplyOpsPayload(params) {
  // 误判防护：dashboardDagApplyOpsPayload 要求 dagKey/baseVersion/ops 数组。
  const payload = requireNumber(
    RPC_METHODS.DASHBOARD_DAG_APPLY_OPS,
    requireKey(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, params), 'dagKey'),
    'baseVersion',
  );
  if (!Array.isArray(payload.ops)) {
    throw new Error(`${RPC_METHODS.DASHBOARD_DAG_APPLY_OPS}: ops must be an array`);
  }
  return {
    dagKey: payload.dagKey,
    baseVersion: payload.baseVersion,
    ops: payload.ops,
  };
}

/** @param {string} method @param {unknown} params */
function cronIdPayload(method, params) {
  return {
    id: requireKey(method, assertPlainObject(method, params), 'id').id,
  };
}

/** @param {unknown} params */
function cronSetEnabledPayload(params) {
  const payload = requireBoolean(
    RPC_METHODS.CRONJOB_SET_ENABLED,
    requireKey(RPC_METHODS.CRONJOB_SET_ENABLED, assertPlainObject(RPC_METHODS.CRONJOB_SET_ENABLED, params), 'id'),
    'enabled',
  );
  return { id: payload.id, enabled: payload.enabled };
}

/** @param {unknown} params */
function cronListRunsPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.CRONJOB_LIST_RUNS, params);
  const jobID = normalizeString(payload.job_id || payload.jobId);
  if (!jobID) throw new Error(`${RPC_METHODS.CRONJOB_LIST_RUNS}: job_id is required`);
  return cleanObject({
    job_id: jobID,
    limit: normalizeOptionalLimit(RPC_METHODS.CRONJOB_LIST_RUNS, payload),
  });
}

/** @param {unknown} params */
function cronListPayload(params) {
  const method = RPC_METHODS.CRONJOB_LIST;
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, 'limit') || !hasOwn(payload, 'cursor')) {
    throw new Error(`${method}: limit and cursor are required`);
  }
  if (Object.keys(payload).some((key) => key !== 'limit' && key !== 'cursor')) {
    throw new Error(`${method}: unexpected payload field`);
  }
  if (typeof payload.limit !== 'number' || !Number.isInteger(payload.limit) || payload.limit < 1 || payload.limit > 100) {
    throw new Error(`${method}: limit must be an integer within range`);
  }
  if (typeof payload.cursor !== 'string') throw new Error(`${method}: cursor must be a string`);
  return { limit: payload.limit, cursor: payload.cursor };
}

/** @param {string} method @param {unknown} params @param {{ requireId?: boolean }} options */
function cronJobMutationPayload(method, params, options = {}) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  const name = normalizeString(payload.name);
  const prompt = normalizeString(payload.prompt);
  const scheduleExpr = normalizeString(payload.schedule_expr ?? payload.scheduleExpr);
  if (!name) throw new Error(`${method}: name is required`);
  if (!prompt) throw new Error(`${method}: prompt is required`);
  if (!scheduleExpr) throw new Error(`${method}: schedule_expr is required`);
  return cleanObject({
    id: options.requireId ? requireKey(method, payload, 'id').id : undefined,
    cwd: payload.cwd,
    name,
    prompt,
    schedule_type: normalizeString(payload.schedule_type ?? payload.scheduleType),
    schedule_expr: scheduleExpr,
    timezone: normalizeString(payload.timezone),
    provider: normalizeString(payload.provider),
    model: normalizeString(payload.model),
    config: cronJobConfigPayload(method, payload),
    skills: cronJobSkillsPayload(method, payload),
    notify_channel: normalizeString(payload.notify_channel ?? payload.notifyChannel),
    enabled: cronJobEnabledPayload(method, payload),
    next_run_at: normalizeString(payload.next_run_at ?? payload.nextRunAt),
    max_attempts: cronJobMaxAttemptsPayload(method, payload),
  });
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobConfigPayload(method, payload) {
  if (!hasOwn(payload, 'config') || payload.config == null) return undefined;
  if (typeof payload.config !== 'object' || Array.isArray(payload.config)) {
    throw new Error(`${method}: config must be an object`);
  }
  return payload.config;
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobSkillsPayload(method, payload) {
  if (!hasOwn(payload, 'skills') || payload.skills == null) return undefined;
  if (!Array.isArray(payload.skills)) throw new Error(`${method}: skills must be an array`);
  return payload.skills.map(normalizeString).filter(Boolean);
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobEnabledPayload(method, payload) {
  if (!hasOwn(payload, 'enabled') || payload.enabled == null) return undefined;
  if (typeof payload.enabled !== 'boolean') throw new Error(`${method}: enabled must be boolean`);
  return payload.enabled;
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobMaxAttemptsPayload(method, payload) {
  const raw = payload.max_attempts ?? payload.maxAttempts;
  if (raw === undefined || raw === null || raw === '') return undefined;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${method}: max_attempts must be a non-negative integer`);
  }
  return value;
}

/** @param {string} method @param {WorkflowPayload} payload */
function codeProjectsPayload(method, payload) {
  if (!hasOwn(payload, 'projects') || payload.projects == null) return undefined;
  if (!Array.isArray(payload.projects)) throw new Error(`${method}: projects must be an array`);
  const projects = payload.projects.map(normalizeString).filter(Boolean);
  return projects.length > 0 ? projects : undefined;
}

/** @param {string} method @param {WorkflowPayload} payload @param {string} key */
function optionalCodeInteger(method, payload, key) {
  if (!hasOwn(payload, key) || payload[key] === undefined || payload[key] === null || payload[key] === '') return undefined;
  const value = Number(payload[key]);
  if (!Number.isFinite(value)) throw new Error(`${method}: ${key} must be a number`);
  return Math.trunc(value);
}

/** @param {string} method @param {WorkflowPayload} payload @param {string} key */
function requiredCodeSaveText(method, payload, key) {
  if (!hasOwn(payload, key) || payload[key] === null || payload[key] === undefined) {
    throw new Error(`${method}: ${key} is required`);
  }
  if (typeof payload[key] !== 'string') {
    throw new Error(`${method}: ${key} must be a string`);
  }
  const value = payload[key].trim();
  if (!value) throw new Error(`${method}: ${key} is required`);
  return value;
}

/** @param {string} method @param {unknown} params @param {{ includePosition?: boolean, includeContent?: boolean }} options */
function codeFilePayload(method, params, options = {}) {
  const payload = requireKey(method, assertPlainObject(method, params), 'filePath');
  /** @type {WorkflowPayload} */
  const request = {
    filePath: payload.filePath,
    project: normalizeString(payload.project),
    projects: codeProjectsPayload(method, payload),
  };
  if (options.includePosition) {
    request.line = optionalCodeInteger(method, payload, 'line');
    request.column = optionalCodeInteger(method, payload, 'column');
  }
  if (options.includeContent) {
    if (!hasOwn(payload, 'content')) throw new Error(`${method}: content is required`);
    if (typeof payload.content !== 'string') throw new Error(`${method}: content must be a string`);
    const previewMode = requiredCodeSaveText(method, payload, 'previewMode');
    if (previewMode !== 'full') throw new Error(`${method}: previewMode must be full`);
    request.content = payload.content;
    request.previewMode = previewMode;
    request.contentVersion = requiredCodeSaveText(method, payload, 'contentVersion');
  }
  return cleanObject(request);
}


/** @param {unknown} params */
function videoApiKeyPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.UI_VIDEO_SET_API_KEY, params);
  const apiKey = normalizeString(payload.apiKey);
  if (!apiKey) throw new Error(`${RPC_METHODS.UI_VIDEO_SET_API_KEY}: apiKey is required`);
  return { apiKey };
}

/** @param {unknown} params */
function builtinToolWritePayload(params) {
  const payload = requireBoolean(
    RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE,
    requireKey(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, requireCwd(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, params), 'id'),
    'enabled',
  );
  return { cwd: payload.cwd, id: payload.id, enabled: payload.enabled };
}

/** @param {unknown} params */
function dashboardLogsPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.DASHBOARD_LOGS, params);
  return cleanObject({
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
  });
}

/** @param {object} value @param {PropertyKey} key @returns {boolean} */
function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}


export {
  dashboardDagStartPayload, dashboardDagCreateAndStartPayload, dashboardWorkflowMaterialWritePayload, dashboardDagDispatchNodePayload, optionalInteger, requireNumber,
  dashboardDagsPayload, dashboardDagRunsPayload, dashboardDagTerminatePayload, dashboardDagApplyOpsPayload, cronIdPayload, cronSetEnabledPayload,
  cronListRunsPayload, cronListPayload, cronJobMutationPayload, cronJobConfigPayload, cronJobSkillsPayload, cronJobEnabledPayload, cronJobMaxAttemptsPayload,
  codeProjectsPayload, optionalCodeInteger, codeFilePayload,
  videoApiKeyPayload, builtinToolWritePayload, dashboardLogsPayload, hasOwn,
};
