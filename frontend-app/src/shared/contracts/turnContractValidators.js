import { generatedSchemas } from './turnContracts.generated.js';

/**
 * @typedef {Record<string, unknown> & {
 *   $ref?: string,
 *   const?: unknown,
 *   enum?: unknown[],
 *   type?: string,
 *   allOf?: JSONSchema[],
 *   anyOf?: JSONSchema[],
 *   not?: JSONSchema,
 *   properties?: Record<string, JSONSchema>,
 *   required?: string[],
 *   additionalProperties?: boolean,
 *   items?: JSONSchema,
 *   uniqueItems?: boolean,
 *   maxItems?: number,
 *   minLength?: number,
 *   maxLength?: number,
 *   pattern?: string,
 *   if?: JSONSchema,
 *   then?: JSONSchema,
 *   else?: JSONSchema,
 * }} JSONSchema
 */

/** @param {unknown} value */
export function validateTurnRefV1(value) {
  return validateNamedSchema('TurnRefV1', value);
}

/** @param {unknown} value */
export function validatePublicErrorV1(value) {
  return validateNamedSchema('PublicErrorV1', value);
}

/** @param {unknown} value */
export function validateTurnTerminalV2(value) {
  if (isRecord(value) && value.publicError !== undefined) validatePublicErrorV1(value.publicError);
  return validateNamedSchema('TurnTerminalV2', value);
}

/** @param {string} name @param {unknown} value */
function validateNamedSchema(name, value) {
  const schema = /** @type {JSONSchema | undefined} */ (generatedSchemas[name]);
  if (!schema) throw new TypeError(`unknown generated turn schema: ${name}`);
  validateSchema(schema, value, '$', 0);
  return value;
}

/** @param {JSONSchema} schema @param {unknown} value @param {string} path @param {number} depth */
function validateSchema(schema, value, path, depth) {
  if (depth > 32) throw new TypeError(`${path} exceeds schema recursion limit`);
  if (schema.$ref) {
    const referenced = /** @type {JSONSchema | undefined} */ (generatedSchemas[schema.$ref]);
    if (!referenced) throw new TypeError(`${path} references unknown schema ${schema.$ref}`);
    validateSchema(referenced, value, path, depth + 1);
    return;
  }
  if (Object.hasOwn(schema, 'const') && !sameJSONValue(value, schema.const)) {
    throw new TypeError(`${path} must equal ${String(schema.const)}`);
  }
  if (schema.enum && !schema.enum.some((candidate) => sameJSONValue(value, candidate))) {
    throw new TypeError(`${path} has unsupported value ${String(value)}`);
  }
  if (schema.type && !matchesType(schema.type, value)) {
    throw new TypeError(`${path} must be a ${schema.type}`);
  }
  if (schema.allOf) schema.allOf.forEach((part) => validateConditional(part, value, path, depth + 1));
  if (schema.anyOf && !schema.anyOf.some((part) => matchesSchema(part, value, path, depth + 1))) {
    throw new TypeError(`${path} does not match any permitted contract shape`);
  }
  if (schema.not && matchesSchema(schema.not, value, path, depth + 1)) {
    throw new TypeError(`${path} matches a forbidden contract shape`);
  }
  if (isRecord(value)) validateObject(schema, value, path, depth + 1);
  if (Array.isArray(value)) validateArray(schema, value, path, depth + 1);
  const stringLength = typeof value === 'string' ? [...value].length : 0;
  if (typeof value === 'string' && schema.minLength && stringLength < schema.minLength) {
    throw new TypeError(`${path} must not be empty`);
  }
  if (typeof value === 'string' && schema.maxLength && stringLength > schema.maxLength) {
    throw new TypeError(`${path} exceeds maximum length`);
  }
  if (typeof value === 'string' && schema.pattern && !(new RegExp(schema.pattern)).test(value)) {
    throw new TypeError(`${path} does not match required pattern`);
  }
}

/** @param {JSONSchema} schema @param {unknown} value @param {string} path @param {number} depth */
function validateConditional(schema, value, path, depth) {
  if (!schema.if) {
    validateSchema(schema, value, path, depth);
    return;
  }
  if (matchesSchema(schema.if, value, path, depth)) {
    if (schema.then) validateSchema(schema.then, value, path, depth);
  } else if (schema.else) {
    validateSchema(schema.else, value, path, depth);
  }
}

/** @param {JSONSchema} schema @param {Record<string, unknown>} value @param {string} path @param {number} depth */
function validateObject(schema, value, path, depth) {
  const properties = objectProperties(schema, path);
  for (const field of requiredFields(schema, path)) {
    if (!Object.hasOwn(value, field)) throw new TypeError(`${path}.${field} is required`);
  }
  if (schema.additionalProperties === false) {
    for (const field of Object.keys(value)) {
      if (!Object.hasOwn(properties, field)) throw new TypeError(`${path}.${field} is unknown`);
    }
  }
  for (const [field, fieldValue] of Object.entries(value)) {
    if (properties[field]) validateSchema(properties[field], fieldValue, `${path}.${field}`, depth);
  }
}

/** @param {JSONSchema} schema @param {string} path */
function objectProperties(schema, path) {
  if (!Object.hasOwn(schema, 'properties')) return {};
  if (!isRecord(schema.properties)) throw new TypeError(`${path} has invalid schema properties`);
  return /** @type {Record<string, JSONSchema>} */ (schema.properties);
}

/** @param {JSONSchema} schema @param {string} path */
function requiredFields(schema, path) {
  if (!Object.hasOwn(schema, 'required')) return [];
  if (!Array.isArray(schema.required) || schema.required.some((field) => typeof field !== 'string')) {
    throw new TypeError(`${path} has invalid schema required fields`);
  }
  return schema.required;
}

/** @param {JSONSchema} schema @param {unknown[]} value @param {string} path @param {number} depth */
function validateArray(schema, value, path, depth) {
  if (schema.maxItems !== undefined && value.length > schema.maxItems) {
    throw new TypeError(`${path} exceeds maximum item count ${schema.maxItems}`);
  }
  if (schema.uniqueItems && new Set(value.map((item) => JSON.stringify(item))).size !== value.length) {
    throw new TypeError(`${path} contains duplicate item`);
  }
  const items = schema.items;
  if (items) value.forEach((item, index) => validateSchema(items, item, `${path}[${index}]`, depth));
}

/** @param {JSONSchema} schema @param {unknown} value @param {string} path @param {number} depth */
function matchesSchema(schema, value, path, depth) {
  try {
    validateSchema(schema, value, path, depth);
    return true;
  } catch {
    return false;
  }
}

/** @param {string} type @param {unknown} value */
function matchesType(type, value) {
  if (type === 'object') return isRecord(value);
  if (type === 'array') return Array.isArray(value);
  return typeof value === type;
}

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** @param {unknown} left @param {unknown} right */
function sameJSONValue(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}
