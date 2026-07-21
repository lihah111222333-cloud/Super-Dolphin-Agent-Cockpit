export const DEFAULT_BASE_URL = "http://127.0.0.1:5175/";
export const DEFAULT_PROTOCOL_VERSION = "2024-11-05";
export const SERVER_INFO = Object.freeze({
  name: "super-dolphin-ui-test-mcp",
  version: "0.1.0",
});
export const ERROR_CODES = Object.freeze({
  PARSE_ERROR: -32700,
  INVALID_REQUEST: -32600,
  METHOD_NOT_FOUND: -32601,
  INVALID_PARAMS: -32602,
  INTERNAL_ERROR: -32603,
  LIFECYCLE_ERROR: -32000,
});

export class JSONRPCError extends Error {
  constructor(code, message, data) {
    super(message);
    this.name = "JSONRPCError";
    this.code = code;
    this.data = data;
  }
}

export class ToolExecutionError extends Error {
  constructor(message, details = {}) {
    super(message);
    this.name = "ToolExecutionError";
    this.details = details;
  }
}

export function validateBaseURL(value = DEFAULT_BASE_URL) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch (error) {
    throw new Error(
      `SUPER_DOLPHIN_UI_TEST_BASE_URL must be a valid URL: ${error.message}`,
    );
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
    throw new Error("SUPER_DOLPHIN_UI_TEST_BASE_URL must use http or https");
  if (parsed.username || parsed.password)
    throw new Error(
      "SUPER_DOLPHIN_UI_TEST_BASE_URL must not include credentials",
    );
  if (
    !["127.0.0.1", "localhost", "[::1]", "::1"].includes(
      parsed.hostname.toLowerCase(),
    )
  ) {
    throw new Error(
      "SUPER_DOLPHIN_UI_TEST_BASE_URL must use 127.0.0.1, localhost, or [::1]",
    );
  }
  return parsed;
}

export function isPlainObject(value) {
  return (
    value != null &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    Object.getPrototypeOf(value) === Object.prototype
  );
}

export function invalidParams(message) {
  return new JSONRPCError(ERROR_CODES.INVALID_PARAMS, message);
}
export function wrapContractValidation(fn) {
  try {
    return fn();
  } catch (error) {
    throw invalidParams(error.message);
  }
}
export function validatePlainObject(value, label) {
  if (!isPlainObject(value)) throw invalidParams(`${label} must be an object`);
}
export function validateExactObject(value, allowedKeys, label, contract) {
  validatePlainObject(value, label);
  if (contract && typeof contract.validateExactKeys === "function")
    return wrapContractValidation(() =>
      contract.validateExactKeys(value, allowedKeys, label),
    );
  const extra = Object.keys(value).filter((key) => !allowedKeys.includes(key));
  if (extra.length > 0)
    throw invalidParams(`${label} contains unknown field: ${extra[0]}`);
}
export function validateNoParams(params, label, contract) {
  if (params != null) validateExactObject(params, [], label, contract);
}
export function validateInitializeParams(params, contract) {
  if (params == null) return;
  validateExactObject(
    params,
    ["protocolVersion", "capabilities", "clientInfo"],
    "initialize params",
    contract,
  );
  if (
    params.protocolVersion != null &&
    typeof params.protocolVersion !== "string"
  )
    throw invalidParams("initialize protocolVersion must be a string");
  if (params.capabilities != null && !isPlainObject(params.capabilities))
    throw invalidParams("initialize capabilities must be an object");
  if (params.clientInfo != null && !isPlainObject(params.clientInfo))
    throw invalidParams("initialize clientInfo must be an object");
}
export function responseID(message) {
  return isPlainObject(message) &&
    Object.hasOwn(message, "id") &&
    isValidID(message.id)
    ? message.id
    : null;
}
export function isValidID(id) {
  return (
    id === null ||
    typeof id === "string" ||
    (typeof id === "number" && Number.isFinite(id))
  );
}
export function successResponse(id, result, isNotification) {
  return isNotification ? null : { jsonrpc: "2.0", id, result };
}
export function makeErrorResponse(code, message, id, data) {
  const error = { code, message };
  if (data !== undefined) error.data = data;
  return { jsonrpc: "2.0", id, error };
}
export function toolResult(structuredContent) {
  return {
    content: [{ type: "text", text: JSON.stringify(structuredContent) }],
    structuredContent,
    isError: false,
  };
}
export function toolError({
  tool,
  action,
  target,
  timeoutMs,
  reason,
  ...details
}) {
  const structuredContent = {
    error: { tool, ...details, action, target, timeoutMs, reason },
  };
  return {
    content: [{ type: "text", text: JSON.stringify(structuredContent) }],
    structuredContent,
    isError: true,
  };
}
export function assertContract(contract) {
  if (contract == null || typeof contract !== "object")
    throw new Error("UI test MCP contract module is required");
  for (const key of [
    "UI_TEST_TOOLS",
    "UI_TEST_ACTIONS",
    "UI_TEST_TARGETS",
    "UI_TEST_WAIT_STATES",
    "UI_TEST_SCENARIO_IDS",
  ])
    if (!Array.isArray(contract[key]))
      throw new Error(`UI test MCP contract missing ${key}`);
  for (const key of ["UI_TEST_ROUTES", "UI_TEST_SCENARIOS", "UI_TEST_LIMITS"])
    if (!isPlainObject(contract[key]))
      throw new Error(`UI test MCP contract missing ${key}`);
  for (const key of [
    "assertKnownToolName",
    "assertKnownActionName",
    "assertKnownTargetName",
    "assertKnownScenarioName",
    "normalizeLimit",
    "normalizeTimeoutMs",
    "validateExactKeys",
  ])
    if (typeof contract[key] !== "function")
      throw new Error(`UI test MCP contract missing ${key}`);
  for (const key of [
    "maxLimit",
    "maxTextLength",
    "maxTimeoutMs",
    "pollIntervalMs",
    "maxFrameBytes",
    "maxHeaderBytes",
    "maxLineBytes",
  ])
    if (
      !Number.isSafeInteger(contract.UI_TEST_LIMITS[key]) ||
      contract.UI_TEST_LIMITS[key] <= 0
    )
      throw new Error(
        `UI test MCP contract UI_TEST_LIMITS.${key} must be a positive integer`,
      );
  return contract;
}
export function strictSchema(properties, required = []) {
  return { type: "object", properties, required, additionalProperties: false };
}
export function assertProductionGate(env) {
  if (
    (env.NODE_ENV === "production" ||
      env.MODE === "production" ||
      env.VITE_MODE === "production") &&
    env.SUPER_DOLPHIN_UI_TEST_MCP !== "1"
  )
    throw new Error(
      "SUPER_DOLPHIN_UI_TEST_MCP=1 is required in production mode",
    );
}
export function truthy(value) {
  return value === "1" || value === "true" || value === "yes";
}
export function writeStderr(stderr, message) {
  if (stderr && typeof stderr.write === "function")
    stderr.write(`[ui-test-mcp] ${message}\n`);
}
