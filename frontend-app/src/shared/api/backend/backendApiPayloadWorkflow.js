import { RPC_METHODS } from './backendRpcMethods.js';
import {
  objectPrototype,
  DEFAULT_PROMPT_INTENT_KIND,
  DEFAULT_PROMPT_SOURCE_TYPE,
  assertPlainObject,
  normalizeString,
  normalizeOptionalString,
  optionalPayloadObject,
  requireCwd,
  requireKey,
  cleanObject,
  requireBoolean,
  normalizeOptionalLimit,
} from './backendApiCommon.js';
import {
} from './backendApiPayloadCore.js';

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
    request.content = payload.content;
  }
  return cleanObject(request);
}


/** @param {unknown} params */
function promptWritePayload(params) {
  const payload = requireKey(
    RPC_METHODS.PROMPTS_WRITE,
    requireCwd(RPC_METHODS.PROMPTS_WRITE, params),
    'name',
  );
  const promptID = normalizeString(payload.id) || normalizeString(payload.key);
  if (!promptID) {
    throw new Error(`${RPC_METHODS.PROMPTS_WRITE}: id or key is required`);
  }
  const priority = optionalInteger(payload.priority);
  const matchWhen = promptMatchWhen(payload);
  return cleanObject({
    cwd: payload.cwd,
    id: promptID,
    name: payload.name,
    description: normalizeString(payload.description),
    agentType: normalizeString(payload.agentType || payload.agent_key || payload.agentKey) || 'main',
    priority,
    when_to_use: normalizeString(payload.when_to_use ?? payload.whenToUse),
    content: hasOwn(payload, 'content') ? normalizeOptionalString(payload.content) : undefined,
    tags: Array.isArray(payload.tags) ? payload.tags : [],
    enabled: hasOwn(payload, 'enabled') ? Boolean(payload.enabled) : undefined,
    scope: normalizeString(payload.scope) || 'project',
    match_when: matchWhen,
  });
}

/** @param {WorkflowPayload} payload */
function promptMatchWhen(payload) {
  if (hasOwn(payload, 'match_when')) return payload.match_when;
  if (hasOwn(payload, 'matchWhen')) return payload.matchWhen;
  return undefined;
}

/** @param {unknown} params */
function promptDeletePayload(params) {
  const payload = requireKey(
    RPC_METHODS.PROMPTS_DELETE,
    requireCwd(RPC_METHODS.PROMPTS_DELETE, params),
    'id',
  );
  return cleanObject({
    cwd: payload.cwd,
    id: payload.id,
    scope: normalizeString(payload.scope) || 'project',
  });
}

/** @param {unknown} params */
function promptIntentDraftPayload(params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(RPC_METHODS.PROMPT_INTENTS_DRAFT, params));
  const rawInput = promptIntentRawInput(payload);
  return cleanObject({
    cwd: payload.cwd,
    kind: normalizeString(payload.kind) || DEFAULT_PROMPT_INTENT_KIND,
    raw_input: rawInput,
    ...promptIntentSourceFields(payload),
    enable_global: promptIntentEnableGlobal(payload),
    ...promptProviderFields(payload),
  });
}

/** @param {WorkflowPayload} payload */
function promptIntentRawInput(payload) {
  const rawInput = normalizeString(payload.raw_input ?? payload.rawInput);
  if (!rawInput) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRAFT}: raw_input is required`);
  return rawInput;
}

/** @param {WorkflowPayload} payload */
function promptIntentEnableGlobal(payload) {
  const scope = normalizeString(payload.scope);
  return payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined);
}

/** @param {WorkflowPayload} payload */
function promptIntentSourceFields(payload) {
  return {
    source_type: normalizeString(payload.source_type ?? payload.sourceType) || DEFAULT_PROMPT_SOURCE_TYPE,
    source_url: normalizeString(payload.source_url ?? payload.sourceUrl),
    license_hint: normalizeString(payload.license_hint ?? payload.licenseHint),
  };
}

/** @param {WorkflowPayload} payload */
function promptProviderFields(payload) {
  return {
    provider: normalizeString(payload.provider ?? payload.modelProvider),
    model: normalizeString(payload.model),
    model_provider: normalizeString(payload.model_provider ?? payload.codexModelProvider),
  };
}

/** @param {string} method @param {unknown} params */
function memoryConsolidationPayload(method, params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  return cleanObject({
    cwd: payload.cwd,
    provider: normalizeString(payload.provider ?? payload.modelProvider),
    model: normalizeString(payload.model),
    model_provider: normalizeString(payload.model_provider ?? payload.codexModelProvider),
  });
}

/** @param {string} method @param {unknown} params @returns {WorkflowPayload & { cwd: string, draft_key: string }} */
function promptDraftKeyPayload(method, params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  const draftKey = normalizeString(payload.draft_key ?? payload.draftKey);
  if (!draftKey) throw new Error(`${method}: draft_key is required`);
  return { ...payload, draft_key: draftKey };
}

/** @param {unknown} params */
function promptIntentCommitPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_COMMIT, params);
  const scope = normalizeString(payload.scope);
  const enableGlobal = payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined);
  return cleanObject({
    cwd: payload.cwd,
    draft_key: payload.draft_key,
    confirm_risk: payload.confirm_risk ?? payload.confirmRisk,
    enable_global: enableGlobal,
    confirm_global: payload.confirm_global ?? payload.confirmGlobal,
  });
}

/** @param {unknown} params */
function promptIntentDiscardPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_DISCARD, params);
  return { cwd: payload.cwd, draft_key: payload.draft_key };
}

/** @param {unknown} params */
function promptIntentDryRunPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, params);
  const question = normalizeString(payload.question);
  if (!question) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRY_RUN}: question is required`);
  return cleanObject({
    cwd: payload.cwd,
    draft_key: payload.draft_key,
    kind: normalizeString(payload.kind),
    card: payload.card,
    question,
  });
}

/** @param {string} method @param {unknown} params */
function personalizationProfilePayload(method, params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  if (method === RPC_METHODS.PERSONALIZATION_PROFILE_GET) return { cwd: payload.cwd };
  if (!payload.profile || typeof payload.profile !== 'object' || Array.isArray(payload.profile)) {
    throw new Error(`${method}: profile must be an object`);
  }
  return { cwd: payload.cwd, profile: payload.profile };
}

/** @param {string} method @param {unknown} params */
function promptSectionPayload(method, params) {
  return requireKey(method, requireCwd(method, params), 'prompt_id');
}

/** @param {unknown} params */
function lspPromptHintWritePayload(params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE, params));
  if (!hasOwn(payload, 'hint')) throw new Error(`${RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE}: hint is required`);
  return { cwd: payload.cwd, hint: normalizeOptionalString(payload.hint) };
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
  codeProjectsPayload, optionalCodeInteger, codeFilePayload, promptWritePayload, promptMatchWhen, promptDeletePayload,
  promptIntentDraftPayload, promptIntentRawInput, promptIntentEnableGlobal, promptIntentSourceFields, promptProviderFields, memoryConsolidationPayload,
  promptDraftKeyPayload, promptIntentCommitPayload, promptIntentDiscardPayload, promptIntentDryRunPayload, personalizationProfilePayload, promptSectionPayload,
  lspPromptHintWritePayload, videoApiKeyPayload, builtinToolWritePayload, dashboardLogsPayload, hasOwn,
};
