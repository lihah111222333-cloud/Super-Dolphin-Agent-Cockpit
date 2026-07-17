import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { parse } from '@babel/parser';

import { generatedSchemas } from '../src/shared/contracts/turnContracts.generated.js';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const defaultRepoRoot = path.resolve(appRoot, '..');
const registryRelativePath = 'internal/dto/turn/schema/field_consumers.json';
const validatorRelativePath = 'frontend-app/src/shared/contracts/turnContractValidators.js';

export function validateTurnContractFieldGuard({ repoRoot = defaultRepoRoot, sourceOverrides = new Map() } = {}) {
  const schemas = canonicalSchemas(repoRoot, sourceOverrides);
  const registry = loadRegistry(repoRoot, sourceOverrides);
  if (registry.version !== 2 || !isRecord(registry.schemas)) {
    throw new Error('consumer registry must be version 2 with a schemas object');
  }
  const registeredSchemas = Object.keys(registry.schemas).sort();
  const schemaNames = [...schemas.keys()].sort();
  assertExactSet('consumer registry schemas', schemaNames, registeredSchemas);
  const targetSchemas = new Map(Object.entries(registry.schemas)
    .map(([schemaName, entry]) => [entry.jsValidator.symbol, schemaName]));
  const resolveValidatorExports = createValidatorExportResolver(repoRoot, sourceOverrides, targetSchemas);
  const discoveredConsumers = discoverJSValidatorConsumers(
    repoRoot,
    sourceOverrides,
    targetSchemas,
    resolveValidatorExports,
  );
  for (const [schemaName, schema] of schemas) {
    const entry = registry.schemas[schemaName];
    validateSchemaRegistryEntry(
      repoRoot,
      sourceOverrides,
      schemaName,
      entry,
      targetSchemas,
      resolveValidatorExports,
    );
    const registeredConsumers = entry.jsConsumers.map((consumer) => consumerKey(consumer)).sort();
    assertExactSet(`${schemaName} JS production consumers`, discoveredConsumers.get(schemaName) ?? [], registeredConsumers);
    if (stableJSON(schema) !== stableJSON(generatedSchemas[schemaName])) {
      throw new Error(`generated JS schema drift: ${schemaName}`);
    }
  }
  if (!Array.isArray(registry.jsMappers) || registry.jsMappers.length === 0) {
    throw new Error('consumer registry must contain JS mapper chains');
  }
  const mapperNames = new Set();
  for (const mapper of registry.jsMappers) {
    if (!mapper?.name || mapperNames.has(mapper.name)) throw new Error(`JS mapper has blank or duplicate name ${mapper?.name ?? ''}`);
    mapperNames.add(mapper.name);
    const source = readRepositorySource(repoRoot, mapper.path, sourceOverrides);
    validateMapperSource(source, mapper);
  }
  return { schemaCount: schemas.size, mapperCount: registry.jsMappers.length };
}

function discoverJSValidatorConsumers(repoRoot, sourceOverrides, targetSchemas, resolveValidatorExports) {
  const discovered = new Map([...targetSchemas.values()].map((schemaName) => [schemaName, []]));
  const sourceRoot = path.join(repoRoot, 'frontend-app/src');
  for (const absolutePath of productionJavaScriptFiles(sourceRoot)) {
    const relativePath = path.relative(repoRoot, absolutePath).split(path.sep).join('/');
    const ast = parseModule(readRepositorySource(repoRoot, relativePath, sourceOverrides), relativePath);
    resolveValidatorExports(relativePath);
    const bindings = validatorBindings(
      repoRoot,
      sourceOverrides,
      ast,
      relativePath,
      targetSchemas,
      resolveValidatorExports,
    );
    if (!hasValidatorBindings(bindings)) continue;
    assertValidatorBindingsSafe(ast, bindings, relativePath);
    const claimedCalls = new Set();
    const functions = namedProductionFunctions(ast);
    assertUniqueProductionSymbols(functions, relativePath);
    for (const fn of functions) {
      walkFunctionBody(fn, (node) => {
        if (node.type !== 'CallExpression') return;
        const target = validatorBindingTarget(node.callee, bindings);
        if (!target) return;
        claimedCalls.add(node);
        discovered.get(target.schemaName).push(consumerKey({ path: relativePath, symbol: fn.symbol, calls: target.symbol }));
      });
    }
    walkNode(ast.program, (node) => {
      if (node.type !== 'CallExpression') return;
      const target = validatorBindingTarget(node.callee, bindings);
      if (target && !claimedCalls.has(node)) {
        throw new Error(`${relativePath} validator call ${target.symbol} cannot be attributed to a stable production symbol`);
      }
    });
  }
  for (const [schemaName, consumers] of discovered) discovered.set(schemaName, [...new Set(consumers)].sort());
  return discovered;
}

function productionJavaScriptFiles(root) {
  const files = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const absolutePath = path.join(root, entry.name);
    if (entry.isDirectory()) {
      files.push(...productionJavaScriptFiles(absolutePath));
      continue;
    }
    if (!/\.(?:js|jsx)$/.test(entry.name) || /\.(?:test|spec)\.(?:js|jsx)$/.test(entry.name)) continue;
    files.push(absolutePath);
  }
  return files;
}

function validatorBindings(repoRoot, sourceOverrides, ast, relativePath, targetSchemas, resolveValidatorExports) {
  const bindings = importedValidatorBindings(
    repoRoot,
    sourceOverrides,
    ast,
    relativePath,
    resolveValidatorExports,
  );
  if (relativePath === validatorRelativePath) {
    for (const [symbol, schemaName] of targetSchemas) {
      bindings.identifiers.set(symbol, { schemaName, symbol });
    }
  }
  return bindings;
}

function importedValidatorBindings(repoRoot, sourceOverrides, ast, relativePath, resolveValidatorExports) {
  const bindings = { identifiers: new Map(), namespaces: new Map() };
  for (const statement of ast.program.body) {
    if (statement.type !== 'ImportDeclaration' || typeof statement.source?.value !== 'string') continue;
    const sourcePath = resolveLocalModulePath(repoRoot, sourceOverrides, relativePath, statement.source.value);
    if (!sourcePath) continue;
    const sourceExports = resolveValidatorExports(sourcePath);
    if (sourceExports.size === 0) continue;
    for (const specifier of statement.specifiers) {
      if (specifier.type === 'ImportSpecifier') {
        const target = sourceExports.get(moduleName(specifier.imported));
        if (target) bindings.identifiers.set(specifier.local.name, target);
      } else if (specifier.type === 'ImportNamespaceSpecifier') {
        bindings.namespaces.set(specifier.local.name, sourceExports);
      } else if (specifier.type === 'ImportDefaultSpecifier') {
        const target = sourceExports.get('default');
        if (target) bindings.identifiers.set(specifier.local.name, target);
      }
    }
  }
  return bindings;
}

function createValidatorExportResolver(repoRoot, sourceOverrides, targetSchemas) {
  const directExports = new Map([...targetSchemas]
    .map(([symbol, schemaName]) => [symbol, { schemaName, symbol }]));
  const cache = new Map([[validatorRelativePath, directExports]]);
  const resolving = new Set();

  function resolveValidatorExports(modulePath) {
    if (cache.has(modulePath)) return cache.get(modulePath);
    if (resolving.has(modulePath)) return new Map();
    resolving.add(modulePath);
    try {
      const ast = parseModule(readRepositorySource(repoRoot, modulePath, sourceOverrides), modulePath);
      const bindings = importedValidatorBindings(
        repoRoot,
        sourceOverrides,
        ast,
        modulePath,
        resolveValidatorExports,
      );
      const exports = collectValidatorExports(
        repoRoot,
        sourceOverrides,
        ast,
        modulePath,
        bindings,
        resolveValidatorExports,
      );
      cache.set(modulePath, exports);
      return exports;
    } finally {
      resolving.delete(modulePath);
    }
  }

  return resolveValidatorExports;
}

function collectValidatorExports(repoRoot, sourceOverrides, ast, modulePath, bindings, resolveValidatorExports) {
  const exports = new Map();
  for (const statement of ast.program.body) {
    if (statement.type === 'ExportAllDeclaration') {
      const sourceExports = validatorExportsFromSource(
        repoRoot,
        sourceOverrides,
        modulePath,
        statement.source?.value,
        resolveValidatorExports,
      );
      if (sourceExports.size > 0) throw new Error(`${modulePath} validator export escape requires explicit named re-exports`);
      continue;
    }
    if (statement.type === 'ExportNamedDeclaration') {
      collectNamedValidatorExports(
        repoRoot,
        sourceOverrides,
        modulePath,
        statement,
        bindings,
        resolveValidatorExports,
        exports,
      );
      continue;
    }
    if (statement.type === 'ExportDefaultDeclaration') {
      const target = validatorBindingTarget(statement.declaration, bindings);
      if (target) setValidatorExport(exports, 'default', target, modulePath);
      else if (statement.declaration?.type === 'Identifier' && bindings.namespaces.has(statement.declaration.name)) {
        throw new Error(`${modulePath} validator namespace export escape cannot be resolved exactly`);
      }
    }
  }
  return exports;
}

function collectNamedValidatorExports(
  repoRoot,
  sourceOverrides,
  modulePath,
  statement,
  bindings,
  resolveValidatorExports,
  exports,
) {
  if (typeof statement.source?.value === 'string') {
    const sourceExports = validatorExportsFromSource(
      repoRoot,
      sourceOverrides,
      modulePath,
      statement.source.value,
      resolveValidatorExports,
    );
    for (const specifier of statement.specifiers) {
      if (specifier.type !== 'ExportSpecifier') {
        if (sourceExports.size > 0) throw new Error(`${modulePath} validator export escape cannot be resolved exactly`);
        continue;
      }
      const target = sourceExports.get(moduleName(specifier.local));
      if (target) setValidatorExport(exports, moduleName(specifier.exported), target, modulePath);
    }
    return;
  }
  for (const specifier of statement.specifiers) {
    if (specifier.type !== 'ExportSpecifier') continue;
    const localName = moduleName(specifier.local);
    const target = bindings.identifiers.get(localName);
    if (target) setValidatorExport(exports, moduleName(specifier.exported), target, modulePath);
    else if (bindings.namespaces.has(localName)) {
      throw new Error(`${modulePath} validator namespace export escape cannot be resolved exactly`);
    }
  }
  if (statement.declaration?.type !== 'VariableDeclaration') return;
  for (const declarator of statement.declaration.declarations) {
    if (validatorBindingTarget(declarator.init, bindings)) {
      throw new Error(`${modulePath} validator export escape cannot be resolved exactly`);
    }
  }
}

function validatorExportsFromSource(
  repoRoot,
  sourceOverrides,
  modulePath,
  sourceValue,
  resolveValidatorExports,
) {
  const sourcePath = resolveLocalModulePath(repoRoot, sourceOverrides, modulePath, sourceValue);
  return sourcePath ? resolveValidatorExports(sourcePath) : new Map();
}

function resolveLocalModulePath(repoRoot, sourceOverrides, importerPath, sourceValue) {
  if (typeof sourceValue !== 'string' || !sourceValue.startsWith('.')) return '';
  const base = path.posix.normalize(path.posix.join(path.posix.dirname(importerPath), sourceValue));
  if (!base.startsWith('frontend-app/src/')) return '';
  const extension = path.posix.extname(base);
  if (extension && !/\.(?:js|jsx)$/.test(extension)) return '';
  const candidates = extension ? [base] : [`${base}.js`, `${base}.jsx`, `${base}/index.js`, `${base}/index.jsx`];
  for (const candidate of candidates) {
    if (sourceOverrides.has(candidate)) return candidate;
    try {
      const info = fs.lstatSync(path.join(repoRoot, candidate));
      if (info.isFile() && !info.isSymbolicLink()) return candidate;
    } catch (error) {
      if (error?.code !== 'ENOENT') throw error;
    }
  }
  return '';
}

function setValidatorExport(exports, exportedName, target, modulePath) {
  if (!exportedName) throw new Error(`${modulePath} validator export has a blank name`);
  const existing = exports.get(exportedName);
  if (existing && (existing.schemaName !== target.schemaName || existing.symbol !== target.symbol)) {
    throw new Error(`${modulePath} validator export ${exportedName} is ambiguous`);
  }
  exports.set(exportedName, target);
}

function validatorBindingTarget(callee, bindings) {
  if (callee?.type === 'Identifier') return bindings.identifiers.get(callee.name);
  if (callee?.type !== 'MemberExpression' || callee.object?.type !== 'Identifier') return undefined;
  const namespace = bindings.namespaces.get(callee.object.name);
  return namespace?.get(memberPropertyName(callee));
}

function memberPropertyName(member) {
  if (!member.computed && member.property?.type === 'Identifier') return member.property.name;
  return stringLiteralValue(member.property);
}

function moduleName(node) {
  if (node?.type === 'Identifier') return node.name;
  return stringLiteralValue(node);
}

function hasValidatorBindings(bindings) {
  return bindings.identifiers.size > 0 || bindings.namespaces.size > 0;
}

function assertValidatorBindingsSafe(ast, bindings, relativePath) {
  const parents = new WeakMap();
  walkNodeWithParent(ast.program, (node, parent) => {
    if (parent) parents.set(node, parent);
    if (node.type !== 'Identifier' || isStaticPropertyName(node, parent)) return;
    if (bindings.identifiers.has(node.name)) {
      if (isControlledValidatorIdentifierUse(node, parent, relativePath)) return;
      throw new Error(`${relativePath} validator binding ${node.name} escapes direct calls or controlled explicit re-exports`);
    }
    const namespace = bindings.namespaces.get(node.name);
    if (!namespace) return;
    if (parent?.type === 'ImportNamespaceSpecifier' && parent.local === node) return;
    if (parent?.type === 'MemberExpression' && parent.object === node) {
      const propertyName = memberPropertyName(parent);
      if (!namespace.has(propertyName)) return;
      const memberParent = parents.get(parent);
      if (memberParent?.type === 'CallExpression' && memberParent.callee === parent) return;
      throw new Error(`${relativePath} validator namespace member ${node.name}.${propertyName} escapes direct calls`);
    }
    throw new Error(`${relativePath} validator namespace ${node.name} escapes direct calls or controlled explicit re-exports`);
  });
}

function isControlledValidatorIdentifierUse(node, parent, relativePath) {
  if (parent?.type === 'CallExpression' && parent.callee === node) return true;
  if (parent?.type === 'ImportSpecifier' || parent?.type === 'ImportDefaultSpecifier') return true;
  if (parent?.type === 'ExportSpecifier' || parent?.type === 'ExportDefaultDeclaration') return true;
  return relativePath === validatorRelativePath
    && parent?.type === 'FunctionDeclaration'
    && parent.id === node;
}

function isStaticPropertyName(node, parent) {
  if ((parent?.type === 'MemberExpression' || parent?.type === 'OptionalMemberExpression')
    && !parent.computed
    && parent.property === node) return true;
  if (parent?.computed || parent?.key !== node) return false;
  return parent.type === 'ObjectProperty'
    || parent.type === 'ObjectMethod'
    || parent.type === 'ClassProperty'
    || parent.type === 'ClassMethod';
}

function namedProductionFunctions(ast) {
  const functions = [];
  for (const statement of ast.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' || statement.type === 'ExportDefaultDeclaration'
      ? statement.declaration
      : statement;
    collectNamedDeclaration(declaration, functions);
  }
  return functions;
}

function collectNamedDeclaration(declaration, functions) {
  if (declaration?.type === 'FunctionDeclaration' && declaration.id?.name) {
    functions.push({ symbol: declaration.id.name, body: declaration.body });
    return;
  }
  if (declaration?.type === 'VariableDeclaration') {
    for (const declarator of declaration.declarations) {
      if (declarator.id?.type !== 'Identifier') continue;
      collectNamedValue(declarator.id.name, declarator.init, functions);
    }
    return;
  }
  if (declaration?.type === 'ClassDeclaration' && declaration.id?.name) {
    collectNamedClassMethods(declaration.id.name, declaration, functions);
  }
}

function collectNamedValue(owner, value, functions) {
  if (isFunctionNode(value)) {
    functions.push({ symbol: owner, body: value.body });
    return;
  }
  if (value?.type === 'ObjectExpression') {
    collectNamedObjectMethods(owner, value, functions);
    return;
  }
  if (value?.type === 'ClassExpression') collectNamedClassMethods(owner, value, functions);
}

function collectNamedObjectMethods(owner, object, functions) {
  for (const property of object.properties) {
    const propertyName = staticPropertyName(property);
    if (!propertyName) continue;
    const symbol = `${owner}.${propertyName}`;
    if (property.type === 'ObjectMethod') {
      functions.push({ symbol, body: property.body });
    } else if (property.type === 'ObjectProperty') {
      collectNamedValue(symbol, property.value, functions);
    }
  }
}

function collectNamedClassMethods(owner, classNode, functions) {
  for (const member of classNode.body.body) {
    const memberName = staticPropertyName(member);
    if (!memberName) continue;
    const symbol = `${owner}.${memberName}`;
    if (member.type === 'ClassMethod') {
      functions.push({ symbol, body: member.body });
    } else if (member.type === 'ClassProperty') {
      collectNamedValue(symbol, member.value, functions);
    }
  }
}

function staticPropertyName(property) {
  if (property.computed) return '';
  if (property.key?.type === 'Identifier') return property.key.name;
  return stringLiteralValue(property.key);
}

function assertUniqueProductionSymbols(functions, filePath) {
  const seen = new Set();
  for (const fn of functions) {
    if (seen.has(fn.symbol)) throw new Error(`${filePath}:${fn.symbol} resolved multiple production functions`);
    seen.add(fn.symbol);
  }
}

function consumerKey(consumer) {
  return `${consumer.path}:${consumer.symbol}:${consumer.calls}`;
}

export function validateMapperSource(source, mapper) {
  if (!isRecord(mapper.fields) || Object.keys(mapper.fields).length === 0) {
    throw new Error(`JS mapper ${mapper.name} has no registered fields`);
  }
  const fn = findFunction(parseModule(source, mapper.path), mapper.symbol, mapper.path);
  const derived = deriveRequiredAliasMappings(fn);
  assertExactSet(`JS mapper ${mapper.name} fields`, Object.keys(mapper.fields).sort(), [...derived.keys()].sort());
  const returned = deriveReturnedProperties(fn);
  for (const [field, mapping] of Object.entries(mapper.fields)) {
    if (!isRecord(mapping) || !Array.isArray(mapping.aliases) || typeof mapping.wire !== 'string') {
      throw new Error(`JS mapper ${mapper.name}.${field} registration is incomplete`);
    }
    const actual = derived.get(field);
    assertExactSet(`JS mapper ${mapper.name}.${field} aliases`, [...mapping.aliases].sort(), [...actual].sort());
    if (returned.get(mapping.wire) !== field) {
      throw new Error(`JS mapper ${mapper.name}.${field} does not map to wire field ${mapping.wire}`);
    }
  }
}

function canonicalSchemas(repoRoot, sourceOverrides) {
  const schemaDir = path.join(repoRoot, 'internal/dto/turn/schema');
  const schemas = new Map();
  for (const entry of fs.readdirSync(schemaDir, { withFileTypes: true })) {
    if (entry.isDirectory() || !entry.name.endsWith('.json') || entry.name === 'field_consumers.json') continue;
    const relativePath = `internal/dto/turn/schema/${entry.name}`;
    const schema = parseJSON(readRepositorySource(repoRoot, relativePath, sourceOverrides), `canonical schema ${entry.name}`);
    if (!schema.title || !isRecord(schema.properties)) throw new Error(`canonical schema ${entry.name} must have title and properties`);
    if (schemas.has(schema.title)) throw new Error(`duplicate canonical schema ${schema.title}`);
    schemas.set(schema.title, schema);
  }
  if (schemas.size !== 3) throw new Error(`expected exactly three canonical turn schemas, found ${schemas.size}`);
  return schemas;
}

function loadRegistry(repoRoot, sourceOverrides) {
  return parseJSON(readRepositorySource(repoRoot, registryRelativePath, sourceOverrides), 'consumer registry');
}

function validateSchemaRegistryEntry(
  repoRoot,
  sourceOverrides,
  schemaName,
  entry,
  targetSchemas,
  resolveValidatorExports,
) {
  if (!isRecord(entry)) throw new Error(`consumer registry missing schema ${schemaName}`);
  validateLocatorShape(repoRoot, entry.goType, '.go');
  validateLocatorShape(repoRoot, entry.goValidator, '.go');
  validateCallLocators(repoRoot, entry.goConsumers, '.go', `${schemaName} Go consumers`);
  const validator = resolveJSFunction(repoRoot, sourceOverrides, entry.jsValidator);
  if (!functionHasCall(validator.fn, 'validateNamedSchema', schemaName)) {
    throw new Error(`${schemaName} JS validator does not call validateNamedSchema for its schema`);
  }
  if (!Array.isArray(entry.jsConsumers) || entry.jsConsumers.length === 0) {
    throw new Error(`${schemaName} has no JS production consumers`);
  }
  for (const consumer of entry.jsConsumers) {
    const resolved = resolveJSFunction(repoRoot, sourceOverrides, consumer);
    if (!functionHasValidatorCall(
      repoRoot,
      sourceOverrides,
      resolved,
      consumer,
      targetSchemas,
      resolveValidatorExports,
    )) {
      throw new Error(`${consumer.path}:${consumer.symbol} missing call ${consumer.calls}`);
    }
  }
}

function validateCallLocators(repoRoot, locators, extension, label) {
  if (!Array.isArray(locators) || locators.length === 0) throw new Error(`${label} must not be empty`);
  for (const locator of locators) {
    validateLocatorShape(repoRoot, locator, extension);
    if (!locator.calls) throw new Error(`${label} contains a blank call target`);
  }
}

function resolveJSFunction(repoRoot, sourceOverrides, locator) {
  validateLocatorShape(repoRoot, locator, '.js');
  const source = readRepositorySource(repoRoot, locator.path, sourceOverrides);
  const ast = parseModule(source, locator.path);
  return { ast, fn: findFunction(ast, locator.symbol, locator.path) };
}

function validateLocatorShape(repoRoot, locator, extension) {
  if (!isRecord(locator) || typeof locator.path !== 'string' || typeof locator.symbol !== 'string' || !locator.symbol.trim()) {
    throw new Error('source locator is incomplete');
  }
  const normalized = path.posix.normalize(locator.path);
  if (!locator.path || path.isAbsolute(locator.path) || normalized !== locator.path || normalized.startsWith('../')) {
    throw new Error(`locator path ${locator.path} must be normalized and repository-confined`);
  }
  if (path.posix.extname(locator.path) !== extension) throw new Error(`locator path ${locator.path} must end with ${extension}`);
  const absolutePath = path.join(repoRoot, locator.path);
  const info = fs.lstatSync(absolutePath);
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`locator path ${locator.path} is not a regular file`);
}

function parseModule(source, filePath) {
  try {
    return parse(source, { sourceType: 'module', plugins: ['jsx'] });
  } catch (error) {
    throw new Error(`parse ${filePath}: ${error.message}`);
  }
}

function findFunction(ast, symbol, filePath) {
  const matches = namedProductionFunctions(ast).filter((fn) => fn.symbol === symbol);
  if (matches.length !== 1) throw new Error(`${filePath}:${symbol} resolved ${matches.length} production functions`);
  return matches[0];
}

function functionHasCall(fn, target, firstStringArgument = '') {
  let found = false;
  walkFunctionBody(fn, (node) => {
    if (node.type !== 'CallExpression' || calleeName(node.callee) !== target) return;
    if (firstStringArgument && stringLiteralValue(node.arguments[0]) !== firstStringArgument) return;
    found = true;
  });
  return found;
}

function functionHasValidatorCall(
  repoRoot,
  sourceOverrides,
  resolved,
  locator,
  targetSchemas,
  resolveValidatorExports,
) {
  const bindings = validatorBindings(
    repoRoot,
    sourceOverrides,
    resolved.ast,
    locator.path,
    targetSchemas,
    resolveValidatorExports,
  );
  assertValidatorBindingsSafe(resolved.ast, bindings, locator.path);
  let found = false;
  walkFunctionBody(resolved.fn, (node) => {
    if (node.type !== 'CallExpression') return;
    const target = validatorBindingTarget(node.callee, bindings);
    if (target?.symbol === locator.calls) found = true;
  });
  return found;
}

function deriveRequiredAliasMappings(fn) {
  const mappings = new Map();
  walkFunctionBody(fn, (node) => {
    if (node.type !== 'VariableDeclarator' || node.id?.type !== 'Identifier') return;
    if (node.init?.type !== 'CallExpression' || calleeName(node.init.callee) !== 'requiredStringAliasValue') return;
    const aliases = new Set();
    walkNode(node.init, (child) => {
      if (child.type !== 'CallExpression' || calleeName(child.callee) !== 'takePayloadField') return;
      const alias = stringLiteralValue(child.arguments[1]);
      if (alias) aliases.add(alias);
    });
    if (aliases.size === 0 || mappings.has(node.id.name)) throw new Error(`mapper variable ${node.id.name} has missing or duplicate aliases`);
    mappings.set(node.id.name, aliases);
  });
  return mappings;
}

function deriveReturnedProperties(fn) {
  const mappings = new Map();
  walkFunctionBody(fn, (node) => {
    if (node.type !== 'ReturnStatement' || node.argument?.type !== 'ObjectExpression') return;
    for (const property of node.argument.properties) {
      if (property.type !== 'ObjectProperty' || property.computed || property.value?.type !== 'Identifier') continue;
      const key = property.key.type === 'Identifier' ? property.key.name : stringLiteralValue(property.key);
      if (key) mappings.set(key, property.value.name);
    }
  });
  return mappings;
}

function walkFunctionBody(fn, visitor) {
  walkNode(fn.body, visitor, true);
}

function walkNode(node, visitor, skipNestedFunctions = false, root = true) {
  if (!node || typeof node !== 'object') return;
  visitor(node);
  if (!root && skipNestedFunctions && isFunctionNode(node)) return;
  for (const [key, value] of Object.entries(node)) {
    if (key === 'loc' || key === 'start' || key === 'end') continue;
    if (Array.isArray(value)) value.forEach((item) => walkNode(item, visitor, skipNestedFunctions, false));
    else if (value && typeof value === 'object' && typeof value.type === 'string') walkNode(value, visitor, skipNestedFunctions, false);
  }
}

function walkNodeWithParent(node, visitor, parent = null) {
  if (!node || typeof node !== 'object') return;
  visitor(node, parent);
  for (const [key, value] of Object.entries(node)) {
    if (key === 'loc' || key === 'start' || key === 'end') continue;
    if (Array.isArray(value)) value.forEach((item) => walkNodeWithParent(item, visitor, node));
    else if (value && typeof value === 'object' && typeof value.type === 'string') {
      walkNodeWithParent(value, visitor, node);
    }
  }
}

function isFunctionNode(node) {
  return node?.type === 'FunctionDeclaration' || node?.type === 'FunctionExpression' || node?.type === 'ArrowFunctionExpression';
}

function calleeName(callee) {
  if (callee?.type === 'Identifier') return callee.name;
  if (callee?.type === 'MemberExpression' && !callee.computed && callee.property?.type === 'Identifier') return callee.property.name;
  return '';
}

function stringLiteralValue(node) {
  return node?.type === 'StringLiteral' ? node.value : '';
}

function readRepositorySource(repoRoot, relativePath, sourceOverrides) {
  if (sourceOverrides.has(relativePath)) return sourceOverrides.get(relativePath);
  const absolutePath = path.join(repoRoot, relativePath);
  return fs.readFileSync(absolutePath, 'utf8');
}

function parseJSON(source, label) {
  try {
    return JSON.parse(source);
  } catch (error) {
    throw new Error(`parse ${label}: ${error.message}`);
  }
}

function assertExactSet(label, expected, actual) {
  if (expected.length === actual.length && expected.every((value, index) => value === actual[index])) return;
  const expectedSet = new Set(expected);
  const actualSet = new Set(actual);
  const missing = expected.filter((value) => !actualSet.has(value));
  const stale = actual.filter((value) => !expectedSet.has(value));
  throw new Error(`${label} missing=${missing.join(',')} stale=${stale.join(',')}`);
}

function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

const invokedDirectly = process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url;
if (invokedDirectly) {
  try {
    const report = validateTurnContractFieldGuard();
    console.log(`turn contract field guard passed: schemas=${report.schemaCount} mappers=${report.mapperCount}`);
  } catch (error) {
    console.error(`turn contract field guard failed: ${error.message}`);
    process.exitCode = 1;
  }
}
