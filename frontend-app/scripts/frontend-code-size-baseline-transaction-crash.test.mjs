import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import { FRONTEND_CODE_SIZE_LIMITS } from './frontend-code-size-guard.mjs';
import {
  acquireBaselineLock,
  baselineBytes,
  hashBaselineBytes,
  writeBaselineTransaction,
} from './lib/frontend-code-size-baseline-transaction.mjs';

const appRoot = path.resolve(import.meta.dirname, '..');
const marker = 'crash-injection-ready\n';

function baselineData(files = {}) {
  return {
    _meta: { updatedAt: '2026-07-09T00:00:00Z' },
    files,
  };
}

function frozenFileLengthMetrics(lines) {
  return {
    lines,
    maxFuncLen: 0,
    maxNesting: 0,
    maxParams: 0,
    exportCount: 0,
    consoleLogs: 0,
    anyCount: 0,
    emptyFuncs: 0,
    [['to', 'do', 'Count'].join('')]: 0,
    longLineCount: 0,
    frozenViolations: [
      `file-length\0文件有效代码 ${lines} 行，超过上限 ${FRONTEND_CODE_SIZE_LIMITS.maxFileLines} 行`,
    ],
  };
}

function transactionFixture() {
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-code-size-crash-')));
  const filePath = path.join(root, '.frontend_code_size_guard_baseline.json');
  const previous = baselineData({
    'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3),
  });
  const candidate = baselineData({
    'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
  });
  const oldBytes = baselineBytes(previous);
  const newBytes = baselineBytes({
    _meta: { updatedAt: '2026-07-10T00:00:00Z' },
    files: candidate.files,
  });
  fs.writeFileSync(filePath, oldBytes);
  return { root, filePath, previous, candidate, oldBytes, newBytes };
}

function waitForMarker(child) {
  return new Promise((resolve, reject) => {
    let stdout = '';
    let stderr = '';
    const timeout = setTimeout(() => reject(new Error(`transaction child did not reach crash marker; stderr=${stderr}`)), 10_000);
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
      if (stdout.includes(marker)) {
        clearTimeout(timeout);
        resolve();
      }
    });
    child.stderr.on('data', (chunk) => {
      stderr += chunk;
    });
    child.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.once('exit', (code, signal) => {
      clearTimeout(timeout);
      reject(new Error(`transaction child exited before crash marker: code=${code} signal=${signal}; stderr=${stderr}`));
    });
  });
}

function spawnTransactionChild(fixture, failurePoint) {
  const modulePath = path.join(appRoot, 'scripts/lib/frontend-code-size-baseline-transaction.mjs');
  const childSource = [
    `import { writeBaselineTransaction, baselineBytes, hashBaselineBytes } from ${JSON.stringify(modulePath)};`,
    `const previous = ${JSON.stringify(fixture.previous)};`,
    `const candidate = ${JSON.stringify(fixture.candidate)};`,
    'writeBaselineTransaction({',
    `  filePath: ${JSON.stringify(fixture.filePath)},`,
    '  previous,',
    '  candidate,',
    '  expectedHash: hashBaselineBytes(baselineBytes(previous)),',
    "  now: () => new Date('2026-07-10T00:00:00Z'),",
    '  failpoint(point) {',
    `    if (point !== ${JSON.stringify(failurePoint)}) return;`,
    `    process.stdout.write(${JSON.stringify(marker)});`,
    '    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0);',
    '  },',
    '});',
  ].join('\n');
  return spawn(process.execPath, ['--input-type=module', '-e', childSource], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function spawnRollbackCleanupFailureChild(fixture, cleanupFailurePoint) {
  const modulePath = path.join(appRoot, 'scripts/lib/frontend-code-size-baseline-transaction.mjs');
  const childSource = [
    `import { writeBaselineTransaction, baselineBytes, hashBaselineBytes } from ${JSON.stringify(modulePath)};`,
    `const previous = ${JSON.stringify(fixture.previous)};`,
    `const candidate = ${JSON.stringify(fixture.candidate)};`,
    'try {',
    '  writeBaselineTransaction({',
    `    filePath: ${JSON.stringify(fixture.filePath)},`,
    '    previous,',
    '    candidate,',
    '    expectedHash: hashBaselineBytes(baselineBytes(previous)),',
    "    now: () => new Date('2026-07-10T00:00:00Z'),",
    '    failpoint(point) {',
    "      if (point === 'before-atomic-replace') throw new Error('primary failure');",
    "      if (point === 'before-rollback-rename') throw new Error('rollback failure');",
    `      if (point === ${JSON.stringify(cleanupFailurePoint)}) throw new Error('owned artifact cleanup failure');`,
    '    },',
    '  });',
    '} catch {}',
    `process.stdout.write(${JSON.stringify(marker)});`,
    'setInterval(() => {}, 1000);',
  ].join('\n');
  return spawn(process.execPath, ['--input-type=module', '-e', childSource], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function ownedArtifactPaths(filePath, owner) {
  const prefix = path.join(path.dirname(filePath), `.${path.basename(filePath)}.${owner.pid}.${owner.nonce}`);
  return {
    tempPath: `${prefix}.tmp`,
    backupPath: `${prefix}.bak`,
  };
}

describe.skipIf(process.platform === 'win32')('frontend code size baseline crash atomicity', () => {
  it('orders candidate, backup, atomic replace, commit fsync and cleanup boundaries', () => {
    const fixture = transactionFixture();
    const stages = [];
    try {
      writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        previous: fixture.previous,
        candidate: fixture.candidate,
        now: () => new Date('2026-07-10T00:00:00Z'),
        failpoint: (point) => stages.push(point),
      });
      expect(stages).toEqual([
        'after-lock',
        'before-temp',
        'after-temp-fsync',
        'before-claim-rename',
        'before-backup-link',
        'before-backup-dir-fsync',
        'before-install',
        'before-atomic-replace',
        'before-commit-dir-fsync',
        'after-atomic-replace-before-cleanup',
        'before-cleanup-dir-fsync',
        'after-cleanup-dir-fsync',
        'before-lock-release',
      ]);
      expect(fs.readFileSync(fixture.filePath).equals(fixture.newBytes)).toBe(true);
      expect(fs.readFileSync(fixture.filePath, 'utf8')).toContain('文件有效代码');
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    'before-backup-link',
    'before-backup-dir-fsync',
    'before-atomic-replace',
  ])('rolls back complete old UTF-8 bytes on a captured %s failure', (failurePoint) => {
    const fixture = transactionFixture();
    try {
      expect(() => writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        previous: fixture.previous,
        candidate: fixture.candidate,
        now: () => new Date('2026-07-10T00:00:00Z'),
        failpoint(point) {
          if (point === failurePoint) throw new Error(`injected ${failurePoint}`);
        },
      })).toThrow();
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
      expect(fs.readFileSync(fixture.filePath, 'utf8')).toContain('文件有效代码');
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('keeps complete new bytes and reports committed durability unknown after atomic replace', () => {
    const fixture = transactionFixture();
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: hashBaselineBytes(fixture.oldBytes),
          previous: fixture.previous,
          candidate: fixture.candidate,
          now: () => new Date('2026-07-10T00:00:00Z'),
          failpoint(point) {
            if (point === 'before-commit-dir-fsync') throw new Error('injected target directory fsync failure');
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_COMMITTED_DURABILITY_UNKNOWN');
      expect(caught?.committed).toBe(true);
      expect(caught?.cause?.message).toContain('injected target directory fsync failure');
      expect(fs.readFileSync(fixture.filePath).equals(fixture.newBytes)).toBe(true);
      expect(() => JSON.parse(fs.readFileSync(fixture.filePath, 'utf8'))).not.toThrow();
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['before-backup-link', 'old', 'BASELINE_PRIMARY_IO_FAILURE'],
    ['before-commit-dir-fsync', 'new', 'BASELINE_COMMITTED_DURABILITY_UNKNOWN'],
  ])('does not mask the %s primary error with a later cleanup error', (failurePoint, expectedVersion, expectedCode) => {
    const fixture = transactionFixture();
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: hashBaselineBytes(fixture.oldBytes),
          previous: fixture.previous,
          candidate: fixture.candidate,
          now: () => new Date('2026-07-10T00:00:00Z'),
          failpoint(point) {
            if (point === failurePoint) {
              const error = new Error(`primary ${failurePoint}`);
              error.code = 'BASELINE_PRIMARY_IO_FAILURE';
              throw error;
            }
            if (point === 'before-lock-release') throw new Error('secondary cleanup failure');
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe(expectedCode);
      const primaryError = expectedVersion === 'old' ? caught : caught?.cause;
      expect(primaryError?.message).toContain(`primary ${failurePoint}`);
      expect(primaryError?.message).not.toContain('secondary cleanup failure');
      const expectedBytes = expectedVersion === 'old' ? fixture.oldBytes : fixture.newBytes;
      expect(fs.readFileSync(fixture.filePath).equals(expectedBytes)).toBe(true);
      expect(fs.existsSync(`${fixture.filePath}.lock`)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('fails typed and mutation-free for changed win32 transactions while no-change stays read-only', () => {
    const fixture = transactionFixture();
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: hashBaselineBytes(fixture.oldBytes),
          previous: fixture.previous,
          candidate: fixture.candidate,
          platform: 'win32',
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_ATOMIC_REPLACE_UNSUPPORTED');
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);

      expect(writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        previous: fixture.previous,
        candidate: fixture.previous,
        platform: 'win32',
      })).toEqual({ changed: false, diff: [] });
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('publishes a complete versioned transaction marker atomically before staging artifacts', () => {
    const fixture = transactionFixture();
    try {
      let observedOwner;
      let observedNames;
      expect(() => writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        previous: fixture.previous,
        candidate: fixture.candidate,
        now: () => new Date('2026-07-10T00:00:00Z'),
        failpoint(point) {
          if (point !== 'after-lock') return;
          observedOwner = JSON.parse(fs.readFileSync(`${fixture.filePath}.lock`, 'utf8'));
          observedNames = fs.readdirSync(fixture.root);
          throw new Error('stop after marker publication');
        },
      })).toThrow(/stop after marker publication/);
      expect(observedOwner).toMatchObject({
        version: 2,
        transaction: {
          expectedHash: hashBaselineBytes(fixture.oldBytes),
          nextHash: hashBaselineBytes(fixture.newBytes),
        },
      });
      expect(observedNames).toEqual([
        '.frontend_code_size_guard_baseline.json',
        '.frontend_code_size_guard_baseline.json.lock',
      ]);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['unknown top-level field', (owner) => ({ ...owner, unexpected: true })],
    ['unknown transaction field', (owner) => ({
      ...owner,
      version: 2,
      transaction: {
        expectedHash: hashBaselineBytes(Buffer.from('old')),
        nextHash: hashBaselineBytes(Buffer.from('new')),
        unexpected: true,
      },
    })],
    ['path-escaping nonce', (owner) => ({ ...owner, nonce: '../../outside-artifact' })],
  ])('keeps a malformed marker for manual recovery: %s', (_label, mutateOwner) => {
    const fixture = transactionFixture();
    const lockPath = `${fixture.filePath}.lock`;
    const owner = mutateOwner({
      version: 1,
      pid: 2147483647,
      startIdentity: 'dead-process',
      nonce: '0123456789abcdef',
      createdAt: '2026-07-10T00:00:00.000Z',
    });
    fs.writeFileSync(lockPath, `${JSON.stringify(owner)}\n`);
    try {
      let caught;
      try {
        acquireBaselineLock(fixture.filePath, {
          installSignalHandlers: false,
          resolveProcessIdentity: () => null,
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_LOCK_PROTOCOL_ERROR');
      expect(caught?.recoveryAction).toBe('inspect-lock-marker-without-mutating');
      expect(caught?.message).not.toContain(fixture.root);
      expect(caught?.message).not.toContain(lockPath);
      expect(fs.existsSync(lockPath)).toBe(true);
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('fails typed without guessing when a legacy dead marker owns artifacts', () => {
    const fixture = transactionFixture();
    const lockPath = `${fixture.filePath}.lock`;
    const owner = {
      version: 1,
      pid: 2147483647,
      startIdentity: 'dead-process',
      nonce: '0123456789abcdef',
      createdAt: '2026-07-10T00:00:00.000Z',
    };
    const artifacts = ownedArtifactPaths(fixture.filePath, owner);
    fs.writeFileSync(lockPath, `${JSON.stringify(owner)}\n`);
    fs.writeFileSync(artifacts.backupPath, fixture.oldBytes);
    try {
      let caught;
      try {
        acquireBaselineLock(fixture.filePath, {
          installSignalHandlers: false,
          resolveProcessIdentity: () => null,
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_LOCK_MANUAL_RECOVERY_REQUIRED');
      expect(caught?.recoveryAction).toBe('inspect-marker-and-owned-artifacts-without-deleting');
      expect(caught?.message).not.toContain(fixture.root);
      expect(caught?.message).not.toContain(fixture.filePath);
      expect(fs.existsSync(lockPath)).toBe(true);
      expect(fs.existsSync(artifacts.backupPath)).toBe(true);
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('keeps a typed private manual-recovery contract for mismatched owned artifact bytes', () => {
    const fixture = transactionFixture();
    const lockPath = `${fixture.filePath}.lock`;
    const owner = {
      version: 2,
      pid: 2147483647,
      startIdentity: 'dead-process',
      nonce: '0123456789abcdef',
      createdAt: '2026-07-10T00:00:00.000Z',
      transaction: {
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        nextHash: hashBaselineBytes(fixture.newBytes),
      },
    };
    const artifacts = ownedArtifactPaths(fixture.filePath, owner);
    fs.writeFileSync(lockPath, `${JSON.stringify(owner)}\n`);
    fs.writeFileSync(artifacts.backupPath, fixture.newBytes);
    try {
      let caught;
      try {
        acquireBaselineLock(fixture.filePath, {
          installSignalHandlers: false,
          resolveProcessIdentity: () => null,
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_LOCK_MANUAL_RECOVERY_REQUIRED');
      expect(caught?.recoveryAction).toBe('inspect-marker-and-owned-artifacts-without-deleting');
      expect(caught?.message).not.toContain(fixture.root);
      expect(caught?.message).not.toContain(fixture.filePath);
      expect(caught?.message).not.toContain(fixture.newBytes.toString('utf8'));
      expect(fs.existsSync(lockPath)).toBe(true);
      expect(fs.existsSync(artifacts.backupPath)).toBe(true);
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('keeps marker and artifacts when a dead transaction target is in a third state', () => {
    const fixture = transactionFixture();
    const lockPath = `${fixture.filePath}.lock`;
    const owner = {
      version: 2,
      pid: 2147483647,
      startIdentity: 'dead-process',
      nonce: '0123456789abcdef',
      createdAt: '2026-07-10T00:00:00.000Z',
      transaction: {
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        nextHash: hashBaselineBytes(fixture.newBytes),
      },
    };
    const artifacts = ownedArtifactPaths(fixture.filePath, owner);
    const thirdBytes = baselineBytes(baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 2),
    }));
    fs.writeFileSync(lockPath, `${JSON.stringify(owner)}\n`);
    fs.writeFileSync(artifacts.backupPath, fixture.oldBytes);
    fs.writeFileSync(fixture.filePath, thirdBytes);
    try {
      let caught;
      try {
        acquireBaselineLock(fixture.filePath, {
          installSignalHandlers: false,
          resolveProcessIdentity: () => null,
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_STALE_TARGET_CONFLICT');
      expect(caught?.recoveryAction).toBe('inspect-target-and-marker-without-mutating');
      expect(caught?.message).not.toContain(fixture.root);
      expect(caught?.message).not.toContain(fixture.filePath);
      expect(fs.existsSync(lockPath)).toBe(true);
      expect(fs.existsSync(artifacts.backupPath)).toBe(true);
      expect(fs.readFileSync(fixture.filePath).equals(thirdBytes)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('never releases the recovery marker while rollback-owned artifacts remain', () => {
    const fixture = transactionFixture();
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: hashBaselineBytes(fixture.oldBytes),
          previous: fixture.previous,
          candidate: fixture.candidate,
          now: () => new Date('2026-07-10T00:00:00Z'),
          failpoint(point) {
            if (point === 'before-atomic-replace') throw new Error('primary failure');
            if (point === 'before-rollback-rename') throw new Error('rollback failure');
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_DURABILITY_UNKNOWN');
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
      const names = fs.readdirSync(fixture.root);
      const lockPresent = names.some((name) => name.endsWith('.lock'));
      const ownedArtifactPresent = names.some((name) => name.endsWith('.bak') || name.endsWith('.tmp'));
      expect(lockPresent || !ownedArtifactPresent).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    'before-cleanup-backup-unlink',
    'before-resource-cleanup-dir-fsync',
  ])('keeps a live recovery marker on %s failure and recovers after owner exit', async (cleanupFailurePoint) => {
    const fixture = transactionFixture();
    const child = spawnRollbackCleanupFailureChild(fixture, cleanupFailurePoint);
    try {
      await waitForMarker(child);
      expect(fs.readFileSync(fixture.filePath).equals(fixture.oldBytes)).toBe(true);
      expect(fs.existsSync(`${fixture.filePath}.lock`)).toBe(true);
      expect(() => writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        previous: fixture.previous,
        candidate: fixture.candidate,
        now: () => new Date('2026-07-10T00:00:00Z'),
      })).toThrow(/live cooperative process/);

      child.kill('SIGKILL');
      await new Promise((resolve) => child.once('exit', resolve));
      expect(writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(fixture.oldBytes),
        previous: fixture.previous,
        candidate: fixture.candidate,
        now: () => new Date('2026-07-10T00:00:00Z'),
      }).changed).toBe(true);
      expect(fs.readFileSync(fixture.filePath).equals(fixture.newBytes)).toBe(true);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['old', 'before-atomic-replace', true],
    ['new', 'after-atomic-replace-before-cleanup', false],
  ])('keeps complete %s target bytes after SIGKILL at %s', async (expectedVersion, failurePoint, keepsTemp) => {
    const fixture = transactionFixture();
    const child = spawnTransactionChild(fixture, failurePoint);
    try {
      await waitForMarker(child);
      child.kill('SIGKILL');
      await new Promise((resolve) => child.once('exit', resolve));

      const expectedBytes = expectedVersion === 'old' ? fixture.oldBytes : fixture.newBytes;
      expect(fs.readFileSync(fixture.filePath).equals(expectedBytes)).toBe(true);
      expect(() => JSON.parse(fs.readFileSync(fixture.filePath, 'utf8'))).not.toThrow();

      const residualNames = fs.readdirSync(fixture.root)
        .filter((name) => path.join(fixture.root, name) !== fixture.filePath);
      expect(residualNames.some((name) => name.endsWith('.lock'))).toBe(true);
      expect(residualNames.some((name) => name.endsWith('.bak'))).toBe(true);
      expect(residualNames.some((name) => name.endsWith('.tmp'))).toBe(keepsTemp);
      for (const name of residualNames.filter((entry) => entry.endsWith('.bak') || entry.endsWith('.tmp'))) {
        const bytes = fs.readFileSync(path.join(fixture.root, name));
        expect([hashBaselineBytes(fixture.oldBytes), hashBaselineBytes(fixture.newBytes)])
          .toContain(hashBaselineBytes(bytes));
        expect(() => JSON.parse(bytes.toString('utf8'))).not.toThrow();
      }

      const recoveryPrevious = expectedVersion === 'old'
        ? fixture.previous
        : {
            _meta: { updatedAt: '2026-07-10T00:00:00Z' },
            files: fixture.candidate.files,
          };
      const recoveryCandidate = expectedVersion === 'old'
        ? fixture.candidate
        : baselineData({
            'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines),
          });
      const recoveryResult = writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: hashBaselineBytes(expectedBytes),
        previous: recoveryPrevious,
        candidate: recoveryCandidate,
        now: () => new Date(expectedVersion === 'old' ? '2026-07-10T00:00:00Z' : '2026-07-11T00:00:00Z'),
      });
      expect(recoveryResult.changed).toBe(true);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
      expect(() => JSON.parse(fs.readFileSync(fixture.filePath, 'utf8'))).not.toThrow();
    } finally {
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });
});
