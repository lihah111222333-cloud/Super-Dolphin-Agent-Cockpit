// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  hasOwn,
  normalizeString,
} from '../shared.js';

const RUNTIME_CONFIG_RESPONSE_KEYS = new Set(['model', 'modelProvider', 'cwd', 'approvalPolicy', 'sandbox', 'config', 'baseInstructions', 'developerInstructions', 'personality', 'toolRouting']);
const RUNTIME_TOOL_ROUTING_KEYS = new Set(['mode', 'routerModel', 'routerProvider', 'routerBaseURL', 'routerHasAPIKey', 'confidenceThreshold', 'timeoutSec']);
const BUILTIN_TOOLS_RESPONSE_KEYS = new Set(['tools']);
const BUILTIN_TOOL_KEYS = new Set(['id', 'label', 'description', 'enabled', 'provider', 'replacedBy', 'filterMode', 'enforcement']);
const WINDOW_BOOTSTRAP_RESPONSE_KEYS = new Set(['snapshot']);

/**
 * @param {string} method
 * @param {Record<string, unknown>} response
 * @param {readonly string[]} keys
 */
function requireResponseKey(method, response, keys) {
  for (const key of keys) {
    if (normalizeString(response[key])) return;
  }
  throw new Error(`${method} response missing ${keys.join(' or ')}`);
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const snapshotKeys = [
    'threads',
    'agents',
    'active_turn',
    'recent_turns',
    'token_usage',
    'statuses',
    'unchanged',
    'activeThreadId',
    'mainAgentId',
  ];
  if (!snapshotKeys.some((key) => hasOwn(value, key))) {
    throw new Error(`${method} response missing UI state snapshot fields`);
  }
  if (hasOwn(value, 'threads') && value.threads !== null && !Array.isArray(value.threads)) {
    throw new TypeError(`${method} response threads must be an array or null`);
  }
  if (hasOwn(value, 'agents') && value.agents !== null && !Array.isArray(value.agents)) {
    throw new TypeError(`${method} response agents must be an array or null`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateLspPromptHintResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  for (const key of ['hint', 'defaultHint', 'overrideHint']) {
    if (typeof value[key] !== 'string') {
      throw new TypeError(`${method} response ${key} must be a string`);
    }
  }
  if (typeof value.usingDefault !== 'boolean') {
    throw new TypeError(`${method} response usingDefault must be a boolean`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateRuntimeConfigResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, RUNTIME_CONFIG_RESPONSE_KEYS, 'body');
  for (const key of ['model', 'cwd', 'approvalPolicy']) {
    if (typeof value[key] !== 'string') {
      throw new TypeError(`${method} response ${key} must be a string`);
    }
  }
  for (const key of ['modelProvider', 'sandbox', 'config', 'baseInstructions', 'developerInstructions', 'personality']) {
    if (!hasOwn(value, key)) {
      throw new TypeError(`${method} response ${key} is required`);
    }
  }
  const toolRouting = value.toolRouting;
  if (!toolRouting || typeof toolRouting !== 'object' || Array.isArray(toolRouting)) {
    throw new TypeError(`${method} response toolRouting must be an object`);
  }
  const routing = /** @type {Record<string, unknown>} */ (toolRouting);
  assertOnlyResponseKeys(method, routing, RUNTIME_TOOL_ROUTING_KEYS, 'toolRouting');
  for (const key of ['mode', 'routerModel', 'routerProvider', 'routerBaseURL']) {
    if (typeof routing[key] !== 'string') {
      throw new TypeError(`${method} response toolRouting.${key} must be a string`);
    }
  }
  if (typeof routing.routerHasAPIKey !== 'boolean') {
    throw new TypeError(`${method} response toolRouting.routerHasAPIKey must be a boolean`);
  }
  if (typeof routing.confidenceThreshold !== 'number' || !Number.isFinite(routing.confidenceThreshold)) {
    throw new TypeError(`${method} response toolRouting.confidenceThreshold must be a finite number`);
  }
  if (!Number.isInteger(routing.timeoutSec)) {
    throw new TypeError(`${method} response toolRouting.timeoutSec must be an integer`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateBuiltinToolsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, BUILTIN_TOOLS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.tools)) {
    throw new TypeError(`${method} response tools must be an array`);
  }
  /** @type {unknown[]} */
  const tools = value.tools;
  tools.forEach((tool, index) => {
    if (!tool || typeof tool !== 'object' || Array.isArray(tool)) {
      throw new TypeError(`${method} response tools[${index}] must be an object`);
    }
    const toolRecord = /** @type {Record<string, unknown>} */ (tool);
    assertOnlyResponseKeys(method, toolRecord, BUILTIN_TOOL_KEYS, `tools[${index}]`);
    for (const key of ['id', 'label']) {
      if (typeof toolRecord[key] !== 'string') {
        throw new TypeError(`${method} response tools[${index}].${key} must be a string`);
      }
    }
    if (typeof toolRecord.enabled !== 'boolean') {
      throw new TypeError(`${method} response tools[${index}].enabled must be a boolean`);
    }
    for (const key of ['description', 'provider', 'replacedBy', 'filterMode', 'enforcement']) {
      if (hasOwn(toolRecord, key) && typeof toolRecord[key] !== 'string') {
        throw new TypeError(`${method} response tools[${index}].${key} must be a string`);
      }
    }
  });
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateWindowBootstrapResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WINDOW_BOOTSTRAP_RESPONSE_KEYS, 'body');
  // snapshot 为一次性消费：桌面宿主首次加载后即被取走，此后页面加载（浏览器直开、
  // 刷新、HMR）都会得到 null。null 交给 normalizeBootstrapSnapshot 回退为空快照，
  // 不能在这里 fail，否则 bootstrap 永久失败、全部控件被禁用。
  if (value.snapshot === null) return value;
  if (!value.snapshot || typeof value.snapshot !== 'object' || Array.isArray(value.snapshot)) {
    throw new TypeError(`${method} response snapshot must be an object`);
  }
  return value;
}

export { requireResponseKey, validateBuiltinToolsResponse, validateLspPromptHintResponse, validateRuntimeConfigResponse, validateUIStateResponse, validateWindowBootstrapResponse };
