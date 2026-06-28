// @ts-nocheck
import { describe, expect, it } from 'vitest';

import {
  normalizePreferenceScopeCwd,
  normalizeSplitRatio,
  normalizeThreadRailWidth,
  normalizeCmdCardCols,
  normalizeThread,
  normalizeThreadTimestampMap,
} from './stores/thread-ui-normalize.js';

describe('thread-ui-normalize', () => {
  it('normalizes scoped cwd values and trims trailing separators', () => {
    expect(normalizePreferenceScopeCwd(' /repo/path/ ')).toBe('/repo/path');
    expect(normalizePreferenceScopeCwd('C:\\repo\\')).toBe('C:\\repo');
    expect(normalizePreferenceScopeCwd('.')).toBe('');
    expect(normalizePreferenceScopeCwd('')).toBe('');
  });

  it('clamps split ratio, rail width and card columns into allowed ranges', () => {
    expect(normalizeSplitRatio(20)).toBe(30);
    expect(normalizeSplitRatio(80)).toBe(75);
    expect(normalizeSplitRatio('61.6')).toBe(62);

    expect(normalizeThreadRailWidth(100)).toBe(188);
    expect(normalizeThreadRailWidth(999)).toBe(420);
    expect(normalizeThreadRailWidth('233.4')).toBe(233);

    expect(normalizeCmdCardCols(2)).toBe(2);
    expect(normalizeCmdCardCols(4)).toBe(3);
  });

  it('normalizes thread records and timestamp maps', () => {
    expect(normalizeThread({ id: 'thread-1', state: 'RUNNING' })).toEqual({
      id: 'thread-1',
      name: '',
      state: 'running',
    });
    expect(normalizeThread({ id: 'thread-2', name: 'Worker', state: 'unknown' })).toEqual({
      id: 'thread-2',
      name: 'Worker',
      state: 'idle',
    });

    expect(normalizeThreadTimestampMap({
      ' thread-1 ': 10.4,
      '': 99,
      'thread-2': '25.8',
      'thread-3': -1,
    })).toEqual({
      'thread-1': 10,
      'thread-2': 26,
    });
  });
});
