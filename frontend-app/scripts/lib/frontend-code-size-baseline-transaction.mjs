import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
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

function fsyncParentDirectory(filePath) {
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

  const lockPath = `${filePath}.lock`;
  const tempPath = path.join(path.dirname(filePath), `.${path.basename(filePath)}.${process.pid}.${crypto.randomUUID()}.tmp`);
  let lockHandle;
  let tempHandle;
  try {
    lockHandle = fs.openSync(lockPath, 'wx', 0o600);
    fs.writeFileSync(lockHandle, `${process.pid}\n`, 'utf8');
    fs.fsyncSync(lockHandle);
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
    fs.writeFileSync(tempHandle, baselineBytes(next));
    fs.fsyncSync(tempHandle);
    failpoint('after-temp-fsync');
    fs.closeSync(tempHandle);
    tempHandle = undefined;

    fs.renameSync(tempPath, filePath);
    fsyncParentDirectory(filePath);
    return { changed: true, diff };
  } finally {
    if (tempHandle !== undefined) fs.closeSync(tempHandle);
    if (fs.existsSync(tempPath)) fs.unlinkSync(tempPath);
    if (lockHandle !== undefined) {
      fs.closeSync(lockHandle);
      if (fs.existsSync(lockPath)) fs.unlinkSync(lockPath);
    }
  }
}
