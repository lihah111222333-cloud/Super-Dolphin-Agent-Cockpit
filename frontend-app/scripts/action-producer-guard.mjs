import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { parse } from '@babel/parser';
import traverseModule from '@babel/traverse';

const traverse = traverseModule.default || traverseModule;

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const REGISTRY_PATH = path.join(ROOT, 'config/action-producer-registry.json');

function productionSourceFiles(root = ROOT) {
  const sourceRoot = path.join(root, 'src');
  const files = [];
  const visit = (directory) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const absolute = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== '__tests__') visit(absolute);
      } else if (/\.(?:js|jsx)$/.test(entry.name) && !/\.test\.(?:js|jsx)$/.test(entry.name)) {
        files.push(absolute);
      }
    }
  };
  visit(sourceRoot);
  return files.sort();
}

function walk(node, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach((item) => walk(item, visitor));
    else if (value && typeof value === 'object' && typeof value.type === 'string') walk(value, visitor);
  }
}

function parseModule(source, file) {
  return parse(source, { sourceType: 'module', plugins: ['jsx'], sourceFilename: file });
}

function nodeName(node) {
  if (node?.type === 'Identifier' || node?.type === 'StringLiteral') return node.name || node.value;
  return '';
}

function resolveModule(fromFile, request, files) {
  if (typeof request !== 'string' || !request.startsWith('.')) return '';
  const unresolved = path.resolve(path.dirname(fromFile), request);
  const candidates = [unresolved, `${unresolved}.js`, `${unresolved}.jsx`, path.join(unresolved, 'index.js'), path.join(unresolved, 'index.jsx')];
  return candidates.find((candidate) => files.has(candidate)) || '';
}

function actionExportsByFile(asts, canonicalFile) {
  const exportsByFile = new Map([...asts.keys()].map((file) => [file, new Set()]));
  if (!exportsByFile.has(canonicalFile)) throw new Error('canonical runUIAction module is missing');
  exportsByFile.get(canonicalFile).add('runUIAction');
  let changed = true;
  while (changed) {
    changed = false;
    for (const [file, ast] of asts) {
      const localImports = new Map();
      for (const statement of ast.program.body) {
        if (statement.type !== 'ImportDeclaration') continue;
        const sourceFile = resolveModule(file, statement.source.value, asts);
        const sourceExports = exportsByFile.get(sourceFile);
        if (!sourceExports) continue;
        for (const specifier of statement.specifiers) {
          if (specifier.type === 'ImportSpecifier' && sourceExports.has(nodeName(specifier.imported))) {
            localImports.set(specifier.local.name, true);
          } else if (specifier.type === 'ImportDefaultSpecifier' && sourceExports.has('default')) {
            localImports.set(specifier.local.name, true);
          }
        }
      }
      const fileExports = exportsByFile.get(file);
      for (const statement of ast.program.body) {
        if (statement.type === 'ExportAllDeclaration') {
          const sourceFile = resolveModule(file, statement.source.value, asts);
          for (const exportedName of exportsByFile.get(sourceFile) || []) {
            if (!fileExports.has(exportedName)) {
              fileExports.add(exportedName);
              changed = true;
            }
          }
          continue;
        }
        if (statement.type !== 'ExportNamedDeclaration') continue;
        const sourceFile = statement.source ? resolveModule(file, statement.source.value, asts) : '';
        const sourceExports = exportsByFile.get(sourceFile);
        for (const specifier of statement.specifiers) {
          if (specifier.type !== 'ExportSpecifier') continue;
          const localName = nodeName(specifier.local);
          const exportedName = nodeName(specifier.exported);
          const actionBinding = sourceExports ? sourceExports.has(localName) : localImports.has(localName);
          if (actionBinding && !fileExports.has(exportedName)) {
            fileExports.add(exportedName);
            changed = true;
          }
        }
      }
    }
  }
  return exportsByFile;
}

function discoverActionProducers(root = ROOT) {
  const counts = new Map();
  const problems = [];
  const files = new Set(productionSourceFiles(root));
  const asts = new Map([...files].map((file) => {
    const relative = path.relative(root, file).split(path.sep).join('/');
    return [file, parseModule(fs.readFileSync(file, 'utf8'), relative)];
  }));
  const canonicalFile = path.join(root, 'src/shared/ui/runUIAction.js');
  const exportsByFile = actionExportsByFile(asts, canonicalFile);
  for (const [file, ast] of asts) {
    const actionImportNodes = new Set();
    const namespaceImports = new Map();
    for (const statement of ast.program.body) {
      if (statement.type !== 'ImportDeclaration') continue;
      const sourceFile = resolveModule(file, statement.source.value, files);
      const sourceExports = exportsByFile.get(sourceFile);
      if (!sourceExports || sourceExports.size === 0) continue;
      for (const specifier of statement.specifiers) {
        if (specifier.type === 'ImportSpecifier' && sourceExports.has(nodeName(specifier.imported))) {
          actionImportNodes.add(specifier);
        } else if (specifier.type === 'ImportDefaultSpecifier' && sourceExports.has('default')) {
          actionImportNodes.add(specifier);
        } else if (specifier.type === 'ImportNamespaceSpecifier') {
          namespaceImports.set(specifier, sourceExports);
        }
      }
    }
    const relative = path.relative(root, file).split(path.sep).join('/');
    traverse(ast, {
      CallExpression(callPath) {
        const { callee } = callPath.node;
        let isActionBinding = false;
        if (callee?.type === 'Identifier') {
          const binding = callPath.scope.getBinding(callee.name);
          isActionBinding = Boolean(binding && actionImportNodes.has(binding.path.node));
        } else if (callee?.type === 'MemberExpression' && !callee.computed && callee.object?.type === 'Identifier') {
          const binding = callPath.scope.getBinding(callee.object.name);
          const namespaceExports = binding ? namespaceImports.get(binding.path.node) : undefined;
          isActionBinding = Boolean(namespaceExports?.has(nodeName(callee.property)));
        }
        if (!isActionBinding) return;
        const actionId = callPath.node.arguments[0];
        if (actionId?.type !== 'StringLiteral' || !actionId.value.trim()) {
          problems.push(`${relative}:${callPath.node.loc?.start.line || 0} runUIAction requires a literal actionId`);
          return;
        }
        counts.set(actionId.value, (counts.get(actionId.value) || 0) + 1);
      },
    });
  }
  return { counts, problems };
}

function registryEntries(registry) {
  const entries = [...registry.coveredProducers, ...registry.exemptions];
  const duplicate = entries.find((entry, index) => entries.findIndex((item) => item.actionId === entry.actionId) !== index);
  if (duplicate) throw new Error(`duplicate action registry entry: ${duplicate.actionId}`);
  return entries;
}

function discoveredTestNames(source, file) {
  const names = new Set();
  walk(parseModule(source, file), (node) => {
    if (node.type !== 'CallExpression' || !['it', 'test'].includes(node.callee?.name)) return;
    if (node.arguments[0]?.type === 'StringLiteral') names.add(node.arguments[0].value);
  });
  return names;
}

function validateCoveredTests(root, covered, problems) {
  for (const entry of covered) {
    if (!Array.isArray(entry.tests) || entry.tests.length === 0) {
      problems.push(`${entry.actionId} has zero registered failure tests`);
      continue;
    }
    for (const registered of entry.tests) {
      const file = path.join(root, registered.file || '');
      if (!registered.file || !fs.existsSync(file)) {
        problems.push(`${entry.actionId} test file is missing: ${registered.file || '<empty>'}`);
        continue;
      }
      if (!Array.isArray(registered.names) || registered.names.length === 0) {
        problems.push(`${entry.actionId} has zero registered failure tests in ${registered.file}`);
        continue;
      }
      const names = discoveredTestNames(fs.readFileSync(file, 'utf8'), registered.file);
      for (const name of registered.names) {
        if (!names.has(name)) problems.push(`${entry.actionId} registered test is stale: ${name}`);
      }
    }
  }
}

function validateExemptions(exemptions, today, problems) {
  for (const entry of exemptions) {
    if (entry.owner !== 'Task2B' || typeof entry.reason !== 'string' || entry.reason.trim().length < 20) {
      problems.push(`${entry.actionId} exemption must have a narrow Task2B owner and reason`);
    }
    if (!/^\d{4}-\d{2}-\d{2}$/.test(entry.expires) || entry.expires <= today) {
      problems.push(`${entry.actionId} exemption is expired or invalid: ${entry.expires}`);
    }
  }
}

function validateExactDiff(discovered, entries, problems) {
  const registered = new Map(entries.map((entry) => [entry.actionId, entry.producerCount]));
  for (const [actionId, count] of discovered) {
    if (!registered.has(actionId)) problems.push(`missing action producer registry entry: ${actionId}`);
    else if (registered.get(actionId) !== count) problems.push(`producer count mismatch for ${actionId}: discovered=${count} registered=${registered.get(actionId)}`);
  }
  for (const actionId of registered.keys()) {
    if (!discovered.has(actionId)) problems.push(`stale action producer registry entry: ${actionId}`);
  }
}

function loadRegistry(registryPath = REGISTRY_PATH) {
  const registry = JSON.parse(fs.readFileSync(registryPath, 'utf8'));
  if (registry.schemaVersion !== 1 || !Array.isArray(registry.coveredProducers) || !Array.isArray(registry.exemptions)) {
    throw new Error('action producer registry schema is invalid');
  }
  return registry;
}

export function runActionProducerGuard({ root = ROOT, registry = loadRegistry(), today = new Date().toISOString().slice(0, 10) } = {}) {
  const discovery = discoverActionProducers(root);
  const problems = [...discovery.problems];
  const entries = registryEntries(registry);
  validateExactDiff(discovery.counts, entries, problems);
  validateCoveredTests(root, registry.coveredProducers, problems);
  validateExemptions(registry.exemptions, today, problems);
  if (problems.length > 0) throw new Error(`action producer guard failed:\n- ${problems.join('\n- ')}`);
  return { covered: registry.coveredProducers.length, discovered: discovery.counts.size, exempted: registry.exemptions.length };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const result = runActionProducerGuard();
  process.stdout.write(`action producer guard passed: discovered=${result.discovered} covered=${result.covered} exempted=${result.exempted}\n`);
}

export { discoverActionProducers };
