import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { generatedSchemas } from '../src/shared/contracts/turnContracts.generated.js';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(appRoot, '..');
const schemaDir = path.join(repoRoot, 'internal/dto/turn/schema');
const registryPath = path.join(schemaDir, 'field_consumers.json');
const validatorPath = path.join(appRoot, 'src/shared/contracts/turnContractValidators.js');

function canonicalSchemas() {
  const schemas = new Map();
  for (const entry of fs.readdirSync(schemaDir, { withFileTypes: true })) {
    if (entry.isDirectory() || !entry.name.endsWith('.json') || entry.name === 'field_consumers.json') continue;
    const sourcePath = path.join(schemaDir, entry.name);
    let schema;
    try {
      schema = JSON.parse(fs.readFileSync(sourcePath, 'utf8'));
    } catch (error) {
      throw new Error(`parse canonical schema ${entry.name}: ${error.message}`);
    }
    if (!schema.title || !schema.properties || typeof schema.properties !== 'object') {
      throw new Error(`canonical schema ${entry.name} must have title and properties`);
    }
    if (schemas.has(schema.title)) throw new Error(`duplicate canonical schema ${schema.title}`);
    schemas.set(schema.title, schema);
  }
  if (schemas.size !== 3) throw new Error(`expected exactly three canonical turn schemas, found ${schemas.size}`);
  return schemas;
}

function assertExactFields(schemaName, schema, entry) {
  if (!entry || typeof entry !== 'object') throw new Error(`consumer registry missing schema ${schemaName}`);
  if (!entry.goValidator || !entry.jsValidator || !entry.fields || typeof entry.fields !== 'object') {
    throw new Error(`consumer registry ${schemaName} is incomplete`);
  }
  const producerFields = new Set(Object.keys(schema.properties));
  const registeredFields = new Set(Object.keys(entry.fields));
  const missing = [...producerFields].filter((field) => !registeredFields.has(field)).sort();
  const stale = [...registeredFields].filter((field) => !producerFields.has(field)).sort();
  if (missing.length || stale.length) {
    throw new Error(`${schemaName} field coverage missing=${missing.join(',')} stale=${stale.join(',')}`);
  }
  for (const [field, coverage] of Object.entries(entry.fields)) {
    if (!coverage?.go || !coverage?.js) throw new Error(`${schemaName}.${field} has blank consumer coverage`);
  }
}

function main() {
  const schemas = canonicalSchemas();
  let registry;
  try {
    registry = JSON.parse(fs.readFileSync(registryPath, 'utf8'));
  } catch (error) {
    throw new Error(`parse consumer registry: ${error.message}`);
  }
  const validatorSource = fs.readFileSync(validatorPath, 'utf8');
  for (const [schemaName, schema] of schemas) {
    const entry = registry[schemaName];
    assertExactFields(schemaName, schema, entry);
    if (!new RegExp(`export function ${entry.jsValidator}\\(`).test(validatorSource)) {
      throw new Error(`${schemaName} references missing JS validator ${entry.jsValidator}`);
    }
    if (stableJSON(schema) !== stableJSON(generatedSchemas[schemaName])) {
      throw new Error(`generated JS schema drift: ${schemaName}`);
    }
  }
  for (const schemaName of Object.keys(registry)) {
    if (!schemas.has(schemaName)) throw new Error(`consumer registry has stale schema ${schemaName}`);
  }
  console.log(`turn contract field guard passed: ${schemas.size} schemas`);
}

function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

try {
  main();
} catch (error) {
  console.error(`turn contract field guard failed: ${error.message}`);
  process.exitCode = 1;
}
