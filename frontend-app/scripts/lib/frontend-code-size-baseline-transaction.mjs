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
    throw new Error(`baseline lock is malformed or forged: ${lockPath}`);
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
    throw new Error(`baseline lock is malformed or forged: ${lockPath}`);
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
    throw new Error(`baseline lock is owned by live process ${existingOwner.pid}: ${lockPath}`);
  }
  const currentStat = fs.lstatSync(lockPath);
  if (!sameFileIdentity(staleStat, currentStat)) {
    throw new Error(`baseline lock changed during stale recovery: ${lockPath}`);
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

function durableUnknown(filePath, stage, cause) {
  let finalState;
  try {
    const bytes = fs.readFileSync(filePath);
    finalState = { exists: true, hash: hashBaselineBytes(bytes), bytes: bytes.toString('utf8') };
  } catch (error) {
    finalState = { exists: false, readError: error.message };
  }
  const result = new Error(
    `baseline durability unknown after ${stage} for ${filePath}; reconcile required; final state: ${JSON.stringify(finalState)}`,
    { cause },
  );
  result.code = 'BASELINE_DURABILITY_UNKNOWN';
  result.finalState = finalState;
  return result;
}

function restoreClaimedBaseline({ filePath, backupPath, tempPath, failpoint }) {
  try {
    if (fs.existsSync(filePath)) {
      const targetStat = fs.statSync(filePath);
      const tempStat = fs.statSync(tempPath);
      if (!sameFileIdentity(targetStat, tempStat)) {
        throw new Error(`cannot rollback without overwriting an external target: ${filePath}`);
      }
      fs.unlinkSync(filePath);
    }
    failpoint('before-rollback-rename');
    fs.renameSync(backupPath, filePath);
    fsyncParentDirectory(filePath, failpoint, 'before-rollback-dir-fsync');
  } catch (error) {
    throw durableUnknown(filePath, 'rollback', error);
  }
}

export function writeBaselineTransaction({
  filePath,
  expectedHash,
  previous,
  candidate,
  now = () => new Date(),
  failpoint = noFailureInjection,
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
  const nextBytes = baselineBytes(next);
  const nextHash = hashBaselineBytes(nextBytes);

  const tempPath = path.join(path.dirname(filePath), `.${path.basename(filePath)}.${process.pid}.${crypto.randomUUID()}.tmp`);
  const backupPath = path.join(path.dirname(filePath), `.${path.basename(filePath)}.${process.pid}.${crypto.randomUUID()}.bak`);
  let lock;
  let tempHandle;
  let claimed = false;
  let installed = false;
  let committed = false;
  try {
    lock = acquireBaselineLock(filePath);
    failpoint('after-lock');

    const currentBytes = fs.readFileSync(filePath);
    const currentMode = fs.statSync(filePath).mode & 0o777;
    if (hashBaselineBytes(currentBytes) !== expectedHash) {
      throw new Error(`baseline compare-and-swap failed for ${filePath}: tracked hash changed`);
    }
    assertFrontendCodeSizeBaselineSchema(JSON.parse(currentBytes.toString('utf8')), 'current baseline');
    process.stdout.write(`frontend code size baseline update diff: ${filePath}\n`);
    for (const line of diff) process.stdout.write(`${line}\n`);
    failpoint('before-temp');

    tempHandle = fs.openSync(tempPath, 'wx', 0o600);
    fs.fchmodSync(tempHandle, currentMode);
    fs.writeFileSync(tempHandle, nextBytes);
    fs.fsyncSync(tempHandle);
    failpoint('after-temp-fsync');
    fs.closeSync(tempHandle);
    tempHandle = undefined;

    failpoint('before-claim-rename');
    fs.renameSync(filePath, backupPath);
    claimed = true;
    const claimedBytes = fs.readFileSync(backupPath);
    if (hashBaselineBytes(claimedBytes) !== expectedHash) {
      restoreClaimedBaseline({ filePath, backupPath, tempPath, failpoint });
      claimed = false;
      throw new Error(`baseline compare-and-swap failed for ${filePath}: target changed after check`);
    }
    assertFrontendCodeSizeBaselineSchema(JSON.parse(claimedBytes.toString('utf8')), 'claimed baseline');
    failpoint('before-install');
    fs.linkSync(tempPath, filePath);
    installed = true;
    try {
      fsyncParentDirectory(filePath, failpoint, 'before-commit-dir-fsync');
      if (hashBaselineBytes(fs.readFileSync(filePath)) !== nextHash) {
        throw new Error(`baseline candidate changed during commit for ${filePath}`);
      }
      committed = true;
    } catch (error) {
      restoreClaimedBaseline({ filePath, backupPath, tempPath, failpoint });
      claimed = false;
      installed = false;
      throw error;
    }
    fs.unlinkSync(backupPath);
    claimed = false;
    fs.unlinkSync(tempPath);
    fsyncParentDirectory(filePath, failpoint, 'before-cleanup-dir-fsync');
    return { changed: true, diff };
  } catch (error) {
    if (committed) throw durableUnknown(filePath, 'post-commit cleanup', error);
    if (error?.code === 'BASELINE_DURABILITY_UNKNOWN') throw error;
    if (claimed) {
      restoreClaimedBaseline({ filePath, backupPath, tempPath, failpoint });
      claimed = false;
      installed = false;
    }
    throw error;
  } finally {
    if (tempHandle !== undefined) fs.closeSync(tempHandle);
    if (fs.existsSync(tempPath)) fs.unlinkSync(tempPath);
    if (!claimed && fs.existsSync(backupPath)) fs.unlinkSync(backupPath);
    if (lock !== undefined) lock.release();
  }
}
