import {
  getBuildInfo as getWailsBuildInfo,
  onBridgeEvent as subscribeBridgeEvent,
  onAgentEvent as subscribeAgentEvent,
  onFilesDropped as subscribeFilesDropped,
  onRuntimeReconnect as subscribeRuntimeReconnect,
  readDroppedTextFiles as readDroppedTextFilesViaBridge,
  saveClipboardImage as saveClipboardImageViaBridge,
  saveTextFile as saveTextFileViaBridge,
  openSharedFile as openSharedFileViaBridge,
  previewSharedFile as previewSharedFileViaBridge,
  beginTextClipboardWrite as beginTextClipboardWriteViaBridge,
  copyTextToClipboard as copyTextToClipboardViaBridge,
  selectDatasourceImportFile as selectDatasourceImportFileViaBridge,
  selectFiles as selectFilesViaBridge,
  selectProjectDir as selectProjectDirViaBridge,
  selectProjectDirs as selectProjectDirsViaBridge,
} from '../wailsBridge.js';
import { RPC_METHODS } from './backendRpcMethods.js';
import { createBackendResponseValidators } from '../backendResponseValidators.js';
import { INVALID_RECOVERY_DATA_MESSAGE, normalizeRecoveryFailure } from '../../recovery/recoveryFailure.js';
import {
  assertPlainObject,
  normalizeString,
  requireCwd,
  requireThreadId,
  requireKey,
  cleanObject,
  requireBoolean,
  normalizeOptionalLimit,
} from './backendApiCommon.js';
import {
  observabilityTracePayload,
  observabilityThreadPayload,
  observabilityListPayload,
  observabilityRecentPayload,
  memoryEntryGetPayload,
  memoryEntryUpsertPayload,
  memoryPairPayload,
  dashboardDagStartPayload,
  dashboardDagCreateAndStartPayload,
  dashboardWorkflowMaterialWritePayload,
  dashboardDagDispatchNodePayload,
  dashboardDagsPayload,
  dashboardDagRunsPayload,
  dashboardDagTerminatePayload,
  dashboardDagApplyOpsPayload,
  promptWritePayload,
  promptDeletePayload,
  promptIntentDraftPayload,
  memoryConsolidationPayload,
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
} from './backendApiPayloads.js';
import {
  workflowTemplateListPayload,
  workflowTemplateRenderPayload,
  workflowTemplateSavePayload,
  workflowTemplateRollbackPayload,
} from './backendApiFactoryOps.js';

/** @typedef {(method: string, payload: Record<string, unknown>) => Promise<unknown>} BackendCaller */
/** @typedef {(...args: unknown[]) => unknown} NativeDependency */

const BACKEND_RESPONSE_VALIDATORS = createBackendResponseValidators(RPC_METHODS);

/** @type {ReadonlyArray<readonly [string, NativeDependency]>} */
const NATIVE_DEP_FALLBACKS = /** @type {ReadonlyArray<readonly [string, NativeDependency]>} */ (Object.freeze([
  ['getBuildInfo', getWailsBuildInfo],
  ['onAgentEvent', subscribeAgentEvent],
  ['onBridgeEvent', subscribeBridgeEvent],
  ['onFilesDropped', subscribeFilesDropped],
  ['onRuntimeReconnect', subscribeRuntimeReconnect],
  ['readDroppedTextFiles', readDroppedTextFilesViaBridge],
  ['saveClipboardImage', saveClipboardImageViaBridge],
  ['saveTextFile', saveTextFileViaBridge],
  ['openSharedFile', openSharedFileViaBridge],
  ['previewSharedFile', previewSharedFileViaBridge],
  ['beginTextClipboardWrite', beginTextClipboardWriteViaBridge],
  ['copyTextToClipboard', copyTextToClipboardViaBridge],
  ['selectDatasourceImportFile', selectDatasourceImportFileViaBridge],
  ['selectFiles', selectFilesViaBridge],
  ['selectProjectDir', selectProjectDirViaBridge],
  ['selectProjectDirs', selectProjectDirsViaBridge],
]));

/** @param {Record<string, unknown>} deps @returns {Record<string, NativeDependency>} */
function resolveNativeDeps(deps) {
  return Object.fromEntries(NATIVE_DEP_FALLBACKS.map(([key, fallback]) => {
    const dependency = deps[key] || fallback;
    if (typeof dependency !== 'function') throw new TypeError(`native dependency ${key} must be a function`);
    return [key, /** @type {NativeDependency} */ (dependency)];
  }));
}

/** @param {BackendCaller} callAPI @returns {BackendCaller} */
function createBackendCaller(callAPI) {
  return async (method, params = {}) => {
    const rpcMethod = normalizeString(method);
    if (!rpcMethod) throw new Error('backend RPC method is required');
    const response = await callAPI(rpcMethod, assertPlainObject(rpcMethod, params));
    const validator = BACKEND_RESPONSE_VALIDATORS[rpcMethod];
    return validator ? validator(rpcMethod, response) : response;
  };
}

/** @param {BackendCaller} callBackend */
function createConfigProjectApi(callBackend) {
  return {
    readConfig: () => callBackend(RPC_METHODS.CONFIG_READ, {}),
    readLspPromptHint: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ, requireCwd(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ, params)),
    writeLspPromptHint: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE, lspPromptHintWritePayload(params)),
    readBuiltinTools: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, requireCwd(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, params)),
    writeBuiltinTool: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, builtinToolWritePayload(params)),
    getWindowBootstrap: () => callBackend(RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET, {}),
    getSidebarState: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_SIDEBAR_GET, requireCwd(RPC_METHODS.UI_SIDEBAR_GET, params)),
    openNewWindow: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_OPEN_NEW_WINDOW, requireCwd(RPC_METHODS.UI_OPEN_NEW_WINDOW, params)),
    getThreadState: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_STATE_GET,
      requireThreadId(RPC_METHODS.UI_STATE_GET, requireCwd(RPC_METHODS.UI_STATE_GET, params)),
    ),
    getProjects: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_PROJECTS_GET, requireCwd(RPC_METHODS.UI_PROJECTS_GET, params)),
    setActiveProject: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_PROJECTS_SET_ACTIVE,
      requireKey(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, requireCwd(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, params), 'path'),
    ),
    addProject: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_PROJECTS_ADD,
      requireKey(RPC_METHODS.UI_PROJECTS_ADD, requireCwd(RPC_METHODS.UI_PROJECTS_ADD, params), 'path'),
    ),
    removeProject: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_PROJECTS_REMOVE,
      requireKey(RPC_METHODS.UI_PROJECTS_REMOVE, requireCwd(RPC_METHODS.UI_PROJECTS_REMOVE, params), 'path'),
    ),
    getPreference: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_PREFERENCES_GET, assertPlainObject(RPC_METHODS.UI_PREFERENCES_GET, params)),
    getAllPreferences: (/** @type {unknown} */ params = {}) => callBackend(RPC_METHODS.UI_PREFERENCES_GET_ALL, assertPlainObject(RPC_METHODS.UI_PREFERENCES_GET_ALL, params)),
    setPreference: (/** @type {unknown} */ params) => {
      const payload = assertPlainObject(RPC_METHODS.UI_PREFERENCES_SET, params);
      if (!normalizeString(payload.key)) throw new Error(`${RPC_METHODS.UI_PREFERENCES_SET}: key is required`);
      if (!hasOwn(payload, 'value')) throw new Error(`${RPC_METHODS.UI_PREFERENCES_SET}: value is required`);
      return callBackend(RPC_METHODS.UI_PREFERENCES_SET, payload);
    },
    listModelProviders: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.MODEL_PROVIDERS_LIST,
      requireCwd(RPC_METHODS.MODEL_PROVIDERS_LIST, params),
    ),
    saveModelProviders: (/** @type {unknown} */ params) => {
      const payload = /** @type {Record<string, unknown>} */ (requireCwd(RPC_METHODS.MODEL_PROVIDERS_SAVE, params));
      if (!payload.registry || typeof payload.registry !== 'object' || Array.isArray(payload.registry)) {
        throw new Error(`${RPC_METHODS.MODEL_PROVIDERS_SAVE}: registry is required`);
      }
      return callBackend(RPC_METHODS.MODEL_PROVIDERS_SAVE, payload);
    },
    applyModelProvider: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.MODEL_PROVIDERS_APPLY,
      requireKey(RPC_METHODS.MODEL_PROVIDERS_APPLY, requireCwd(RPC_METHODS.MODEL_PROVIDERS_APPLY, params), 'vendorId'),
    ),
    getDashboardPage: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_DASHBOARD_GET,
      requireKey(RPC_METHODS.UI_DASHBOARD_GET, requireCwd(RPC_METHODS.UI_DASHBOARD_GET, params), 'page'),
    ),
    getVideoApiKey: () => callBackend(RPC_METHODS.UI_VIDEO_GET_API_KEY, {}),
    setVideoApiKey: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_VIDEO_SET_API_KEY,
      videoApiKeyPayload(params),
    ),
    listDashboardLogs: (/** @type {unknown} */ params = {}) => callBackend(RPC_METHODS.DASHBOARD_LOGS, dashboardLogsPayload(params)),
  };
}

/** @param {BackendCaller} callBackend */
function createAppUpdateApi(callBackend) {
  return {
    checkAppUpdate: () => callAppUpdateRPC(callBackend, RPC_METHODS.APP_UPDATE_CHECK),
    downloadAppUpdate: () => callAppUpdateRPC(callBackend, RPC_METHODS.APP_UPDATE_DOWNLOAD),
    installAppUpdate: () => callAppUpdateRPC(callBackend, RPC_METHODS.APP_UPDATE_INSTALL),
    installLatestAppUpdate: () => callAppUpdateRPC(callBackend, RPC_METHODS.APP_UPDATE_INSTALL_LATEST),
  };
}

/** @param {BackendCaller} callBackend @param {string} method @returns {Promise<unknown>} */
async function callAppUpdateRPC(callBackend, method) {
  try {
    return await callBackend(method, {});
  }
  catch (error) {
    if (!error || typeof error !== 'object' || !hasOwn(error, 'data')) throw error;
    const rpcError = /** @type {Record<string, unknown>} */ (error);
    try {
      normalizeRecoveryFailure(rpcError.data);
    }
    catch {
      throw new Error(INVALID_RECOVERY_DATA_MESSAGE);
    }
    throw error;
  }
}

/** @param {BackendCaller} callBackend */
function createObservabilityMemoryApi(callBackend) {
  return {
    ...createObservabilityApi(callBackend),
    ...createMemoryApi(callBackend),
  };
}

/** @param {string} method @param {unknown} params */
function datasourceCreatePayload(method, params) {
  const payload = assertPlainObject(method, params);
  const sourcePath = normalizeString(payload.sourcePath || payload.source_path);
  if (!sourcePath) throw new Error(`${method}: sourcePath is required`);
  return { sourcePath };
}

/** @param {unknown} params */
function datasourceImportLocalFilePayload(params) {
  const method = RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE;
  const source = assertPlainObject(method, params);
  const payload = datasourceCreatePayload(method, source);
  const pickerTokenValue = source.pickerToken !== undefined ? source.pickerToken : source.picker_token;
  const pickerToken = normalizeString(pickerTokenValue);
  return cleanObject({ ...payload, pickerToken });
}

/** @param {unknown} params */
function datasourceListPayload(params = {}) {
  const method = RPC_METHODS.DATASOURCE_V2_LIST;
  const payload = assertPlainObject(method, params);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit must be a positive integer`);
  return cleanObject({ keyword: normalizeString(payload.keyword), limit });
}

/** @param {string} method @param {unknown} params */
function datasourceDocumentIDPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const documentID = Number(payload.documentId ?? payload.document_id ?? payload.id);
  if (!Number.isInteger(documentID) || documentID <= 0) {
    throw new Error(`${method}: documentId is required`);
  }
  return { documentId: documentID };
}

/** @param {unknown} params */
function datasourceChunksPayload(params) {
  const method = RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS;
  const payload = assertPlainObject(method, params);
  const { documentId } = datasourceDocumentIDPayload(method, payload);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit must be a positive integer`);
  if (!hasOwn(payload, 'cursor')) throw new Error(`${method}: cursor is required`);
  const cursor = Number(payload.cursor);
  if (!Number.isInteger(cursor) || cursor < -1) {
    throw new Error(`${method}: cursor must be -1 or greater`);
  }
  return { documentId, limit, cursor };
}

/** @param {unknown} params */
function datasourceUpdatePayload(params) {
  const method = RPC_METHODS.DATASOURCE_V2_UPDATE;
  const payload = assertPlainObject(method, params);
  const { documentId } = datasourceDocumentIDPayload(method, payload);
  const sourcePath = normalizeString(payload.sourcePath || payload.source_path);
  const fileName = normalizeString(payload.fileName || payload.file_name);
  if (!sourcePath) throw new Error(`${method}: sourcePath is required`);
  if (!fileName) throw new Error(`${method}: fileName is required`);
  if (!hasOwn(payload, 'sizeBytes') && !hasOwn(payload, 'size_bytes')) {
    throw new Error(`${method}: sizeBytes is required`);
  }
  const sizeBytes = Number(payload.sizeBytes ?? payload.size_bytes);
  if (!Number.isInteger(sizeBytes) || sizeBytes < 0) {
    throw new Error(`${method}: sizeBytes must be a non-negative integer`);
  }
  return cleanObject({
    documentId,
    sourcePath,
    fileName,
    extension: normalizeString(payload.extension),
    sizeBytes,
  });
}

/** @param {BackendCaller} callBackend */
function createDatasourceApi(callBackend) {
  return {
    createDatasourceDocument: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_CREATE,
      datasourceCreatePayload(RPC_METHODS.DATASOURCE_V2_CREATE, params),
    ),
    importDatasourceLocalFile: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE,
      datasourceImportLocalFilePayload(params),
    ),
    listDatasourceDocuments: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.DATASOURCE_V2_LIST,
      datasourceListPayload(params),
    ),
    getDatasourceDocument: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_GET,
      datasourceDocumentIDPayload(RPC_METHODS.DATASOURCE_V2_GET, params),
    ),
    listDatasourceChunks: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS,
      datasourceChunksPayload(params),
    ),
    updateDatasourceDocument: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_UPDATE,
      datasourceUpdatePayload(params),
    ),
    deleteDatasourceDocument: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_DELETE,
      datasourceDocumentIDPayload(RPC_METHODS.DATASOURCE_V2_DELETE, params),
    ),
  };
}

/** @param {BackendCaller} callBackend */
function createObservabilityApi(callBackend) {
  return {
    getObservabilityTrace: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.OBSERVABILITY_TRACE_GET, observabilityTracePayload(RPC_METHODS.OBSERVABILITY_TRACE_GET, params)),
    getObservabilityThreadRecent: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, observabilityThreadPayload(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, params)),
    listObservabilityRecent: (/** @type {unknown} */ params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_RECENT_LIST, observabilityRecentPayload(RPC_METHODS.OBSERVABILITY_RECENT_LIST, params)),
    listObservabilitySlow: (/** @type {unknown} */ params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_SLOW_LIST, observabilityListPayload(RPC_METHODS.OBSERVABILITY_SLOW_LIST, params)),
    listObservabilityErrors: (/** @type {unknown} */ params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_ERROR_LIST, observabilityListPayload(RPC_METHODS.OBSERVABILITY_ERROR_LIST, params)),
    getObservabilityStatus: () => callBackend(RPC_METHODS.OBSERVABILITY_STATUS, {}),
  };
}

/** @param {BackendCaller} callBackend */
function createMemoryApi(callBackend) {
  return {
    getMemorySnapshot: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_GET, requireCwd(RPC_METHODS.UI_MEMORY_GET, params)),
    getMemoryEntry: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_GET, memoryEntryGetPayload(RPC_METHODS.UI_MEMORY_ENTRY_GET, params)),
    upsertMemoryEntry: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, memoryEntryUpsertPayload(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, params)),
    deleteMemoryEntry: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, memoryEntryGetPayload(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, params)),
    setMemoryAutoDreamIntent: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT,
      requireCwd(
        RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT,
        requireBoolean(RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT, params, 'enabled'),
      ),
    ),
    mergeMemoryEntries: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_MERGE, memoryPairPayload(RPC_METHODS.UI_MEMORY_ENTRY_MERGE, params)),
    ignoreMemorySimilarity: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE, memoryPairPayload(RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE, params)),
    consolidateMemorySimilarities: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL, requireCwd(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL, params)),
    startConsolidateMemorySimilarities: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START,
      memoryConsolidationPayload(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START, params),
    ),
    getMemoryConsolidationStatus: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS,
      requireKey(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, requireCwd(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, params), 'jobId'),
    ),
    listSharedFiles: (/** @type {unknown} */ params = {}) => {
      const payload = assertPlainObject(RPC_METHODS.DASHBOARD_SHARED_FILES, params);
      if (Object.keys(payload).length > 0) throw new Error(`${RPC_METHODS.DASHBOARD_SHARED_FILES}: params are not supported`);
      return callBackend(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
    },
    readSharedFile: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_SHARED_FILE_GET,
      requireKey(RPC_METHODS.UI_SHARED_FILE_GET, assertPlainObject(RPC_METHODS.UI_SHARED_FILE_GET, params), 'path'),
    ),
    deleteSharedFile: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.UI_SHARED_FILE_DELETE,
      requireKey(RPC_METHODS.UI_SHARED_FILE_DELETE, assertPlainObject(RPC_METHODS.UI_SHARED_FILE_DELETE, params), 'path'),
    ),
    writeWorkflowMaterial: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE, dashboardWorkflowMaterialWritePayload(params)),
  };
}

/** @param {BackendCaller} callBackend */
function createPromptDagApi(callBackend) {
  return {
    listPromptAssets: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_ASSETS_LIST, requireCwd(RPC_METHODS.PROMPT_ASSETS_LIST, params)),
    getDashboardPrompts: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_PROMPTS, requireCwd(RPC_METHODS.DASHBOARD_PROMPTS, params)),
    getPrompt: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.PROMPTS_GET,
      requireKey(RPC_METHODS.PROMPTS_GET, requireCwd(RPC_METHODS.PROMPTS_GET, params), 'id'),
    ),
    writePrompt: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPTS_WRITE, promptWritePayload(params)),
    deletePrompt: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPTS_DELETE, promptDeletePayload(params)),
    draftPromptIntent: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_INTENTS_DRAFT, promptIntentDraftPayload(params)),
    commitPromptIntent: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_INTENTS_COMMIT, promptIntentCommitPayload(params)),
    discardPromptIntent: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_INTENTS_DISCARD, promptIntentDiscardPayload(params)),
    dryRunPromptIntent: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, promptIntentDryRunPayload(params)),
    getPersonalizationProfile: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.PERSONALIZATION_PROFILE_GET,
      personalizationProfilePayload(RPC_METHODS.PERSONALIZATION_PROFILE_GET, params),
    ),
    savePersonalizationProfile: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.PERSONALIZATION_PROFILE_SAVE,
      personalizationProfilePayload(RPC_METHODS.PERSONALIZATION_PROFILE_SAVE, params),
    ),
    listPromptSections: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_SECTIONS_LIST, promptSectionPayload(RPC_METHODS.PROMPT_SECTIONS_LIST, params)),
    writePromptSection: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_SECTIONS_WRITE, promptSectionPayload(RPC_METHODS.PROMPT_SECTIONS_WRITE, params)),
    deletePromptSection: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.PROMPT_SECTIONS_DELETE, promptSectionPayload(RPC_METHODS.PROMPT_SECTIONS_DELETE, params)),
    listDags: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAGS, dashboardDagsPayload(params)),
    getDagDetail: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_DETAIL,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DETAIL, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DETAIL, params), 'dagKey'),
    ),
    getDagRuns: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_RUNS, dashboardDagRunsPayload(params)),
    getDagRun: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_RUN,
      requireKey(RPC_METHODS.DASHBOARD_DAG_RUN, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_RUN, params), 'runKey'),
    ),
    startDag: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_START, dashboardDagStartPayload(params)),
    createAndStartDag: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START, dashboardDagCreateAndStartPayload(params)),
    dispatchDagNode: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, dashboardDagDispatchNodePayload(params)),
    terminateDagRun: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    terminateDag: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    deleteDag: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_DELETE,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DELETE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DELETE, params), 'dagKey'),
    ),
    applyDagOps: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, dashboardDagApplyOpsPayload(params)),
    listWorkflowTemplates: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_LIST,
      workflowTemplateListPayload(params),
    ),
    getWorkflowTemplate: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_GET,
      requireKey(RPC_METHODS.WORKFLOW_TEMPLATES_GET, assertPlainObject(RPC_METHODS.WORKFLOW_TEMPLATES_GET, params), 'templateId'),
    ),
    renderWorkflowTemplateDraft: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG,
      workflowTemplateRenderPayload(params),
    ),
    saveWorkflowTemplate: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_SAVE,
      workflowTemplateSavePayload(params),
    ),
    rollbackWorkflowTemplate: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_ROLLBACK,
      workflowTemplateRollbackPayload(params),
    ),
  };
}

export {
  BACKEND_RESPONSE_VALIDATORS, NATIVE_DEP_FALLBACKS, resolveNativeDeps, createBackendCaller, createConfigProjectApi, createAppUpdateApi,
  createObservabilityMemoryApi, datasourceCreatePayload, datasourceImportLocalFilePayload, datasourceListPayload, datasourceDocumentIDPayload, datasourceChunksPayload,
  datasourceUpdatePayload, createDatasourceApi, createObservabilityApi, createMemoryApi, createPromptDagApi,
};
