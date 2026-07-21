// @ts-check

const objectPrototype = Object.prototype;

/** @param {unknown} value */
function normalizeString(value) {
  return typeof value === "string" ? value.trim() : "";
}

/**
 * @param {unknown} value
 * @param {string} key
 */
function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}

/**
 * @param {string} method
 * @param {unknown} response
 * @returns {Record<string, unknown>}
 */
function assertBackendResponseObject(method, response) {
  if (!response || typeof response !== "object" || Array.isArray(response)) {
    throw new TypeError(`${method} response must be an object`);
  }
  return /** @type {Record<string, unknown>} */ (response);
}

/**
 * @param {string} method
 * @param {unknown} response
 * @param {string} label
 */
function assertResponseRecord(method, response, label) {
  if (!response || typeof response !== "object" || Array.isArray(response)) {
    throw new TypeError(`${method} response ${label} must be an object`);
  }
  return /** @type {Record<string, unknown>} */ (response);
}

/**
 * @param {string} method
 * @param {Record<string, unknown>} value
 * @param {string} label
 * @param {readonly string[]} required
 * @param {readonly string[]} optional
 */
function validateStringFields(method, value, label, required, optional) {
  for (const key of required) {
    if (typeof value[key] !== "string") {
      throw new TypeError(`${method} response ${label}.${key} must be a string`);
    }
  }
  for (const key of optional) {
    if (hasOwn(value, key) && typeof value[key] !== "string") {
      throw new TypeError(`${method} response ${label}.${key} must be a string`);
    }
  }
}

/**
 * @param {string} method
 * @param {Record<string, unknown>} value
 * @param {ReadonlySet<string>} allowedKeys
 * @param {string} label
 */
function assertOnlyResponseKeys(method, value, allowedKeys, label) {
  for (const key of Object.keys(value)) {
    if (!allowedKeys.has(key)) {
      throw new TypeError(`${method} response ${label} must not include ${key}`);
    }
  }
}

/**
 * @param {string} method
 * @param {unknown} value
 * @param {string} label
 */
function assertResponseArray(method, value, label) {
  if (!Array.isArray(value)) {
    throw new TypeError(`${method} response ${label} must be an array`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {Record<string, unknown>} value
 * @param {string} label
 * @param {{ stringKeys: string[], integerKeys: string[], booleanKeys?: string[] }} fields
 */
function validateRequiredFields(method, value, label, fields) {
  const { stringKeys, integerKeys, booleanKeys = [] } = fields;
  validateStringFields(method, value, label, stringKeys, []);
  for (const key of integerKeys) {
    if (!Number.isInteger(value[key])) {
      throw new TypeError(`${method} response ${label}.${key} must be an integer`);
    }
  }
  for (const key of booleanKeys) {
    if (typeof value[key] !== "boolean") {
      throw new TypeError(`${method} response ${label}.${key} must be a boolean`);
    }
  }
}

export {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseArray,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateRequiredFields,
  validateStringFields,
};
