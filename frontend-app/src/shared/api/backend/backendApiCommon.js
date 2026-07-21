import { RPC_METHODS } from "./backendRpcMethods.js";

/** @typedef {Record<string, unknown>} JSONObject */

const objectPrototype = Object.prototype;
const TOOL_SURFACE_MODES = new Set(["chat", "auto", "agent"]);
const MCP_TOOL_LIFECYCLE_STATES = new Set([
  "enabled",
  "disabled",
  "suspended",
  "removed",
]);
const DEFAULT_PROMPT_INTENT_KIND = "expert";
const DEFAULT_PROMPT_SOURCE_TYPE = "user_input";

/** @param {string} method @param {unknown} params @returns {JSONObject} */
function assertPlainObject(method, params) {
  // 误判防护：assertPlainObject 是 React RPC facade 的对象参数守卫。
  const value = params == null ? {} : params;
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`${method} params must be an object`);
  }
  return /** @type {JSONObject} */ (value);
}

/** @param {string} method @param {unknown} params @returns {JSONObject} */
function assertStrictPlainObject(method, params) {
  const value = assertPlainObject(method, params);
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== objectPrototype && prototype !== null) {
    throw new TypeError(`${method} params must be a plain object`);
  }
  return value;
}

/** @param {string} method @param {JSONObject} payload */
function assertNoExtraPayloadFields(method, payload) {
  const [key] = Object.keys(payload);
  if (key) {
    throw new Error(`${method}: unsupported payload field ${key}`);
  }
}

/** @param {JSONObject} payload @param {string} key */
function takePayloadField(payload, key) {
  const value = payload[key];
  delete payload[key];
  return value;
}

/** @param {JSONObject} payload @param {readonly string[]} keys */
function takePayloadFields(payload, keys) {
  /** @type {JSONObject} */
  const out = {};
  for (const key of keys) {
    if (hasOwn(payload, key)) out[key] = payload[key];
    delete payload[key];
  }
  return out;
}

/** @param {unknown} value @param {PropertyKey} key */
function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}

/** @param {unknown} value */
function normalizeString(value) {
  if (value === undefined || value === null) return "";
  return String(value).trim();
}

/** @param {string} method @param {unknown} value @param {string} key */
function normalizeRequiredString(method, value, key) {
  const normalized = normalizeString(value);
  if (!normalized) throw new Error(`${method}: ${key} is required`);
  return normalized;
}

/** @param {unknown} value */
function normalizeOptionalString(value) {
  if (value === undefined || value === null) return "";
  return String(value);
}

/** @param {unknown} value */
function optionalPayloadObject(value) {
  if (value === undefined || value === null) return {};
  return value;
}

/** @param {unknown} value */
function normalizeProviderConfigValue(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    const fields = /** @type {JSONObject} */ (value);
    for (const key of ["value", "id", "key", "name", "model", "provider"]) {
      const normalized = normalizeString(fields[key]);
      if (normalized) return normalized;
    }
    return "";
  }
  return normalizeString(value);
}

/** @param {unknown} value */
function normalizeToolSurfaceMode(value) {
  const mode = normalizeString(value).toLowerCase();
  if (!mode) return "";
  if (!TOOL_SURFACE_MODES.has(mode))
    throw new Error(
      `${RPC_METHODS.THREAD_START}: toolSurfaceMode must be chat, auto, or agent`,
    );
  return mode;
}

/**
 * @param {string} method
 * @param {unknown} params
 * @returns {Record<string, unknown> & { cwd: string }}
 */
function requireCwd(method, params) {
  // 误判防护：requireCwd 阻断缺 cwd 的 backend RPC 参数。
  const payload = assertPlainObject(method, params);
  const cwd = normalizeString(payload.cwd);
  if (!cwd || cwd === ".") {
    throw new Error(`${method}: cwd is required`);
  }
  return { ...payload, cwd };
}

/** @param {string} method @param {unknown} params */
function requireThreadId(method, params) {
  // 误判防护：requireThreadId 阻断缺 threadId 的 backend RPC 参数。
  const payload = assertPlainObject(method, params);
  const threadId = normalizeString(payload.threadId || payload.thread_id);
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return { ...payload, threadId };
}

/** @param {string} method @param {unknown} params @param {string} key */
function requireKey(method, params, key) {
  // 误判防护：requireKey 阻断缺关键字段的 backend RPC 参数。
  const payload = assertPlainObject(method, params);
  const value = normalizeString(payload[key]);
  if (!value) {
    throw new Error(`${method}: ${key} is required`);
  }
  return { ...payload, [key]: value };
}

/** @param {JSONObject} payload */
function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(
      ([, value]) => value !== undefined && value !== "",
    ),
  );
}

/** @param {string} method @param {unknown} params */
function requireSkillScope(method, params) {
  const payload = assertPlainObject(method, params);
  const scope = normalizeString(payload.scope);
  if (scope !== "project" && scope !== "personal") {
    throw new Error(`${method}: scope must be project or personal`);
  }
  return { ...payload, scope };
}

/** @param {string} method @param {unknown} params */
function requireContent(method, params) {
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, "content"))
    throw new Error(`${method}: content is required`);
  return { ...payload, content: normalizeOptionalString(payload.content) };
}

/** @param {string} method @param {unknown} params */
function requirePaths(method, params) {
  const payload = assertPlainObject(method, params);
  if (!Array.isArray(payload.paths))
    throw new Error(`${method}: paths must be an array`);
  return payload;
}

/** @param {string} method @param {unknown} params @param {string} key */
function requireBoolean(method, params, key) {
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, key)) throw new Error(`${method}: ${key} is required`);
  if (typeof payload[key] !== "boolean")
    throw new Error(`${method}: ${key} must be boolean`);
  return { ...payload, [key]: payload[key] };
}

/** @param {string} method @param {JSONObject} payload */
function normalizeOptionalLimit(method, payload) {
  if (
    !hasOwn(payload, "limit") ||
    payload.limit === undefined ||
    payload.limit === ""
  )
    return undefined;
  const limit = Number(payload.limit);
  if (!Number.isInteger(limit) || limit <= 0)
    throw new Error(`${method}: limit must be a positive integer`);
  return limit;
}

/** @param {string} method @param {JSONObject} payload @param {string} camelKey @param {string} snakeKey */
function normalizeOptionalCursorInteger(method, payload, camelKey, snakeKey) {
  const raw = payload[camelKey] ?? payload[snakeKey];
  if (raw === undefined || raw === null || raw === "") return undefined;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0)
    throw new Error(`${method}: ${camelKey} must be a non-negative integer`);
  return value;
}

export {
  objectPrototype,
  TOOL_SURFACE_MODES,
  MCP_TOOL_LIFECYCLE_STATES,
  DEFAULT_PROMPT_INTENT_KIND,
  DEFAULT_PROMPT_SOURCE_TYPE,
  assertPlainObject,
  assertStrictPlainObject,
  assertNoExtraPayloadFields,
  takePayloadField,
  takePayloadFields,
  hasOwn,
  normalizeString,
  normalizeRequiredString,
  normalizeOptionalString,
  optionalPayloadObject,
  normalizeProviderConfigValue,
  normalizeToolSurfaceMode,
  requireCwd,
  requireThreadId,
  requireKey,
  cleanObject,
  requireSkillScope,
  requireContent,
  requirePaths,
  requireBoolean,
  normalizeOptionalLimit,
  normalizeOptionalCursorInteger,
};
