// @ts-check

import { parseSkillToolMutationResponse, parseSkillToolsListResponse } from "../backendSchemas.js";
import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseArray,
  assertResponseRecord,
  hasOwn,
  validateRequiredFields,
  validateStringFields,
} from "./shared.js";

/** @param {string} method @param {unknown} value @param {string} label */
function validateStringArray(method, value, label) {
  const items = assertResponseArray(method, value, label);
  items.forEach((item, index) => {
    if (typeof item !== "string")
      throw new TypeError(`${method} response ${label}[${index}] must be a string`);
  });
}

/** @param {string} method @param {unknown} response */
export function validateSkillFilesResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["dir", "files"]), "body");
  validateStringFields(method, value, "body", ["dir"], []);
  assertResponseArray(method, value.files, "body.files").forEach((raw, index) => {
    const label = `body.files[${index}]`;
    const file = assertResponseRecord(method, raw, label);
    assertOnlyResponseKeys(method, file, new Set(["name", "path", "size", "is_main"]), label);
    validateRequiredFields(method, file, label, {
      stringKeys: ["name", "path"],
      integerKeys: ["size"],
      booleanKeys: ["is_main"],
    });
    const size = file.size;
    if (typeof size !== "number" || size < 0)
      throw new TypeError(`${method} response ${label}.size must be non-negative`);
  });
  return value;
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateSkillImportItem(method, response, label) {
  const item = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(
    method,
    item,
    new Set(["name", "dir", "skill_file", "source", "files", "bytes"]),
    label,
  );
  validateRequiredFields(method, item, label, {
    stringKeys: ["name", "dir", "skill_file", "source"],
    integerKeys: ["files", "bytes"],
  });
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateSkillMirrorReport(method, response, label) {
  const report = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(
    method,
    report,
    new Set(["published", "skipped", "deleted", "conflicts"]),
    label,
  );
  for (const key of ["published", "skipped", "deleted", "conflicts"]) {
    if (!hasOwn(report, key)) continue;
    assertResponseArray(method, report[key], `${label}.${key}`).forEach((raw, index) => {
      const itemLabel = `${label}.${key}[${index}]`;
      const item = assertResponseRecord(method, raw, itemLabel);
      assertOnlyResponseKeys(
        method,
        item,
        new Set([
          "target_id",
          "provider",
          "scope",
          "relative_mirror_path",
          "canonical_id",
          "old_hash",
          "new_hash",
          "conflict_kind",
          "error",
        ]),
        itemLabel,
      );
      validateStringFields(
        method,
        item,
        itemLabel,
        ["target_id"],
        [
          "provider",
          "scope",
          "relative_mirror_path",
          "canonical_id",
          "old_hash",
          "new_hash",
          "conflict_kind",
          "error",
        ],
      );
    });
  }
}

/** @param {string} method @param {unknown} response */
export function validateSkillImportResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(
    method,
    value,
    new Set(["requested", "imported", "failures", "skill", "mirror_publish"]),
    "body",
  );
  validateRequiredFields(method, value, "body", { stringKeys: [], integerKeys: ["requested"] });
  const imported = assertResponseArray(method, value.imported, "body.imported");
  imported.forEach((item, index) =>
    validateSkillImportItem(method, item, `body.imported[${index}]`),
  );
  if (hasOwn(value, "failures")) {
    assertResponseArray(method, value.failures, "body.failures").forEach((raw, index) => {
      const label = `body.failures[${index}]`;
      const failure = assertResponseRecord(method, raw, label);
      assertOnlyResponseKeys(method, failure, new Set(["source", "error"]), label);
      validateStringFields(method, failure, label, ["source", "error"], []);
    });
  }
  if (hasOwn(value, "skill")) validateSkillImportItem(method, value.skill, "body.skill");
  if (imported.length > 0 && !hasOwn(value, "mirror_publish")) {
    throw new TypeError(
      `${method} response body.mirror_publish is required when skills were imported`,
    );
  }
  if (hasOwn(value, "mirror_publish"))
    validateSkillMirrorReport(method, value.mirror_publish, "body.mirror_publish");
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateSkillSummarySuggestionResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["description"]), "body");
  validateStringFields(method, value, "body", ["description"], []);
  return value;
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateSkillResolutionSource(method, response, label) {
  const source = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(
    method,
    source,
    new Set([
      "scope",
      "canonical_id",
      "personal_type",
      "content_hash",
      "canonical_hash",
      "path",
      "skill_file",
    ]),
    label,
  );
  validateStringFields(
    method,
    source,
    label,
    ["scope", "canonical_id"],
    ["personal_type", "content_hash", "canonical_hash", "path", "skill_file"],
  );
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateSkillResolutionListItem(method, response, label) {
  const item = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(
    method,
    item,
    new Set([
      "conflict_id",
      "kind",
      "scope",
      "personal_type",
      "name",
      "available_actions",
      "provider_entries",
      "sources",
    ]),
    label,
  );
  validateStringFields(
    method,
    item,
    label,
    ["conflict_id", "kind", "name"],
    ["scope", "personal_type"],
  );
  validateStringArray(method, item.available_actions, `${label}.available_actions`);
  if (hasOwn(item, "provider_entries")) {
    assertResponseArray(method, item.provider_entries, `${label}.provider_entries`).forEach(
      (raw, index) => {
        const itemLabel = `${label}.provider_entries[${index}]`;
        const entry = assertResponseRecord(method, raw, itemLabel);
        assertOnlyResponseKeys(
          method,
          entry,
          new Set([
            "provider",
            "source_path",
            "target_path",
            "source_hash",
            "target_hash",
            "target_id",
            "source_path_id",
          ]),
          itemLabel,
        );
        validateStringFields(
          method,
          entry,
          itemLabel,
          ["provider"],
          [
            "source_path",
            "target_path",
            "source_hash",
            "target_hash",
            "target_id",
            "source_path_id",
          ],
        );
      },
    );
  }
  if (hasOwn(item, "sources")) {
    assertResponseArray(method, item.sources, `${label}.sources`).forEach((raw, index) =>
      validateSkillResolutionSource(method, raw, `${label}.sources[${index}]`),
    );
  }
}

/** @param {string} method @param {unknown} response */
export function validateSkillResolutionListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["items"]), "body");
  assertResponseArray(method, value.items, "body.items").forEach((item, index) =>
    validateSkillResolutionListItem(method, item, `body.items[${index}]`),
  );
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateSkillResolutionPreviewResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, new Set(["conflict_id", "kind", "items"]), "body");
  validateStringFields(method, value, "body", ["conflict_id", "kind"], []);
  assertResponseArray(method, value.items, "body.items").forEach((raw, index) => {
    const label = `body.items[${index}]`;
    const item = assertResponseRecord(method, raw, label);
    assertOnlyResponseKeys(
      method,
      item,
      new Set([
        "action",
        "provider",
        "preview_id",
        "source_provider",
        "source_path_id",
        "source_path",
        "target_path",
        "source_hash",
        "target_hash",
        "preview_hash",
        "backup_path",
        "confirm_delete_mirror_hash",
        "diff",
      ]),
      label,
    );
    validateStringFields(
      method,
      item,
      label,
      ["action"],
      [
        "provider",
        "preview_id",
        "source_provider",
        "source_path_id",
        "source_path",
        "target_path",
        "source_hash",
        "target_hash",
        "preview_hash",
        "backup_path",
        "confirm_delete_mirror_hash",
        "diff",
      ],
    );
  });
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateSkillResolutionApplyResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(
    method,
    value,
    new Set(["action", "name", "resultingHash", "partialFailure", "followUpAction"]),
    "body",
  );
  validateRequiredFields(method, value, "body", {
    stringKeys: ["action", "name", "resultingHash", "followUpAction"],
    integerKeys: [],
    booleanKeys: ["partialFailure"],
  });
  return value;
}

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
export const validateSkillToolsListResponse = (method, response) =>
  validateSchemaResponse(method, response, parseSkillToolsListResponse);
/** @type {(method: string, response: unknown) => unknown} */
export const validateSkillToolMutationResponse = (method, response) =>
  validateSchemaResponse(method, response, parseSkillToolMutationResponse);
