import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { parse } from '@babel/parser';
import { Linter } from 'eslint';

import { generatedSchemas } from '../src/shared/contracts/turnContracts.generated.js';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const defaultRepoRoot = path.resolve(appRoot, '..');
const registryRelativePath = 'internal/dto/turn/schema/field_consumers.json';
const validatorRelativePath = 'frontend-app/src/shared/contracts/turnContractValidators.js';
const requiredTerminalChainNames = [
  'terminal-runtime-dispatch',
  'terminal-public-error-projection',
  'terminal-public-error-notice',
  'terminal-timeline-render',
  'terminal-public-error-clipboard-sink',
  'terminal-public-error-diagnostic-projection',
];

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
  validateJSTerminalChains(repoRoot, sourceOverrides, registry.jsTerminalChains);
  const mapperNames = new Set();
  for (const mapper of registry.jsMappers) {
    if (!mapper?.name || mapperNames.has(mapper.name)) throw new Error(`JS mapper has blank or duplicate name ${mapper?.name ?? ''}`);
    mapperNames.add(mapper.name);
    const source = readRepositorySource(repoRoot, mapper.path, sourceOverrides);
    validateMapperSource(source, mapper);
  }
  return { schemaCount: schemas.size, mapperCount: registry.jsMappers.length };
}

function validateJSTerminalChains(repoRoot, sourceOverrides, chains) {
  if (!Array.isArray(chains) || chains.length === 0) {
    throw new Error('consumer registry must contain JS terminal chains');
  }
  assertExactSet('JS terminal chain registry', [...requiredTerminalChainNames].sort(), chains.map((chain) => chain?.name).sort());
  const names = new Set();
  for (const chain of chains) {
    if (!isRecord(chain) || typeof chain.name !== 'string' || !chain.name || names.has(chain.name)) {
      throw new Error(`JS terminal chain has blank or duplicate name ${chain?.name ?? ''}`);
    }
    names.add(chain.name);
    validateJavaScriptLocator(repoRoot, chain);
    const resolved = resolveJSFunction(repoRoot, sourceOverrides, chain);
    const evidence = functionEvidence(resolved.fn);
    validateRequiredEvidence(chain, 'call', chain.calls, evidence.calls);
    validateRequiredEvidence(chain, 'call argument', chain.callArguments, evidence.callArguments);
    validateForbiddenEvidence(chain, 'member path', chain.forbiddenMemberPaths, evidence.memberPaths);
    validateRequiredEvidence(chain, 'member path', chain.memberPaths, evidence.memberPaths);
    validateForbiddenEvidence(chain, 'projection', chain.forbiddenProjections, evidence.projections);
    validateRequiredEvidence(chain, 'call member path', chain.callMemberPaths, evidence.callMemberPaths);
    validateExactProjections(chain, evidence.projections);
    validateRequiredEvidence(chain, 'JSX prop', chain.jsxProps, evidence.jsxProps);
  }
}

function validateForbiddenEvidence(chain, kind, forbidden, actual) {
  if (forbidden === undefined) return;
  if (!Array.isArray(forbidden)) throw new Error(`JS terminal chain ${chain.name} forbidden ${kind} registration must be an array`);
  for (const value of forbidden) {
    if (typeof value !== 'string' || !value) throw new Error(`JS terminal chain ${chain.name} forbidden ${kind} registration contains a blank value`);
    if (actual.has(value)) throw new Error(`JS terminal chain ${chain.name} retains forbidden ${kind} ${value}`);
  }
}

function validateRequiredEvidence(chain, kind, expected, actual) {
  if (expected === undefined) return;
  if (!Array.isArray(expected)) throw new Error(`JS terminal chain ${chain.name} ${kind} registration must be an array`);
  for (const value of expected) {
    if (typeof value !== 'string' || !value || !actual.has(value)) {
      throw new Error(`JS terminal chain ${chain.name} missing ${kind} ${value}`);
    }
  }
}

function validateExactProjections(chain, actual) {
  if (chain.projections === undefined) return;
  if (!isRecord(chain.projections) || Object.keys(chain.projections).length === 0) {
    throw new Error(`JS terminal chain ${chain.name} projections registration must be a non-empty object`);
  }
  for (const [target, source] of Object.entries(chain.projections)) {
    const projection = `${target}=${source}`;
    if (!actual.has(projection)) throw new Error(`JS terminal chain ${chain.name} missing projection ${projection}`);
  }
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
    bindings.lexical ??= createLexicalBindingIndex(
      readRepositorySource(repoRoot, relativePath, sourceOverrides),
      relativePath,
    );
    for (const [symbol, schemaName] of targetSchemas) {
      const binding = bindings.lexical.programBinding(symbol);
      if (!binding) throw new Error(`${relativePath} validator declaration ${symbol} is missing`);
      bindings.identifiers.set(binding, { schemaName, symbol });
    }
  }
  return bindings;
}

function importedValidatorBindings(repoRoot, sourceOverrides, ast, relativePath, resolveValidatorExports) {
  const bindings = {
    identifiers: new Map(),
    namespaces: new Map(),
    lexical: undefined,
  };
  const imported = [];
  for (const statement of ast.program.body) {
    if (statement.type !== 'ImportDeclaration' || typeof statement.source?.value !== 'string') continue;
    const sourcePath = resolveLocalModulePath(repoRoot, sourceOverrides, relativePath, statement.source.value);
    if (!sourcePath) continue;
    const sourceExports = resolveValidatorExports(sourcePath);
    if (sourceExports.size === 0) continue;
    for (const specifier of statement.specifiers) {
      if (specifier.type === 'ImportSpecifier') {
        const target = sourceExports.get(moduleName(specifier.imported));
        if (target) imported.push({ kind: 'identifier', local: specifier.local, target });
      } else if (specifier.type === 'ImportNamespaceSpecifier') {
        imported.push({ kind: 'namespace', local: specifier.local, target: sourceExports });
      } else if (specifier.type === 'ImportDefaultSpecifier') {
        const target = sourceExports.get('default');
        if (target) imported.push({ kind: 'identifier', local: specifier.local, target });
      }
    }
  }
  if (imported.length === 0) return bindings;
  bindings.lexical = createLexicalBindingIndex(
    readRepositorySource(repoRoot, relativePath, sourceOverrides),
    relativePath,
  );
  for (const entry of imported) {
    const binding = requiredLexicalBinding(bindings, entry.local, relativePath);
    const registry = entry.kind === 'namespace' ? bindings.namespaces : bindings.identifiers;
    registry.set(binding, entry.target);
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
      else if (validatorNamespace(statement.declaration, bindings)) {
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
    const target = validatorBindingTarget(specifier.local, bindings);
    if (target) setValidatorExport(exports, moduleName(specifier.exported), target, modulePath);
    else if (validatorNamespace(specifier.local, bindings)) {
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
  if (callee?.type === 'Identifier') {
    const binding = bindings.lexical?.bindingFor(callee);
    return binding ? bindings.identifiers.get(binding) : undefined;
  }
  if (callee?.type !== 'MemberExpression' || callee.object?.type !== 'Identifier') return undefined;
  const namespace = validatorNamespace(callee.object, bindings);
  return namespace?.get(memberPropertyName(callee));
}

function validatorNamespace(identifier, bindings) {
  if (identifier?.type !== 'Identifier') return undefined;
  const binding = bindings.lexical?.bindingFor(identifier);
  return binding ? bindings.namespaces.get(binding) : undefined;
}

function requiredLexicalBinding(bindings, identifier, relativePath) {
  const binding = bindings.lexical?.bindingFor(identifier);
  if (!binding) throw new Error(`${relativePath} import binding ${identifier?.name ?? ''} cannot be resolved`);
  return binding;
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
    const binding = bindings.lexical.bindingFor(node);
    if (binding && bindings.identifiers.has(binding)) {
      if (isControlledValidatorIdentifierUse(node, parent, relativePath)) return;
      throw new Error(`${relativePath} validator binding ${node.name} escapes direct calls or controlled explicit re-exports`);
    }
    const namespace = binding ? bindings.namespaces.get(binding) : undefined;
    if (!namespace) return;
    if (parent?.type === 'ImportNamespaceSpecifier' && parent.local === node) return;
    if (parent?.type === 'MemberExpression' && parent.object === node) {
      const propertyName = memberPropertyName(parent);
      if (!propertyName) {
        throw new Error(`${relativePath} validator namespace ${node.name} uses a dynamic computed member`);
      }
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
  if (parent?.type === 'ObjectProperty' && parent.shorthand && parent.value === node) return false;
  if (parent?.computed || parent?.key !== node) return false;
  return parent.type === 'ObjectProperty'
    || parent.type === 'ObjectMethod'
    || parent.type === 'ClassProperty'
    || parent.type === 'ClassMethod';
}

function createLexicalBindingIndex(source, filePath) {
  const linter = new Linter({ configType: 'flat' });
  const messages = linter.verify(source, [{
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
  }], { filename: filePath });
  const fatal = messages.find((message) => message.fatal);
  if (fatal) throw new Error(`scope analysis ${filePath}:${fatal.line}:${fatal.column}: ${fatal.message}`);
  const scopeManager = linter.getSourceCode()?.scopeManager;
  if (!scopeManager) throw new Error(`scope analysis ${filePath} did not produce a scope manager`);

  const bindingsByRange = new Map();
  const programBindings = new Map();
  for (const scope of scopeManager.scopes) {
    for (const variable of scope.variables) {
      if (variable.identifiers.length === 0) continue;
      for (const identifier of variable.identifiers) {
        bindingsByRange.set(nodeRangeKey(identifier), variable);
      }
      for (const reference of variable.references) {
        bindingsByRange.set(nodeRangeKey(reference.identifier), variable);
      }
      if (scope.type === 'module') programBindings.set(variable.name, variable);
    }
  }
  return {
    bindingFor(identifier) {
      return identifier?.type === 'Identifier'
        ? bindingsByRange.get(nodeRangeKey(identifier))
        : undefined;
    },
    programBinding(name) {
      return programBindings.get(name);
    },
  };
}

function nodeRangeKey(node) {
  const start = node?.start ?? node?.range?.[0];
  const end = node?.end ?? node?.range?.[1];
  return Number.isInteger(start) && Number.isInteger(end) ? `${start}:${end}` : '';
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
  if (value?.type === 'CallExpression') {
    const functionArguments = value.arguments.filter(isFunctionNode);
    if (functionArguments.length === 1) functions.push({ symbol: owner, body: functionArguments[0].body });
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
  validateJavaScriptLocator(repoRoot, locator);
  const source = readRepositorySource(repoRoot, locator.path, sourceOverrides);
  const ast = parseModule(source, locator.path);
  return { ast, fn: findFunction(ast, locator.symbol, locator.path) };
}

function validateJavaScriptLocator(repoRoot, locator) {
  if (!isRecord(locator) || typeof locator.path !== 'string' || typeof locator.symbol !== 'string' || !locator.symbol.trim()) {
    throw new Error('source locator is incomplete');
  }
  const normalized = path.posix.normalize(locator.path);
  if (!locator.path || path.isAbsolute(locator.path) || normalized !== locator.path || normalized.startsWith('../')) {
    throw new Error(`locator path ${locator.path} must be normalized and repository-confined`);
  }
  if (!/\.(?:js|jsx)$/.test(locator.path)) throw new Error(`locator path ${locator.path} must end with .js or .jsx`);
  const absolutePath = path.join(repoRoot, locator.path);
  const info = fs.lstatSync(absolutePath);
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`locator path ${locator.path} is not a regular file`);
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

function functionEvidence(fn) {
  const calls = new Set();
  const memberPaths = new Set();
  const callArguments = new Set();
  const callMemberPaths = new Set();
  const projections = new Set();
  const jsxProps = new Set();
  walkNode(fn.body, (node) => {
    if (node.type === 'CallExpression') {
      const name = calleeName(node.callee);
      if (name) calls.add(name);
      const argument = expressionPath(node.arguments[0]);
      if (name && argument) callArguments.add(`${name}=${argument}`);
    }
    if (node.type === 'MemberExpression' || node.type === 'OptionalMemberExpression') {
      const memberPath = memberExpressionPath(node);
      if (memberPath) memberPaths.add(memberPath);
      const sanitizedMemberPath = callMemberPath(node);
      if (sanitizedMemberPath) callMemberPaths.add(sanitizedMemberPath);
    }
    if (node.type === 'ObjectProperty' && !node.computed) {
      const target = staticPropertyName(node);
      const source = memberExpressionPath(node.value);
      if (target && source) projections.add(`${target}=${source}`);
    }
    if (node.type === 'JSXOpeningElement') {
      const element = jsxElementName(node.name);
      if (!element) return;
      for (const attribute of node.attributes) {
        if (attribute.type !== 'JSXAttribute' || attribute.name?.type !== 'JSXIdentifier') continue;
        const expression = attribute.value?.type === 'JSXExpressionContainer' ? attribute.value.expression : undefined;
        if (expression?.type === 'Identifier') jsxProps.add(`${element}:${attribute.name.name}=${expression.name}`);
      }
    }
  });
  return { calls, memberPaths, callArguments, callMemberPaths, projections, jsxProps };
}

function expressionPath(node) {
  if (node?.type === 'Identifier') return node.name;
  if (node?.type !== 'MemberExpression' && node?.type !== 'OptionalMemberExpression') return '';
  const object = expressionPath(node.object);
  const property = memberPropertyName(node);
  return object && property ? `${object}.${property}` : '';
}

function memberExpressionPath(node) {
  return expressionPath(node);
}

function callMemberPath(node) {
  if (node?.type !== 'MemberExpression' && node?.type !== 'OptionalMemberExpression') return '';
  if (node.object?.type !== 'CallExpression') return '';
  const callee = calleeName(node.object.callee);
  const argument = expressionPath(node.object.arguments[0]);
  const property = memberPropertyName(node);
  return callee && argument && property ? `${callee}(${argument}).${property}` : '';
}

function jsxElementName(node) {
  if (node?.type === 'JSXIdentifier') return node.name;
  if (node?.type === 'JSXMemberExpression') {
    const object = jsxElementName(node.object);
    const property = jsxElementName(node.property);
    return object && property ? `${object}.${property}` : '';
  }
  return '';
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
