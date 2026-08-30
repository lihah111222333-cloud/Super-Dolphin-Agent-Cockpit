// @ts-check

import {
  parseDashboardPromptsResponse,
  parseModelProviderRegistryResponse,
  parseObservabilityResultResponse,
  parsePersonalizationProfileResponse,
  parsePromptAssetsResponse,
  parsePromptDetailResponse,
  parsePromptIntentCommitResponse,
  parsePromptIntentDiscardResponse,
  parsePromptIntentDraftResponse,
  parsePromptIntentDryRunResponse,
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
  validateDashboardDagDetailResponse,
  validateDashboardDagRunResponse,
  validateDashboardDagRunsResponse,
  validateSidebarStateResponse,
  validateThreadPromptHistoryResponse,
  validateThreadRecoverResponse,
  validateToolbridgeToolsListResponse,
  validateUIStateResponse,
  validateWorkflowTemplateDraftResponse,
  validateWorkflowTemplateResponse,
  validateWorkflowTemplateSaveResponse,
  validateWorkflowTemplatesListResponse,
} from './backendResponseValidatorsRuntime.js';
import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseArray,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateStringFields,
} from './backendResponseValidatorShared.js';

const MODEL_PROVIDER_REGISTRY_RESPONSE_KEYS = new Set(['activeVendorId', 'vendors']);
const MODEL_PROVIDER_VENDOR_KEYS = new Set(['id', 'label', 'enabled', 'baseURL', 'envKey', 'codexModelProvider', 'defaultModel', 'codexHome', 'codexInstanceKey', 'budget', 'tokenPool', 'configured', 'maskedEnv', 'envStatus']);
const MODEL_PROVIDER_BUDGET_KEYS = new Set(['dailyUsd', 'monthlyUsd']);
const MODEL_PROVIDER_TOKEN_POOL_KEYS = new Set(['priority', 'fallbackVendorId']);

const MCP_SERVER_LIST_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'mcpServers', 'mcp_servers']);
const MCP_SERVER_STATUS_RESPONSE_KEYS = new Set(['enabled']);
const MCP_SERVER_CONTROL_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'serverName', 'server_name', 'added', 'enabled']);

/**
 * @param {string} method
 * @param {unknown} response
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
 * @param {unknown} response
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
 * @param {unknown} response
 * @param {(response: unknown) => unknown} parser
 */
function validateSchemaResponse(method, response, parser) {
  try {
    return parser(response);
  }
  catch (error) {
    const message = error instanceof Error ? error.message : '';
    throw new TypeError(`${method} response ${message || 'schema is invalid'}`, { cause: error });
  }
}

/** @type {(method: string, response: unknown) => unknown} */
const validateObservabilityResultResponse = (method, response) => validateSchemaResponse(method, response, parseObservabilityResultResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSharedFilesDashboardResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFilesDashboardResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateParsedModelProviderRegistryResponse = (method, response) => validateSchemaResponse(method, response, parseModelProviderRegistryResponse);
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
const validateStrictModelProviderRegistryResponse = (method, response) => {
  const parsed = validateParsedModelProviderRegistryResponse(method, response);
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
  return parsed;
};

/**
 * @param {string} method
 * @param {Record<string, unknown>} value
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

/** @param {string} method @param {unknown} response @param {string} label */
function validateDatasourceDocument(method, response, label) {
  const document = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, document, new Set([
    'documentId', 'sourcePath', 'fileName', 'extension', 'sizeBytes', 'contentHash',
    'chunkCount', 'totalChars', 'status', 'errorMessage', 'qualityStatus', 'qualityReason',
    'extractorName', 'extractorVersion', 'pageCount', 'runeCount', 'visibleRunes',
    'controlRunes', 'nulRunes', 'replacementRunes', 'unmappedFonts', 'createdAt', 'updatedAt',
  ]), label);
  validateRequiredFields(method, document, label, {
    stringKeys: ['sourcePath', 'fileName', 'extension', 'contentHash', 'status', 'errorMessage', 'qualityStatus', 'qualityReason', 'extractorName', 'extractorVersion', 'createdAt', 'updatedAt'],
    integerKeys: ['documentId', 'sizeBytes', 'chunkCount', 'totalChars', 'pageCount', 'runeCount', 'visibleRunes', 'controlRunes', 'nulRunes', 'replacementRunes', 'unmappedFonts'],
  });
  if (typeof document.qualityStatus !== 'string' || !['unknown', 'passed', 'failed'].includes(document.qualityStatus)) {
    throw new TypeError(`${method} response ${label}.qualityStatus is invalid`);
  }
  return document;
}

/** @param {string} method @param {unknown} response */
function validateDatasourceImportResponse(method, response) {
  const document = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, document, new Set([
    'documentId', 'sourcePath', 'fileName', 'extension', 'sizeBytes', 'contentHash',
    'chunkCount', 'totalChars', 'status', 'qualityStatus', 'qualityReason', 'extractorName',
    'extractorVersion', 'pageCount', 'runeCount', 'visibleRunes', 'controlRunes',
    'nulRunes', 'replacementRunes', 'unmappedFonts',
  ]), 'body');
  validateRequiredFields(method, document, 'body', {
    stringKeys: ['sourcePath', 'fileName', 'extension', 'contentHash', 'status', 'qualityStatus', 'qualityReason', 'extractorName', 'extractorVersion'],
    integerKeys: ['documentId', 'sizeBytes', 'chunkCount', 'totalChars', 'pageCount', 'runeCount', 'visibleRunes', 'controlRunes', 'nulRunes', 'replacementRunes', 'unmappedFonts'],
  });
  if (document.status !== 'ready' || document.qualityStatus !== 'passed') {
    throw new TypeError(`${method} response body must be ready with passed quality`);
  }
  return document;
}

/** @param {string} method @param {unknown} response @param {string} label @param {number} [documentId] */
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

/** @param {string} method @param {unknown} response */
function validateDatasourceDocumentsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['documents']), 'body');
  assertResponseArray(method, value.documents, 'body.documents').forEach((item, index) => validateDatasourceDocument(method, item, `body.documents[${index}]`));
  return value;
}

/** @param {string} method @param {Record<string, unknown>} value */
function validateDatasourcePageFields(method, value) {
  validateRequiredFields(method, value, 'body', {
    stringKeys: [], integerKeys: ['nextCursor'], booleanKeys: ['hasMore'],
  });
  const chunks = assertResponseArray(method, value.chunks, 'body.chunks');
  return chunks;
}

/** @param {string} method @param {unknown} response */
function validateDatasourceDetailResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['document', 'chunks', 'hasMore', 'nextCursor']), 'body');
  const document = validateDatasourceDocument(method, value.document, 'body.document');
  if (typeof document.documentId !== 'number') throw new TypeError(`${method} response body.document.documentId must be an integer`);
  const documentId = document.documentId;
  validateDatasourcePageFields(method, value).forEach((item, index) => validateDatasourceChunk(method, item, `body.chunks[${index}]`, documentId));
  return value;
}

/** @param {string} method @param {unknown} response */
function validateDatasourceChunksResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['chunks', 'hasMore', 'nextCursor']), 'body');
  validateDatasourcePageFields(method, value).forEach((item, index) => validateDatasourceChunk(method, item, `body.chunks[${index}]`));
  return value;
}

/** @param {string} method @param {unknown} response */
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
    return validateStrictModelProviderRegistryResponse(method, response);
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
	[methods.DATASOURCE_V2_IMPORT_LOCAL_FILE]: validateDatasourceImportResponse,
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
