// @ts-check

import {
  assertBackendResponseObject,
} from '../shared.js';
import { requireResponseKey } from './config.js';

/** @param {string} method @param {unknown} response */
function validateDashboardDagStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateDashboardDagCreateAndStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['dagKey', 'dag_key']);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

export { validateDashboardDagCreateAndStartResponse, validateDashboardDagStartResponse };
