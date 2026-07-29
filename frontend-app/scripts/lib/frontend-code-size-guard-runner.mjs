import { writeBaselineTransaction } from './frontend-code-size-baseline-transaction.mjs';

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
  if (options.scope === 'all') throw new Error('--update requires exactly one scope: --scope production or --scope test; the two baselines are separate transactions');
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
  const target = options.scope === 'production'
    ? { filePath: productionPath, snapshot: snapshots.production, candidate: plan.productionCandidate }
    : { filePath: testPath, snapshot: snapshots.test, candidate: plan.testCandidate };
  const result = writeBaselineTransaction({
    filePath: target.filePath,
    expectedHash: target.snapshot.hash,
    previous: target.snapshot.baseline,
    candidate: target.candidate,
  });
  writeStandard(result.changed
    ? `frontend code size baseline updated atomically for ${options.scope}; no cross-baseline atomicity is claimed`
    : `frontend code size baseline already current for ${options.scope}; no write performed`);
}
