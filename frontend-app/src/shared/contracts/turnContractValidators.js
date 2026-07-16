import { generatedSchemas } from './turnContracts.generated.js';

export function validateTurnRefV1(value) {
  return validateNamedSchema('TurnRefV1', value);
}

export function validatePublicErrorV1(value) {
  return validateNamedSchema('PublicErrorV1', value);
}

export function validateTurnTerminalV2(value) {
  if (isRecord(value) && value.publicError !== undefined) validatePublicErrorV1(value.publicError);
  return validateNamedSchema('TurnTerminalV2', value);
}

function validateNamedSchema(name, value) {
  const schema = generatedSchemas[name];
  if (!schema) throw new TypeError(`unknown generated turn schema: ${name}`);
  validateSchema(schema, value, '$', 0);
  return value;
}

function validateSchema(schema, value, path, depth) {
  if (depth > 32) throw new TypeError(`${path} exceeds schema recursion limit`);
  if (schema.$ref) {
    const referenced = generatedSchemas[schema.$ref];
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
  if (typeof value === 'string' && schema.minLength && value.length < schema.minLength) {
    throw new TypeError(`${path} must not be empty`);
  }
}

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

function objectProperties(schema, path) {
  if (!Object.hasOwn(schema, 'properties')) return {};
  if (!isRecord(schema.properties)) throw new TypeError(`${path} has invalid schema properties`);
  return schema.properties;
}

function requiredFields(schema, path) {
  if (!Object.hasOwn(schema, 'required')) return [];
  if (!Array.isArray(schema.required) || schema.required.some((field) => typeof field !== 'string')) {
    throw new TypeError(`${path} has invalid schema required fields`);
  }
  return schema.required;
}

function validateArray(schema, value, path, depth) {
  if (schema.uniqueItems && new Set(value.map((item) => JSON.stringify(item))).size !== value.length) {
    throw new TypeError(`${path} contains duplicate item`);
  }
  if (schema.items) value.forEach((item, index) => validateSchema(schema.items, item, `${path}[${index}]`, depth));
}

function matchesSchema(schema, value, path, depth) {
  try {
    validateSchema(schema, value, path, depth);
    return true;
  } catch {
    return false;
  }
}

function matchesType(type, value) {
  if (type === 'object') return isRecord(value);
  if (type === 'array') return Array.isArray(value);
  return typeof value === type;
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function sameJSONValue(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}
