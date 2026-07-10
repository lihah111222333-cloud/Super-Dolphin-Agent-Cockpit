// @ts-nocheck

import { positiveApprovalRequestIdFromFields } from '../approvalRequestId.js';
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

function createThreadApi(callBackend) {
  return {
    listThreadsPage: (params) => callBackend(RPC_METHODS.THREAD_LIST_PAGE, threadListPagePayload(RPC_METHODS.THREAD_LIST_PAGE, params)),
    listLoadedThreadsPage: (params) => callBackend(RPC_METHODS.THREAD_LOADED_LIST_PAGE, threadListPagePayload(RPC_METHODS.THREAD_LOADED_LIST_PAGE, params)),
    getThreadMessages: (params) => callBackend(RPC_METHODS.THREAD_MESSAGES, threadMessagesPayload(params)),
    resolveThreadIdentity: (params) => callBackend(RPC_METHODS.THREAD_RESOLVE, threadIdOnlyPayload(RPC_METHODS.THREAD_RESOLVE, params)),
    archiveThread: (params) => callBackend(RPC_METHODS.THREAD_ARCHIVE, threadIdOnlyPayload(RPC_METHODS.THREAD_ARCHIVE, params)),
    unarchiveThread: (params) => callBackend(RPC_METHODS.THREAD_UNARCHIVE, threadIdOnlyPayload(RPC_METHODS.THREAD_UNARCHIVE, params)),
    deleteThread: (params) => callBackend(RPC_METHODS.THREAD_DELETE, threadIdOnlyPayload(RPC_METHODS.THREAD_DELETE, params)),
    getThreadConfig: (params) => callBackend(RPC_METHODS.THREAD_CONFIG_GET, threadIdOnlyPayload(RPC_METHODS.THREAD_CONFIG_GET, params)),
    setThreadConfig: (params) => callBackend(RPC_METHODS.THREAD_CONFIG_SET, threadConfigPayload(params)),
    forkThread: (params) => {
      const payload = strictThreadIdOnlyPayload(RPC_METHODS.THREAD_FORK, params);
      return callBackend(RPC_METHODS.THREAD_FORK, payload).then((response) => (
        normalizeForkResponseSource(RPC_METHODS.THREAD_FORK, response, payload.threadId)
      ));
    },
    startThread: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
    startTurn: (params) => callBackend(RPC_METHODS.TURN_START, turnStartPayload(params)),
    interruptTurn: (params) => callBackend(RPC_METHODS.TURN_INTERRUPT, turnInterruptPayload(params)),
    forceCompleteTurn: (params) => callBackend(RPC_METHODS.TURN_FORCE_COMPLETE, forceCompleteTurnPayload(params)),
    respondApproval: (params) => callBackend(RPC_METHODS.APPROVAL_RESPOND, approvalRespondPayload(params)),
    compactThread: (params) => callBackend(RPC_METHODS.THREAD_COMPACT_START, compactThreadPayload(params)),
    recoverThread: (params) => callBackend(RPC_METHODS.THREAD_RECOVER, threadIdOnlyPayload(RPC_METHODS.THREAD_RECOVER, requireCwd(RPC_METHODS.THREAD_RECOVER, params))),
    renameThread: (params) => callBackend(RPC_METHODS.THREAD_NAME_SET, legacyThreadNamePayload(RPC_METHODS.THREAD_NAME_SET, params)),
  };
}

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

function threadIdOnlyPayload(method, params) {
  const { unused, threadId } = threadScopedPayload(method, params);
  assertNoExtraPayloadFields(method, unused);
  return { threadId };
}

function strictThreadIdOnlyPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = resolveThreadIdAliases(method, payload);
  const unused = { ...payload };
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  assertNoExtraPayloadFields(method, unused);
  return { threadId };
}

function normalizeForkResponseSource(method, response, sourceThreadId) {
  const forkedFrom = normalizeString(response.thread.forkedFrom || response.thread.forked_from);
  if (forkedFrom !== sourceThreadId) {
    throw new Error(`${method} response thread.forkedFrom must equal ${sourceThreadId}`);
  }
  if (normalizeString(response.thread.id) === sourceThreadId) {
    throw new Error(`${method} response thread.id must differ from ${sourceThreadId}`);
  }
  return {
    ...response,
    thread: { ...response.thread, forkedFrom },
    kickoffState: normalizeString(response.kickoffState || response.kickoff_state),
  };
}

function threadMessagesPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_MESSAGES, params);
  const limit = takePayloadField(unused, 'limit');
  const before = takePayloadField(unused, 'before');
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_MESSAGES, unused);
  return cleanObject({ threadId, limit, before });
}

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

function threadScopedPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = resolveThreadIdAliases(method, payload);
  const unused = { ...payload };
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  takePayloadField(unused, 'cwd');
  return { unused, threadId };
}

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
  return { ...request, ...normalizeTurnInput(input, attachments) };
}

function turnInterruptPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.TURN_INTERRUPT, requireCwd(RPC_METHODS.TURN_INTERRUPT, params));
  const source = normalizeString(takePayloadField(unused, 'source'));
  takePayloadField(unused, 'turnId');
  takePayloadField(unused, 'turn_id');
  assertNoExtraPayloadFields(RPC_METHODS.TURN_INTERRUPT, unused);
  return cleanObject({ thread_id: threadId, source });
}

function forceCompleteTurnPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_FORCE_COMPLETE, requireCwd(RPC_METHODS.TURN_FORCE_COMPLETE, params));
  const unused = { ...payload };
  delete unused.cwd;
  delete unused.threadId;
  delete unused.thread_id;
  assertNoExtraPayloadFields(RPC_METHODS.TURN_FORCE_COMPLETE, unused);
  return { threadId: payload.threadId };
}

function approvalRespondPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.APPROVAL_RESPOND, params);
  const {
    approved,
    requestId,
    request_id: requestIdAlias,
    ...unused
  } = payload;
  assertNoExtraPayloadFields(RPC_METHODS.APPROVAL_RESPOND, unused);
  const normalizedRequestId = positiveApprovalRequestIdFromFields(payload);
  if (normalizedRequestId <= 0) {
    const hasRequestId = hasOwn(payload, 'requestId') || hasOwn(payload, 'request_id');
    const rawRequestId = hasOwn(payload, 'requestId') ? requestId : requestIdAlias;
    if (!hasRequestId || rawRequestId === undefined || rawRequestId === null || rawRequestId === '' || rawRequestId === 0) {
      throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: requestId is required`);
    }
    throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: requestId must be a positive integer`);
  }
  if (!hasOwn(payload, 'approved')) throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved is required`);
  if (typeof approved !== 'boolean') throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved must be boolean`);
  return { requestId: normalizedRequestId, approved };
}

function compactThreadPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_COMPACT_START, requireCwd(RPC_METHODS.THREAD_COMPACT_START, params));
  const args = takePayloadField(unused, 'args');
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_COMPACT_START, unused);
  return cleanObject({ threadId, args });
}

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
    openSharedFile: (params) => {
      const payload = requireKey('openSharedFile', assertPlainObject('openSharedFile', params), 'path');
      return payload.preview === true
        ? native.previewSharedFile({ path: payload.path })
        : native.openSharedFile({ path: payload.path });
    },
    previewSharedFile: (params) => native.previewSharedFile(requireKey('previewSharedFile', assertPlainObject('previewSharedFile', params), 'path')),
    beginTextClipboardWrite: native.beginTextClipboardWrite,
    copyTextToClipboard: native.copyTextToClipboard,
    selectDatasourceImportFile: native.selectDatasourceImportFile,
    selectFiles: native.selectFiles,
    selectProjectDir: native.selectProjectDir,
    selectProjectDirs: native.selectProjectDirs,
  };
}

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
  createThreadApi, threadListPagePayload, threadIdOnlyPayload, threadMessagesPayload, threadConfigPayload, threadScopedPayload,
  resolveThreadIdAliases, threadStartPayload, turnStartPayload, turnInterruptPayload, forceCompleteTurnPayload, approvalRespondPayload,
  compactThreadPayload, createNativeApi,
};
