import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { discoverActionProducers, discoverP0P1Callsites, runActionProducerGuard } from './action-producer-guard.mjs';

const semanticFamilies = [
  'approval-pending', 'background-reconnect', 'file', 'invalid-response-validator', 'mcp',
  'prompt-history', 'settings-save', 'skill', 'thread-mutation',
];
const fixtureRoots = new Set();

function fixture(source = `
  import { startThread } from './shared/api/backendApi.js';
  import { runUIAction } from './shared/ui/runUIAction.js';
  runUIAction('fixture.action', () => startThread());
`) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'action-producer-guard-'));
  fixtureRoots.add(root);
  fs.mkdirSync(path.join(root, 'src/shared/api'), { recursive: true });
  fs.mkdirSync(path.join(root, 'src/shared/ui'), { recursive: true });
  fs.writeFileSync(path.join(root, 'src/shared/ui/runUIAction.js'), 'export function runUIAction() {}\nexport function runBackgroundAction() {}');
  fs.writeFileSync(path.join(root, 'src/shared/api/backendApi.js'), 'export function startThread() {}');
  fs.writeFileSync(path.join(root, 'src/shared/api/sessionApi.js'), 'export const sessionApi = {};');
  fs.writeFileSync(path.join(root, 'src/shared/api/backendApi.contractMatrix.js'), `
    const contract = (...args) => args;
    export const RPC_CONTRACT_REGISTRY = Object.freeze({
      FIXTURE_START: contract('FIXTURE_START', 'fixture/start', 'startThread', 'P0', 'fixture', [], [], false, { responseValidator: 'fixtureResponse' }),
    });
  `);
  fs.writeFileSync(path.join(root, 'src/action.js'), source);
  fs.writeFileSync(path.join(root, 'src/action.test.js'), "import './action.js';\nit('reports the failure', () => {});\nit('validates the semantic path', () => {});");
  return root;
}

function cleanupFixtures() {
  for (const root of fixtureRoots) fs.rmSync(root, { recursive: true, force: true });
  fixtureRoots.clear();
  assert.equal(fixtureRoots.size, 0, 'all mkdtemp fixtures must be released');
}

function coveredRegistry(overrides = {}) {
  return {
    schemaVersion: 2,
    coveredProducers: [{
      actionId: 'fixture.action',
      producerCount: 1,
      kind: 'user',
      owner: 'fixture',
      visibleSink: 'ActionFailureSink',
      healthSink: 'frontendHealthStore',
      errorSources: ['promise-reject'],
      ...overrides,
    }],
    exemptions: [],
  };
}

function evidenceCases() {
  return [
    { caseId: 'a.producer.fixture.action', layer: 'A', actionId: 'fixture.action' },
    { caseId: 'b.wrapper.promise-reject', layer: 'B', file: 'src/action.test.js', testName: 'reports the failure' },
    ...semanticFamilies.map((semanticFamily) => ({
      caseId: `c.${semanticFamily}`,
      layer: 'C',
      semanticFamily,
      file: 'src/action.test.js',
      testName: 'validates the semantic path',
    })),
  ];
}

function matrix(overrides = {}) {
  const cases = evidenceCases();
  return {
    schemaVersion: 2,
    cases,
    cells: [{
      actionId: 'fixture.action',
      errorSource: 'promise-reject',
      evidence: cases.map((entry) => entry.caseId),
    }],
    rpcCallsites: [{
      file: 'src/action.js', via: 'backendApi', facade: 'startThread', level: 'P0', count: 1, actionIds: ['fixture.action'],
    }],
    runtimeBindings: [{
      semanticClass: 'fixture-runtime',
      actionId: 'fixture.action',
      sourcePath: 'src/action.js',
      testFile: 'src/action.test.js',
      testName: 'reports the failure',
    }],
    uiEntrypoints: [],
    ...overrides,
  };
}

function expectFailure(run, text) {
  assert.throws(run, (error) => error.message.includes(text));
}

try {
{
  const root = fixture();
  const result = runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: matrix(), today: '2026-07-17' });
  assert.equal(result.covered, 1);
  assert.equal(result.discovered, 1);
  assert.equal(result.exempted, 0);
  assert.deepEqual(result.bindings.map(({ actionId, sourcePath, callbackKind, handlers }) => ({
    actionId, sourcePath, callbackKind, handlers,
  })), [{
    actionId: 'fixture.action', sourcePath: 'src/action.js', callbackKind: 'arrow', handlers: ['startThread'],
  }]);
  assert.deepEqual(result.bindings[0].guardMutationDetection, {
    mutationId: 'empty-production-callback',
    detected: true,
    sourceSha256: result.bindings[0].sourceSha256,
    mutatedSha256: result.bindings[0].guardMutationDetection.mutatedSha256,
  });
  assert.notEqual(result.bindings[0].guardMutationDetection.mutatedSha256, result.bindings[0].sourceSha256);
}

{
  const root = fixture("import { runUIAction } from './shared/ui/runUIAction.js'; runUIAction(() => task());");
  const discovery = discoverActionProducers(root);
  assert.equal(discovery.problems.length, 1);
  assert.match(discovery.problems[0], /literal actionId/);
}

{
  const root = fixture("import { runUIAction } from './shared/ui/runUIAction.js'; runUIAction('fixture.action', true);");
  const discovery = discoverActionProducers(root);
  assert.match(discovery.problems.join('\n'), /action-specific production callback/);
}

{
  const root = fixture("import { runUIAction } from './shared/ui/runUIAction.js'; runUIAction('fixture.action', runUIAction);");
  const discovery = discoverActionProducers(root);
  assert.match(discovery.problems.join('\n'), /action-specific production callback/);
}

{
  const root = fixture(`
    import { startThread } from './shared/api/backendApi.js';
    import { runUIAction as executeAction } from './shared/ui/runUIAction.js';
    executeAction('fixture.action', () => startThread());
  `);
  assert.equal(discoverActionProducers(root).counts.get('fixture.action'), 1, 'an imported alias must be discovered');
  assert.equal(discoverP0P1Callsites(root).size, 1, 'a P0 backend facade callsite must be independently discovered');
}

{
  const root = fixture("function runUIAction() {} runUIAction('local.false-positive', () => task());");
  assert.equal(discoverActionProducers(root).counts.size, 0, 'a local same-name function must not be discovered');
}

{
  const root = fixture(`
    import { startThread } from './shared/api/backendApi.js';
    import { runUIAction } from './shared/ui/runUIAction.js';
    function invoke(runUIAction) { runUIAction('shadow.false-positive', () => task()); }
    runUIAction('fixture.action', () => startThread());
  `);
  const discovery = discoverActionProducers(root);
  assert.equal(discovery.counts.get('fixture.action'), 1);
  assert.equal(discovery.counts.has('shadow.false-positive'), false, 'a shadowed parameter must not be discovered');
}

{
  const root = fixture("import { executeAction } from './chatUiActions.js'; executeAction('fixture.action', () => task());");
  fs.writeFileSync(path.join(root, 'src/chatUiActions.js'), "export { runUIAction as executeAction } from './shared/ui/runUIAction.js';");
  assert.equal(discoverActionProducers(root).counts.get('fixture.action'), 1, 'a re-exported binding must be discovered');
}

{
  const root = fixture();
  expectFailure(
    () => runActionProducerGuard({ root, registry: { schemaVersion: 2, coveredProducers: [], exemptions: [] }, testMatrix: { ...matrix(), cells: [], cases: [], rpcCallsites: [] }, today: '2026-07-17' }),
    'missing action producer registry entry',
  );
  expectFailure(
    () => runActionProducerGuard({ root, registry: coveredRegistry({ actionId: 'stale.action' }), testMatrix: matrix(), today: '2026-07-17' }),
    'stale action producer registry entry',
  );
  expectFailure(
    () => runActionProducerGuard({ root, registry: coveredRegistry({ tests: [] }), testMatrix: matrix(), today: '2026-07-17' }),
    'must register evidence cases',
  );
}

{
  const root = fixture();
  const missingCell = { ...matrix(), cells: [] };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: missingCell, today: '2026-07-17' }), 'missing producer error test cell');
  const staleCell = { ...matrix(), cells: [...matrix().cells, { actionId: 'stale', errorSource: 'promise-reject', evidence: [] }] };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: staleCell, today: '2026-07-17' }), 'stale producer error test cell');
}

{
  const root = fixture();
  const missingCallsite = { ...matrix(), rpcCallsites: [] };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: missingCallsite, today: '2026-07-17' }), 'missing P0/P1 RPC production callsite');
  const missingActionMapping = { ...matrix(), rpcCallsites: matrix().rpcCallsites.map((entry) => ({ ...entry, actionIds: ['missing.action'] })) };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: missingActionMapping, today: '2026-07-17' }), 'maps to missing canonical actionId');
  const staleCase = { ...matrix(), cases: [...matrix().cases, { caseId: 'b.stale', layer: 'B', file: 'src/action.test.js', testName: 'reports the failure' }] };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: staleCase, today: '2026-07-17' }), 'stale unreferenced action evidence case');
  const staleTest = { ...matrix(), cases: matrix().cases.map((entry) => (
    entry.caseId === 'b.wrapper.promise-reject' ? { ...entry, testName: 'missing test' } : entry
  )) };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: staleTest, today: '2026-07-17' }), 'evidence test is stale');
  const missingFamily = { ...matrix(), cases: matrix().cases.filter((entry) => entry.caseId !== 'c.skill') };
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: missingFamily, today: '2026-07-17' }), 'missing C-layer semantic family evidence: skill');
}

{
  const root = fixture(`
    import { startThread } from './shared/api/backendApi.js';
    import { runUIAction } from './shared/ui/runUIAction.js';
    runUIAction('fixture.action', () => startThread(), { rejectFalse: true });
  `);
  expectFailure(() => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: matrix(), today: '2026-07-17' }), 'rejectFalse requires unsuccessful-result');
}

{
  const root = fixture(`
    import { startThread } from './shared/api/backendApi.js';
    import { runUIAction } from './shared/ui/runUIAction.js';
    function FixtureRetry({ retry }) { return <button onClick={() => { void runUIAction('fixture.action', retry); }}>retry</button>; }
    startThread();
  `);
  const uiEntrypoints = [{
    actionId: 'fixture.action', sourcePath: 'src/action.js', component: 'FixtureRetry', event: 'onClick', handler: 'retry',
  }];
  runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: matrix({ uiEntrypoints }), today: '2026-07-17' });
  fs.writeFileSync(path.join(root, 'src/action.js'), fs.readFileSync(path.join(root, 'src/action.js'), 'utf8').replace("runUIAction('fixture.action', retry)", 'retry()'));
  expectFailure(
    () => runActionProducerGuard({ root, registry: coveredRegistry(), testMatrix: matrix({ uiEntrypoints }), today: '2026-07-17' }),
    'UI entrypoint has no canonical user action binding',
  );
}

} finally {
  cleanupFixtures();
}

process.stdout.write('action producer guard tests passed\n');
