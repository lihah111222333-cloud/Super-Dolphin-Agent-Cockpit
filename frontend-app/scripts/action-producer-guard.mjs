import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import {
  discoverActionProducers,
  discoverP0P1Callsites,
  productionBindingGuardMutationDetection,
} from './action-producer-discovery.mjs';
import { runActionProducerValidation } from './action-producer-validation.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const REGISTRY_PATH = path.join(ROOT, 'config/action-producer-registry.json');
const TEST_MATRIX_PATH = path.join(ROOT, 'config/action-producer-test-matrix.json');

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
  const discovery = runActionProducerValidation({ root, registry, testMatrix, today });
  const bindings = discovery.bindings.map((entry) => {
    const binding = { ...entry };
    delete binding.uiEntrypoint;
    return { ...binding, guardMutationDetection: productionBindingGuardMutationDetection(binding, root) };
  });
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

function writeReport(result, reportPath) {
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
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const result = runActionProducerGuard();
  if (process.argv.length === 2) {
    process.stdout.write(`action producer guard passed: discovered=${result.discovered} covered=${result.covered} exempted=${result.exempted}\n`);
  } else if (process.argv.length === 4 && process.argv[2] === '--report') {
    writeReport(result, path.resolve(ROOT, process.argv[3]));
  } else {
    throw new Error('usage: action-producer-guard.mjs [--report <path>]');
  }
}

export {
  discoverActionProducers,
  discoverP0P1Callsites,
  productionBindingGuardMutationDetection,
};
