import { describe, expect, it, vi } from 'vitest';
import {
  activeTurnPayload,
  isInterruptibleTurnSummary,
  isTerminalActiveTurnStatus,
  normalizeActivityStats,
  normalizeTokenUsage,
  normalizeTurnSummary,
  shouldFloatThreadPatch,
  threadActivityTimestamp,
} from './threadActivityMetrics.js';

describe('threadActivityMetrics', () => {
  it('normalizes active turn summaries from backend field variants', () => {
    expect(normalizeTurnSummary({
      turn_id: ' turn-1 ',
      thread_id: ' thread-1 ',
      agent_id: ' agent-1 ',
      status: 'running',
      created_at: '2026-06-15T01:00:00Z',
      updated_at: '2026-06-15T01:00:01Z',
      finished_at: '2026-06-15T01:00:02Z',
    })).toEqual({
      id: 'turn-1',
      threadId: 'thread-1',
      agentId: 'agent-1',
      status: 'running',
      startedAt: '2026-06-15T01:00:00Z',
      updatedAt: '2026-06-15T01:00:01Z',
      completedAt: '2026-06-15T01:00:02Z',
    });

    expect(normalizeTurnSummary({ status: 'running' })).toBeNull();
    expect(normalizeTurnSummary({ id: 'turn-2', threadId: 'launch-1' }).threadId).toBe('');
  });

  it('classifies terminal and interruptible active turn states', () => {
    expect(isTerminalActiveTurnStatus('completed')).toBe(true);
    expect(isTerminalActiveTurnStatus('已完成')).toBe(true);
    expect(isTerminalActiveTurnStatus('running')).toBe(false);
    expect(isInterruptibleTurnSummary({ id: 'turn-1', status: 'running' })).toBe(true);
    expect(isInterruptibleTurnSummary({ id: 'turn-1', status: 'failed' })).toBe(false);
    expect(isInterruptibleTurnSummary({ status: 'running' })).toBe(false);
  });

  it('extracts active turn payload variants without hiding explicit null', () => {
    expect(activeTurnPayload({ active_turn: null })).toBeNull();
    expect(activeTurnPayload({ activeTurn: { id: 'turn-1' } })).toEqual({ id: 'turn-1' });
    expect(activeTurnPayload({})).toBeUndefined();
  });

  it('floats thread patches only for completed turn snapshots', () => {
    expect(shouldFloatThreadPatch({ source: 'turn/completed', status: 'completed' })).toBe(true);
    expect(shouldFloatThreadPatch({ type: 'turn/completed', thread: { state: 'idle' } })).toBe(true);
    expect(shouldFloatThreadPatch({ event: 'turn/completed', status: 'running' })).toBe(false);
    expect(shouldFloatThreadPatch({ source: 'thread/update', status: 'completed' })).toBe(false);
  });

  it('normalizes token usage from current, direct, and cumulative fields', () => {
    expect(normalizeTokenUsage({
      tokenUsage: {
        last: { input_tokens: 10, output_tokens: 5 },
        total: { total_tokens: 40 },
        context_window_tokens: 200,
      },
    })).toEqual({ usedTokens: 15, contextWindowTokens: 200, usedPercent: 7.5 });

    expect(normalizeTokenUsage({
      usage: { promptTokens: 4, completionTokens: 6 },
      contextWindow: 20,
      usedPercent: 150,
    })).toEqual({ usedTokens: 10, contextWindowTokens: 20, usedPercent: 100 });

    expect(normalizeTokenUsage(null)).toBeNull();
  });

  it('normalizes activity stats and drops invalid tool call counts', () => {
    expect(normalizeActivityStats({
      lsp_calls: '2',
      commands: -1,
      fileEdits: '3',
      tool_calls: {
        read_file: '4',
        empty: 0,
        invalid: 'x',
        '': 5,
      },
    })).toEqual({
      lspCalls: 2,
      commands: 0,
      fileEdits: 3,
      toolCalls: { read_file: 4 },
    });

    expect(normalizeActivityStats([])).toBeNull();
  });

  it('uses current time for local thread activity timestamps', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-06-15T01:00:00Z'));
    expect(threadActivityTimestamp()).toBe(new Date('2026-06-15T01:00:00Z').getTime());
    vi.useRealTimers();
  });
});
