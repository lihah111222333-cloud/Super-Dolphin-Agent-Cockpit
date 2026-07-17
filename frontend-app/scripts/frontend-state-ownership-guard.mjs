import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { parse } from '@babel/parser';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const registryPath = 'scripts/frontend-state-ownership-registry.json';
const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
const testFilePattern = /\.(?:test|spec)\.[cm]?[jt]sx?$/;
const sharedParseCache = new Map();

export function validateFrontendStateOwnership({
  root = appRoot,
  registry = readJSON(path.join(root, registryPath)),
  sourceOverrides = new Map(),
} = {}) {
  validateRegistryShape(registry);
  const actualCaseIds = discoverCaseIds(readSource(root, registry.testFile, sourceOverrides), registry.testFile, 'A02-');
  assertExactSet('A02 regression case IDs', registry.caseIds, actualCaseIds);

  const definitions = Object.entries(registry.states);
  for (const [stateId, definition] of definitions) validateStateDefinition(stateId, definition);

  const parseCache = sharedParseCache;
  const sourcesByRoot = new Map();
  const sourcesForRoot = (sourceRoot) => {
    if (!sourcesByRoot.has(sourceRoot)) {
      const allSources = sourcesByRoot.get('src');
      if (sourceRoot !== 'src' && allSources && sourceRoot.startsWith('src/')) {
        sourcesByRoot.set(
          sourceRoot,
          new Map([...allSources].filter(([relativePath]) => relativePath.startsWith(`${sourceRoot}/`))),
        );
      } else {
        sourcesByRoot.set(sourceRoot, productionSources(root, sourceRoot, sourceOverrides));
      }
    }
    return sourcesByRoot.get(sourceRoot);
  };
  for (const consumerRoot of new Set(definitions.map(([, definition]) => definition.consumerRoot))) {
    sourcesForRoot(consumerRoot);
  }
  const writerRecordsByRoot = discoverRecordsByRoot({
    definitions,
    rootField: 'sourceRoot',
    sourcesForRoot,
    discover: discoverStateWriterRecordsFromSources,
    parseCache,
  });
  const consumerRecordsByRoot = discoverRecordsByRoot({
    definitions,
    rootField: 'consumerRoot',
    sourcesForRoot,
    discover: discoverStateConsumersFromSources,
    parseCache,
  });

  const summaries = {};
  for (const [stateId, definition] of definitions) {
    const discoveredWriters = recordsForProperties(
      writerRecordsByRoot.get(definition.sourceRoot),
      definition.properties,
    );
    assertExactRecords(`${stateId} writers`, definition.writers, discoveredWriters, ['key', 'value']);
    const owners = definition.writers.filter(({ role }) => role === 'owner');
    if (owners.length !== 1 || owners[0].key !== definition.ownerWriter) {
      throw new Error(`${stateId} must have exactly one owner writer matching ownerWriter`);
    }
    const discoveredConsumers = recordsForProperties(
      consumerRecordsByRoot.get(definition.consumerRoot),
      definition.properties,
    );
    assertExactRecords(`${stateId} consumers`, definition.consumers, discoveredConsumers, ['key']);
    summaries[stateId] = {
      consumerCount: discoveredConsumers.length,
      writerCount: discoveredWriters.length,
      ownerWriter: definition.ownerWriter,
    };
  }
  return summaries;
}

function discoverRecordsByRoot({
  definitions,
  rootField,
  sourcesForRoot,
  discover,
  parseCache,
}) {
  const propertiesByRoot = new Map();
  for (const [, definition] of definitions) {
    const root = definition[rootField];
    const properties = propertiesByRoot.get(root) || new Set();
    definition.properties.forEach((property) => properties.add(property));
    propertiesByRoot.set(root, properties);
  }
  const recordsByRoot = new Map();
  for (const [sourceRoot, properties] of propertiesByRoot) {
    recordsByRoot.set(
      sourceRoot,
      discover(sourcesForRoot(sourceRoot), [...properties], parseCache),
    );
  }
  return recordsByRoot;
}

function recordsForProperties(records = [], properties) {
  return records.filter(({ key }) => properties.some((property) => key.endsWith(`:${property}`)));
}

export function discoverStateWritersFromSources(sources, properties) {
  return discoverStateWriterRecordsFromSources(sources, properties).map(({ key }) => key);
}

export function discoverStateWriterRecordsFromSources(sources, properties, parseCache = new Map()) {
  const guardedProperties = new Set(properties);
  const writers = [];
  for (const [relativePath, source] of [...sources].sort(([left], [right]) => left.localeCompare(right))) {
    const ast = parseModuleCached(source, relativePath, parseCache);
    const stringBindings = staticStringBindings(ast);
    walkNode(ast, [], (node, ancestors) => {
      const property = writtenProperty(node, stringBindings);
      if (!property || !guardedProperties.has(property.name)) return;
      const symbol = enclosingSymbol(ancestors);
      writers.push({
        key: `${relativePath}:${symbol}:${property.operation}:${property.name}`,
        value: property.value,
      });
    });
  }
  assertUniqueRecords('discovered state writer', writers);
  return writers.sort((left, right) => left.key.localeCompare(right.key));
}

export function discoverStateConsumersFromSources(sources, properties, parseCache = new Map()) {
  const guardedProperties = new Set(properties);
  const consumers = new Map();
  for (const [relativePath, source] of [...sources].sort(([left], [right]) => left.localeCompare(right))) {
    const ast = parseModuleCached(source, relativePath, parseCache);
    const stringBindings = staticStringBindings(ast);
    walkNode(ast, [], (node, ancestors) => {
      const read = readProperty(node, ancestors, stringBindings);
      if (!read || !guardedProperties.has(read.name)) return;
      const symbol = enclosingSymbol(ancestors);
      const key = `${relativePath}:${symbol}:${read.operation}:${read.name}`;
      consumers.set(key, { key });
    });
  }
  return [...consumers.values()].sort((left, right) => left.key.localeCompare(right.key));
}

function writtenProperty(node, stringBindings) {
  if (node.type === 'ObjectProperty') {
    const name = staticPropertyName(node.key, node.computed, stringBindings);
    return name ? {
      name,
      operation: 'object-property',
      value: expressionShape(node.value, stringBindings),
    } : null;
  }
  if ((node.type === 'AssignmentExpression' || node.type === 'UpdateExpression')
      && isMemberExpression(node.type === 'AssignmentExpression' ? node.left : node.argument)) {
    const member = node.type === 'AssignmentExpression' ? node.left : node.argument;
    const name = memberPropertyName(member, stringBindings);
    const value = node.type === 'AssignmentExpression'
      ? expressionShape(node.right, stringBindings)
      : `${node.operator}update`;
    return name ? { name, operation: 'member-mutation', value } : null;
  }
  if (node.type === 'UnaryExpression' && node.operator === 'delete' && isMemberExpression(node.argument)) {
    const name = memberPropertyName(node.argument, stringBindings);
    return name ? { name, operation: 'member-mutation', value: 'delete' } : null;
  }
  if (node.type !== 'CallExpression') return null;
  const callee = dottedName(node.callee, stringBindings);
  if (callee === 'Object.defineProperty' || callee === 'Reflect.defineProperty') {
    const name = staticStringValue(node.arguments[1], stringBindings);
    const descriptor = node.arguments[2];
    const valueNode = descriptor?.type === 'ObjectExpression'
      ? descriptor.properties.find((property) => (
        property.type === 'ObjectProperty'
        && staticPropertyName(property.key, property.computed, stringBindings) === 'value'
      ))?.value
      : null;
    return name ? {
      name,
      operation: 'define-property',
      value: valueNode ? expressionShape(valueNode, stringBindings) : 'descriptor',
    } : null;
  }
  if (callee === 'Reflect.set') {
    const name = staticStringValue(node.arguments[1], stringBindings);
    return name ? {
      name,
      operation: 'reflect-set',
      value: expressionShape(node.arguments[2], stringBindings),
    } : null;
  }
  return null;
}

function readProperty(node, ancestors, stringBindings) {
  if (isMemberExpression(node)) {
    if (isWriteTarget(node, ancestors)) return null;
    const name = memberPropertyName(node, stringBindings);
    return name ? { name, operation: 'member-read' } : null;
  }
  if (node.type !== 'ObjectProperty' || ancestors.at(-1)?.type !== 'ObjectPattern') return null;
  const name = staticPropertyName(node.key, node.computed, stringBindings);
  return name ? { name, operation: 'destructure-read' } : null;
}

function isWriteTarget(node, ancestors) {
  const parent = ancestors.at(-1);
  if (!parent) return false;
  if (parent.type === 'AssignmentExpression' && parent.left === node) return true;
  if (parent.type === 'UpdateExpression' && parent.argument === node) return true;
  return parent.type === 'UnaryExpression' && parent.operator === 'delete' && parent.argument === node;
}

function enclosingSymbol(ancestors) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const node = ancestors[index];
    if (node.type === 'FunctionDeclaration' && node.id?.name) return node.id.name;
    if (node.type === 'FunctionExpression' && node.id?.name) return node.id.name;
    if (node.type === 'ObjectMethod' || node.type === 'ClassMethod') {
      return propertyName(node.key) || '<anonymous>';
    }
    if (node.type !== 'FunctionExpression' && node.type !== 'ArrowFunctionExpression') continue;
    const parent = ancestors[index - 1];
    if (parent?.type === 'VariableDeclarator') return propertyName(parent.id) || '<anonymous>';
    if (parent?.type === 'ObjectProperty' || parent?.type === 'ClassProperty') {
      return propertyName(parent.key) || '<anonymous>';
    }
    if (parent?.type === 'AssignmentExpression' && isMemberExpression(parent.left)) {
      return memberPropertyName(parent.left) || '<anonymous>';
    }
  }
  return '<module>';
}

function productionSources(root, sourceRoot, overrides) {
  const normalizedRoot = normalizeRelativePath(sourceRoot);
  const files = walkSourceFiles(path.join(root, normalizedRoot))
    .map((absolutePath) => normalizeRelativePath(path.relative(root, absolutePath)));
  const candidatePaths = new Set(files);
  for (const relativePath of overrides.keys()) {
    const normalized = normalizeRelativePath(relativePath);
    if (normalized === normalizedRoot || normalized.startsWith(`${normalizedRoot}/`)) candidatePaths.add(normalized);
  }
  const sources = new Map();
  for (const relativePath of candidatePaths) {
    if (testFilePattern.test(relativePath) || relativePath.endsWith('.generated.js')) continue;
    sources.set(relativePath, readSource(root, relativePath, overrides));
  }
  if (sources.size === 0) throw new Error(`state ownership source root ${sourceRoot} has zero production files`);
  return sources;
}

function walkSourceFiles(directory) {
  if (!fs.existsSync(directory)) return [];
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolutePath = path.join(directory, entry.name);
    if (entry.isDirectory() && entry.name !== '__tests__') {
      files.push(...walkSourceFiles(absolutePath));
    } else if (sourceExtensionPattern.test(entry.name)) {
      files.push(absolutePath);
    }
  }
  return files;
}

function discoverCaseIds(source, filePath, prefix) {
  const ids = [];
  walkNode(parseModule(source, filePath), [], (node) => {
    if (node.type !== 'CallExpression' || !['it', 'test'].includes(dottedName(node.callee))) return;
    const title = stringValue(node.arguments[0]);
    if (title?.startsWith(`[${prefix}`)) ids.push(title.slice(1, title.indexOf(']')));
  });
  return [...new Set(ids)].sort();
}

function validateRegistryShape(registry) {
  if (registry?.version !== 1 || !isRecord(registry.states) || Object.keys(registry.states).length === 0) {
    throw new Error('state ownership registry must be version 1 with non-empty states');
  }
  if (!Array.isArray(registry.caseIds) || registry.caseIds.length === 0) {
    throw new Error('state ownership registry must register at least one regression case');
  }
  if (typeof registry.testFile !== 'string' || !registry.testFile.endsWith('.test.mjs')) {
    throw new Error('state ownership registry testFile must name a test module');
  }
}

function validateStateDefinition(stateId, definition) {
  if (
    !isRecord(definition)
    || typeof definition.sourceRoot !== 'string'
    || typeof definition.consumerRoot !== 'string'
  ) {
    throw new Error(`${stateId} state definition is incomplete`);
  }
  if (!Array.isArray(definition.properties) || definition.properties.length === 0) {
    throw new Error(`${stateId} must guard at least one property`);
  }
  if (!Array.isArray(definition.writers) || definition.writers.length === 0) {
    throw new Error(`${stateId} must register at least one writer`);
  }
  if (!Array.isArray(definition.consumers) || definition.consumers.length === 0) {
    throw new Error(`${stateId} must register at least one consumer`);
  }
  const keys = new Set();
  for (const writer of definition.writers) {
    if (
      !isRecord(writer)
      || typeof writer.key !== 'string'
      || typeof writer.value !== 'string'
      || !['owner', 'initializer', 'projector'].includes(writer.role)
    ) {
      throw new Error(`${stateId} writer registration is incomplete`);
    }
    if (keys.has(writer.key)) throw new Error(`${stateId} has duplicate writer ${writer.key}`);
    keys.add(writer.key);
  }
  const consumerKeys = new Set();
  for (const consumer of definition.consumers) {
    if (
      !isRecord(consumer)
      || typeof consumer.key !== 'string'
      || !['contract-validator', 'diagnostics', 'mapper-input', 'renderer', 'selector'].includes(consumer.role)
    ) {
      throw new Error(`${stateId} consumer registration is incomplete`);
    }
    if (consumerKeys.has(consumer.key)) throw new Error(`${stateId} has duplicate consumer ${consumer.key}`);
    consumerKeys.add(consumer.key);
  }
}

function parseModule(source, filePath) {
  try {
    return parse(source, { sourceType: 'module', plugins: ['jsx', 'typescript', 'dynamicImport'] });
  } catch (error) {
    throw new Error(`parse ${filePath}: ${error.message}`);
  }
}

function parseModuleCached(source, filePath, cache) {
  const cached = cache.get(filePath);
  if (cached?.source === source) return cached.ast;
  const ast = parseModule(source, filePath);
  cache.set(filePath, { source, ast });
  return ast;
}

function walkNode(node, ancestors, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node, ancestors);
  const nextAncestors = [...ancestors, node];
  for (const [key, value] of Object.entries(node)) {
    if (['loc', 'start', 'end', 'extra'].includes(key)) continue;
    if (Array.isArray(value)) value.forEach((child) => walkNode(child, nextAncestors, visitor));
    else if (value && typeof value === 'object' && typeof value.type === 'string') {
      walkNode(value, nextAncestors, visitor);
    }
  }
}

function isMemberExpression(node) {
  return node?.type === 'MemberExpression' || node?.type === 'OptionalMemberExpression';
}

function memberPropertyName(node, stringBindings = new Map()) {
  return staticPropertyName(node.property, node.computed, stringBindings);
}

function propertyName(node) {
  if (node?.type === 'Identifier') return node.name;
  return stringValue(node);
}

function dottedName(node, stringBindings = new Map()) {
  if (node?.type === 'Identifier') return node.name;
  if (!isMemberExpression(node)) return '';
  const object = dottedName(node.object, stringBindings);
  const property = memberPropertyName(node, stringBindings);
  return object && property ? `${object}.${property}` : '';
}

function stringValue(node) {
  if (node?.type === 'StringLiteral' || node?.type === 'Literal') return node.value;
  if (node?.type === 'TemplateLiteral' && node.expressions.length === 0) return node.quasis[0]?.value?.cooked;
  return '';
}

function staticStringValue(node, bindings) {
  const direct = stringValue(node);
  if (direct) return direct;
  if (node?.type === 'Identifier') return bindings.get(node) || '';
  return '';
}

function staticPropertyName(node, computed, bindings) {
  if (!computed && node?.type === 'Identifier') return node.name;
  return staticStringValue(node, bindings);
}

function staticStringBindings(ast) {
  const scopeByNode = new WeakMap();
  walkNode(ast, [], (node, ancestors) => {
    if (!isScopeNode(node)) return;
    scopeByNode.set(node, {
      bindings: new Map(),
      node,
      parent: nearestScope(ancestors, scopeByNode),
    });
  });

  walkNode(ast, [], (node, ancestors) => {
    if (node.type === 'VariableDeclarator') {
      const declaration = ancestors.at(-1);
      const scope = declaration?.kind === 'var'
        ? nearestFunctionScope(ancestors, scopeByNode)
        : nearestScope(ancestors, scopeByNode);
      const names = bindingNames(node.id);
      for (const name of names) {
        const value = names.length === 1 && node.id.type === 'Identifier'
          ? stringValue(node.init)
          : '';
        declareBinding(scope, name, { kind: declaration?.kind || 'unknown', value });
      }
      return;
    }
    if (isFunctionNode(node)) {
      const functionScope = scopeByNode.get(node);
      for (const parameter of node.params || []) {
        for (const name of bindingNames(parameter)) declareBinding(functionScope, name);
      }
      if (node.type === 'FunctionDeclaration' && node.id?.name) {
        declareBinding(nearestScope(ancestors, scopeByNode), node.id.name);
      } else if (node.id?.name) {
        declareBinding(functionScope, node.id.name);
      }
      return;
    }
    if (node.type === 'ClassDeclaration' && node.id?.name) {
      declareBinding(nearestScope(ancestors, scopeByNode), node.id.name);
      return;
    }
    if (node.type === 'CatchClause') {
      const catchScope = scopeByNode.get(node);
      for (const name of bindingNames(node.param)) declareBinding(catchScope, name);
      return;
    }
    if (node.type === 'ImportDeclaration') {
      const scope = nearestScope(ancestors, scopeByNode);
      for (const specifier of node.specifiers || []) {
        if (specifier.local?.name) declareBinding(scope, specifier.local.name);
      }
    }
  });

  walkNode(ast, [], (node, ancestors) => {
    let target = null;
    if (node.type === 'AssignmentExpression') target = node.left;
    else if (node.type === 'UpdateExpression') target = node.argument;
    else if (node.type === 'ForInStatement' || node.type === 'ForOfStatement') target = node.left;
    if (!target || target.type === 'VariableDeclaration') return;
    const scope = nearestScope(ancestors, scopeByNode);
    for (const name of bindingNames(target)) {
      const binding = resolveBinding(scope, name);
      if (binding) binding.mutated = true;
    }
  });

  const valuesByIdentifier = new WeakMap();
  walkNode(ast, [], (node, ancestors) => {
    if (node.type !== 'Identifier') return;
    const binding = resolveBinding(nearestScope(ancestors, scopeByNode), node.name);
    if (binding?.value && !binding.invalid && !binding.mutated) {
      valuesByIdentifier.set(node, binding.value);
    }
  });
  return valuesByIdentifier;
}

function isFunctionNode(node) {
  return node?.type === 'FunctionDeclaration'
    || node?.type === 'FunctionExpression'
    || node?.type === 'ArrowFunctionExpression'
    || node?.type === 'ObjectMethod'
    || node?.type === 'ClassMethod'
    || node?.type === 'ClassPrivateMethod';
}

function isScopeNode(node) {
  return node?.type === 'Program'
    || node?.type === 'BlockStatement'
    || node?.type === 'CatchClause'
    || node?.type === 'ForStatement'
    || node?.type === 'ForInStatement'
    || node?.type === 'ForOfStatement'
    || node?.type === 'SwitchStatement'
    || node?.type === 'StaticBlock'
    || isFunctionNode(node);
}

function nearestScope(ancestors, scopeByNode) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const scope = scopeByNode.get(ancestors[index]);
    if (scope) return scope;
  }
  return null;
}

function nearestFunctionScope(ancestors, scopeByNode) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const node = ancestors[index];
    if (node.type !== 'Program' && !isFunctionNode(node)) continue;
    const scope = scopeByNode.get(node);
    if (scope) return scope;
  }
  return null;
}

function bindingNames(pattern) {
  if (!pattern) return [];
  if (pattern.type === 'Identifier') return [pattern.name];
  if (pattern.type === 'RestElement') return bindingNames(pattern.argument);
  if (pattern.type === 'AssignmentPattern') return bindingNames(pattern.left);
  if (pattern.type === 'ArrayPattern') return pattern.elements.flatMap(bindingNames);
  if (pattern.type === 'ObjectPattern') {
    return pattern.properties.flatMap((property) => (
      property.type === 'RestElement' ? bindingNames(property.argument) : bindingNames(property.value)
    ));
  }
  return [];
}

function declareBinding(scope, name, { kind = 'unknown', value = '' } = {}) {
  if (!scope || !name) return;
  const existing = scope.bindings.get(name);
  if (existing) {
    existing.invalid = true;
    return;
  }
  scope.bindings.set(name, { invalid: false, kind, mutated: false, value });
}

function resolveBinding(scope, name) {
  for (let current = scope; current; current = current.parent) {
    const binding = current.bindings.get(name);
    if (binding) return binding;
  }
  return null;
}

function expressionShape(node, stringBindings) {
  if (!node) return 'missing';
  if (node.type === 'Identifier') return node.name;
  if (isMemberExpression(node)) return dottedName(node, stringBindings) || 'computed-member';
  if (node.type === 'CallExpression') {
    const callee = dottedName(node.callee, stringBindings) || node.callee?.type || 'call';
    return `${callee}(${node.arguments.map((argument) => expressionShape(argument, stringBindings)).join(',')})`;
  }
  if (node.type === 'ArrayExpression') return 'array';
  if (node.type === 'ObjectExpression') return 'object';
  if (node.type === 'StringLiteral' || node.type === 'NumericLiteral' || node.type === 'BooleanLiteral') {
    return JSON.stringify(node.value);
  }
  if (node.type === 'NullLiteral') return 'null';
  if (node.type === 'UnaryExpression') return `${node.operator}${expressionShape(node.argument, stringBindings)}`;
  return node.type;
}

function readSource(root, relativePath, overrides) {
  const normalized = normalizeRelativePath(relativePath);
  if (overrides.has(normalized)) return overrides.get(normalized);
  return fs.readFileSync(path.join(root, normalized), 'utf8');
}

function readJSON(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    throw new Error(`read ${path.basename(filePath)}: ${error.message}`);
  }
}

function normalizeRelativePath(value) {
  const normalized = path.posix.normalize(String(value).split(path.sep).join('/'));
  if (!normalized || path.posix.isAbsolute(normalized) || normalized.startsWith('../')) {
    throw new Error(`path must be app-relative: ${value}`);
  }
  return normalized;
}

function assertExactSet(label, expectedValues, actualValues) {
  const expected = [...new Set(expectedValues)].sort();
  const actual = [...new Set(actualValues)].sort();
  const missing = actual.filter((value) => !expected.includes(value));
  const stale = expected.filter((value) => !actual.includes(value));
  if (missing.length > 0 || stale.length > 0) {
    throw new Error(`${label} registry drift: missing=${JSON.stringify(missing)} stale=${JSON.stringify(stale)}`);
  }
}

function assertExactRecords(label, expectedRecords, actualRecords, fields) {
  const serialize = (record) => fields.map((field) => `${field}=${record[field]}`).join('|');
  assertExactSet(label, expectedRecords.map(serialize), actualRecords.map(serialize));
}

function assertUniqueRecords(label, records) {
  const seen = new Set();
  for (const record of records) {
    if (seen.has(record.key)) throw new Error(`${label} is duplicated: ${record.key}`);
    seen.add(record.key);
  }
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const summaries = validateFrontendStateOwnership();
  console.log(`frontend state ownership guard passed (${Object.keys(summaries).length} states)`);
}
