import { describe, expect, it } from 'vitest';
import { buildChatHistoryFixture } from './chatHistoryBenchmarkFixture.js';
import {
  TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
  TIMELINE_MATERIALIZATION_INCREMENT,
  selectMaterializedTimeline,
} from './timelineMaterializationModel.js';

describe('timeline materialization model', () => {
  it('owns the production materialization constants', () => {
    expect(TIMELINE_INITIAL_MATERIALIZED_MESSAGES).toBe(80);
    expect(TIMELINE_MATERIALIZATION_INCREMENT).toBe(80);
  });

  it('selects the newest bounded window without mutating history', () => {
    const history = buildChatHistoryFixture({ turns: 5_000, toolsPerTurn: 3, archived: true, seed: 7 });
    const before = history.slice();

    expect(selectMaterializedTimeline(history, 80)).toEqual(history.slice(-80));
    expect(selectMaterializedTimeline(history, 160)).toEqual(history.slice(-160));
    expect(selectMaterializedTimeline(history, 1)).toEqual(history.slice(-80));
    expect(history).toEqual(before);
  });

  it('returns all messages when history is smaller than the initial window', () => {
    const history = buildChatHistoryFixture({ turns: 10, toolsPerTurn: 1, archived: false, seed: 7 });

    expect(selectMaterializedTimeline(history, 80)).toEqual(history);
    expect(selectMaterializedTimeline(history, 80)).not.toBe(history);
  });

  it.each([
    [null, 80, 'messages'],
    [[], undefined, 'count'],
    [[], -1, 'count'],
    [[], 1.5, 'count'],
  ])('fails fast for invalid model input %#', (messages, count, field) => {
    expect(() => selectMaterializedTimeline(messages, count)).toThrow(field);
  });
});
