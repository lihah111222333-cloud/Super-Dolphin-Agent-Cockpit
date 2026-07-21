// @ts-check

import {
  assertBackendResponseObject,
  hasOwn,
  normalizeString,
} from '../shared.js';
import { requireResponseKey } from './config.js';

/** @param {string} method @param {unknown} response */
function validateSkillReadResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const skill = value.skill;
  if (!skill || typeof skill !== 'object' || Array.isArray(skill)) {
    throw new TypeError(`${method} response skill must be an object`);
  }
  const skillValue = /** @type {Record<string, unknown>} */ (skill);
  requireResponseKey(method, skillValue, ['path']);
  if (!hasOwn(skillValue, 'content') || typeof skillValue.content !== 'string') {
    throw new TypeError(`${method} response skill.content must be a string`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateAppUpdateInstallResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (value.started !== true) {
    throw new TypeError(`${method} response started must be true`);
  }
  if (typeof value.helper !== 'string' || !normalizeString(value.helper)) {
    throw new TypeError(`${method} response helper must be a non-empty string`);
  }
  return value;
}

export { validateAppUpdateInstallResponse, validateSkillReadResponse };
