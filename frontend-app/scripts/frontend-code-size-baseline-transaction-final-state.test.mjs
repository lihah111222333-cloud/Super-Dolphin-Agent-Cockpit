import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { FRONTEND_CODE_SIZE_LIMITS } from './frontend-code-size-guard.mjs';
import {
  baselineBytes,
  hashBaselineBytes,
  writeBaselineTransaction,
} from './lib/frontend-code-size-baseline-transaction.mjs';

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
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-code-size-final-state-')));
  const filePath = path.join(root, '.frontend_code_size_guard_baseline.json');
  const previous = baselineData({
    'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3),
  });
  const candidate = baselineData({
    'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
  });
  const bytes = baselineBytes(previous);
  fs.writeFileSync(filePath, bytes);
  return { root, filePath, previous, candidate, hash: hashBaselineBytes(bytes) };
}

describe('frontend code size baseline transaction final-state reconciliation', () => {
  it('does not return changed when the target is replaced after cleanup directory fsync', () => {
    const fixture = transactionFixture();
    const concurrentBytes = baselineBytes(baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 2),
    }));
    const replacementPath = path.join(fixture.root, 'concurrent-replacement.json');
    fs.writeFileSync(replacementPath, concurrentBytes);
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: fixture.hash,
          previous: fixture.previous,
          candidate: fixture.candidate,
          failpoint(point) {
            if (point === 'after-cleanup-dir-fsync') fs.renameSync(replacementPath, fixture.filePath);
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_COMMITTED_DURABILITY_UNKNOWN');
      expect(caught?.committed).toBe(true);
      expect(caught?.finalState.hash).toBe(hashBaselineBytes(concurrentBytes));
      expect(fs.readFileSync(fixture.filePath).equals(concurrentBytes)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('classifies a concurrent target deletion at claim rename from the final missing state', () => {
    const fixture = transactionFixture();
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: fixture.hash,
          previous: fixture.previous,
          candidate: fixture.candidate,
          failpoint(point) {
            if (point === 'before-claim-rename') fs.unlinkSync(fixture.filePath);
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_DURABILITY_UNKNOWN');
      expect(caught?.finalState.exists).toBe(false);
      expect(fs.existsSync(fixture.filePath)).toBe(false);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['before-cleanup-dir-fsync', 'cleanup directory fsync'],
    ['before-lock-release', 'lock release'],
  ])('classifies a post-commit %s failure from the final candidate bytes', (failurePoint) => {
    const fixture = transactionFixture();
    const now = () => new Date('2026-07-10T00:00:00Z');
    const candidateBytes = baselineBytes({
      _meta: { updatedAt: '2026-07-10T00:00:00Z' },
      files: fixture.candidate.files,
    });
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: fixture.hash,
          previous: fixture.previous,
          candidate: fixture.candidate,
          now,
          failpoint(point) {
            if (point === 'before-cleanup-dir-fsync' && point === failurePoint) {
              throw new Error('injected cleanup fsync failure');
            }
            if (point === 'before-lock-release' && point === failurePoint) {
              const lockPath = `${fixture.filePath}.lock`;
              const owner = JSON.parse(fs.readFileSync(lockPath, 'utf8'));
              fs.writeFileSync(lockPath, `${JSON.stringify({ ...owner, nonce: 'fedcba9876543210' })}\n`);
            }
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_COMMITTED_DURABILITY_UNKNOWN');
      expect(caught?.committed).toBe(true);
      expect(caught?.finalState.hash).toBe(hashBaselineBytes(candidateBytes));
      expect(fs.readFileSync(fixture.filePath).equals(candidateBytes)).toBe(true);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });
});
