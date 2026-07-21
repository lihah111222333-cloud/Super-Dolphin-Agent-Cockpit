// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseArray,
  assertResponseRecord,
  validateRequiredFields,
} from "./shared.js";

/** @param {string} method @param {unknown} response @param {string} label */
function validateDatasourceDocument(method, response, label) {
  const document = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(
    method,
    document,
    new Set([
      "documentId",
      "sourcePath",
      "fileName",
      "extension",
      "sizeBytes",
      "contentHash",
      "chunkCount",
      "totalChars",
      "status",
      "errorMessage",
      "createdAt",
      "updatedAt",
    ]),
    label,
  );
  validateRequiredFields(method, document, label, {
    stringKeys: [
      "sourcePath",
      "fileName",
      "extension",
      "contentHash",
      "status",
      "errorMessage",
      "createdAt",
      "updatedAt",
    ],
    integerKeys: ["documentId", "sizeBytes", "chunkCount", "totalChars"],
  });
  return document;
}

/** @param {string} method @param {unknown} response @param {string} label @param {number} [documentId] */
function validateDatasourceChunk(method, response, label, documentId) {
  const chunk = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(
    method,
    chunk,
    new Set([
      "id",
      "documentId",
      "chunkIndex",
      "content",
      "charCount",
      "byteCount",
      "embeddingModel",
      "embeddingDim",
      "tokenCount",
      "createdAt",
    ]),
    label,
  );
  validateRequiredFields(method, chunk, label, {
    stringKeys: ["content", "embeddingModel", "createdAt"],
    integerKeys: [
      "id",
      "documentId",
      "chunkIndex",
      "charCount",
      "byteCount",
      "embeddingDim",
      "tokenCount",
    ],
  });
  if (documentId !== undefined && chunk.documentId !== documentId) {
    throw new TypeError(
      `${method} response ${label}.documentId must match body.document.documentId`,
    );
  }
}

/** @param {string} method @param {Record<string, unknown>} value */
function validateDatasourcePageFields(method, value) {
  validateRequiredFields(method, value, "body", {
    stringKeys: [],
    integerKeys: ["nextCursor"],
    booleanKeys: ["hasMore"],
  });
  return assertResponseArray(method, value.chunks, "body.chunks");
}

/** @param {string} method @param {unknown} response */
export function validateDatasourceDocumentsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["documents"]), "body");
  assertResponseArray(method, value.documents, "body.documents").forEach((item, index) =>
    validateDatasourceDocument(method, item, `body.documents[${index}]`),
  );
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateDatasourceDetailResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(
    method,
    value,
    new Set(["document", "chunks", "hasMore", "nextCursor"]),
    "body",
  );
  const document = validateDatasourceDocument(method, value.document, "body.document");
  const documentId = document.documentId;
  if (typeof documentId !== "number")
    throw new TypeError(`${method} response body.document.documentId must be an integer`);
  validateDatasourcePageFields(method, value).forEach((item, index) =>
    validateDatasourceChunk(method, item, `body.chunks[${index}]`, documentId),
  );
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateDatasourceChunksResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["chunks", "hasMore", "nextCursor"]), "body");
  validateDatasourcePageFields(method, value).forEach((item, index) =>
    validateDatasourceChunk(method, item, `body.chunks[${index}]`),
  );
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateDatasourceImportResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(
    method,
    value,
    new Set([
      "documentId",
      "sourcePath",
      "fileName",
      "extension",
      "sizeBytes",
      "contentHash",
      "chunkCount",
      "totalChars",
      "status",
    ]),
    "body",
  );
  validateRequiredFields(method, value, "body", {
    stringKeys: ["sourcePath", "fileName", "extension", "contentHash", "status"],
    integerKeys: ["documentId", "sizeBytes", "chunkCount", "totalChars"],
  });
  return value;
}

/** @param {string} method @param {unknown} response @param {unknown} request */
export function validateDatasourceDeleteResponse(method, response, request) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["documentId", "deleted"]), "body");
  validateRequiredFields(method, value, "body", {
    stringKeys: [],
    integerKeys: ["documentId"],
    booleanKeys: ["deleted"],
  });
  if (value.deleted !== true) throw new TypeError(`${method} response body.deleted must be true`);
  const payload = assertResponseRecord(method, request, "request");
  if (!Number.isInteger(payload.documentId) || /** @type {number} */ (payload.documentId) <= 0) {
    throw new TypeError(
      `${method} request documentId must be a positive integer for response correlation`,
    );
  }
  if (value.documentId !== payload.documentId) {
    throw new TypeError(`${method} response documentId must match request documentId`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateDatasourceDocumentResponse(method, response) {
  return validateDatasourceDocument(method, response, "body");
}
