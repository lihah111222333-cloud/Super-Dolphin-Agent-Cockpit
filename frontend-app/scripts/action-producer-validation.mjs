import fs from 'node:fs';
import path from 'node:path';
import { callsiteKey, discoverActionProducers, discoverP0P1Callsites, parseModule } from './action-producer-discovery.mjs';

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

function walk(node, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach((item) => walk(item, visitor));
    else if (value && typeof value === 'object' && typeof value.type === 'string') walk(value, visitor);
  }
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

function validateUiEntrypoints(discovery, matrix, problems) {
  if (!Array.isArray(matrix.uiEntrypoints)) {
    problems.push('action producer uiEntrypoints must be an array');
    return;
  }
  const expected = new Set();
  for (const anchor of matrix.uiEntrypoints) {
    if (!anchor || typeof anchor.actionId !== 'string' || typeof anchor.sourcePath !== 'string'
      || typeof anchor.component !== 'string' || typeof anchor.event !== 'string'
      || typeof anchor.handler !== 'string') {
      problems.push('action producer UI entrypoint anchor is invalid');
      continue;
    }
    const key = [anchor.actionId, anchor.sourcePath, anchor.component, anchor.event, anchor.handler].join('\n');
    if (expected.has(key)) problems.push(`duplicate UI entrypoint anchor: ${anchor.actionId}`);
    expected.add(key);
    const binding = discovery.bindings.find((entry) => (
      entry.actionId === anchor.actionId
      && entry.kind === 'user'
      && entry.sourcePath === anchor.sourcePath
      && entry.uiEntrypoint?.component === anchor.component
      && entry.uiEntrypoint?.event === anchor.event
      && entry.handlers.includes(anchor.handler)
    ));
    if (!binding) problems.push(`${anchor.actionId} UI entrypoint has no canonical user action binding`);
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
  if (matrix.schemaVersion !== 2 || !Array.isArray(matrix.cells) || !Array.isArray(matrix.cases) || !Array.isArray(matrix.rpcCallsites) || !Array.isArray(matrix.uiEntrypoints)) {
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

export function runActionProducerValidation({ root, registry, testMatrix, today }) {
  const discovery = discoverActionProducers(root);
  const problems = [...discovery.problems];
  const entries = registryEntries(registry);
  validateExactDiff(discovery.counts, discovery.kinds, entries, problems);
  validateCoveredContracts(registry.coveredProducers, problems);
  const matrixShapeValid = testMatrix.schemaVersion === 2
    && Array.isArray(testMatrix.cells)
    && Array.isArray(testMatrix.cases)
    && Array.isArray(testMatrix.rpcCallsites)
    && Array.isArray(testMatrix.uiEntrypoints);
  if (!matrixShapeValid) problems.push('action producer test matrix schema is invalid');
  const cases = matrixShapeValid ? validateEvidenceCases(root, discovery, testMatrix, problems) : new Map();
  if (matrixShapeValid) {
    validateProducerErrorMatrix(registry, discovery, testMatrix, cases, problems);
    validateRpcCallsites(root, registry, testMatrix, problems);
    validateRuntimeBindings(root, discovery, testMatrix, problems);
    validateUiEntrypoints(discovery, testMatrix, problems);
  }
  validateExemptions(registry.exemptions, today, problems);
  const expectedBindingCount = [...discovery.counts.values()].reduce((sum, count) => sum + count, 0);
  if (discovery.bindings.length !== expectedBindingCount) {
    problems.push(`production action binding count mismatch: discovered=${expectedBindingCount} bound=${discovery.bindings.length}`);
  }
  if (problems.length > 0) throw new Error(`action producer guard failed:\n- ${problems.join('\n- ')}`);
  return discovery;
}
