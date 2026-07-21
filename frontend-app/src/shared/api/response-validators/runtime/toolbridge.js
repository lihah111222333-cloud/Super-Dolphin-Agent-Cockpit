// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  normalizeString,
  validateStringFields,
} from '../shared.js';

const TOOLBRIDGE_TOOLS_RESPONSE_KEYS = new Set(['tools']);
const TOOLBRIDGE_TOOL_RESPONSE_KEYS = new Set(['serverName', 'toolName', 'displayName', 'description', 'enabled', 'disabledReason']);

/** @param {string} method @param {unknown} response */
function validateToolbridgeToolsListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, TOOLBRIDGE_TOOLS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.tools)) throw new TypeError(`${method} response tools must be an array`);
  /** @type {unknown[]} */ (value.tools).forEach((candidate, index) => {
    const label = `tools[${index}]`;
    const tool = assertResponseRecord(method, candidate, label);
    assertOnlyResponseKeys(method, tool, TOOLBRIDGE_TOOL_RESPONSE_KEYS, label);
    for (const key of ['serverName', 'toolName', 'displayName']) {
      if (!normalizeString(tool[key])) {
        throw new TypeError(`${method} response ${label}.${key} must be a non-empty string`);
      }
    }
    validateStringFields(method, tool, label, [], ['description', 'disabledReason']);
    if (typeof tool.enabled !== 'boolean') {
      throw new TypeError(`${method} response ${label}.enabled must be a boolean`);
    }
  });
  return value;
}

/** @param {string} method @param {unknown} response */

export { validateToolbridgeToolsListResponse };
