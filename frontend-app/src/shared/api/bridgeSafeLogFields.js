import {
  isSafeLogForbiddenKey,
  normalizeSafeLogFieldKey,
  safeLogFields,
} from '../diagnostics/safeLogFields.js';

const BRIDGE_LOG_FORBIDDEN_KEYS = new Set([
  'prompt',
  'user_prompt',
  'user_message',
  'message_text',
  'text',
  'content',
  'file_content',
  'file_contents',
  'tool_result',
  'tool_results',
  'stack',
  'raw_stack',
  'params',
  'raw_params',
  'request_params',
  'secret',
  'token',
  'password',
  'api_key',
  'auth',
  'credential',
  'credentials',
  'authorization',
  'auth_token',
  'access_token',
  'refresh_token',
  'id_token',
  'stack_trace',
  'stacktrace',
]);

export const BRIDGE_REDACTED_VALUE = '[redacted]';

/** @param {unknown} key */
export function normalizeBridgeLogFieldKey(key) {
  return normalizeSafeLogFieldKey(key);
}

/** @param {unknown} key */
export function isForbiddenBridgeLogKey(key) {
  return isSafeLogForbiddenKey(key, { forbiddenKeys: BRIDGE_LOG_FORBIDDEN_KEYS });
}

/** @param {unknown} fields */
export function safeBridgeLogFields(fields) {
  return safeLogFields(fields, {
    forbiddenKeys: BRIDGE_LOG_FORBIDDEN_KEYS,
    forbiddenKeyMode: 'omit',
    redactedValue: BRIDGE_REDACTED_VALUE,
  });
}
