// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  hasOwn,
} from '../shared.js';
import { validateSidebarStateResponse as validateCoreSidebarStateResponse } from '../core/thread-state.js';
import {
  SIDEBAR_STATE_RESPONSE_KEYS,
  validateStateMaps,
} from './ui.js';

const SIDEBAR_REQUIRED_RESPONSE_KEYS = ['threads', 'agents', 'recent_turns', 'workspace', 'token_usage'];

/** @param {string} method @param {unknown} response */
export function validateSidebarStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, SIDEBAR_STATE_RESPONSE_KEYS, 'body');
  const { activityStatsByThread, ...coreValue } = value;
  for (const requiredField of SIDEBAR_REQUIRED_RESPONSE_KEYS) {
    if (!hasOwn(value, requiredField)) {
      throw new TypeError(`${method} response ${requiredField} is required`);
    }
  }
  validateCoreSidebarStateResponse(method, coreValue);
  validateStateMaps(method, value);
  return value;
}
