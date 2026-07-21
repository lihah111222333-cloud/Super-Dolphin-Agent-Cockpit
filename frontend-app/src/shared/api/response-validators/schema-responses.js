// @ts-check

import {
  parseDashboardPromptsResponse,
  parseMemoryAutoDreamIntentResponse,
  parseMemoryConsolidationJobResponse,
  parseMemoryEntryDeleteResponse,
  parseMemoryEntryDetailResponse,
  parseMemorySimilarityIgnoreResponse,
  parseMemorySnapshotResponse,
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
} from "../backendSchemas.js";

/** @param {string} method @param {unknown} response @param {(response: unknown) => unknown} parser */
function validateSchemaResponse(method, response, parser) {
  try {
    return parser(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "";
    throw new TypeError(`${method} response ${message || "schema is invalid"}`, { cause: error });
  }
}

/** @type {(method: string, response: unknown) => unknown} */
export const validateObservabilityResultResponse = (method, response) =>
  validateSchemaResponse(method, response, parseObservabilityResultResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateMemorySnapshotResponse = (method, response) =>
  validateSchemaResponse(method, response, parseMemorySnapshotResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateSharedFilesDashboardResponse = (method, response) =>
  validateSchemaResponse(method, response, parseSharedFilesDashboardResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateSharedFileDetailResponse = (method, response) =>
  validateSchemaResponse(method, response, parseSharedFileDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateMemoryEntryDetailResponse = (method, response) =>
  validateSchemaResponse(method, response, parseMemoryEntryDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateMemoryEntryDeleteResponse = (method, response) =>
  validateSchemaResponse(method, response, parseMemoryEntryDeleteResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateMemoryAutoDreamIntentResponse = (method, response) =>
  validateSchemaResponse(method, response, parseMemoryAutoDreamIntentResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateMemorySimilarityIgnoreResponse = (method, response) =>
  validateSchemaResponse(method, response, parseMemorySimilarityIgnoreResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateMemoryConsolidationJobResponse = (method, response) =>
  validateSchemaResponse(method, response, parseMemoryConsolidationJobResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateSharedFileDeleteResponse = (method, response) =>
  validateSchemaResponse(method, response, parseSharedFileDeleteResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateWorkflowMaterialWriteResponse = (method, response) =>
  validateSchemaResponse(method, response, parseWorkflowMaterialWriteResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePromptAssetsResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePromptAssetsResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateDashboardPromptsResponse = (method, response) =>
  validateSchemaResponse(method, response, parseDashboardPromptsResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePromptDetailResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePromptDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePromptIntentDraftResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePromptIntentDraftResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePromptIntentCommitResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePromptIntentCommitResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePromptIntentDiscardResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePromptIntentDiscardResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePromptIntentDryRunResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePromptIntentDryRunResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validatePersonalizationProfileResponse = (method, response) =>
  validateSchemaResponse(method, response, parsePersonalizationProfileResponse);
