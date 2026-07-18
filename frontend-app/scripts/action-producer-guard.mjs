import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { parse } from '@babel/parser';
import traverseModule from '@babel/traverse';
import { parseContractMatrixForTest } from './rpc-contract-audit.mjs';

const traverse = traverseModule.default || traverseModule;

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const REGISTRY_PATH = path.join(ROOT, 'config/action-producer-registry.json');
const TEST_MATRIX_PATH = path.join(ROOT, 'config/action-producer-test-matrix.json');
const RPC_CONTRACT_MATRIX_PATH = path.join(ROOT, 'src/shared/api/backendApi.contractMatrix.js');
const BACKEND_API_PATH = path.join(ROOT, 'src/shared/api/backendApi.js');
const SESSION_API_PATH = path.join(ROOT, 'src/shared/api/sessionApi.js');
const SESSION_FACADES = new Map([
  ['fork', 'forkThread'],
  ['interrupt', 'interruptTurn'],
  ['messages', 'getThreadMessages'],
  ['start', 'startThread'],
  ['startTurn', 'startTurn'],
]);
const REQUIRED_SEMANTIC_FAMILIES = new Set([
  'approval-pending',
  'background-reconnect',
  'file',
  'invalid-response-validator',
  'mcp',
  'prompt-history',
  'settings-save',
  'skill',
  'thread-mutation',
]);

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

function memberName(node) {
  if (node?.type === 'Identifier') return node.name;
  if (node?.type === 'ThisExpression') return 'this';
  if (['MemberExpression', 'OptionalMemberExpression'].includes(node?.type) && !node.computed) {
    const object = memberName(node.object);
    const property = nodeName(node.property);
    return object && property ? `${object}.${property}` : '';
  }
  return '';
}

function callbackBinding(node) {
  if (!node) return undefined;
  if (node.type === 'Identifier') return { callbackKind: 'identifier', handlers: [node.name] };
  if (['MemberExpression', 'OptionalMemberExpression'].includes(node.type)) {
    const handler = memberName(node);
    return handler ? { callbackKind: 'member', handlers: [handler] } : undefined;
  }
  if (!['ArrowFunctionExpression', 'FunctionExpression'].includes(node.type)) return undefined;
  const handlers = new Set();
  walk(node.body, (candidate) => {
    if (!['CallExpression', 'OptionalCallExpression'].includes(candidate.type)) return;
    const handler = memberName(candidate.callee);
    if (handler) handlers.add(handler);
  });
  return {
    callbackKind: node.type === 'ArrowFunctionExpression' ? 'arrow' : 'function',
    handlers: [...handlers].sort(),
  };
}

const genericActionHandlers = new Set(['runUIAction', 'runBackgroundAction']);

export function mutateProductionBindingsSource(source, bindings) {
  let mutated = source;
  for (const binding of [...bindings].sort((left, right) => right.callbackStart - left.callbackStart)) {
    if (!Number.isInteger(binding.callbackStart) || !Number.isInteger(binding.callbackEnd)
      || binding.callbackStart < 0 || binding.callbackEnd <= binding.callbackStart
      || binding.callbackEnd > mutated.length) {
      throw new Error(`${binding.actionId}: production callback range is invalid`);
    }
    mutated = `${mutated.slice(0, binding.callbackStart)}() => {}${mutated.slice(binding.callbackEnd)}`;
  }
  return mutated;
}

function focusedBindingCallback(source, binding) {
  const ast = parseModule(source, binding.sourcePath);
  let callback;
  traverse(ast, {
    CallExpression(callPath) {
      const actionId = callPath.node.arguments[0];
      if (actionId?.type !== 'StringLiteral' || actionId.value !== binding.actionId
        || actionId.loc?.start.line !== binding.line
        || (actionId.loc?.start.column || 0) + 1 !== binding.column) return;
      callback = callbackBinding(callPath.node.arguments[1]);
      callPath.stop();
    },
  });
  return callback;
}

export function productionBindingGuardMutationDetection(binding, root = ROOT) {
  const source = fs.readFileSync(path.join(root, binding.sourcePath), 'utf8');
  const mutated = mutateProductionBindingsSource(source, [binding]);
  const callback = focusedBindingCallback(mutated, binding);
  if (callback?.handlers.some((handler) => !genericActionHandlers.has(handler))) {
    throw new Error(`${binding.actionId}: empty callback mutation did not make the focused guard RED`);
  }
  return {
    mutationId: 'empty-production-callback',
    detected: true,
    sourceSha256: createHash('sha256').update(source).digest('hex'),
    mutatedSha256: createHash('sha256').update(mutated).digest('hex'),
  };
}

function resolveModule(fromFile, request, files) {
  if (typeof request !== 'string' || !request.startsWith('.')) return '';
  const unresolved = path.resolve(path.dirname(fromFile), request);
  const candidates = [unresolved, `${unresolved}.js`, `${unresolved}.jsx`, path.join(unresolved, 'index.js'), path.join(unresolved, 'index.jsx')];
  return candidates.find((candidate) => files.has(candidate)) || '';
}

function actionExportsByFile(asts, canonicalFile) {
  const exportsByFile = new Map([...asts.keys()].map((file) => [file, new Map()]));
  if (!exportsByFile.has(canonicalFile)) throw new Error('canonical action module is missing');
  exportsByFile.get(canonicalFile).set('runUIAction', 'user');
  exportsByFile.get(canonicalFile).set('runBackgroundAction', 'background');
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
            localImports.set(specifier.local.name, sourceExports.get(nodeName(specifier.imported)));
          } else if (specifier.type === 'ImportDefaultSpecifier' && sourceExports.has('default')) {
            localImports.set(specifier.local.name, sourceExports.get('default'));
          }
        }
      }
      const fileExports = exportsByFile.get(file);
      for (const statement of ast.program.body) {
        if (statement.type === 'ExportAllDeclaration') {
          const sourceFile = resolveModule(file, statement.source.value, asts);
          for (const [exportedName, kind] of exportsByFile.get(sourceFile) || []) {
            if (!fileExports.has(exportedName)) {
              fileExports.set(exportedName, kind);
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
          const actionKind = sourceExports ? sourceExports.get(localName) : localImports.get(localName);
          if (actionKind && !fileExports.has(exportedName)) {
            fileExports.set(exportedName, actionKind);
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
  const kinds = new Map();
  const locations = new Map();
  const unsuccessfulResultActions = new Set();
  const bindings = [];
  const problems = [];
  const files = new Set(productionSourceFiles(root));
  const asts = new Map([...files].map((file) => {
    const relative = path.relative(root, file).split(path.sep).join('/');
    return [file, parseModule(fs.readFileSync(file, 'utf8'), relative)];
  }));
  const canonicalFile = path.join(root, 'src/shared/ui/runUIAction.js');
  const exportsByFile = actionExportsByFile(asts, canonicalFile);
  for (const [file, ast] of asts) {
    const actionImportNodes = new Map();
    const namespaceImports = new Map();
    for (const statement of ast.program.body) {
      if (statement.type !== 'ImportDeclaration') continue;
      const sourceFile = resolveModule(file, statement.source.value, files);
      const sourceExports = exportsByFile.get(sourceFile);
      if (!sourceExports || sourceExports.size === 0) continue;
      for (const specifier of statement.specifiers) {
        if (specifier.type === 'ImportSpecifier' && sourceExports.has(nodeName(specifier.imported))) {
          actionImportNodes.set(specifier, sourceExports.get(nodeName(specifier.imported)));
        } else if (specifier.type === 'ImportDefaultSpecifier' && sourceExports.has('default')) {
          actionImportNodes.set(specifier, sourceExports.get('default'));
        } else if (specifier.type === 'ImportNamespaceSpecifier') {
          namespaceImports.set(specifier, sourceExports);
        }
      }
    }
    const relative = path.relative(root, file).split(path.sep).join('/');
    traverse(ast, {
      CallExpression(callPath) {
        const { callee } = callPath.node;
        let actionKind = '';
        if (callee?.type === 'Identifier') {
          const binding = callPath.scope.getBinding(callee.name);
          actionKind = binding ? actionImportNodes.get(binding.path.node) || '' : '';
        } else if (callee?.type === 'MemberExpression' && !callee.computed && callee.object?.type === 'Identifier') {
          const binding = callPath.scope.getBinding(callee.object.name);
          const namespaceExports = binding ? namespaceImports.get(binding.path.node) : undefined;
          actionKind = namespaceExports?.get(nodeName(callee.property)) || '';
        }
        if (!actionKind) return;
        const actionId = callPath.node.arguments[0];
        if (actionId?.type !== 'StringLiteral' || !actionId.value.trim()) {
          problems.push(`${relative}:${callPath.node.loc?.start.line || 0} action producer requires a literal actionId`);
          return;
        }
        counts.set(actionId.value, (counts.get(actionId.value) || 0) + 1);
        if (kinds.has(actionId.value) && kinds.get(actionId.value) !== actionKind) {
          problems.push(`${relative}:${callPath.node.loc?.start.line || 0} action producer kind conflicts for ${actionId.value}`);
        }
        kinds.set(actionId.value, actionKind);
        const actionLocations = locations.get(actionId.value) || [];
        actionLocations.push(`${relative}:${callPath.node.loc?.start.line || 0}`);
        locations.set(actionId.value, actionLocations);
        const callback = callbackBinding(callPath.node.arguments[1]);
        if (!callback || callback.handlers.length === 0
          || callback.handlers.every((handler) => genericActionHandlers.has(handler))) {
          problems.push(`${relative}:${callPath.node.loc?.start.line || 0} ${actionId.value} requires an action-specific production callback`);
        } else {
          bindings.push({
            actionId: actionId.value,
            kind: actionKind,
            sourcePath: relative,
            line: actionId.loc?.start.line || 0,
            column: (actionId.loc?.start.column || 0) + 1,
            callbackKind: callback.callbackKind,
            handlers: callback.handlers,
            callbackStart: callPath.node.arguments[1].start,
            callbackEnd: callPath.node.arguments[1].end,
            sourceSha256: createHash('sha256').update(fs.readFileSync(file)).digest('hex'),
          });
        }
        const options = callPath.node.arguments[2];
        if (options?.type === 'ObjectExpression' && options.properties.some((property) => (
          property.type === 'ObjectProperty'
          && nodeName(property.key) === 'rejectFalse'
          && property.value?.type === 'BooleanLiteral'
          && property.value.value === true
        ))) unsuccessfulResultActions.add(actionId.value);
      },
    });
  }
  return { counts, kinds, locations, bindings, problems, unsuccessfulResultActions };
}

function p0P1Facades(root) {
  const matrixPath = path.join(root, path.relative(ROOT, RPC_CONTRACT_MATRIX_PATH));
  if (!fs.existsSync(matrixPath)) return new Map();
  return new Map(parseContractMatrixForTest(fs.readFileSync(matrixPath, 'utf8'))
    .filter((entry) => entry.level === 'P0' || entry.level === 'P1')
    .map((entry) => [entry.facade, entry.level]));
}

function callsiteKey(callsite) {
  return `${callsite.file}\n${callsite.via}\n${callsite.facade}`;
}

function discoverP0P1Callsites(root = ROOT) {
  const files = new Set(productionSourceFiles(root));
  const backendApiPath = path.join(root, path.relative(ROOT, BACKEND_API_PATH));
  const sessionApiPath = path.join(root, path.relative(ROOT, SESSION_API_PATH));
  const facadeLevels = p0P1Facades(root);
  const discovered = new Map();
  for (const file of files) {
    if (file === backendApiPath || file === sessionApiPath) continue;
    const relative = path.relative(root, file).split(path.sep).join('/');
    const ast = parseModule(fs.readFileSync(file, 'utf8'), relative);
    const backendBindings = new Map();
    const backendNamespaces = new Set();
    const sessionBindings = new Set();
    for (const statement of ast.program.body) {
      if (statement.type !== 'ImportDeclaration') continue;
      const sourceFile = resolveModule(file, statement.source.value, files);
      for (const specifier of statement.specifiers) {
        if (sourceFile === backendApiPath && specifier.type === 'ImportSpecifier') {
          backendBindings.set(specifier.local.name, nodeName(specifier.imported));
        } else if (sourceFile === backendApiPath && specifier.type === 'ImportNamespaceSpecifier') {
          backendNamespaces.add(specifier.local.name);
        } else if (sourceFile === sessionApiPath && specifier.type === 'ImportSpecifier' && nodeName(specifier.imported) === 'sessionApi') {
          sessionBindings.add(specifier.local.name);
        }
      }
    }
    traverse(ast, {
      CallExpression(callPath) {
        const { callee } = callPath.node;
        let facade = '';
        let via = '';
        if (callee?.type === 'Identifier') {
          const binding = callPath.scope.getBinding(callee.name);
          if (binding?.path.node.type === 'ImportSpecifier') {
            facade = backendBindings.get(callee.name) || '';
            via = facade ? 'backendApi' : '';
          }
        } else if (callee?.type === 'MemberExpression' && !callee.computed && callee.object?.type === 'Identifier') {
          const binding = callPath.scope.getBinding(callee.object.name);
          if (binding?.path.node.type === 'ImportNamespaceSpecifier' && backendNamespaces.has(callee.object.name)) {
            facade = nodeName(callee.property);
            via = 'backendApi';
          } else if (binding?.path.node.type === 'ImportSpecifier' && sessionBindings.has(callee.object.name)) {
            facade = SESSION_FACADES.get(nodeName(callee.property)) || '';
            via = facade ? 'sessionApi' : '';
          }
        }
        const level = facadeLevels.get(facade);
        if (!level) return;
        const callsite = { file: relative, facade, via, level, count: 1 };
        const key = callsiteKey(callsite);
        const current = discovered.get(key);
        if (current) current.count += 1;
        else discovered.set(key, callsite);
      },
    });
  }
  return discovered;
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

function resolveImportedModule(fromFile, request) {
  if (typeof request !== 'string' || !request.startsWith('.')) return '';
  const unresolved = path.resolve(path.dirname(fromFile), request);
  return [
    unresolved,
    `${unresolved}.js`,
    `${unresolved}.jsx`,
    `${unresolved}.mjs`,
    path.join(unresolved, 'index.js'),
    path.join(unresolved, 'index.jsx'),
  ].find((candidate) => fs.existsSync(candidate) && fs.statSync(candidate).isFile()) || '';
}

function testReachesProductionSource(root, testFile, sourcePath) {
  const target = path.resolve(root, sourcePath);
  const queue = [path.resolve(root, testFile)];
  const visited = new Set();
  while (queue.length > 0) {
    const file = queue.shift();
    if (file === target) return true;
    if (visited.has(file) || !fs.existsSync(file)) continue;
    visited.add(file);
    const ast = parseModule(fs.readFileSync(file, 'utf8'), path.relative(root, file));
    for (const statement of ast.program.body) {
      if (statement.type !== 'ImportDeclaration') continue;
      const imported = resolveImportedModule(file, statement.source.value);
      if (imported && !visited.has(imported)) queue.push(imported);
    }
  }
  return false;
}

function validateRuntimeBindings(root, discovery, matrix, problems) {
  if (!Array.isArray(matrix.runtimeBindings)) {
    problems.push('action producer runtimeBindings must be an array');
    return;
  }
  const seen = new Set();
  const seenSemanticClasses = new Set();
  for (const anchor of matrix.runtimeBindings) {
    if (!anchor || typeof anchor.semanticClass !== 'string' || !anchor.semanticClass.trim()
      || typeof anchor.actionId !== 'string' || typeof anchor.sourcePath !== 'string'
      || typeof anchor.testFile !== 'string' || typeof anchor.testName !== 'string') {
      problems.push('action producer runtime binding anchor is invalid');
      continue;
    }
    if (seen.has(anchor.actionId)) problems.push(`duplicate runtime binding anchor: ${anchor.actionId}`);
    seen.add(anchor.actionId);
    if (seenSemanticClasses.has(anchor.semanticClass)) {
      problems.push(`duplicate runtime binding semantic class: ${anchor.semanticClass}`);
    }
    seenSemanticClasses.add(anchor.semanticClass);
    const bindings = discovery.bindings.filter(({ actionId, sourcePath }) => (
      actionId === anchor.actionId && sourcePath === anchor.sourcePath
    ));
    if (bindings.length === 0) problems.push(`${anchor.actionId} runtime anchor has no production binding`);
    const testPath = path.join(root, anchor.testFile);
    if (!fs.existsSync(testPath)) {
      problems.push(`${anchor.actionId} runtime test file is missing: ${anchor.testFile}`);
      continue;
    }
    const names = discoveredTestNames(fs.readFileSync(testPath, 'utf8'), anchor.testFile);
    if (!names.has(anchor.testName)) problems.push(`${anchor.actionId} runtime test is stale: ${anchor.testName}`);
    if (!testReachesProductionSource(root, anchor.testFile, anchor.sourcePath)) {
      problems.push(`${anchor.actionId} runtime test does not import its production component/service`);
    }
  }
}

function validateCoveredContracts(covered, problems) {
  for (const entry of covered) {
    if (!['user', 'background'].includes(entry.kind)) problems.push(`${entry.actionId} has invalid producer kind`);
    if (entry.kind === 'user' && entry.visibleSink !== 'ActionFailureSink') {
      problems.push(`${entry.actionId} user action requires ActionFailureSink`);
    }
    if (entry.kind === 'background' && entry.visibleSink !== null) {
      problems.push(`${entry.actionId} background action must not claim a direct visible sink`);
    }
    if (entry.healthSink !== 'frontendHealthStore') problems.push(`${entry.actionId} requires frontendHealthStore`);
    if (!Array.isArray(entry.errorSources) || entry.errorSources.length === 0) {
      problems.push(`${entry.actionId} has zero registered error sources`);
    } else if (new Set(entry.errorSources).size !== entry.errorSources.length) {
      problems.push(`${entry.actionId} has duplicate error sources`);
    }
    if ('tests' in entry) problems.push(`${entry.actionId} must register evidence cases in the test matrix, not generic registry tests`);
  }
}

function producerErrorCells(entries) {
  const cells = new Set();
  for (const entry of entries) {
    if (!Array.isArray(entry.errorSources) || entry.errorSources.length === 0) continue;
    for (const errorSource of entry.errorSources) cells.add(`${entry.actionId}\n${errorSource}`);
  }
  return cells;
}

function validateEvidenceCases(root, discovery, matrix, problems) {
  const cases = new Map();
  const requiredA = new Set([...discovery.counts.keys()].map((actionId) => `a.producer.${actionId}`));
  const actualA = new Set();
  const semanticFamilies = new Set();
  for (const evidenceCase of matrix.cases) {
    if (!evidenceCase || typeof evidenceCase.caseId !== 'string' || !['A', 'B', 'C'].includes(evidenceCase.layer)) {
      problems.push('action producer evidence case is invalid');
      continue;
    }
    if (cases.has(evidenceCase.caseId)) problems.push(`duplicate action evidence case: ${evidenceCase.caseId}`);
    cases.set(evidenceCase.caseId, evidenceCase);
    if (evidenceCase.layer === 'A') {
      actualA.add(evidenceCase.caseId);
      const actionId = evidenceCase.caseId.replace(/^a\.producer\./, '');
      if (evidenceCase.actionId !== actionId) problems.push(`${evidenceCase.caseId} producer actionId mismatch`);
      continue;
    }
    const file = path.join(root, evidenceCase.file || '');
    if (!evidenceCase.file || !fs.existsSync(file)) {
      problems.push(`${evidenceCase.caseId} evidence file is missing: ${evidenceCase.file || '<empty>'}`);
      continue;
    }
    if (typeof evidenceCase.testName !== 'string' || !evidenceCase.testName.trim()) {
      problems.push(`${evidenceCase.caseId} has no exact testName`);
      continue;
    }
    const names = discoveredTestNames(fs.readFileSync(file, 'utf8'), evidenceCase.file);
    if (!names.has(evidenceCase.testName)) problems.push(`${evidenceCase.caseId} evidence test is stale: ${evidenceCase.testName}`);
    if (evidenceCase.layer === 'C') {
      if (typeof evidenceCase.semanticFamily !== 'string' || !evidenceCase.semanticFamily.trim()) {
        problems.push(`${evidenceCase.caseId} C-layer evidence requires semanticFamily`);
      } else semanticFamilies.add(evidenceCase.semanticFamily);
    }
  }
  for (const caseId of requiredA) if (!actualA.has(caseId)) problems.push(`missing A-layer producer evidence: ${caseId}`);
  for (const caseId of actualA) if (!requiredA.has(caseId)) problems.push(`stale A-layer producer evidence: ${caseId}`);
  for (const family of REQUIRED_SEMANTIC_FAMILIES) {
    if (!semanticFamilies.has(family)) problems.push(`missing C-layer semantic family evidence: ${family}`);
  }
  return cases;
}

function validateProducerErrorMatrix(registry, discovery, matrix, cases, problems) {
  if (matrix.schemaVersion !== 2 || !Array.isArray(matrix.cells) || !Array.isArray(matrix.cases) || !Array.isArray(matrix.rpcCallsites)) {
    problems.push('action producer test matrix schema is invalid');
    return;
  }
  const expected = producerErrorCells(registry.coveredProducers);
  const actual = new Set();
  const usedCases = new Set();
  for (const cell of matrix.cells) {
    if (!cell || typeof cell.actionId !== 'string' || typeof cell.errorSource !== 'string' || !Array.isArray(cell.evidence)) {
      problems.push('action producer test matrix cell is invalid');
      continue;
    }
    const key = `${cell.actionId}\n${cell.errorSource}`;
    if (actual.has(key)) problems.push(`duplicate producer error test cell: ${cell.actionId} × ${cell.errorSource}`);
    actual.add(key);
    const requiredProducerCase = `a.producer.${cell.actionId}`;
    if (!cell.evidence.includes(requiredProducerCase)) problems.push(`${cell.actionId} × ${cell.errorSource} is missing A-layer producer evidence`);
    const layers = new Set();
    for (const caseId of cell.evidence) {
      const evidenceCase = cases.get(caseId);
      if (!evidenceCase) problems.push(`${cell.actionId} × ${cell.errorSource} references missing evidence case: ${caseId}`);
      else layers.add(evidenceCase.layer);
      usedCases.add(caseId);
    }
    if (!layers.has('B')) problems.push(`${cell.actionId} × ${cell.errorSource} is missing B-layer wrapper evidence`);
    if (cell.errorSource === 'invalid-response' && !cell.evidence.some((caseId) => cases.get(caseId)?.semanticFamily === 'invalid-response-validator')) {
      problems.push(`${cell.actionId} × invalid-response requires real validator evidence`);
    }
  }
  for (const key of expected) if (!actual.has(key)) problems.push(`missing producer error test cell: ${key.replace('\n', ' × ')}`);
  for (const key of actual) if (!expected.has(key)) problems.push(`stale producer error test cell: ${key.replace('\n', ' × ')}`);
  for (const caseId of cases.keys()) if (!usedCases.has(caseId)) problems.push(`stale unreferenced action evidence case: ${caseId}`);
  for (const actionId of discovery.unsuccessfulResultActions) {
    const entry = registry.coveredProducers.find((item) => item.actionId === actionId);
    if (!entry?.errorSources.includes('unsuccessful-result')) problems.push(`${actionId} rejectFalse requires unsuccessful-result errorSource`);
  }
  for (const entry of registry.coveredProducers) {
    if (entry.errorSources.includes('unsuccessful-result') && !discovery.unsuccessfulResultActions.has(entry.actionId)) {
      problems.push(`${entry.actionId} has stale unsuccessful-result errorSource without rejectFalse`);
    }
  }
}

function validateRpcCallsites(root, registry, matrix, problems) {
  const discovered = discoverP0P1Callsites(root);
  const actual = new Map();
  const actionIds = new Set(registry.coveredProducers.map((entry) => entry.actionId));
  for (const callsite of matrix.rpcCallsites) {
    if (!callsite || typeof callsite.file !== 'string' || !['backendApi', 'sessionApi'].includes(callsite.via)
      || typeof callsite.facade !== 'string' || !['P0', 'P1'].includes(callsite.level)
      || !Number.isInteger(callsite.count) || callsite.count < 1
      || !Array.isArray(callsite.actionIds) || callsite.actionIds.length === 0) {
      problems.push('RPC production callsite manifest entry is invalid');
      continue;
    }
    const key = callsiteKey(callsite);
    if (actual.has(key)) problems.push(`duplicate RPC production callsite manifest entry: ${callsite.file} ${callsite.facade}`);
    actual.set(key, callsite);
    if (new Set(callsite.actionIds).size !== callsite.actionIds.length) problems.push(`${callsite.file} ${callsite.facade} has duplicate actionIds`);
    for (const actionId of callsite.actionIds) {
      if (!actionIds.has(actionId)) problems.push(`${callsite.file} ${callsite.facade} maps to missing canonical actionId: ${actionId}`);
    }
  }
  for (const [key, callsite] of discovered) {
    const registered = actual.get(key);
    if (!registered) problems.push(`missing P0/P1 RPC production callsite: ${callsite.file} ${callsite.via}.${callsite.facade}`);
    else if (registered.count !== callsite.count) problems.push(`P0/P1 RPC callsite count mismatch: ${callsite.file} ${callsite.facade} discovered=${callsite.count} registered=${registered.count}`);
    else if (registered.level !== callsite.level) problems.push(`P0/P1 RPC callsite level mismatch: ${callsite.file} ${callsite.facade} discovered=${callsite.level} registered=${registered.level}`);
  }
  for (const [key, callsite] of actual) {
    if (!discovered.has(key)) problems.push(`stale P0/P1 RPC production callsite: ${callsite.file} ${callsite.via}.${callsite.facade}`);
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

function validateExactDiff(discovered, discoveredKinds, entries, problems) {
  const registered = new Map(entries.map((entry) => [entry.actionId, entry.producerCount]));
  for (const [actionId, count] of discovered) {
    if (!registered.has(actionId)) problems.push(`missing action producer registry entry: ${actionId}`);
    else if (registered.get(actionId) !== count) problems.push(`producer count mismatch for ${actionId}: discovered=${count} registered=${registered.get(actionId)}`);
    const entry = entries.find((item) => item.actionId === actionId);
    if (entry && entry.kind !== discoveredKinds.get(actionId)) problems.push(`producer kind mismatch for ${actionId}: discovered=${discoveredKinds.get(actionId)} registered=${entry.kind}`);
  }
  for (const actionId of registered.keys()) {
    if (!discovered.has(actionId)) problems.push(`stale action producer registry entry: ${actionId}`);
  }
}

function loadRegistry(registryPath = REGISTRY_PATH) {
  const registry = JSON.parse(fs.readFileSync(registryPath, 'utf8'));
  if (registry.schemaVersion !== 2 || !Array.isArray(registry.coveredProducers) || !Array.isArray(registry.exemptions)) {
    throw new Error('action producer registry schema is invalid');
  }
  return registry;
}

function loadTestMatrix(testMatrixPath = TEST_MATRIX_PATH) {
  return JSON.parse(fs.readFileSync(testMatrixPath, 'utf8'));
}

export function runActionProducerGuard({ root = ROOT, registry = loadRegistry(), testMatrix = loadTestMatrix(), today = new Date().toISOString().slice(0, 10) } = {}) {
  const discovery = discoverActionProducers(root);
  const problems = [...discovery.problems];
  const entries = registryEntries(registry);
  validateExactDiff(discovery.counts, discovery.kinds, entries, problems);
  validateCoveredContracts(registry.coveredProducers, problems);
  const matrixShapeValid = testMatrix.schemaVersion === 2
    && Array.isArray(testMatrix.cells)
    && Array.isArray(testMatrix.cases)
    && Array.isArray(testMatrix.rpcCallsites);
  if (!matrixShapeValid) problems.push('action producer test matrix schema is invalid');
  const cases = matrixShapeValid ? validateEvidenceCases(root, discovery, testMatrix, problems) : new Map();
  if (matrixShapeValid) {
    validateProducerErrorMatrix(registry, discovery, testMatrix, cases, problems);
    validateRpcCallsites(root, registry, testMatrix, problems);
    validateRuntimeBindings(root, discovery, testMatrix, problems);
  }
  validateExemptions(registry.exemptions, today, problems);
  const expectedBindingCount = [...discovery.counts.values()].reduce((sum, count) => sum + count, 0);
  if (discovery.bindings.length !== expectedBindingCount) {
    problems.push(`production action binding count mismatch: discovered=${expectedBindingCount} bound=${discovery.bindings.length}`);
  }
  if (problems.length > 0) throw new Error(`action producer guard failed:\n- ${problems.join('\n- ')}`);
  const bindings = discovery.bindings.map((binding) => ({
    ...binding,
    guardMutationDetection: productionBindingGuardMutationDetection(binding, root),
  }));
  return {
    covered: registry.coveredProducers.length,
    discovered: discovery.counts.size,
    exempted: registry.exemptions.length,
    bindings: bindings.sort((left, right) => (
      left.actionId.localeCompare(right.actionId)
      || left.sourcePath.localeCompare(right.sourcePath)
      || left.line - right.line
      || left.column - right.column
    )),
  };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const result = runActionProducerGuard();
  if (process.argv.length === 2) {
    process.stdout.write(`action producer guard passed: discovered=${result.discovered} covered=${result.covered} exempted=${result.exempted}\n`);
  } else if (process.argv.length === 4 && process.argv[2] === '--report') {
    const reportPath = path.resolve(ROOT, process.argv[3]);
    const repoRoot = path.resolve(ROOT, '..');
    const git = (args) => execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
    const report = {
      schemaVersion: 1,
      generatedAt: new Date().toISOString(),
      subjectSha: git(['rev-parse', 'HEAD']),
      subjectTreeSha: git(['rev-parse', 'HEAD^{tree}']),
      reportKind: 'action-production-binding-structure-v1',
      actionIds: [...new Set(result.bindings.map(({ actionId }) => actionId))].sort(),
      bindingCount: result.bindings.length,
      bindings: result.bindings,
      status: 'structural-only',
    };
    fs.mkdirSync(path.dirname(reportPath), { recursive: true });
    fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
    process.stdout.write(`action production binding report passed: actions=${report.actionIds.length} bindings=${report.bindingCount} report=${reportPath}\n`);
  } else {
    throw new Error('usage: action-producer-guard.mjs [--report <path>]');
  }
}

export { discoverActionProducers, discoverP0P1Callsites };
