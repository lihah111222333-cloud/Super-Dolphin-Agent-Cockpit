import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { discoverActionProducers, runActionProducerGuard } from './action-producer-guard.mjs';

function fixture(source = "import { runUIAction } from './shared/ui/runUIAction.js'; runUIAction('fixture.action', () => task());") {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'action-producer-guard-'));
  fs.mkdirSync(path.join(root, 'src'), { recursive: true });
  fs.mkdirSync(path.join(root, 'src/shared/ui'), { recursive: true });
  fs.writeFileSync(path.join(root, 'src/shared/ui/runUIAction.js'), 'export function runUIAction() {}');
  fs.writeFileSync(path.join(root, 'src/action.js'), source);
  fs.writeFileSync(path.join(root, 'src/action.test.js'), "it('reports the failure', () => {});");
  return root;
}

function coveredRegistry(overrides = {}) {
  return {
    schemaVersion: 1,
    coveredProducers: [{
      actionId: 'fixture.action',
      producerCount: 1,
      owner: 'fixture',
      tests: [{ file: 'src/action.test.js', names: ['reports the failure'] }],
      ...overrides,
    }],
    exemptions: [],
  };
}

function expectFailure(run, text) {
  assert.throws(run, (error) => error.message.includes(text));
}

{
  const root = fixture();
  assert.deepEqual(runActionProducerGuard({ root, registry: coveredRegistry(), today: '2026-07-17' }), {
    covered: 1, discovered: 1, exempted: 0,
  });
}

{
  const root = fixture("import { runUIAction } from './shared/ui/runUIAction.js'; runUIAction(() => task());");
  const discovery = discoverActionProducers(root);
  assert.equal(discovery.problems.length, 1);
  assert.match(discovery.problems[0], /literal actionId/);
}

{
  const root = fixture("import { runUIAction as executeAction } from './shared/ui/runUIAction.js'; executeAction('fixture.action', () => task());");
  assert.equal(discoverActionProducers(root).counts.get('fixture.action'), 1, 'an imported alias must be discovered');
}

{
  const root = fixture("function runUIAction() {} runUIAction('local.false-positive', () => task());");
  assert.equal(discoverActionProducers(root).counts.size, 0, 'a local same-name function must not be discovered');
}

{
  const root = fixture(`
    import { runUIAction } from './shared/ui/runUIAction.js';
    function invoke(runUIAction) { runUIAction('shadow.false-positive', () => task()); }
    runUIAction('fixture.action', () => task());
  `);
  const discovery = discoverActionProducers(root);
  assert.equal(discovery.counts.get('fixture.action'), 1);
  assert.equal(discovery.counts.has('shadow.false-positive'), false, 'a shadowed parameter must not be discovered');
}

{
  const root = fixture("import { executeAction } from './chatUiActions.js'; executeAction('fixture.action', () => task());");
  fs.writeFileSync(
    path.join(root, 'src/chatUiActions.js'),
    "export { runUIAction as executeAction } from './shared/ui/runUIAction.js';",
  );
  assert.equal(discoverActionProducers(root).counts.get('fixture.action'), 1, 'a re-exported binding must be discovered');
}

{
  const root = fixture();
  expectFailure(
    () => runActionProducerGuard({ root, registry: { schemaVersion: 1, coveredProducers: [], exemptions: [] }, today: '2026-07-17' }),
    'missing action producer registry entry',
  );
  expectFailure(
    () => runActionProducerGuard({ root, registry: coveredRegistry({ actionId: 'stale.action' }), today: '2026-07-17' }),
    'stale action producer registry entry',
  );
  expectFailure(
    () => runActionProducerGuard({ root, registry: coveredRegistry({ tests: [] }), today: '2026-07-17' }),
    'zero registered failure tests',
  );
}

{
  const root = fixture();
  const registry = { schemaVersion: 1, coveredProducers: [], exemptions: [{
    actionId: 'fixture.action', producerCount: 1, owner: 'Task2B', reason: 'Narrow fixture follow-up action migration.', expires: '2026-07-17',
  }] };
  expectFailure(() => runActionProducerGuard({ root, registry, today: '2026-07-17' }), 'exemption is expired');
}

process.stdout.write('action producer guard tests passed\n');
