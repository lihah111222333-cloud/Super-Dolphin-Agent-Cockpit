// @ts-check

import { requireApprovalIdentity } from '../approvalRequestId.js';
import { callAPI as callWailsAPI } from '../wailsBridge.js';
import { RPC_METHODS } from './backendRpcMethods.js';
import {
  assertPlainObject,
  assertNoExtraPayloadFields,
  takePayloadField,
  takePayloadFields,
  normalizeString,
  normalizeProviderConfigValue,
  normalizeToolSurfaceMode,
  requireCwd,
  requireThreadId,
  requireKey,
  cleanObject,
  normalizeOptionalLimit,
  normalizeOptionalCursorInteger,
} from './backendApiCommon.js';
import {
  legacyThreadNamePayload,
  hasAttachmentInputContent,
  normalizeTurnInput,
  hasOwn,
} from './backendApiPayloads.js';
import {
  resolveNativeDeps,
  createBackendCaller,
  createConfigProjectApi,
  createAppUpdateApi,
  createObservabilityMemoryApi,
  createDatasourceApi,
  createPromptDagApi,
} from './backendApiFactoryCore.js';
import {
  createCronApi,
  createCodeApi,
  createSkillApi,
  createMCPServerApi,
} from './backendApiFactoryOps.js';

/**
 * @param {(method: string, payload: Record<string, unknown>) => Promise<unknown>} callBackend
 */
function createThreadApi(callBackend) {
  return {
    listThreadsPage: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_LIST_PAGE, threadListPagePayload(RPC_METHODS.THREAD_LIST_PAGE, params)),
    listLoadedThreadsPage: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_LOADED_LIST_PAGE, threadListPagePayload(RPC_METHODS.THREAD_LOADED_LIST_PAGE, params)),
    getThreadMessages: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_MESSAGES, threadMessagesPayload(params)),
    getPromptHistory: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_PROMPT_HISTORY, promptHistoryPayload(params)),
    resolveThreadIdentity: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_RESOLVE, threadIdOnlyPayload(RPC_METHODS.THREAD_RESOLVE, params)),
    archiveThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_ARCHIVE, threadIdOnlyPayload(RPC_METHODS.THREAD_ARCHIVE, params)),
    unarchiveThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_UNARCHIVE, threadIdOnlyPayload(RPC_METHODS.THREAD_UNARCHIVE, params)),
    deleteThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_DELETE, threadIdOnlyPayload(RPC_METHODS.THREAD_DELETE, params)),
    getThreadConfig: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_CONFIG_GET, threadIdOnlyPayload(RPC_METHODS.THREAD_CONFIG_GET, params)),
    setThreadConfig: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_CONFIG_SET, threadConfigPayload(params)),
    forkThread: (/** @type {unknown} */ params) => requestForkThread(callBackend, strictThreadIdOnlyPayload(RPC_METHODS.THREAD_FORK, params)),
    startThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
    startTurn: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.TURN_START, turnStartPayload(params)),
    interruptTurn: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.TURN_INTERRUPT, turnInterruptPayload(params)),
    forceCompleteTurn: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.TURN_FORCE_COMPLETE, forceCompleteTurnPayload(params)),
    respondApproval: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.APPROVAL_RESPOND, approvalRespondPayload(params)),
    compactThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_COMPACT_START, compactThreadPayload(params)),
    recoverThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_RECOVER, threadIdOnlyPayload(RPC_METHODS.THREAD_RECOVER, requireCwd(RPC_METHODS.THREAD_RECOVER, params))),
    renameThread: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.THREAD_NAME_SET, legacyThreadNamePayload(RPC_METHODS.THREAD_NAME_SET, params)),
  };
}

/**
 * @param {(method: string, payload: Record<string, unknown>) => Promise<unknown>} callBackend
 * @param {{ threadId: string }} payload
 */
async function requestForkThread(callBackend, payload) {
  const response = await callBackend(RPC_METHODS.THREAD_FORK, payload);
  return normalizeForkResponseSource(RPC_METHODS.THREAD_FORK, response, payload.threadId);
}

/** @param {string} method @param {unknown} params */
function threadListPagePayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit is required`);
  return cleanObject({
    limit,
    cursor_created_at: normalizeOptionalCursorInteger(method, payload, 'cursorCreatedAt', 'cursor_created_at'),
    cursor_thread_id: normalizeString(payload.cursorThreadId || payload.cursor_thread_id),
  });
}

/** @param {string} method @param {unknown} params */
function threadIdOnlyPayload(method, params) {
  const { unused, threadId } = threadScopedPayload(method, params);
  assertNoExtraPayloadFields(method, unused);
  return { threadId };
}

/** @param {string} method @param {unknown} params */
function strictThreadIdOnlyPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = resolveThreadIdAliases(method, payload);
  const unused = { ...payload };
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  assertNoExtraPayloadFields(method, unused);
  return { threadId };
}

/**
 * @param {string} method
 * @param {unknown} response
 * @param {string} sourceThreadId
 * @returns {Record<string, unknown> & { thread: Record<string, unknown> & { forkedFrom: string }, kickoffState: string }}
 */
function normalizeForkResponseSource(method, response, sourceThreadId) {
  const payload = assertPlainObject(`${method} response`, response);
  const thread = assertPlainObject(`${method} response.thread`, payload.thread);
  const forkedFrom = normalizeString(thread.forkedFrom || thread.forked_from);
  if (forkedFrom !== sourceThreadId) {
    throw new Error(`${method} response thread.forkedFrom must equal ${sourceThreadId}`);
  }
  if (normalizeString(thread.id) === sourceThreadId) {
    throw new Error(`${method} response thread.id must differ from ${sourceThreadId}`);
  }
  return {
    ...payload,
    thread: { ...thread, forkedFrom },
    kickoffState: normalizeString(payload.kickoffState || payload.kickoff_state),
  };
}

/** @param {unknown} params */
function threadMessagesPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_MESSAGES, params);
  const limit = takePayloadField(unused, 'limit');
  const before = takePayloadField(unused, 'before');
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_MESSAGES, unused);
  return cleanObject({ threadId, limit, before });
}

/** @param {unknown} params */
function promptHistoryPayload(params) {
  const method = RPC_METHODS.THREAD_PROMPT_HISTORY;
  const payload = { ...requireCwd(method, params) };
  const cwd = takePayloadField(payload, 'cwd');
  const activeThreadId = takeNormalizedPromptHistoryString(method, payload, 'activeThreadId');
  const cursor = takeOpaquePromptHistoryString(method, payload, 'cursor');
  const nonce = takeOpaquePromptHistoryString(method, payload, 'nonce');
  const limit = takePayloadField(payload, 'limit');
  assertNoExtraPayloadFields(method, payload);
  if (typeof limit !== 'number' || !Number.isInteger(limit) || limit < 1 || limit > 50) {
    throw new Error(`${method}: limit must be an integer between 1 and 50`);
  }
  return { cwd, activeThreadId, cursor, nonce, limit };
}

/** @param {string} method @param {Record<string, unknown>} payload @param {string} key */
function takeNormalizedPromptHistoryString(method, payload, key) {
  const value = takePayloadField(payload, key);
  if (value === undefined || value === null) return '';
  if (typeof value !== 'string') throw new TypeError(`${method}: ${key} must be a string`);
  return value.trim();
}

/** @param {string} method @param {Record<string, unknown>} payload @param {string} key */
function takeOpaquePromptHistoryString(method, payload, key) {
  const value = takePayloadField(payload, key);
  if (value === undefined || value === null) return '';
  if (typeof value !== 'string') throw new TypeError(`${method}: ${key} must be a string`);
  if (new TextEncoder().encode(value).byteLength > 2048) {
    throw new Error(`${method}: ${key} exceeds 2048 bytes`);
  }
  return value;
}

/** @param {unknown} params */
function threadConfigPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_CONFIG_SET, params);
  const model = normalizeProviderConfigValue(takePayloadField(unused, 'model'));
  const effort = normalizeProviderConfigValue(takePayloadField(unused, 'effort'));
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_CONFIG_SET, unused);
  return {
    threadId,
    model,
    effort,
  };
}

/** @param {string} method @param {unknown} params */
function threadScopedPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = resolveThreadIdAliases(method, payload);
  const unused = { ...payload };
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  takePayloadField(unused, 'cwd');
  return { unused, threadId };
}

/** @param {string} method @param {Record<string, unknown>} payload */
function resolveThreadIdAliases(method, payload) {
  const camel = hasOwn(payload, 'threadId') ? normalizeString(payload.threadId) : '';
  const snake = hasOwn(payload, 'thread_id') ? normalizeString(payload.thread_id) : '';
  if (camel && snake && camel !== snake) {
    throw new Error(`${method}: conflicting threadId values for threadId and thread_id`);
  }
  const threadId = camel || snake;
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return threadId;
}

/** @param {string} method @param {string} label @param {Array<{ key: string, value: unknown }>} values */
function requiredStringAliasValue(method, label, values) {
  const normalized = values.map(({ key, value }) => ({ key, value: normalizeString(value) }));
  const present = normalized.filter(({ value }) => value);
  if (new Set(present.map(({ value }) => value)).size > 1) {
    throw new Error(`${method}: conflicting ${label} values for ${present.map(({ key }) => key).join(' and ')}`);
  }
  if (present.length === 0) throw new Error(`${method}: ${label} is required`);
  return present[0].value;
}

/** @param {unknown} params */
function threadStartPayload(params) {
  const payload = requireCwd(RPC_METHODS.THREAD_START, params);
  const unused = { ...payload };
  const providerRaw = takePayloadField(unused, 'provider');
  const modelProvider = takePayloadField(unused, 'modelProvider');
  const modelProviderSnake = takePayloadField(unused, 'model_provider');
  takePayloadField(unused, 'codexModelProvider');
  takePayloadField(unused, 'codex_model_provider');
  const promptKey = normalizeString(takePayloadField(unused, 'promptKey') || takePayloadField(unused, 'prompt_key'));
  const agentKey = normalizeString(takePayloadField(unused, 'agentKey') || takePayloadField(unused, 'agent_key'));
  const deferSpawn = takePayloadField(unused, 'deferSpawn') ?? takePayloadField(unused, 'defer_spawn');
  const toolSurfaceModeRaw = takePayloadField(unused, 'toolSurfaceMode') || takePayloadField(unused, 'tool_surface_mode');
  takePayloadField(unused, 'optimisticUserMessage');
  takePayloadField(unused, 'optimistic_user_message');
  takePayloadField(unused, 'skipInitialRuntimeSync');
  takePayloadField(unused, 'skip_initial_runtime_sync');
  const request = cleanObject({
    cwd: takePayloadField(unused, 'cwd'),
    agentId: takePayloadField(unused, 'agentId'),
    agent_id: takePayloadField(unused, 'agent_id'),
    agent_type: takePayloadField(unused, 'agent_type'),
    agentMemoryScope: takePayloadField(unused, 'agentMemoryScope'),
    agentType: takePayloadField(unused, 'agentType'),
    agent_memory_scope: takePayloadField(unused, 'agent_memory_scope'),
    approvalPolicy: takePayloadField(unused, 'approvalPolicy'),
    approval_policy: takePayloadField(unused, 'approval_policy'),
    baseInstructions: takePayloadField(unused, 'baseInstructions'),
    base_instructions: takePayloadField(unused, 'base_instructions'),
    config: takePayloadField(unused, 'config'),
    developerInstructions: takePayloadField(unused, 'developerInstructions'),
    developer_instructions: takePayloadField(unused, 'developer_instructions'),
    effort: takePayloadField(unused, 'effort'),
    instructions: takePayloadField(unused, 'instructions'),
    language: takePayloadField(unused, 'language'),
    launchIntentId: takePayloadField(unused, 'launchIntentId'),
    launch_intent_id: takePayloadField(unused, 'launch_intent_id'),
    manualSkillSelection: takePayloadField(unused, 'manualSkillSelection'),
    manual_skill_selection: takePayloadField(unused, 'manual_skill_selection'),
    memoryScope: takePayloadField(unused, 'memoryScope'),
    memory_scope: takePayloadField(unused, 'memory_scope'),
    model: takePayloadField(unused, 'model'),
    name: takePayloadField(unused, 'name'),
    parentAgentId: takePayloadField(unused, 'parentAgentId'),
    parentID: takePayloadField(unused, 'parentID'),
    parentId: takePayloadField(unused, 'parentId'),
    parent_agent_id: takePayloadField(unused, 'parent_agent_id'),
    personality: takePayloadField(unused, 'personality'),
    prompt: takePayloadField(unused, 'prompt'),
    sandbox: takePayloadField(unused, 'sandbox'),
    selectedSkillRefs: takePayloadField(unused, 'selectedSkillRefs'),
    selectedSkills: takePayloadField(unused, 'selectedSkills'),
    selected_skill_refs: takePayloadField(unused, 'selected_skill_refs'),
    selected_skills: takePayloadField(unused, 'selected_skills'),
    summary: takePayloadField(unused, 'summary'),
  });
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_START, unused);
  const provider = normalizeString(modelProvider || modelProviderSnake || providerRaw);
  if (!provider) throw new Error(`${RPC_METHODS.THREAD_START}: provider is required`);
  request.provider = provider;
  const toolSurfaceMode = normalizeToolSurfaceMode(toolSurfaceModeRaw);
  if (promptKey) request.prompt_key = promptKey;
  if (agentKey) request.agent_key = agentKey;
  if (toolSurfaceMode) request.toolSurfaceMode = toolSurfaceMode;
  if (deferSpawn === true) request.defer_spawn = true;
  return request;
}

/** @param {unknown} params */
function turnStartPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_START, requireCwd(RPC_METHODS.TURN_START, params));
  const unused = { ...payload };
  const input = takePayloadField(unused, 'input');
  const attachments = takePayloadField(unused, 'attachments');
  const request = takePayloadFields(unused, [
    'additionalWorkingDirectories',
    'additional_working_directories',
    'approvalPolicy',
    'approval_policy',
    'cwd',
    'effort',
    'enabledTools',
    'enabled_tools',
    'files',
    'gitRoot',
    'git_root',
    'images',
    'isWorktree',
    'is_worktree',
    'language',
    'manualSkillSelection',
    'manual_skill_selection',
    'mcpSnapshot',
    'mcp_snapshot',
    'model',
    'outputSchema',
    'output_schema',
    'prompt',
    'provider',
    'selectedSkillRefs',
    'selectedSkills',
    'selected_skill_refs',
    'selected_skills',
    'sessionFlags',
    'session_flags',
    'threadID',
    'threadId',
    'thread_id',
  ]);
  assertNoExtraPayloadFields(RPC_METHODS.TURN_START, unused);
  if (normalizeString(request.prompt) && hasAttachmentInputContent(attachments)) {
    throw new Error(`${RPC_METHODS.TURN_START}: prompt and attachments cannot both contain content`);
  }
  return { ...request, ...normalizeTurnInput(input, Array.isArray(attachments) ? attachments : undefined) };
}

/** @param {unknown} params */
function turnInterruptPayload(params) {
  const payload = requireCwd(RPC_METHODS.TURN_INTERRUPT, params);
  const unused = { ...payload };
  takePayloadField(unused, 'cwd');
  const threadId = requiredStringAliasValue(RPC_METHODS.TURN_INTERRUPT, 'threadId', [
    { key: 'threadId', value: takePayloadField(unused, 'threadId') },
    { key: 'thread_id', value: takePayloadField(unused, 'thread_id') },
    { key: 'threadID', value: takePayloadField(unused, 'threadID') },
  ]);
  const source = normalizeString(takePayloadField(unused, 'source'));
  const expectedTurnId = requiredStringAliasValue(RPC_METHODS.TURN_INTERRUPT, 'expectedTurnId', [
    { key: 'expectedTurnId', value: takePayloadField(unused, 'expectedTurnId') },
    { key: 'expected_turn_id', value: takePayloadField(unused, 'expected_turn_id') },
  ]);
  const requestId = requiredStringAliasValue(RPC_METHODS.TURN_INTERRUPT, 'requestId', [
    { key: 'requestId', value: takePayloadField(unused, 'requestId') },
    { key: 'request_id', value: takePayloadField(unused, 'request_id') },
  ]);
  assertNoExtraPayloadFields(RPC_METHODS.TURN_INTERRUPT, unused);
  return { thread_id: threadId, expected_turn_id: expectedTurnId, request_id: requestId, ...(source ? { source } : {}) };
}

/** @param {unknown} params */
function forceCompleteTurnPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_FORCE_COMPLETE, requireCwd(RPC_METHODS.TURN_FORCE_COMPLETE, params));
  const unused = /** @type {Record<string, unknown>} */ ({ ...payload });
  takePayloadField(unused, 'cwd');
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  assertNoExtraPayloadFields(RPC_METHODS.TURN_FORCE_COMPLETE, unused);
  return { threadId: payload.threadId };
}

/** @param {unknown} params */
function approvalRespondPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.APPROVAL_RESPOND, params);
  const unused = { ...payload };
  const approved = takePayloadField(unused, 'approved');
  takePayloadField(unused, 'sessionScope');
  takePayloadField(unused, 'session_scope');
  takePayloadField(unused, 'callId');
  takePayloadField(unused, 'call_id');
  takePayloadField(unused, 'requestId');
  takePayloadField(unused, 'request_id');
  assertNoExtraPayloadFields(RPC_METHODS.APPROVAL_RESPOND, unused);
  const identity = requireApprovalIdentity(payload, RPC_METHODS.APPROVAL_RESPOND);
  if (!hasOwn(payload, 'approved')) throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved is required`);
  if (typeof approved !== 'boolean') throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved must be boolean`);
  return { ...identity, approved };
}

/** @param {unknown} params */
function compactThreadPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_COMPACT_START, requireCwd(RPC_METHODS.THREAD_COMPACT_START, params));
  const args = takePayloadField(unused, 'args');
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_COMPACT_START, unused);
  return cleanObject({ threadId, args });
}

/** @param {ReturnType<typeof resolveNativeDeps>} native */
function createNativeApi(native) {
  return {
    getBuildInfo: native.getBuildInfo,
    onAgentEvent: native.onAgentEvent,
    onBridgeEvent: native.onBridgeEvent,
    onFilesDropped: native.onFilesDropped,
    onRuntimeReconnect: native.onRuntimeReconnect,
    readDroppedTextFiles: native.readDroppedTextFiles,
    saveClipboardImage: native.saveClipboardImage,
    saveTextFile: native.saveTextFile,
    openSharedFile: /** @param {unknown} params */ (params) => {
      const payload = requireKey('openSharedFile', assertPlainObject('openSharedFile', params), 'path');
      return payload.preview === true
        ? native.previewSharedFile({ path: payload.path })
        : native.openSharedFile({ path: payload.path });
    },
    previewSharedFile: /** @param {unknown} params */ (params) => native.previewSharedFile(requireKey('previewSharedFile', assertPlainObject('previewSharedFile', params), 'path')),
    beginTextClipboardWrite: native.beginTextClipboardWrite,
    copyTextToClipboard: native.copyTextToClipboard,
    selectDatasourceImportFile: native.selectDatasourceImportFile,
    selectFiles: native.selectFiles,
    selectProjectDir: native.selectProjectDir,
    selectProjectDirs: native.selectProjectDirs,
  };
}

/** @param {Record<string, unknown> & { callAPI?: (method: string, payload: Record<string, unknown>) => Promise<unknown> }} deps */
export function createBackendApi(deps = {}) {
  const callBackend = createBackendCaller(deps.callAPI || callWailsAPI);
  return {
    callBackend,
    ...createConfigProjectApi(callBackend),
    ...createAppUpdateApi(callBackend),
    ...createObservabilityMemoryApi(callBackend),
    ...createPromptDagApi(callBackend),
    ...createCronApi(callBackend),
    ...createCodeApi(callBackend),
    ...createSkillApi(callBackend),
    ...createDatasourceApi(callBackend),
    ...createMCPServerApi(callBackend),
    ...createThreadApi(callBackend),
    ...createNativeApi(resolveNativeDeps(deps)),
  };
}
export {
  createThreadApi, threadListPagePayload, threadIdOnlyPayload, threadMessagesPayload, promptHistoryPayload, threadConfigPayload, threadScopedPayload,
  resolveThreadIdAliases, threadStartPayload, turnStartPayload, turnInterruptPayload, forceCompleteTurnPayload, approvalRespondPayload,
  compactThreadPayload, createNativeApi,
};
