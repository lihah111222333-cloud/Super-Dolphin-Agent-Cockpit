import fs from 'node:fs';
import path from 'node:path';

const FRONTEND_RUNTIME_SEED_ENV = 'SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED';
const WRITABLE_OVERLAY_ENTRIES = new Set(['.vite', '.vite-temp']);

export const FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS = Object.freeze([
  '.githooks/pre-commit',
  '.githooks/pre-push',
  'Makefile',
  'run-new-ui-desktop.sh',
  'scripts/frontend_embed_verify.sh',
  'frontend-app/package.json',
  'frontend-app/package-lock.json',
  'frontend-app/eslint.config.js',
  'frontend-app/vite.config.js',
  'frontend-app/playwright.desktop.config.js',
  'frontend-app/playwright.failure.config.js',
  'frontend-app/scripts/frontend-execution-closure.mjs',
  'scripts/ai_maintenance_gates.sh',
  'scripts/ai_maintenance_gates_guard_test.go',
  'scripts/ai_maintenance/main.go',
  'scripts/ai_maintenance/main_test.go',
  'frontend-app/scripts/delivery-smoke-runner.mjs',
  'frontend-app/scripts/managed-command.mjs',
  'frontend-app/scripts/desktop-smoke.mjs',
  'frontend-app/scripts/desktop-smoke-codex-stub.mjs',
  'frontend-app/scripts/desktop-failure-contract.mjs',
  'frontend-app/scripts/desktop-failure-smoke.mjs',
  'frontend-app/scripts/evidence-provenance.mjs',
  'frontend-app/scripts/performance-budget-model.mjs',
  'frontend-app/scripts/sync-frontend-dist.mjs',
  'frontend-app/tests/e2e/desktop-failure.spec.js',
  'internal/ui/wails/testdata/failure_smoke_host/main.go',
  'frontend-app/scripts/no-critical-skip.mjs',
  'frontend-app/scripts/no-silent-async-failure.mjs',
  'frontend-app/scripts/frontend-contract-store-guard.mjs',
  'frontend-app/scripts/frontend-state-ownership-guard.mjs',
  'frontend-app/scripts/frontend-state-ownership-registry.json',
  'frontend-app/scripts/frontend-dependency-direction-guard.mjs',
  'frontend-app/scripts/frontend-dependency-direction-registry.json',
  'frontend-app/scripts/turn-contract-field-guard.mjs',
  'frontend-app/scripts/action-producer-guard.selftest.mjs',
  'frontend-app/scripts/action-producer-guard.mjs',
  'frontend-app/config/action-producer-registry.json',
  'frontend-app/config/action-producer-test-matrix.json',
  'frontend-app/scripts/frontend-code-size-guard.mjs',
  'frontend-app/scripts/frontend-z-index-token-guard.mjs',
  'frontend-app/scripts/critical-typecheck-guard.mjs',
  'frontend-app/scripts/critical-typecheck-files.json',
  'frontend-app/scripts/contracts-typecheck-guard.test.mjs',
  'frontend-app/scripts/rpc-contract-audit.mjs',
  'scripts/turncontract/main.go',
  'scripts/turncontract/schema_support.go',
  'internal/dto/turn/contract_generate.go',
  'internal/dto/turn/contract_field_guard_discovery_test.go',
  'internal/dto/turn/contract_field_guard_test.go',
  'internal/dto/turn/contract_validator.go',
  'internal/dto/turn/contract_validator_test.go',
  'internal/dto/turn/turn_contract_schemas_generated.go',
]);

function fail(message) {
  throw new Error(message);
}

export function nodeModulesPath(root, packagePath = '') {
  const relativePath = packagePath.replace(/^node_modules\/?/u, '');
  return path.join(root, relativePath);
}

export function immutableDependencyRoot(appRoot) {
  const overlayRoot = path.join(appRoot, 'node_modules');
  const overlayStat = fs.lstatSync(overlayRoot);
  if (!overlayStat.isDirectory() || overlayStat.isSymbolicLink()) {
    fail(`immutable dependency root must be a physical directory: ${overlayRoot}`);
  }
  const configuredSeed = process.env[FRONTEND_RUNTIME_SEED_ENV];
  if (!configuredSeed) return overlayRoot;
  const seedRoot = path.resolve(configuredSeed);
  const seedStat = fs.lstatSync(seedRoot);
  if (!seedStat.isDirectory() || seedStat.isSymbolicLink()) {
    fail(`immutable dependency seed must be a physical directory: ${seedRoot}`);
  }
  const seedEntries = fs.readdirSync(seedRoot).sort();
  const overlayEntries = fs.readdirSync(overlayRoot).sort();
  const expectedEntries = [...seedEntries, ...WRITABLE_OVERLAY_ENTRIES].sort();
  if (JSON.stringify(overlayEntries) !== JSON.stringify(expectedEntries)) {
    fail('immutable dependency overlay entries do not match the configured seed');
  }
  for (const entry of seedEntries) {
    const overlayEntry = path.join(overlayRoot, entry);
    const target = path.join(seedRoot, entry);
    if (!fs.lstatSync(overlayEntry).isSymbolicLink() || fs.readlinkSync(overlayEntry) !== target) {
      fail(`immutable dependency overlay link mismatch: ${entry}`);
    }
  }
  for (const entry of WRITABLE_OVERLAY_ENTRIES) {
    const stat = fs.lstatSync(path.join(overlayRoot, entry));
    if (!stat.isDirectory() || stat.isSymbolicLink()) {
      fail(`immutable dependency overlay is not writable: ${entry}`);
    }
  }
  return seedRoot;
}
