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
  parseSkillToolMutationResponse,
  parseSkillToolsListResponse,
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
const validateMemorySnapshotResponse = (method, response) => validateSchemaResponse(method, response, parseMemorySnapshotResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSkillToolsListResponse = (method, response) => validateSchemaResponse(method, response, parseSkillToolsListResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSkillToolMutationResponse = (method, response) => validateSchemaResponse(method, response, parseSkillToolMutationResponse);
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

/** @param {string} method @param {unknown} value @param {string} label */
function validateStringArray(method, value, label) {
  const items = assertResponseArray(method, value, label);
  items.forEach((item, index) => {
    if (typeof item !== 'string') {
      throw new TypeError(`${method} response ${label}[${index}] must be a string`);
    }
  });
}

/** @param {string} method @param {unknown} response */
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
    if (typeof file.size !== 'number' || file.size < 0) {
      throw new TypeError(`${method} response ${label}.size must be non-negative`);
    }
  });
  return value;
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateSkillImportItem(method, response, label) {
  const item = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, item, new Set(['name', 'dir', 'skill_file', 'source', 'files', 'bytes']), label);
  validateRequiredFields(method, item, label, {
    stringKeys: ['name', 'dir', 'skill_file', 'source'], integerKeys: ['files', 'bytes'],
  });
}

/** @param {string} method @param {unknown} response @param {string} label */
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

/** @param {string} method @param {unknown} response */
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

/** @param {string} method @param {unknown} response */
function validateSkillSummarySuggestionResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['description']), 'body');
  validateStringFields(method, value, 'body', ['description'], []);
  return value;
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateSkillResolutionSource(method, response, label) {
  const source = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, source, new Set(['scope', 'canonical_id', 'personal_type', 'content_hash', 'canonical_hash', 'path', 'skill_file']), label);
  validateStringFields(method, source, label, ['scope', 'canonical_id'], ['personal_type', 'content_hash', 'canonical_hash', 'path', 'skill_file']);
}

/** @param {string} method @param {unknown} response @param {string} label */
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

/** @param {string} method @param {unknown} response */
function validateSkillResolutionListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(['items']), 'body');
  assertResponseArray(method, value.items, 'body.items').forEach((item, index) => validateSkillResolutionListItem(method, item, `body.items[${index}]`));
  return value;
}

/** @param {string} method @param {unknown} response */
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

/** @param {string} method @param {unknown} response */
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

/** @param {string} method @param {unknown} response @param {string} label */
function validateDatasourceDocument(method, response, label) {
  const document = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, document, new Set(['documentId', 'sourcePath', 'fileName', 'extension', 'sizeBytes', 'contentHash', 'chunkCount', 'totalChars', 'status', 'errorMessage', 'createdAt', 'updatedAt']), label);
  validateRequiredFields(method, document, label, {
    stringKeys: ['sourcePath', 'fileName', 'extension', 'contentHash', 'status', 'errorMessage', 'createdAt', 'updatedAt'],
    integerKeys: ['documentId', 'sizeBytes', 'chunkCount', 'totalChars'],
  });
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
    [methods.SKILL_TOOLS_CREATE]: validateSkillToolMutationResponse,
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
