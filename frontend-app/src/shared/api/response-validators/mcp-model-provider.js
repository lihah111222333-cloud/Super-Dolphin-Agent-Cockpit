// @ts-check

import { parseModelProviderRegistryResponse } from "../backendSchemas.js";
import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
} from "./shared.js";

const MODEL_PROVIDER_REGISTRY_RESPONSE_KEYS = new Set(["activeVendorId", "vendors"]);
const MODEL_PROVIDER_VENDOR_KEYS = new Set([
  "id",
  "label",
  "enabled",
  "baseURL",
  "envKey",
  "codexModelProvider",
  "defaultModel",
  "codexHome",
  "codexInstanceKey",
  "budget",
  "tokenPool",
  "configured",
  "maskedEnv",
  "envStatus",
]);
const MODEL_PROVIDER_BUDGET_KEYS = new Set(["dailyUsd", "monthlyUsd"]);
const MODEL_PROVIDER_TOKEN_POOL_KEYS = new Set(["priority", "fallbackVendorId"]);
const MCP_SERVER_LIST_RESPONSE_KEYS = new Set([
  "configPath",
  "config_path",
  "mcpServers",
  "mcp_servers",
]);
const MCP_SERVER_STATUS_RESPONSE_KEYS = new Set(["enabled"]);
const MCP_SERVER_CONTROL_RESPONSE_KEYS = new Set([
  "configPath",
  "config_path",
  "serverName",
  "server_name",
  "added",
  "enabled",
]);

/** @param {string} method @param {unknown} response */
export function validateMCPServerListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MCP_SERVER_LIST_RESPONSE_KEYS, "body");
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) throw new Error(`${method} response configPath must be a non-empty string`);
  const servers = value.mcpServers || value.mcp_servers;
  if (!servers || typeof servers !== "object" || Array.isArray(servers)) {
    throw new TypeError(`${method} response mcpServers must be an object`);
  }
  for (const [serverName, server] of Object.entries(servers)) {
    const normalizedName = normalizeString(serverName);
    if (!normalizedName)
      throw new Error(`${method} response mcpServers must not include an empty server name`);
    if (!server || typeof server !== "object" || Array.isArray(server)) {
      throw new TypeError(`${method} response mcpServers.${normalizedName} must be an object`);
    }
    assertOnlyResponseKeys(
      method,
      server,
      MCP_SERVER_STATUS_RESPONSE_KEYS,
      `mcpServers.${normalizedName}`,
    );
    if (typeof server.enabled !== "boolean") {
      throw new TypeError(
        `${method} response mcpServers.${normalizedName}.enabled must be a boolean`,
      );
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
  assertOnlyResponseKeys(method, value, MCP_SERVER_CONTROL_RESPONSE_KEYS, "body");
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) throw new Error(`${method} response configPath must be a non-empty string`);
  const spec = controlSpecs[method];
  const serverName = normalizeString(value.serverName || value.server_name);
  if (!spec || serverName !== spec.serverName) {
    throw new Error(
      `${method} response serverName must be ${spec?.serverName || "a known MCP server"}`,
    );
  }
  if (value.enabled !== spec.enabled)
    throw new TypeError(`${method} response enabled must be ${spec.enabled}`);
  if (hasOwn(value, "added") && typeof value.added !== "boolean") {
    throw new TypeError(`${method} response added must be a boolean`);
  }
  return value;
}

/** @param {string} method @param {unknown} response @param {number} index */
function validateSavedModelProviderVendor(method, response, index) {
  const label = `body.vendors[${index}]`;
  const vendor = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, vendor, MODEL_PROVIDER_VENDOR_KEYS, label);
  if (hasOwn(vendor, "budget")) {
    const budget = assertResponseRecord(method, vendor.budget, `${label}.budget`);
    assertOnlyResponseKeys(method, budget, MODEL_PROVIDER_BUDGET_KEYS, `${label}.budget`);
  }
  if (hasOwn(vendor, "tokenPool")) {
    const tokenPool = assertResponseRecord(method, vendor.tokenPool, `${label}.tokenPool`);
    assertOnlyResponseKeys(method, tokenPool, MODEL_PROVIDER_TOKEN_POOL_KEYS, `${label}.tokenPool`);
  }
}

/** @param {string} method @param {unknown} response */
export function validateModelProviderRegistryResponse(method, response) {
  let parsed;
  try {
    parsed = parseModelProviderRegistryResponse(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "";
    throw new TypeError(`${method} response ${message || "schema is invalid"}`, { cause: error });
  }
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MODEL_PROVIDER_REGISTRY_RESPONSE_KEYS, "body");
  if (!Array.isArray(value.vendors)) {
    throw new TypeError(`${method} response model provider registry body.vendors must be an array`);
  }
  value.vendors.forEach((vendor, index) => validateSavedModelProviderVendor(method, vendor, index));
  return parsed;
}

/** @param {Record<string, string>} methods */
export function createMCPServerControlResponseValidator(methods) {
  const controlSpecs = Object.freeze({
    [methods.MCP_SERVER_SQLITE_START]: { serverName: "sqlite", enabled: true },
    [methods.MCP_SERVER_SQLITE_STOP]: { serverName: "sqlite", enabled: false },
    [methods.MCP_SERVER_PLAYWRIGHT_START]: { serverName: "playwright", enabled: true },
    [methods.MCP_SERVER_PLAYWRIGHT_STOP]: { serverName: "playwright", enabled: false },
  });
  /** @type {(method: string, response: unknown) => unknown} */
  const validateControlResponse = (method, response) =>
    validateMCPServerControlResponse(method, response, controlSpecs);
  return validateControlResponse;
}
