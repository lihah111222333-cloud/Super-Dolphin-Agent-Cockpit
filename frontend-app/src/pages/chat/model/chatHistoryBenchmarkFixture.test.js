import { describe, expect, it } from 'vitest';
import { buildChatHistoryFixture } from './chatHistoryBenchmarkFixture.js';

describe('chat history benchmark fixture', () => {
  it('builds deterministic user/tool/assistant turns from a seed', () => {
    const input = { turns: 5_000, toolsPerTurn: 3, archived: true, seed: 7 };
    const first = buildChatHistoryFixture(input);
    const second = buildChatHistoryFixture(input);

    expect(first).toHaveLength(5_000 * 5);
    expect(first).toEqual(second);
    expect(first.slice(0, 5).map((message) => message.role)).toEqual([
      'user',
      'tool',
      'tool',
      'tool',
      'assistant',
    ]);
    expect(new Set(first.map((message) => message.id)).size).toBe(first.length);
    expect(first.every((message) => message.archived === true)).toBe(true);
    const toolMessages = first.filter((message) => message.role === 'tool');
    expect(toolMessages).toHaveLength(5_000 * 3);
    expect(toolMessages.every((message) => message.archived === true)).toBe(true);
  });

  it('changes deterministically with seed and preserves the archive flag', () => {
    const archived = buildChatHistoryFixture({ turns: 2, toolsPerTurn: 1, archived: true, seed: 7 });
    const active = buildChatHistoryFixture({ turns: 2, toolsPerTurn: 1, archived: false, seed: 8 });

    expect(active).not.toEqual(archived);
    expect(active.every((message) => message.archived === false)).toBe(true);
  });

  it.each([
    [{ turns: 0, toolsPerTurn: 1, archived: true, seed: 7 }, 'turns'],
    [{ turns: 1, toolsPerTurn: 0, archived: true, seed: 7 }, 'toolsPerTurn'],
    [{ turns: 1, toolsPerTurn: 1, archived: 'yes', seed: 7 }, 'archived'],
    [{ turns: 1, toolsPerTurn: 1, archived: true, seed: -1 }, 'seed'],
  ])('fails fast for invalid fixture input %#', (input, field) => {
    expect(() => buildChatHistoryFixture(input)).toThrow(field);
  });
});
