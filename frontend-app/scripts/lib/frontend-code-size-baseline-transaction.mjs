import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { canonicalViolationSignature } from './frontend-code-size-baseline.mjs';

const metricKeys = Object.freeze([
  'lines',
  'maxFuncLen',
  'maxNesting',
  'maxParams',
  'exportCount',
  'consoleLogs',
  'anyCount',
  'emptyFuncs',
  ['to', 'do', 'Count'].join(''),
  'longLineCount',
]);
const baselineEntryKeys = new Set([...metricKeys, 'frozenViolations']);
const isoTimestampPattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function assertBaselineEntry(entry, entryName) {
  if (!isPlainObject(entry)) throw new Error(`invalid baseline entry ${entryName}: expected object`);
  for (const key of Object.keys(entry)) {
    if (!baselineEntryKeys.has(key)) throw new Error(`invalid baseline entry ${entryName}: unknown field ${key}`);
  }
  for (const key of metricKeys) {
    if (entry[key] !== undefined && (!Number.isInteger(entry[key]) || entry[key] < 0)) {
      throw new Error(`invalid baseline entry ${entryName}: ${key} must be a non-negative integer`);
    }
  }
  if (entry.frozenViolations !== undefined && (
    !Array.isArray(entry.frozenViolations)
    || entry.frozenViolations.some((signature) => typeof signature !== 'string' || !signature.includes('\u0000'))
  )) {
    throw new Error(`invalid baseline entry ${entryName}: frozenViolations must contain signatures`);
  }
  if (entryName.startsWith('__dir__:')) {
    if (Object.keys(entry).length !== 1 || entry.lines === undefined) {
      throw new Error(`invalid directory baseline entry ${entryName}: expected only lines`);
    }
  } else if (!Array.isArray(entry.frozenViolations)) {
    throw new Error(`invalid baseline entry ${entryName}: frozenViolations is required`);
  }
}

export function assertFrontendCodeSizeBaselineSchema(baseline, label = 'baseline') {
  if (!isPlainObject(baseline)) throw new Error(`invalid ${label}: expected object`);
  if (Object.keys(baseline).sort().join(',') !== '_meta,files') {
    throw new Error(`invalid ${label}: expected only _meta and files`);
  }
  if (!isPlainObject(baseline._meta) || !isoTimestampPattern.test(baseline._meta.updatedAt)) {
    throw new Error(`invalid ${label}: _meta.updatedAt must be a canonical UTC timestamp`);
  }
  if (Object.keys(baseline._meta).length !== 1) throw new Error(`invalid ${label}: _meta contains unknown fields`);
  if (!isPlainObject(baseline.files)) throw new Error(`invalid ${label}: files must be an object`);
  for (const [entryName, entry] of Object.entries(baseline.files)) assertBaselineEntry(entry, entryName);
  return baseline;
}

export function baselineBytes(baseline) {
  return Buffer.from(`${JSON.stringify(baseline, null, 2)}\n`, 'utf8');
}

export function hashBaselineBytes(bytes) {
  return crypto.createHash('sha256').update(bytes).digest('hex');
}

function canonicalBudget(signatures) {
  const budget = new Map();
  for (const signature of signatures) {
    const canonical = canonicalViolationSignature(signature);
    budget.set(canonical, (budget.get(canonical) ?? 0) + 1);
  }
  return budget;
}

function assertViolationBudgetDoesNotGrow(previous, next, entryName) {
  const previousBudget = canonicalBudget(previous);
  for (const [signature, count] of canonicalBudget(next)) {
    if (count > (previousBudget.get(signature) ?? 0)) {
      throw new Error(`baseline update would add or widen debt for ${entryName}: ${signature}`);
    }
  }
}

export function assertBaselineUpdateOnlyImproves(previous, next) {
  assertFrontendCodeSizeBaselineSchema(previous, 'tracked baseline');
  assertFrontendCodeSizeBaselineSchema(next, 'candidate baseline');
  for (const [entryName, nextEntry] of Object.entries(next.files)) {
    const previousEntry = previous.files[entryName];
    if (!previousEntry) throw new Error(`baseline update would add debt entry: ${entryName}`);
    for (const key of Object.keys(previousEntry)) {
      if (key !== 'frozenViolations' && nextEntry[key] === undefined) {
        throw new Error(`baseline update would remove metric ${entryName}.${key}`);
      }
    }
    for (const key of metricKeys) {
      if (nextEntry[key] !== undefined && previousEntry[key] === undefined) {
        throw new Error(`baseline update would add metric ${entryName}.${key}`);
      }
      if (nextEntry[key] !== undefined && nextEntry[key] > previousEntry[key]) {
        throw new Error(`baseline update would widen ${entryName}.${key}: ${previousEntry[key]} -> ${nextEntry[key]}`);
      }
    }
    if (!entryName.startsWith('__dir__:')) {
      assertViolationBudgetDoesNotGrow(previousEntry.frozenViolations, nextEntry.frozenViolations, entryName);
    }
  }
}

export function describeBaselineDiff(previous, next) {
  const lines = [];
  const names = new Set([...Object.keys(previous.files), ...Object.keys(next.files)]);
  for (const entryName of [...names].sort()) {
    const before = previous.files[entryName];
    const after = next.files[entryName];
    if (JSON.stringify(before) === JSON.stringify(after)) continue;
    lines.push(`- ${entryName}: ${before === undefined ? '<absent>' : JSON.stringify(before)}`);
    lines.push(`+ ${entryName}: ${after === undefined ? '<removed>' : JSON.stringify(after)}`);
  }
  return lines;
}

function fsyncParentDirectory(filePath, failpoint = noFailureInjection, stage = 'directory-fsync') {
  failpoint(stage);
  const directoryHandle = fs.openSync(path.dirname(filePath), 'r');
  try {
    fs.fsyncSync(directoryHandle);
  } finally {
    fs.closeSync(directoryHandle);
  }
}

function noFailureInjection() {
  return undefined;
}

function processStartIdentity(pid) {
  try {
    const stat = fs.readFileSync(`/proc/${pid}/stat`, 'utf8');
    const fields = stat.slice(stat.lastIndexOf(')') + 2).trim().split(/\s+/);
    if (fields[19]) return `proc:${fields[19]}`;
  } catch {
    // 非 Linux 平台继续使用 ps 的进程启动时间。
  }
  const result = spawnSync('ps', ['-o', 'lstart=', '-p', String(pid)], { encoding: 'utf8' });
  const value = result.status === 0 ? result.stdout.trim() : '';
  return value ? `ps:${value}` : null;
}

function lockOwner({ pid = process.pid, nonce = crypto.randomUUID() } = {}) {
  const startIdentity = processStartIdentity(pid);
  if (!startIdentity) throw new Error(`cannot determine baseline lock owner identity for pid ${pid}`);
  return { version: 1, pid, startIdentity, nonce, createdAt: new Date().toISOString() };
}

function parseLockOwner(bytes, lockPath) {
  let owner;
  try {
    owner = JSON.parse(bytes.toString('utf8'));
  } catch {
    throw new Error(`baseline lock is malformed or outside the cooperative protocol: ${lockPath}`);
  }
  if (
    owner?.version !== 1
    || !Number.isInteger(owner.pid)
    || owner.pid <= 0
    || typeof owner.startIdentity !== 'string'
    || owner.startIdentity.length === 0
    || typeof owner.nonce !== 'string'
    || owner.nonce.length < 16
    || typeof owner.createdAt !== 'string'
  ) {
    throw new Error(`baseline lock is malformed or outside the cooperative protocol: ${lockPath}`);
  }
  return owner;
}

function sameFileIdentity(left, right) {
  return left.dev === right.dev && left.ino === right.ino;
}

function createLockFile(lockPath, owner) {
  const handle = fs.openSync(lockPath, 'wx', 0o600);
  try {
    fs.writeFileSync(handle, `${JSON.stringify(owner)}\n`, 'utf8');
    fs.fsyncSync(handle);
    fsyncParentDirectory(lockPath);
    return handle;
  } catch (error) {
    fs.closeSync(handle);
    if (fs.existsSync(lockPath)) fs.unlinkSync(lockPath);
    throw error;
  }
}

function transactionArtifactPaths(filePath, owner) {
  const prefix = path.join(path.dirname(filePath), `.${path.basename(filePath)}.${owner.pid}.${owner.nonce}`);
  return {
    tempPath: `${prefix}.tmp`,
    backupPath: `${prefix}.bak`,
  };
}

function cleanupStaleTransactionArtifacts(filePath, owner) {
  const artifacts = transactionArtifactPaths(filePath, owner);
  let removed = false;
  for (const artifactPath of [artifacts.tempPath, artifacts.backupPath]) {
    if (!fs.existsSync(artifactPath)) continue;
    const artifactStat = fs.lstatSync(artifactPath);
    if (!artifactStat.isFile() || artifactStat.isSymbolicLink()) {
      throw new Error(`baseline stale transaction artifact is not a regular file: ${artifactPath}`);
    }
    fs.unlinkSync(artifactPath);
    removed = true;
  }
  if (removed) fsyncParentDirectory(filePath);
}

function recoverStaleLock(lockPath, resolveProcessIdentity) {
  const staleHandle = fs.openSync(lockPath, 'r');
  let staleStat;
  let existingOwner;
  try {
    staleStat = fs.fstatSync(staleHandle);
    existingOwner = parseLockOwner(fs.readFileSync(staleHandle), lockPath);
  } finally {
    fs.closeSync(staleHandle);
  }
  const liveIdentity = resolveProcessIdentity(existingOwner.pid);
  if (liveIdentity === existingOwner.startIdentity) {
    throw new Error(`baseline lock is owned by live cooperative process ${existingOwner.pid}: ${lockPath}`);
  }
  const currentStat = fs.lstatSync(lockPath);
  if (!sameFileIdentity(staleStat, currentStat)) {
    throw new Error(`baseline lock changed during stale recovery: ${lockPath}`);
  }
  cleanupStaleTransactionArtifacts(lockPath.slice(0, -'.lock'.length), existingOwner);
  fs.unlinkSync(lockPath);
  fsyncParentDirectory(lockPath);
}

function createLockRelease(lockPath, owner, handle, handlers) {
  let released = false;
  return () => {
    if (released) return;
    released = true;
    for (const [signal, handler] of handlers) process.off(signal, handler);
    fs.closeSync(handle);
    if (!fs.existsSync(lockPath)) return;
    const currentOwner = parseLockOwner(fs.readFileSync(lockPath), lockPath);
    if (currentOwner.nonce !== owner.nonce) {
      throw new Error(`baseline lock ownership changed before release: ${lockPath}`);
    }
    fs.unlinkSync(lockPath);
    fsyncParentDirectory(lockPath);
  };
}

function installLockSignalHandlers(release, handlers) {
  for (const [signal, exitCode] of [['SIGTERM', 143], ['SIGINT', 130]]) {
    const handler = () => {
      try {
        release();
      } finally {
        process.exit(exitCode);
      }
    };
    handlers.set(signal, handler);
    process.once(signal, handler);
  }
}

// 该锁只协调遵守本模块协议的协作进程；能以同一 UID 主动改写目标或锁文件的进程位于可信边界之外。
// PID、启动身份和 nonce 用于正常崩溃恢复与 PID reuse 判定，不构成抵御同 UID 恶意伪造的安全凭据。
export function acquireBaselineLock(filePath, {
  resolveProcessIdentity = processStartIdentity,
  installSignalHandlers = true,
} = {}) {
  const lockPath = `${filePath}.lock`;
  const owner = lockOwner();
  let handle;
  try {
    handle = createLockFile(lockPath, owner);
  } catch (error) {
    if (error?.code !== 'EEXIST') throw error;
    recoverStaleLock(lockPath, resolveProcessIdentity);
    handle = createLockFile(lockPath, owner);
  }

  const handlers = new Map();
  const release = createLockRelease(lockPath, owner, handle, handlers);
  if (installSignalHandlers) installLockSignalHandlers(release, handlers);
  return { lockPath, owner, release };
}

function readBaselineFileState(filePath) {
  let handle;
  try {
    handle = fs.openSync(filePath, 'r');
    const handleStat = fs.fstatSync(handle);
    const bytes = fs.readFileSync(handle);
    const pathStat = fs.statSync(filePath);
    return {
      exists: true,
      stable: sameFileIdentity(handleStat, pathStat),
      identity: { dev: pathStat.dev, ino: pathStat.ino },
      hash: hashBaselineBytes(bytes),
      bytes: bytes.toString('utf8'),
    };
  } catch (error) {
    return {
      exists: false,
      stable: false,
      readError: error.message,
      readErrorCode: error.code,
    };
  } finally {
    if (handle !== undefined) fs.closeSync(handle);
  }
}

function durableUnknown(
  filePath,
  stage,
  cause,
  finalState = readBaselineFileState(filePath),
  { committed = false } = {},
) {
  const result = new Error(
    `baseline ${committed ? 'committed but ' : ''}durability unknown after ${stage} for ${filePath}; reconcile required; final state: ${JSON.stringify(finalState)}`,
    { cause },
  );
  result.code = committed ? 'BASELINE_COMMITTED_DURABILITY_UNKNOWN' : 'BASELINE_DURABILITY_UNKNOWN';
  result.committed = committed;
  result.finalState = finalState;
  return result;
}

function stateMatchesSnapshot(state, expectedHash) {
  return state.exists && state.stable && state.hash === expectedHash;
}

function stateMatchesCandidate(state, candidateHash, candidateBytes, candidateIdentity) {
  return stateMatchesSnapshot(state, candidateHash)
    && state.bytes === candidateBytes.toString('utf8')
    && sameFileIdentity(state.identity, candidateIdentity);
}

function classifyTransactionFailure({
  filePath,
  expectedHash,
  mutationStarted,
  committed,
  stage,
  error,
}) {
  if (!mutationStarted && !committed && error?.code === 'BASELINE_CAS_CONFLICT') return error;
  const finalState = readBaselineFileState(filePath);
  if (!mutationStarted && !committed) {
    if (stateMatchesSnapshot(finalState, expectedHash)) return error;
    return durableUnknown(filePath, stage, error, finalState);
  }
  if (!committed && stateMatchesSnapshot(finalState, expectedHash)) return error;
  return durableUnknown(filePath, stage, error, finalState, { committed });
}

function restoreClaimedBaseline({ filePath, backupPath, failpoint }) {
  try {
    failpoint('before-rollback-rename');
    fs.renameSync(backupPath, filePath);
    fsyncParentDirectory(filePath, failpoint, 'before-rollback-dir-fsync');
  } catch (error) {
    throw durableUnknown(filePath, 'rollback', error);
  }
}

function stageBaselineCandidate(context, state) {
  state.lock = acquireBaselineLock(context.filePath);
  Object.assign(context, transactionArtifactPaths(context.filePath, state.lock.owner));
  context.failpoint('after-lock');
  const currentBytes = fs.readFileSync(context.filePath);
  const currentMode = fs.statSync(context.filePath).mode & 0o777;
  if (hashBaselineBytes(currentBytes) !== context.expectedHash) {
    throw new Error(`baseline compare-and-swap failed for ${context.filePath}: tracked hash changed`);
  }
  assertFrontendCodeSizeBaselineSchema(JSON.parse(currentBytes.toString('utf8')), 'current baseline');
  process.stdout.write(`frontend code size baseline update diff: ${context.filePath}\n`);
  for (const line of context.diff) process.stdout.write(`${line}\n`);
  context.failpoint('before-temp');
  state.tempHandle = fs.openSync(context.tempPath, 'wx', 0o600);
  fs.fchmodSync(state.tempHandle, currentMode);
  fs.writeFileSync(state.tempHandle, context.nextBytes);
  fs.fsyncSync(state.tempHandle);
  context.failpoint('after-temp-fsync');
  fs.closeSync(state.tempHandle);
  state.tempHandle = undefined;
}

function claimBaselineTarget(context, state) {
  context.failpoint('before-claim-rename');
  context.failpoint('before-backup-link');
  fs.linkSync(context.filePath, context.backupPath);
  state.claimed = true;
  const targetStat = fs.statSync(context.filePath);
  const backupStat = fs.statSync(context.backupPath);
  const claimedBytes = fs.readFileSync(context.backupPath);
  if (
    !sameFileIdentity(targetStat, backupStat)
    || hashBaselineBytes(claimedBytes) !== context.expectedHash
    || hashBaselineBytes(fs.readFileSync(context.filePath)) !== context.expectedHash
  ) {
    fs.unlinkSync(context.backupPath);
    fsyncParentDirectory(context.filePath);
    state.claimed = false;
    const error = new Error(`baseline compare-and-swap failed for ${context.filePath}: target changed after check`);
    error.code = 'BASELINE_CAS_CONFLICT';
    throw error;
  }
  assertFrontendCodeSizeBaselineSchema(JSON.parse(claimedBytes.toString('utf8')), 'claimed baseline');
  fsyncParentDirectory(context.filePath, context.failpoint, 'before-backup-dir-fsync');
}

function installBaselineCandidate(context, state) {
  context.failpoint('before-install');
  state.candidateIdentity = fs.statSync(context.tempPath);
  context.failpoint('before-atomic-replace');
  state.mutationStarted = true;
  fs.renameSync(context.tempPath, context.filePath);
  state.committed = true;
  try {
    fsyncParentDirectory(context.filePath, context.failpoint, 'before-commit-dir-fsync');
    if (hashBaselineBytes(fs.readFileSync(context.filePath)) !== context.nextHash) {
      throw new Error(`baseline candidate changed during commit for ${context.filePath}`);
    }
    context.failpoint('after-atomic-replace-before-cleanup');
  } catch (error) {
    throw durableUnknown(
      context.filePath,
      'atomic replace commit',
      error,
      readBaselineFileState(context.filePath),
      { committed: true },
    );
  }
}

function finalizeBaselineCandidate(context, state) {
  fs.unlinkSync(context.backupPath);
  state.claimed = false;
  fsyncParentDirectory(context.filePath, context.failpoint, 'before-cleanup-dir-fsync');
  context.failpoint('after-cleanup-dir-fsync');
  const finalState = readBaselineFileState(context.filePath);
  if (stateMatchesCandidate(finalState, context.nextHash, context.nextBytes, state.candidateIdentity)) return;
  throw durableUnknown(
    context.filePath,
    'final candidate verification',
    new Error(`baseline final inode, bytes, or hash does not match candidate: ${context.filePath}`),
    finalState,
    { committed: true },
  );
}

function handleBaselineTransactionFailure(context, state, error) {
  if (error?.code === 'BASELINE_DURABILITY_UNKNOWN' || error?.code === 'BASELINE_COMMITTED_DURABILITY_UNKNOWN') {
    return error;
  }
  if (state.claimed && !state.committed) {
    restoreClaimedBaseline(context);
    state.claimed = false;
  }
  return classifyTransactionFailure({
    filePath: context.filePath,
    expectedHash: context.expectedHash,
    mutationStarted: state.mutationStarted,
    committed: state.committed,
    stage: state.committed ? 'post-commit cleanup' : 'claim or install',
    error,
  });
}

function cleanupBaselineTransaction(context, state) {
  try {
    if (state.tempHandle !== undefined) fs.closeSync(state.tempHandle);
    if (context.tempPath && fs.existsSync(context.tempPath)) fs.unlinkSync(context.tempPath);
    if (
      (!state.claimed || state.committed)
      && context.backupPath
      && fs.existsSync(context.backupPath)
    ) {
      fs.unlinkSync(context.backupPath);
      state.claimed = false;
    }
    if (state.lock === undefined) return;
    context.failpoint('before-lock-release');
    state.lock.release();
  } catch (error) {
    throw classifyTransactionFailure({
      filePath: context.filePath,
      expectedHash: context.expectedHash,
      mutationStarted: state.mutationStarted,
      committed: state.committed,
      stage: 'resource or lock cleanup',
      error,
    });
  }
}

export function writeBaselineTransaction({
  filePath,
  expectedHash,
  previous,
  candidate,
  now = () => new Date(),
  failpoint = noFailureInjection,
  platform = process.platform,
}) {
  assertBaselineUpdateOnlyImproves(previous, candidate);
  if (hashBaselineBytes(baselineBytes(previous)) !== expectedHash) {
    throw new Error(`baseline expected snapshot mismatch for ${filePath}`);
  }
  const next = {
    _meta: { updatedAt: now().toISOString().replace(/\.\d{3}Z$/, 'Z') },
    files: candidate.files,
  };
  assertFrontendCodeSizeBaselineSchema(next, 'candidate baseline');
  const diff = describeBaselineDiff(previous, next);
  if (diff.length === 0) return { changed: false, diff: [] };
  if (platform === 'win32') {
    const error = new Error('crash-atomic baseline replacement is not verified on win32; refusing to mutate the baseline');
    error.code = 'BASELINE_ATOMIC_REPLACE_UNSUPPORTED';
    throw error;
  }
  const nextBytes = baselineBytes(next);
  const context = {
    filePath,
    expectedHash,
    diff,
    nextBytes,
    nextHash: hashBaselineBytes(nextBytes),
    tempPath: undefined,
    backupPath: undefined,
    failpoint,
  };
  const state = {
    lock: undefined,
    tempHandle: undefined,
    claimed: false,
    committed: false,
    mutationStarted: false,
    candidateIdentity: undefined,
  };
  let result;
  let transactionError;
  try {
    stageBaselineCandidate(context, state);
    claimBaselineTarget(context, state);
    installBaselineCandidate(context, state);
    finalizeBaselineCandidate(context, state);
    result = { changed: true, diff };
  } catch (error) {
    try {
      transactionError = handleBaselineTransactionFailure(context, state, error);
    } catch (handlingError) {
      transactionError = handlingError;
    }
  }
  try {
    cleanupBaselineTransaction(context, state);
  } catch (cleanupError) {
    if (transactionError === undefined) transactionError = cleanupError;
  }
  if (transactionError !== undefined) throw transactionError;
  return result;
}
