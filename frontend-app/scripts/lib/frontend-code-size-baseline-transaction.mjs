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
const recoveryActionPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const publicPhasePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const durabilityPhases = ['rollback', 'claim-or-install', 'atomic-replace-commit', 'post-commit-cleanup', 'final-candidate-verification', 'resource-or-lock-cleanup'];
const publicErrorContracts = Object.freeze({
  BASELINE_LOCK_PROTOCOL_ERROR: [['lock-marker-validation'], 'inspect-lock-marker-without-mutating'],
  BASELINE_LOCK_MANUAL_RECOVERY_REQUIRED: [['stale-lock-recovery'], 'inspect-marker-and-owned-artifacts-without-deleting'],
  BASELINE_STALE_TARGET_CONFLICT: [['stale-target-validation'], 'inspect-target-and-marker-without-mutating'],
  BASELINE_LOCK_HELD: [['lock-acquisition'], 'wait-for-owner-exit-before-retrying'],
  BASELINE_LOCK_RECOVERY_CONFLICT: [['stale-lock-recovery'], 'inspect-lock-marker-without-mutating'],
  BASELINE_LOCK_RELEASE_CONFLICT: [['lock-release'], 'inspect-lock-marker-without-mutating'],
  BASELINE_DURABILITY_UNKNOWN: [durabilityPhases, 'inspect-final-state-and-marker-without-mutating'],
  BASELINE_COMMITTED_DURABILITY_UNKNOWN: [durabilityPhases, 'inspect-final-state-and-marker-without-mutating'],
  BASELINE_CAS_CONFLICT: [['compare-and-swap'], 'refresh-baseline-snapshot-before-retrying'],
  BASELINE_TRANSACTION_STATE_CONFLICT: [['owned-artifact-cleanup'], 'inspect-target-and-marker-without-mutating'],
  BASELINE_TRANSACTION_FAILED_WITH_RECOVERY_REQUIRED: [['resource-or-lock-cleanup'], 'inspect-marker-and-owned-artifacts-without-deleting'],
  BASELINE_ATOMIC_REPLACE_UNSUPPORTED: [['platform-validation'], 'use-supported-platform-for-changed-update'],
  BASELINE_PUBLIC_ERROR_CONTRACT_REJECTED: [['stderr-projection'], 'inspect-error-contract-without-outputting-private-data'],
});
export const BASELINE_PUBLIC_ERROR_CONTRACT_REJECTED_PROJECTION = 'code=BASELINE_PUBLIC_ERROR_CONTRACT_REJECTED phase=stderr-projection recoveryAction=inspect-error-contract-without-outputting-private-data';

function isPlainObject(value) { return value !== null && typeof value === 'object' && !Array.isArray(value); }
function safePathName(filePath) { return path.basename(filePath); }

function assertPublicErrorContract(code, phase, recoveryAction) {
  const contract = publicErrorContracts[code];
  if (contract === undefined) throw new Error('baseline public error contract rejected: unknown code');
  if (!contract[0].includes(phase)) throw new Error('baseline public error contract rejected: invalid or missing phase');
  if (recoveryAction !== contract[1]) throw new Error('baseline public error contract rejected: invalid or missing recovery action');
}

function typedRecoveryError(message, code, phase, recoveryAction, options) {
  assertPublicErrorContract(code, phase, recoveryAction);
  const error = new Error(message, options);
  Object.assign(error, { code, phase, recoveryAction });
  return error;
}

function protocolError(lockPath) {
  const message = `baseline lock marker is malformed or outside the cooperative protocol: ${safePathName(lockPath)}`;
  return typedRecoveryError(message, 'BASELINE_LOCK_PROTOCOL_ERROR', 'lock-marker-validation', 'inspect-lock-marker-without-mutating');
}

function assertStablePublicToken(value, pattern, label) {
  if (typeof value !== 'string' || !pattern.test(value)) throw new Error(`baseline public error ${label} is outside the stable token protocol`);
  return value;
}

export function formatBaselineTransactionErrorForStderr(error) {
  if (!(error instanceof Error) || !String(error.code ?? '').startsWith('BASELINE_')) return null;
  assertPublicErrorContract(error.code, error.phase, error.recoveryAction);
  const fields = [`code=${error.code}`];
  fields.push(`phase=${assertStablePublicToken(error.phase, publicPhasePattern, 'phase')}`);
  fields.push(`recoveryAction=${assertStablePublicToken(error.recoveryAction, recoveryActionPattern, 'recovery action')}`);
  return fields.join(' ');
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

function lockOwner({
  pid = process.pid,
  nonce = crypto.randomUUID(),
  transaction,
} = {}) {
  const startIdentity = processStartIdentity(pid);
  if (!startIdentity) throw new Error(`cannot determine baseline lock owner identity for pid ${pid}`);
  return {
    version: transaction === undefined ? 1 : 2,
    pid,
    startIdentity,
    nonce,
    createdAt: new Date().toISOString(),
    ...(transaction === undefined ? {} : { transaction }),
  };
}

function parseLockOwner(bytes, lockPath) {
  let owner;
  try {
    owner = JSON.parse(bytes.toString('utf8'));
  } catch {
    throw protocolError(lockPath);
  }
  const expectedKeys = owner?.version === 2
    ? ['createdAt', 'nonce', 'pid', 'startIdentity', 'transaction', 'version']
    : ['createdAt', 'nonce', 'pid', 'startIdentity', 'version'];
  if (
    ![1, 2].includes(owner?.version)
    || Object.keys(owner).sort().join(',') !== expectedKeys.join(',')
    || !Number.isInteger(owner.pid)
    || owner.pid <= 0
    || typeof owner.startIdentity !== 'string'
    || owner.startIdentity.length === 0
    || typeof owner.nonce !== 'string'
    || !/^[0-9a-f-]{16,64}$/i.test(owner.nonce)
    || typeof owner.createdAt !== 'string'
    || (
      owner.version === 2
      && (
        !isPlainObject(owner.transaction)
        || Object.keys(owner.transaction).sort().join(',') !== 'expectedHash,nextHash'
        || !/^[0-9a-f]{64}$/.test(owner.transaction.expectedHash)
        || !/^[0-9a-f]{64}$/.test(owner.transaction.nextHash)
      )
    )
    || (owner.version === 1 && owner.transaction !== undefined)
  ) {
    throw protocolError(lockPath);
  }
  return owner;
}

function sameFileIdentity(left, right) {
  return left.dev === right.dev && left.ino === right.ino;
}

function lockInitializationPath(lockPath, owner) {
  return `${lockPath}.${owner.pid}.${owner.nonce}.init`;
}

function createLockFile(lockPath, owner) {
  const initializationPath = lockInitializationPath(lockPath, owner);
  const handle = fs.openSync(initializationPath, 'wx', 0o600);
  let installed = false;
  try {
    fs.writeFileSync(handle, `${JSON.stringify(owner)}\n`, 'utf8');
    fs.fsyncSync(handle);
    fs.linkSync(initializationPath, lockPath);
    installed = true;
    fsyncParentDirectory(lockPath);
    fs.unlinkSync(initializationPath);
    fsyncParentDirectory(lockPath);
    return handle;
  } catch (error) {
    fs.closeSync(handle);
    if (!installed && fs.existsSync(initializationPath)) {
      fs.unlinkSync(initializationPath);
      fsyncParentDirectory(lockPath);
    }
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

function assertOwnedArtifactPath(filePath, artifactPath, expectedPath) {
  const directory = path.dirname(filePath);
  const relative = path.relative(directory, artifactPath);
  if (
    artifactPath !== expectedPath
    || path.dirname(artifactPath) !== directory
    || relative.length === 0
    || path.isAbsolute(relative)
    || relative.startsWith(`..${path.sep}`)
    || relative.includes(path.sep)
  ) {
    throw new Error(
      `baseline transaction artifact escapes target directory: ${safePathName(artifactPath)}`,
    );
  }
}

function manualRecoveryRequired(message, options) {
  return typedRecoveryError(message, 'BASELINE_LOCK_MANUAL_RECOVERY_REQUIRED', 'stale-lock-recovery', 'inspect-marker-and-owned-artifacts-without-deleting', options);
}

function assertRecoverableBaselineBytes(bytes, label, artifactName) {
  try {
    assertFrontendCodeSizeBaselineSchema(JSON.parse(bytes.toString('utf8')), label);
  } catch (cause) {
    const message = `baseline stale transaction content is malformed; manual recovery required for ${artifactName}`;
    throw manualRecoveryRequired(message, { cause });
  }
}

function assertRecoverableTransactionState(filePath, owner, artifacts) {
  const existingArtifacts = [artifacts.tempPath, artifacts.backupPath].filter((artifactPath) => (
    fs.existsSync(artifactPath)
  ));
  if (owner.transaction === undefined) {
    if (existingArtifacts.length > 0) {
      throw manualRecoveryRequired(
        `baseline stale transaction metadata is missing; manual recovery required for ${path.basename(filePath)}`,
      );
    }
    return;
  }
  const finalState = readBaselineFileState(filePath);
  if (
    !finalState.exists
    || !finalState.stable
    || ![owner.transaction.expectedHash, owner.transaction.nextHash].includes(finalState.hash)
  ) {
    const message = `baseline stale transaction target does not match expected or candidate hash: ${path.basename(filePath)}`;
    throw typedRecoveryError(message, 'BASELINE_STALE_TARGET_CONFLICT', 'stale-target-validation', 'inspect-target-and-marker-without-mutating');
  }
  assertRecoverableBaselineBytes(Buffer.from(finalState.bytes, 'utf8'), 'stale transaction target', safePathName(filePath));
  for (const [artifactPath, expectedHash] of [
    [artifacts.tempPath, owner.transaction.nextHash],
    [artifacts.backupPath, owner.transaction.expectedHash],
  ]) {
    if (!fs.existsSync(artifactPath)) continue;
    const artifactBytes = fs.readFileSync(artifactPath);
    if (hashBaselineBytes(artifactBytes) !== expectedHash) {
      throw manualRecoveryRequired(
        `baseline stale transaction artifact hash mismatch: ${path.basename(artifactPath)}`,
      );
    }
    assertRecoverableBaselineBytes(artifactBytes, 'stale transaction artifact', safePathName(artifactPath));
  }
}

function cleanupStaleTransactionArtifacts(filePath, owner) {
  const artifacts = transactionArtifactPaths(filePath, owner);
  assertOwnedArtifactPath(filePath, artifacts.tempPath, transactionArtifactPaths(filePath, owner).tempPath);
  assertOwnedArtifactPath(filePath, artifacts.backupPath, transactionArtifactPaths(filePath, owner).backupPath);
  assertRecoverableTransactionState(filePath, owner, artifacts);
  let removed = false;
  for (const artifactPath of [artifacts.tempPath, artifacts.backupPath]) {
    if (!fs.existsSync(artifactPath)) continue;
    const artifactStat = fs.lstatSync(artifactPath);
    if (!artifactStat.isFile() || artifactStat.isSymbolicLink()) {
      throw manualRecoveryRequired(
        `baseline stale transaction artifact is not a regular file: ${safePathName(artifactPath)}`,
      );
    }
    fs.unlinkSync(artifactPath);
    removed = true;
  }
  if (removed) fsyncParentDirectory(filePath);
  for (const artifactPath of [artifacts.tempPath, artifacts.backupPath]) {
    if (fs.existsSync(artifactPath)) {
      throw manualRecoveryRequired(
        `baseline stale transaction artifact survived cleanup: ${safePathName(artifactPath)}`,
      );
    }
  }
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
    const message = `baseline lock is owned by live cooperative process ${existingOwner.pid}: ${safePathName(lockPath)}`;
    throw typedRecoveryError(message, 'BASELINE_LOCK_HELD', 'lock-acquisition', 'wait-for-owner-exit-before-retrying');
  }
  const currentStat = fs.lstatSync(lockPath);
  if (!sameFileIdentity(staleStat, currentStat)) {
    const message = `baseline lock changed during stale recovery: ${safePathName(lockPath)}`;
    throw typedRecoveryError(message, 'BASELINE_LOCK_RECOVERY_CONFLICT', 'stale-lock-recovery', 'inspect-lock-marker-without-mutating');
  }
  cleanupStaleTransactionArtifacts(lockPath.slice(0, -'.lock'.length), existingOwner);
  const initializationPath = lockInitializationPath(lockPath, existingOwner);
  if (fs.existsSync(initializationPath)) {
    const initializationStat = fs.lstatSync(initializationPath);
    if (!initializationStat.isFile() || !sameFileIdentity(staleStat, initializationStat)) {
      throw manualRecoveryRequired(
        `baseline lock initialization artifact identity mismatch: ${path.basename(initializationPath)}`,
      );
    }
    fs.unlinkSync(initializationPath);
    fsyncParentDirectory(lockPath);
  }
  const finalLockStat = fs.lstatSync(lockPath);
  const finalOwner = parseLockOwner(fs.readFileSync(lockPath), lockPath);
  if (!sameFileIdentity(staleStat, finalLockStat) || finalOwner.nonce !== existingOwner.nonce) {
    const message = `baseline lock changed during stale artifact cleanup: ${safePathName(lockPath)}`;
    throw typedRecoveryError(message, 'BASELINE_LOCK_RECOVERY_CONFLICT', 'stale-lock-recovery', 'inspect-lock-marker-without-mutating');
  }
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
      const message = `baseline lock ownership changed before release: ${safePathName(lockPath)}`;
      throw typedRecoveryError(message, 'BASELINE_LOCK_RELEASE_CONFLICT', 'lock-release', 'inspect-lock-marker-without-mutating');
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
  transaction,
} = {}) {
  const lockPath = `${filePath}.lock`;
  const owner = lockOwner({ transaction });
  parseLockOwner(Buffer.from(`${JSON.stringify(owner)}\n`, 'utf8'), lockPath);
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
  const phase = stage.replaceAll(' ', '-');
  if (!publicPhasePattern.test(phase)) {
    throw new Error('baseline durability phase is outside the stable token protocol');
  }
  const finalStateKind = !finalState.exists
    ? 'missing'
    : (finalState.stable ? 'present-stable' : 'present-unstable');
  const result = typedRecoveryError(
    `baseline ${committed ? 'committed but ' : ''}durability unknown after ${phase} for ${safePathName(filePath)}; reconcile required; final-state=${finalStateKind}`,
    committed ? 'BASELINE_COMMITTED_DURABILITY_UNKNOWN' : 'BASELINE_DURABILITY_UNKNOWN',
    phase,
    'inspect-final-state-and-marker-without-mutating',
    { cause },
  );
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
  state.lock = acquireBaselineLock(context.filePath, {
    installSignalHandlers: false,
    transaction: {
      expectedHash: context.expectedHash,
      nextHash: context.nextHash,
    },
  });
  Object.assign(context, transactionArtifactPaths(context.filePath, state.lock.owner));
  context.failpoint('after-lock');
  const currentBytes = fs.readFileSync(context.filePath);
  const currentMode = fs.statSync(context.filePath).mode & 0o777;
  if (hashBaselineBytes(currentBytes) !== context.expectedHash) {
    throw new Error(
      `baseline compare-and-swap failed for ${safePathName(context.filePath)}: tracked hash changed`,
    );
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
    const message = `baseline compare-and-swap failed for ${safePathName(context.filePath)}: target changed after check`;
    throw typedRecoveryError(message, 'BASELINE_CAS_CONFLICT', 'compare-and-swap', 'refresh-baseline-snapshot-before-retrying');
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
  state.cleanupDirFsyncRequired = true;
  state.committed = true;
  try {
    fsyncParentDirectory(context.filePath, context.failpoint, 'before-commit-dir-fsync');
    state.cleanupDirFsyncRequired = false;
    if (hashBaselineBytes(fs.readFileSync(context.filePath)) !== context.nextHash) {
      throw new Error(`baseline candidate changed during commit for ${safePathName(context.filePath)}`);
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
  state.cleanupDirFsyncRequired = true;
  state.claimed = false;
  fsyncParentDirectory(context.filePath, context.failpoint, 'before-cleanup-dir-fsync');
  state.cleanupDirFsyncRequired = false;
  context.failpoint('after-cleanup-dir-fsync');
  const finalState = readBaselineFileState(context.filePath);
  if (stateMatchesCandidate(finalState, context.nextHash, context.nextBytes, state.candidateIdentity)) return;
  const mismatch = new Error(`baseline final inode, bytes, or hash does not match candidate: ${safePathName(context.filePath)}`);
  throw durableUnknown(
    context.filePath,
    'final candidate verification',
    mismatch,
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

function cleanupOwnedTransactionArtifacts(context, state) {
  if (state.tempHandle !== undefined) {
    fs.closeSync(state.tempHandle);
    state.tempHandle = undefined;
  }
  const artifacts = [
    [context.tempPath, 'before-cleanup-temp-unlink'],
    [context.backupPath, 'before-cleanup-backup-unlink'],
  ];
  if (state.claimed) {
    const finalState = readBaselineFileState(context.filePath);
    const expectedHash = state.committed ? context.nextHash : context.expectedHash;
    if (!stateMatchesSnapshot(finalState, expectedHash)) {
      const message = `baseline target changed before owned artifact cleanup: ${safePathName(context.filePath)}`;
      throw typedRecoveryError(message, 'BASELINE_TRANSACTION_STATE_CONFLICT', 'owned-artifact-cleanup', 'inspect-target-and-marker-without-mutating');
    }
  }
  let removed = false;
  for (const [artifactPath, failurePoint] of artifacts) {
    if (!artifactPath || !fs.existsSync(artifactPath)) continue;
    if (state.lock === undefined) {
      throw new Error(`baseline transaction artifact exists without an owned lock: ${safePathName(artifactPath)}`);
    }
    const expectedPath = transactionArtifactPaths(context.filePath, state.lock.owner)[
      artifactPath.endsWith('.tmp') ? 'tempPath' : 'backupPath'
    ];
    assertOwnedArtifactPath(context.filePath, artifactPath, expectedPath);
    const artifactStat = fs.lstatSync(artifactPath);
    if (!artifactStat.isFile() || artifactStat.isSymbolicLink()) {
      throw manualRecoveryRequired(
        `baseline transaction artifact is not a regular file: ${safePathName(artifactPath)}`,
      );
    }
    context.failpoint(failurePoint);
    fs.unlinkSync(artifactPath);
    removed = true;
    state.cleanupDirFsyncRequired = true;
  }
  if (removed || state.cleanupDirFsyncRequired) {
    fsyncParentDirectory(
      context.filePath,
      context.failpoint,
      'before-resource-cleanup-dir-fsync',
    );
    state.cleanupDirFsyncRequired = false;
  }
  for (const [artifactPath] of artifacts) {
    if (artifactPath && fs.existsSync(artifactPath)) {
      throw manualRecoveryRequired(
        `baseline transaction artifact survived cleanup: ${safePathName(artifactPath)}`,
      );
    }
  }
  state.claimed = false;
}

function cleanupBaselineTransaction(context, state) {
  try {
    cleanupOwnedTransactionArtifacts(context, state);
    if (state.lock === undefined) return;
    if (state.cleanupDirFsyncRequired) {
      throw manualRecoveryRequired(
        `baseline transaction cleanup directory fsync is incomplete: ${safePathName(context.filePath)}`,
      );
    }
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
    throw new Error(`baseline expected snapshot mismatch for ${safePathName(filePath)}`);
  }
  const next = {
    _meta: { updatedAt: now().toISOString().replace(/\.\d{3}Z$/, 'Z') },
    files: candidate.files,
  };
  assertFrontendCodeSizeBaselineSchema(next, 'candidate baseline');
  const diff = describeBaselineDiff(previous, next);
  if (diff.length === 0) return { changed: false, diff: [] };
  if (platform === 'win32') {
    const message = 'crash-atomic baseline replacement is not verified on win32; refusing to mutate the baseline';
    throw typedRecoveryError(message, 'BASELINE_ATOMIC_REPLACE_UNSUPPORTED', 'platform-validation', 'use-supported-platform-for-changed-update');
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
    cleanupDirFsyncRequired: false,
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
    if (transactionError === undefined) {
      transactionError = cleanupError;
    } else if (state.lock !== undefined) {
      const message = `baseline transaction failed while owned recovery artifacts remain for ${safePathName(filePath)}`;
      transactionError = typedRecoveryError(message, 'BASELINE_TRANSACTION_FAILED_WITH_RECOVERY_REQUIRED', 'resource-or-lock-cleanup', 'inspect-marker-and-owned-artifacts-without-deleting', { cause: transactionError });
    }
  }
  if (transactionError !== undefined) throw transactionError;
  return result;
}
