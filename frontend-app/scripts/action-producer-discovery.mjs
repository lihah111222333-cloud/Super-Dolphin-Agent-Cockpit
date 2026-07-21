import fs from 'node:fs';
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

export function parseModule(source, file) {
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

function uiEntrypointBinding(callPath) {
  let component = '';
  let event = '';
  for (let current = callPath; current; current = current.parentPath) {
    const node = current.node;
    if (!event && node?.type === 'JSXAttribute' && node.name?.type === 'JSXIdentifier' && /^on[A-Z]/.test(node.name.name)) {
      event = node.name.name;
    }
    if (!component && node?.type === 'FunctionDeclaration' && node.id?.name) component = node.id.name;
    if (component && event) return { component, event };
  }
  return undefined;
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
          const uiEntrypoint = uiEntrypointBinding(callPath);
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
            uiEntrypoint,
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

export function callsiteKey(callsite) {
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

export {
  discoverActionProducers,
  discoverP0P1Callsites,
};
