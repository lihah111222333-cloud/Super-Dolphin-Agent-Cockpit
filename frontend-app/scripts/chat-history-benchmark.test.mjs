import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cwd, execPath } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  ATTEMPTS_PER_SAMPLE,
  MEASUREMENT_ITERATIONS,
  buildChatHistoryBenchmarkCases,
  measureChatHistoryCase,
  runChatHistoryBenchmarkSamples,
  verifyChatHistoryEvidence,
} from './chat-history-benchmark.mjs';
import { buildChatHistoryFixture } from '../src/pages/chat/model/chatHistoryBenchmarkFixture.js';

const RESULT_KEYS = [
  'case',
  'turns',
  'toolsPerTurn',
  'materializedCount',
  'durationMs',
  'heapDeltaBytes',
  'node',
  'commit',
];

describe('chat history benchmark', () => {
  it('owns the exact default cross-product and gates 10000 turns behind extended mode', () => {
    expect(buildChatHistoryBenchmarkCases({ extended: false })).toEqual([
      { turns: 200, toolsPerTurn: 1 },
      { turns: 200, toolsPerTurn: 3 },
      { turns: 1_000, toolsPerTurn: 1 },
      { turns: 1_000, toolsPerTurn: 3 },
      { turns: 5_000, toolsPerTurn: 1 },
      { turns: 5_000, toolsPerTurn: 3 },
    ]);
    expect(buildChatHistoryBenchmarkCases({ extended: true })).toEqual([
      { turns: 200, toolsPerTurn: 1 },
      { turns: 200, toolsPerTurn: 3 },
      { turns: 1_000, toolsPerTurn: 1 },
      { turns: 1_000, toolsPerTurn: 3 },
      { turns: 5_000, toolsPerTurn: 1 },
      { turns: 5_000, toolsPerTurn: 3 },
      { turns: 10_000, toolsPerTurn: 1 },
      { turns: 10_000, toolsPerTurn: 3 },
    ]);
  });

  it('measures only bounded numeric evidence with exact output keys', () => {
    const history = buildChatHistoryFixture({ turns: 200, toolsPerTurn: 1, archived: true, seed: 7 });
    const result = measureChatHistoryCase(history, {
      caseName: 'turns-200-tools-1',
      turns: 200,
      toolsPerTurn: 1,
      node: 'v-test',
      commit: 'commit-test',
    });

    expect(Object.keys(result)).toEqual(RESULT_KEYS);
    expect(result).toEqual({
      case: 'turns-200-tools-1',
      turns: 200,
      toolsPerTurn: 1,
      materializedCount: 80,
      durationMs: expect.any(Number),
      heapDeltaBytes: expect.any(Number),
      node: 'v-test',
      commit: 'commit-test',
    });
    expect(Number.isFinite(result.durationMs)).toBe(true);
    expect(Number.isFinite(result.heapDeltaBytes)).toBe(true);
    const serialized = JSON.stringify(result);
    expect(serialized).not.toContain('synthetic-message-body');
    expect(serialized).not.toContain('fixture_tool_');
    expect(serialized).not.toContain('content');
  });

  it('prints one JSON array document with six exact private-free default results', () => {
    const stdout = execFileSync(execPath, [join(cwd(), 'scripts/chat-history-benchmark.mjs')], {
      cwd: cwd(),
      encoding: 'utf8',
    });
    const report = JSON.parse(stdout);

    expect(report).toHaveLength(6);
    report.forEach((result) => {
      expect(Object.keys(result)).toEqual(RESULT_KEYS);
      expect(result.materializedCount).toBe(80);
      expect(Number.isFinite(result.durationMs)).toBe(true);
      expect(Number.isFinite(result.heapDeltaBytes)).toBe(true);
    });
    expect(stdout.trimStart().startsWith('[')).toBe(true);
    expect(stdout.trimEnd().endsWith(']')).toBe(true);
    expect(stdout).not.toContain('synthetic-message-body');
    expect(stdout).not.toContain('fixture_tool_');
    expect(stdout).not.toContain('content');
    expect(stdout).not.toContain('npm run');
  });

  it('adds the 10000-turn cases only for the extended CLI flag', () => {
    const stdout = execFileSync(
      execPath,
      [join(cwd(), 'scripts/chat-history-benchmark.mjs'), '--extended'],
      { cwd: cwd(), encoding: 'utf8' },
    );
    const report = JSON.parse(stdout);

    expect(report).toHaveLength(8);
    expect(report.slice(-2).map(({ turns, toolsPerTurn }) => ({ turns, toolsPerTurn }))).toEqual([
      { turns: 10_000, toolsPerTurn: 1 },
      { turns: 10_000, toolsPerTurn: 3 },
    ]);
  });

  it('warms each case and reports exactly five duration samples with a median', () => {
    const evidence = runChatHistoryBenchmarkSamples({ commit: 'a'.repeat(40) });

    expect(evidence).toEqual(expect.objectContaining({
      metricId: 'P02-history-budget',
      sampleCount: 5,
      warmupCount: 1,
      subjectSha: 'a'.repeat(40),
    }));
    expect(Object.keys(evidence.cases)).toHaveLength(6);
    Object.values(evidence.cases).forEach((entry) => {
      expect(entry.attemptsPerSample).toBe(ATTEMPTS_PER_SAMPLE);
      expect(entry.iterationCount).toBe(MEASUREMENT_ITERATIONS);
      expect(entry.durationAttemptSamplesMs).toHaveLength(5);
      entry.durationAttemptSamplesMs.forEach((attempts, index) => {
        expect(attempts).toHaveLength(ATTEMPTS_PER_SAMPLE);
        expect(entry.durationSamplesMs[index]).toBe(Math.min(...attempts));
      });
      expect(entry.durationSamplesMs).toHaveLength(5);
      expect(Number.isFinite(entry.durationMedianMs)).toBe(true);
      expect(entry.durationMedianMs).toBeGreaterThanOrEqual(0);
    });
  }, 20_000);

  it('keeps verify NOT_VERIFIED until an exact frozen five-sample baseline exists', () => {
    expect(verifyChatHistoryEvidence({ cases: {} }, {
      metrics: {
        'P02-history-budget': {
          status: 'NOT_VERIFIED',
          reason: 'not frozen',
        },
      },
    })).toEqual(expect.objectContaining({
      metricId: 'P02-history-budget',
      status: 'NOT_VERIFIED',
    }));
  });

  it('registers the exact package script without lifecycle flags', () => {
    const packageJSON = JSON.parse(readFileSync(join(cwd(), 'package.json'), 'utf8'));

    expect(packageJSON.scripts['benchmark:chat-history']).toBe('node scripts/chat-history-benchmark.mjs');
    expect(packageJSON.scripts['benchmark:chat-history:verify']).toBe('node scripts/chat-history-benchmark.mjs --verify');
    expect(packageJSON.scripts['benchmark:verify']).toBe('node scripts/chat-history-benchmark.mjs --verify');
  });
});
