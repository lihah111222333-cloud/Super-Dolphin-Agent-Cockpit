import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { cwd, execPath } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  ATTEMPTS_PER_SAMPLE,
  DEFAULT_BASELINE_PATH,
  HISTORY_BLOCK_COUNT,
  HISTORY_BLOCK_ITERATIONS,
  MEASUREMENT_ITERATIONS,
  buildChatHistoryBenchmarkCases,
  measureChatHistoryCase,
  runChatHistoryBenchmarkSamples,
  verifyChatHistoryEvidence,
} from './chat-history-benchmark.mjs';
import { buildChatHistoryFixture } from '../src/pages/chat/model/chatHistoryBenchmarkFixture.js';
import { measurementMedian, median } from './performance-budget-model.mjs';

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

  it('warms each case and reports five recomputable paired normalized samples', () => {
    expect(HISTORY_BLOCK_COUNT).toBe(9);
    expect(HISTORY_BLOCK_ITERATIONS).toBe(500_000);
    expect(MEASUREMENT_ITERATIONS).toBe(4_500_000);
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
      expect(entry.blockCount).toBe(HISTORY_BLOCK_COUNT);
      expect(entry.blockIterationCount).toBe(HISTORY_BLOCK_ITERATIONS);
      expect(entry.iterationCount).toBe(MEASUREMENT_ITERATIONS);
      expect(entry.materializedCount).toBe(80);
      expect(entry.referenceMaterializedCount).toBe(80);
      expect(entry.durationClock).toContain('production/reference');
      expect(entry).not.toHaveProperty('durationSamplesMs');
      expect(entry).not.toHaveProperty('durationMedianMs');
      expect(entry.sampleDiagnostics).toHaveLength(5);
      entry.sampleDiagnostics.forEach((sample, sampleIndex) => {
        expect(sample.blockOrders).toEqual(Array.from(
          { length: HISTORY_BLOCK_COUNT },
          (_, blockIndex) => ((sampleIndex + blockIndex) % 2 === 0
            ? 'production-reference'
            : 'reference-production'),
        ));
        expect(sample.productionBlockCpuDurationsMs).toHaveLength(HISTORY_BLOCK_COUNT);
        expect(sample.referenceBlockCpuDurationsMs).toHaveLength(HISTORY_BLOCK_COUNT);
        expect(sample.rawNormalizedBlockRatios).toHaveLength(HISTORY_BLOCK_COUNT);
        sample.rawNormalizedBlockRatios.forEach((ratio, blockIndex) => {
          const production = sample.productionBlockCpuDurationsMs[blockIndex];
          const reference = sample.referenceBlockCpuDurationsMs[blockIndex];
          expect(Number.isFinite(production) && production > 0).toBe(true);
          expect(Number.isFinite(reference) && reference > 0).toBe(true);
          expect(ratio).toBe(production / reference);
        });
        expect(sample.normalizedRatio).toBe(measurementMedian(sample.rawNormalizedBlockRatios));
      });
      expect(entry.normalizedRatioSamples).toEqual(
        entry.sampleDiagnostics.map(({ normalizedRatio }) => normalizedRatio),
      );
      expect(entry.normalizedRatioMedian).toBe(median(entry.normalizedRatioSamples));
      expect(Object.keys(entry).filter((key) => key.includes('Ratio') && key.endsWith('Ms'))).toEqual([]);
    });
  }, 60_000);

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

  it('uses the frozen default baseline path', () => {
    expect(DEFAULT_BASELINE_PATH).toBe(join(cwd(), 'scripts/frontend-maintainability-baseline.json'));
  });
});
