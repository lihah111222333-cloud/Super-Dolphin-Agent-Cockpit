import fs from 'node:fs';
import path from 'node:path';
import { writeBaselineTransaction } from './frontend-code-size-baseline-transaction.mjs';

const reproducibleBaselineEnv = 'SUPER_DOLPHIN_FRONTEND_CODE_SIZE_REPRODUCIBLE';

export function resolveFrontendCodeSizeAppRoot(defaultRoot) {
  const configured = process.env.SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT;
  if (configured === undefined) return defaultRoot;
  if (!path.isAbsolute(configured)) {
    throw new Error('SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT must be canonical and absolute');
  }
  const root = fs.realpathSync.native(configured);
  if (!fs.statSync(root).isDirectory()) {
    throw new Error('SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT must be canonical and absolute');
  }
  return root;
}

function preserveBaselineTimestamp() {
  const value = process.env[reproducibleBaselineEnv];
  if (value === undefined) return false;
  if (value === '1') return true;
  throw new Error(`${reproducibleBaselineEnv} must be 1 when configured`);
}

function writeStandard(message) {
  process.stdout.write(`${message}\n`);
}

function writeError(message) {
  process.stderr.write(`${message}\n`);
}

function printViolations(violations) {
  writeError(`frontend code size guard failed: ${violations.length} violation(s)`);
  for (const entry of violations.slice(0, 80)) {
    const location = entry.line > 0 ? `${entry.file}:${entry.line}` : entry.file;
    writeError(`- ${location} [${entry.rule}] ${entry.message}`);
  }
  if (violations.length > 80) writeError(`- ... ${violations.length - 80} more violation(s)`);
}

export function runFrontendCodeSizeCheck({ records, snapshots, options, planGuard }) {
  const plan = planGuard({
    records,
    productionBaseline: snapshots.production.baseline,
    testBaseline: snapshots.test.baseline,
    canonical: options.canonical,
    strict: options.mode === 'strict',
    scope: options.scope,
  });
  if (plan.violations.length > 0) printViolations(plan.violations);
  if (plan.driftScopes.length > 0) {
    writeError(`frontend code size guard baseline drift (${plan.driftScopes.join(', ')}): tracked baseline is unchanged`);
    for (const driftScope of plan.driftScopes) {
      writeError(`- review the candidate, then run: node scripts/frontend-code-size-guard.mjs --update --scope ${driftScope} --dir src --dir scripts`);
    }
  }
  if (plan.violations.length > 0 || plan.driftScopes.length > 0) return false;
  writeStandard(`frontend code size guard passed: files=${records.length}, frozen=${plan.frozenFiles}`);
  return true;
}

export function runFrontendCodeSizeUpdate({
  records,
  snapshots,
  options,
  planGuard,
  productionPath,
  testPath,
}) {
  if (!options.canonical) throw new Error('--update only accepts the canonical full scan: --dir src --dir scripts with no --file');
  const plan = planGuard({
    records,
    productionBaseline: snapshots.production.baseline,
    testBaseline: snapshots.test.baseline,
    canonical: true,
    scope: options.scope,
  });
  if (plan.violations.length > 0) {
    printViolations(plan.violations);
    throw new Error('--update refused because source violations or ratchet regressions exist');
  }
  const preserveTimestamp = preserveBaselineTimestamp();
  const targets = [
    ['production', productionPath, snapshots.production, plan.productionCandidate],
    ['test', testPath, snapshots.test, plan.testCandidate],
  ].filter(([scope]) => options.scope === 'all' || options.scope === scope);
  const original = new Map(targets.map(([, filePath]) => [filePath, Buffer.from(requireBaselineBytes(filePath))]));
  const changed = [];
  try {
    for (const [scope, filePath, snapshot, candidate] of targets) {
      const result = writeBaselineTransaction({
        filePath,
        expectedHash: snapshot.hash,
        previous: snapshot.baseline,
        candidate,
        preserveTimestamp,
      });
      if (result.changed) changed.push([scope, filePath]);
    }
  } catch (error) {
    // The first replacement is never left published when the second fails.
    for (const [, filePath] of changed.reverse()) restoreBaselineBytes(filePath, original.get(filePath));
    throw error;
  }
  writeStandard(changed.length > 0
    ? `frontend code size baselines updated atomically for ${options.scope}`
    : `frontend code size baselines already current for ${options.scope}; no write performed`);
}

function requireBaselineBytes(filePath) {
  const stat = fs.lstatSync(filePath);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`baseline rollback source is not a regular file: ${filePath}`);
  return fs.readFileSync(filePath);
}

function restoreBaselineBytes(filePath, bytes) {
  const temporary = `${filePath}.rollback-${process.pid}`;
  fs.writeFileSync(temporary, bytes, { mode: fs.statSync(filePath).mode & 0o777, flag: 'wx' });
  try {
    fs.renameSync(temporary, filePath);
  } finally {
    if (fs.existsSync(temporary)) fs.unlinkSync(temporary);
  }
}
