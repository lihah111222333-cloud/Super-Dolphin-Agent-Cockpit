import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { parse } from '@babel/parser';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const registryPath = 'scripts/frontend-dependency-direction-registry.json';
const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
const testFilePattern = /\.(?:test|spec)\.[cm]?[jt]sx?$/;
const sourceCandidates = Object.freeze(['.js', '.jsx', '.ts', '.tsx', '.mjs', '.cjs']);

export function validateFrontendDependencyDirection({
  root = appRoot,
  registry = readJSON(path.join(root, registryPath)),
  sources = null,
  today = currentDate(),
} = {}) {
  validateRegistryShape(registry);
  const graphSources = sources || productionSources(root, registry);
  const actualCaseIds = discoverCaseIds(
    fs.readFileSync(path.join(root, registry.testFile), 'utf8'),
    registry.testFile,
    'A03-',
  );
  assertExactSet('A03 regression case IDs', registry.caseIds, actualCaseIds);

  const result = dependencyDirectionResult({ sources: graphSources, registry, today });
  if (result.unresolved.length > 0) {
    throw new Error(`dependency direction unresolved local imports: ${JSON.stringify(result.unresolved)}`);
  }
  if (result.unexempted.length > 0 || result.staleExemptions.length > 0 || result.expiredExemptions.length > 0) {
    throw new Error(
      `dependency direction drift: violations=${JSON.stringify(result.unexempted)}`
      + ` stale=${JSON.stringify(result.staleExemptions)}`
      + ` expired=${JSON.stringify(result.expiredExemptions)}`,
    );
  }
  return {
    checkedImports: result.checkedImports,
    exemptionCount: registry.exemptions.length,
    layerCount: registry.layers.length,
  };
}

export function dependencyDirectionResult({ sources, registry, today = currentDate() }) {
  validateRegistryShape(registry, { requireTests: false });
  const normalizedSources = normalizeSourceMap(sources);
  const allowedDirections = new Set(registry.allowedDirections);
  const violations = [];
  const unresolved = [];
  const unknownSources = [];
  let checkedImports = 0;

  for (const [from, source] of [...normalizedSources].sort(([left], [right]) => left.localeCompare(right))) {
    const fromLayer = layerForPath(from, registry);
    if (!fromLayer) {
      unknownSources.push(from);
      continue;
    }
    for (const dependency of discoverDependencies(source, from)) {
      if (!isLocalSpecifier(dependency.specifier, registry.aliases)) continue;
      const resolution = resolveLocalDependency(from, dependency.specifier, normalizedSources, registry.aliases);
      if (resolution.ignored) continue;
      if (!resolution.path) {
        unresolved.push(dependencyKey({ ...dependency, from, to: '<unresolved>' }));
        continue;
      }
      const toLayer = layerForPath(resolution.path, registry);
      if (!toLayer) {
        throw new Error(`dependency target has no registered layer: ${from} -> ${resolution.path}`);
      }
      checkedImports += 1;
      if (fromLayer.name !== toLayer.name && !allowedDirections.has(`${fromLayer.name}->${toLayer.name}`)) {
        violations.push({
          from,
          kind: dependency.kind,
          specifier: dependency.specifier,
          to: resolution.path,
          fromLayer: fromLayer.name,
          toLayer: toLayer.name,
        });
      }
    }
  }
  if (unknownSources.length > 0) {
    throw new Error(`dependency sources have no registered layer: ${JSON.stringify(unknownSources.sort())}`);
  }

  const violationByKey = new Map(violations.map((violation) => [dependencyKey(violation), violation]));
  const unresolvedKeys = new Set(unresolved);
  const exemptionByKey = new Map();
  const expiredExemptions = [];
  for (const exemption of registry.exemptions) {
    const key = dependencyKey(exemption);
    if (exemptionByKey.has(key)) throw new Error(`duplicate dependency exemption ${key}`);
    exemptionByKey.set(key, exemption);
    if (today > exemption.expiresOn) expiredExemptions.push(key);
  }
  const actualKeys = new Set([...violationByKey.keys(), ...unresolvedKeys]);
  const unexempted = [...violationByKey.keys()].filter((key) => !exemptionByKey.has(key)).sort();
  const unexemptedUnresolved = [...unresolvedKeys].filter((key) => !exemptionByKey.has(key)).sort();
  const staleExemptions = [...exemptionByKey.keys()].filter((key) => !actualKeys.has(key)).sort();
  return {
    checkedImports,
    expiredExemptions: expiredExemptions.sort(),
    staleExemptions,
    unexempted,
    unresolved: unexemptedUnresolved,
    violations,
  };
}

export function discoverDependencies(source, filePath) {
  const dependencies = [];
  walkNode(parseModule(source, filePath), (node) => {
    if (node.type === 'ImportDeclaration') {
      addDependency(dependencies, 'import', node.source);
      return;
    }
    if (node.type === 'ExportNamedDeclaration' || node.type === 'ExportAllDeclaration') {
      addDependency(dependencies, 're-export', node.source);
      return;
    }
    if (node.type === 'ImportExpression') {
      addDependency(dependencies, 'dynamic-import', node.source, { failOnNonLiteral: true });
      return;
    }
    if (node.type === 'CallExpression' && node.callee?.type === 'Import') {
      addDependency(dependencies, 'dynamic-import', node.arguments[0], { failOnNonLiteral: true });
      return;
    }
    if (node.type === 'CallExpression' && node.callee?.type === 'Identifier' && node.callee.name === 'require') {
      addDependency(dependencies, 'require', node.arguments[0], { failOnNonLiteral: true });
    }
  });
  return dependencies;
}

function addDependency(dependencies, kind, sourceNode, { failOnNonLiteral = false } = {}) {
  const specifier = stringValue(sourceNode);
  if (specifier) dependencies.push({ kind, specifier });
  else if (failOnNonLiteral) dependencies.push({ kind, specifier: '<non-literal>' });
}

function resolveLocalDependency(from, specifier, sources, aliases) {
  const extension = path.posix.extname(specifier);
  if (extension && !sourceCandidates.includes(extension)) return { ignored: true, path: '' };
  let base = '';
  if (specifier.startsWith('.')) {
    base = path.posix.normalize(path.posix.join(path.posix.dirname(from), specifier));
  } else {
    const alias = Object.keys(aliases).sort((left, right) => right.length - left.length)
      .find((prefix) => specifier.startsWith(prefix));
    if (!alias) return { ignored: false, path: '' };
    base = path.posix.normalize(path.posix.join(aliases[alias], specifier.slice(alias.length)));
  }
  if (base.startsWith('../') || path.posix.isAbsolute(base)) return { ignored: false, path: '' };
  const candidates = [base];
  if (!path.posix.extname(base)) {
    for (const candidateExtension of sourceCandidates) candidates.push(`${base}${candidateExtension}`);
    for (const candidateExtension of sourceCandidates) candidates.push(`${base}/index${candidateExtension}`);
  }
  return { ignored: false, path: candidates.find((candidate) => sources.has(candidate)) || '' };
}

function layerForPath(relativePath, registry) {
  const matches = [];
  for (const layer of registry.layers) {
    if (layer.prefixes.some((prefix) => relativePath.startsWith(prefix))) matches.push(layer);
  }
  if (matches.length > 1) throw new Error(`dependency path ${relativePath} matches multiple layers`);
  if (matches.length === 1) return matches[0];
  if (path.posix.dirname(relativePath) === 'src') {
    return registry.layers.find(({ name }) => name === registry.rootLayer) || null;
  }
  return null;
}

function productionSources(root, registry) {
  const absolutePaths = walkSourceFiles(path.join(root, 'src'));
  const relativePaths = absolutePaths.map((absolutePath) => normalizeRelativePath(path.relative(root, absolutePath)));
  const registeredExclusions = registry.sourceExclusions.map(({ path: relativePath }) => normalizeRelativePath(relativePath));
  const discoveredTestOnlySources = relativePaths.filter((relativePath) => (
    !testFilePattern.test(relativePath) && isTestOnlyDirectoryPath(relativePath)
  ));
  assertExactSet('dependency test-only source exclusions', registeredExclusions, discoveredTestOnlySources);
  const exclusionSet = new Set(registeredExclusions);
  const sources = new Map();
  for (let index = 0; index < absolutePaths.length; index += 1) {
    const absolutePath = absolutePaths[index];
    const relativePath = relativePaths[index];
    if (testFilePattern.test(relativePath) || exclusionSet.has(relativePath)) continue;
    sources.set(relativePath, fs.readFileSync(absolutePath, 'utf8'));
  }
  if (sources.size === 0) throw new Error('dependency direction guard found zero production files');
  return sources;
}

function walkSourceFiles(directory) {
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolutePath = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...walkSourceFiles(absolutePath));
    else if (sourceExtensionPattern.test(entry.name)) files.push(absolutePath);
  }
  return files;
}

function normalizeSourceMap(sources) {
  const normalized = new Map();
  for (const [relativePath, source] of sources) {
    const key = normalizeRelativePath(relativePath);
    if (!key.startsWith('src/')) throw new Error(`dependency source must be under src/: ${key}`);
    if (normalized.has(key)) throw new Error(`duplicate dependency source ${key}`);
    normalized.set(key, source);
  }
  if (normalized.size === 0) throw new Error('dependency direction source map must not be empty');
  return normalized;
}

function validateRegistryShape(registry, { requireTests = true } = {}) {
  if (registry?.version !== 1 || !Array.isArray(registry.layers) || registry.layers.length === 0) {
    throw new Error('dependency direction registry must be version 1 with non-empty layers');
  }
  if (
    !isRecord(registry.aliases)
    || !Array.isArray(registry.allowedDirections)
    || registry.allowedDirections.length === 0
    || !Array.isArray(registry.sourceExclusions)
    || !Array.isArray(registry.exemptions)
  ) {
    throw new Error('dependency direction registry aliases, directions, or exemptions are invalid');
  }
  const sourceExclusionPaths = new Set();
  for (const exclusion of registry.sourceExclusions) {
    if (
      !isRecord(exclusion)
      || typeof exclusion.path !== 'string'
      || typeof exclusion.owner !== 'string'
      || typeof exclusion.reason !== 'string'
      || !exclusion.path.trim()
      || !exclusion.owner.trim()
      || !exclusion.reason.trim()
    ) {
      throw new Error('dependency source exclusion must contain exact path, owner, and reason');
    }
    const relativePath = normalizeRelativePath(exclusion.path);
    if (!relativePath.startsWith('src/') || !isTestOnlyDirectoryPath(relativePath)) {
      throw new Error(`dependency source exclusion is not a test-only file: ${relativePath}`);
    }
    if (sourceExclusionPaths.has(relativePath)) throw new Error(`duplicate dependency source exclusion ${relativePath}`);
    sourceExclusionPaths.add(relativePath);
  }
  const layerNames = new Set();
  for (const layer of registry.layers) {
    if (!layer?.name || !Number.isInteger(layer.rank) || !Array.isArray(layer.prefixes) || layer.prefixes.length === 0) {
      throw new Error('dependency layer registration is incomplete');
    }
    if (layerNames.has(layer.name)) throw new Error(`duplicate dependency layer ${layer.name}`);
    layerNames.add(layer.name);
  }
  if (!layerNames.has(registry.rootLayer)) throw new Error('dependency rootLayer must name a registered layer');
  const layerByName = new Map(registry.layers.map((layer) => [layer.name, layer]));
  const directions = new Set();
  for (const direction of registry.allowedDirections) {
    if (typeof direction !== 'string' || !direction.includes('->') || directions.has(direction)) {
      throw new Error(`dependency allowed direction is invalid or duplicate: ${direction}`);
    }
    directions.add(direction);
    const [from, to] = direction.split('->');
    const fromLayer = layerByName.get(from);
    const toLayer = layerByName.get(to);
    if (!fromLayer || !toLayer || (fromLayer.rank <= toLayer.rank && direction !== 'app->devtools')) {
      throw new Error(`dependency allowed direction violates layer ranks: ${direction}`);
    }
  }
  for (const exemption of registry.exemptions) validateExemption(exemption);
  if (requireTests && (!Array.isArray(registry.caseIds) || registry.caseIds.length === 0)) {
    throw new Error('dependency direction registry must register at least one regression case');
  }
}

function validateExemption(exemption) {
  const fields = ['from', 'kind', 'specifier', 'to', 'expiresOn', 'owner', 'reason'];
  if (!isRecord(exemption) || fields.some((field) => typeof exemption[field] !== 'string' || !exemption[field].trim())) {
    throw new Error('dependency exemption must contain exact edge, expiry, and reason');
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(exemption.expiresOn)) {
    throw new Error(`dependency exemption has invalid expiry ${exemption.expiresOn}`);
  }
  if (!['import', 'dynamic-import', 're-export', 'require'].includes(exemption.kind)) {
    throw new Error(`dependency exemption has invalid kind ${exemption.kind}`);
  }
}

function isTestOnlyDirectoryPath(relativePath) {
  const segments = relativePath.split('/');
  return segments.includes('__tests__') || segments.includes('test-utils');
}

function discoverCaseIds(source, filePath, prefix) {
  const ids = [];
  walkNode(parseModule(source, filePath), (node) => {
    if (node.type !== 'CallExpression' || !['it', 'test'].includes(identifierName(node.callee))) return;
    const title = stringValue(node.arguments[0]);
    if (title?.startsWith(`[${prefix}`)) ids.push(title.slice(1, title.indexOf(']')));
  });
  return [...new Set(ids)].sort();
}

function parseModule(source, filePath) {
  try {
    return parse(source, { sourceType: 'module', plugins: ['jsx', 'typescript', 'dynamicImport'] });
  } catch (error) {
    throw new Error(`parse ${filePath}: ${error.message}`);
  }
}

function walkNode(node, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node);
  for (const [key, value] of Object.entries(node)) {
    if (['loc', 'start', 'end', 'extra'].includes(key)) continue;
    if (Array.isArray(value)) value.forEach((child) => walkNode(child, visitor));
    else if (value && typeof value === 'object' && typeof value.type === 'string') walkNode(value, visitor);
  }
}

function dependencyKey(value) {
  return `${value.from}|${value.kind}|${value.specifier}|${value.to}`;
}

function isLocalSpecifier(specifier, aliases) {
  return specifier === '<non-literal>'
    || specifier.startsWith('.')
    || Object.keys(aliases).some((prefix) => specifier.startsWith(prefix));
}

function identifierName(node) {
  return node?.type === 'Identifier' ? node.name : '';
}

function stringValue(node) {
  if (node?.type === 'StringLiteral' || node?.type === 'Literal') return node.value;
  if (node?.type === 'TemplateLiteral' && node.expressions.length === 0) return node.quasis[0]?.value?.cooked;
  return '';
}

function currentDate() {
  return new Date().toISOString().slice(0, 10);
}

function normalizeRelativePath(value) {
  const normalized = path.posix.normalize(String(value).split(path.sep).join('/'));
  if (!normalized || path.posix.isAbsolute(normalized) || normalized.startsWith('../')) {
    throw new Error(`path must be app-relative: ${value}`);
  }
  return normalized;
}

function readJSON(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    throw new Error(`read ${path.basename(filePath)}: ${error.message}`);
  }
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

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const summary = validateFrontendDependencyDirection();
  console.log(
    `frontend dependency direction guard passed`
    + ` (${summary.checkedImports} local edges, ${summary.exemptionCount} expiring exemptions)`,
  );
}
