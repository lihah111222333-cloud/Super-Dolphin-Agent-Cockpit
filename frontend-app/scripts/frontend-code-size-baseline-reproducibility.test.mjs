import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { expect, it } from 'vitest';
import {
  baselineBytes,
  hashBaselineBytes,
  writeBaselineTransaction,
} from './lib/frontend-code-size-baseline-transaction.mjs';

function baseline(lines, updatedAt = '2026-07-09T00:00:00Z') {
  return {
    _meta: { updatedAt },
    files: {
      'src/fixture.js': {
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
        frozenViolations: [],
      },
    },
  };
}

it('keeps trusted baseline refresh reproducible across repeated migration checks', () => {
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-code-size-reproducible-')));
  const filePath = path.join(root, '.frontend_code_size_guard_baseline.json');
  try {
    const previous = baseline(2);
    const initialBytes = baselineBytes(previous);
    fs.writeFileSync(filePath, initialBytes);
    const first = writeBaselineTransaction({
      filePath,
      expectedHash: hashBaselineBytes(initialBytes),
      previous,
      candidate: baseline(1),
      preserveTimestamp: true,
      now: () => new Date('2030-01-01T00:00:00Z'),
    });
    expect(first.changed).toBe(true);
    const refreshed = JSON.parse(fs.readFileSync(filePath, 'utf8'));
    expect(refreshed._meta.updatedAt).toBe(previous._meta.updatedAt);
    const refreshedBytes = baselineBytes(refreshed);
    const second = writeBaselineTransaction({
      filePath,
      expectedHash: hashBaselineBytes(refreshedBytes),
      previous: refreshed,
      candidate: refreshed,
      preserveTimestamp: true,
    });
    expect(second.changed).toBe(false);
    expect(fs.readFileSync(filePath)).toEqual(refreshedBytes);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
