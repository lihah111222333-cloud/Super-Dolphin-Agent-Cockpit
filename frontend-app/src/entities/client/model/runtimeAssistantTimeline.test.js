import { describe, expect, it } from 'vitest';
import {
  appendAssistantDeltaText,
  assistantDeltaBufferKey,
  isAssistantMessageDeltaEvent,
  isRuntimeAssistantItem,
  mergeRuntimeAssistantCompletion,
  runtimeAssistantCompletion,
  runtimeAssistantFallbackId,
  runtimeAssistantStreamId,
  parseRuntimeTurnRef,
  parseRuntimeTurnTerminal,
  runtimeTerminalFingerprint,
  runtimeTurnRefKey,
  runtimeTurnId } from './runtimeAssistantTimeline.js';

describe('runtimeAssistantTimeline', () => {
  it('normalizes runtime assistant ids and fallback ids', () => {
    expect(runtimeTurnId({ turnId: 'turn-1' })).toBe('turn-1');
    expect(runtimeTurnId({ turn_id: 'turn-legacy' })).toBe('');
    expect(runtimeAssistantStreamId({ turnId: 'turn-1' })).toBe('assistant-stream-turn-1');
    expect(runtimeAssistantFallbackId({ turnId: 'turn-1' }, {
      runtimeThreadIdentifier: () => 'thread-ignored',
      normalizeThreadId: (value) => value,
      nowMillis: () => 123,
    })).toBe('assistant-stream-turn-1');
    expect(runtimeAssistantFallbackId({}, {
      runtimeThreadIdentifier: () => 'thread-1',
      normalizeThreadId: (value) => value.toUpperCase(),
      nowMillis: () => 123,
    })).toBe('assistant-stream-THREAD-1');
    expect(runtimeAssistantFallbackId({}, {
      runtimeThreadIdentifier: () => '',
      normalizeThreadId: (value) => value,
      nowMillis: () => 123,
    })).toBe('assistant-stream-123');
  });

  it('recognizes assistant item and delta event variants', () => {
    expect(isRuntimeAssistantItem({ type: 'agent_message' })).toBe(true);
    expect(isRuntimeAssistantItem({ kind: 'final_answer' })).toBe(true);
    expect(isRuntimeAssistantItem({ role: 'tool' })).toBe(false);

    expect(isAssistantMessageDeltaEvent('item/agentmessage/delta')).toBe(true);
    expect(isAssistantMessageDeltaEvent('turn/output/delta', { stream: 'assistant' })).toBe(true);
    expect(isAssistantMessageDeltaEvent('turn/output/delta', { stream: 'stderr' })).toBe(false);
    expect(isAssistantMessageDeltaEvent('tool/output/delta')).toBe(false);
  });

  it('builds assistant completion items from runtime payloads', () => {
    expect(runtimeAssistantCompletion({
      turnId: 'turn-1',
      timestamp: '2026-06-15T01:00:00Z',
      item: { type: 'agent_message', content: { text: 'final answer' } },
    }, {
      nowISO: () => '2026-06-15T02:00:00Z',
      nowMillis: () => 123,
    })).toEqual({
      item: {
        id: 'assistant-final-turn-1',
        role: 'assistant',
        kind: 'assistant',
        text: 'final answer',
        time: '2026-06-15T01:00:00Z',
        done: true,
        optimistic: false,
        runtime: true,
        turnId: 'turn-1',
      },
      explicitId: false,
      streamId: 'assistant-stream-turn-1',
    });

    expect(runtimeAssistantCompletion({
      item: { type: 'tool', text: 'not assistant' },
    })).toBeNull();
    expect(runtimeAssistantCompletion({ item: { type: 'assistant' } })).toBeNull();
  });

  it('appends canonical output deltas without interpreting repeated text as snapshots', () => {
    expect(appendAssistantDeltaText('', 'hello')).toBe('hello');
    expect(appendAssistantDeltaText('hello', '')).toBe('hello');
    expect(appendAssistantDeltaText('hello ', 'hello ')).toBe('hello hello ');
    expect(appendAssistantDeltaText('**bold', '**')).toBe('**bold**');
    expect(appendAssistantDeltaText('```js\ncode\n', '```')).toBe('```js\ncode\n```');
    expect(appendAssistantDeltaText('line one\n', '\nline three')).toBe('line one\n\nline three');
    expect(assistantDeltaBufferKey('thread', 'item', 'turn')).toBe('thread\u0000turn\u0000item');
  });

  it('uses the canonical terminal validator and produces stable turn keys', () => {
    const terminal = {
      schemaVersion: 2,
      eventId: 'terminal-1',
      threadId: 'thread-1',
      turnId: 'turn-1',
      outcome: 'failed',
      occurredAt: '2026-07-16T01:00:00Z',
      publicError: { code: 'FAILED', title: '运行失败', message: '本轮执行失败', diagnosticId: 'diag-1', retryable: false, recoveryActions: ['copy_diagnostics'] },
    };
    const parsed = parseRuntimeTurnTerminal(terminal);
    expect(parsed.value).toBeDefined();
    expect(parseRuntimeTurnRef({ threadId: 'thread-1', turnId: 'turn-1', itemId: 'item-1' })).toEqual({
      value: { threadId: 'thread-1', turnId: 'turn-1' },
    });
    expect(parseRuntimeTurnRef({ threadId: 'thread-1', turn_id: 'turn-legacy' })).toEqual({ error: 'canonical_turn_ref_contract' });
    expect(runtimeTurnRefKey('thread-1', 'turn-1')).toBe('thread-1\u0000turn-1');
    expect(runtimeTerminalFingerprint(parsed.value)).toContain('failed');
    expect(runtimeTerminalFingerprint(parsed.value)).toBe(runtimeTerminalFingerprint({
      ...parsed.value,
      eventId: 'terminal-duplicate',
      publicError: {
        recoveryActions: ['copy_diagnostics'],
        retryable: false,
        diagnosticId: 'diag-1',
        message: '本轮执行失败',
        title: '运行失败',
        code: 'FAILED',
      },
    }));
    expect(parseRuntimeTurnTerminal({ ...terminal, outcome: 'unknown' })).toEqual({ error: 'canonical_terminal_contract' });
    const { eventId: _missingEventId, ...missingEventId } = terminal;
    expect(parseRuntimeTurnTerminal(missingEventId)).toEqual({ error: 'canonical_terminal_contract' });
    expect(parseRuntimeTurnTerminal({ ...terminal, outcome: 'success', publicError: undefined, success: true })).toEqual({ error: 'canonical_terminal_contract' });
  });

  it('merges final completion into a matching stream item', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'assistant-stream-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: false, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:01Z' },
    ], {
      item: { id: 'assistant-final-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: true, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:02Z' },
      explicitId: false,
      streamId: 'assistant-stream-turn-1',
    });

    expect(merged).toEqual([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'assistant-final-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: true, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:02Z' },
    ]);
  });

  it('replaces duplicated live delta text with the authoritative completion', () => {
    const merged = mergeRuntimeAssistantCompletion([
      {
        id: 'assistant-stream-turn-1',
        role: 'assistant',
        kind: 'assistant',
        text: '权限上下文权限上下文仍然是：仍然是：',
        done: false,
        runtime: true,
        turnId: 'turn-1',
      },
    ], {
      item: {
        id: 'assistant-item-1',
        role: 'assistant',
        kind: 'assistant',
        text: '权限上下文仍然是：',
        done: true,
        runtime: true,
        turnId: 'turn-1',
      },
      explicitId: true,
      streamId: 'assistant-stream-turn-1',
    });

    expect(merged).toEqual([
      expect.objectContaining({
        id: 'assistant-item-1',
        text: '权限上下文仍然是：',
        done: true,
        turnId: 'turn-1',
      }),
    ]);
  });

  it('keeps completion merge and reused item ids isolated by TurnRef', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'shared-id', role: 'assistant', kind: 'assistant', text: 'turn one', done: true, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:01Z' },
      { id: 'shared-id', role: 'assistant', kind: 'assistant', text: 'turn two partial', done: false, runtime: true, turnId: 'turn-2', time: '2026-06-15T01:00:02Z' },
    ], {
      item: { id: 'shared-id', role: 'assistant', kind: 'assistant', text: 'turn two final', done: true, runtime: true, turnId: 'turn-2', time: '2026-06-15T01:00:03Z' },
      explicitId: true,
      streamId: 'assistant-stream-turn-2',
    });

    expect(merged).toEqual([
      expect.objectContaining({ id: 'shared-id', text: 'turn one', turnId: 'turn-1' }),
      expect.objectContaining({ id: 'shared-id', text: 'turn two final', turnId: 'turn-2', done: true }),
    ]);
  });

  it('deduplicates a later runtime completion against an existing done assistant item', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'a1', role: 'assistant', kind: 'assistant', text: 'short answer', done: true, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:01Z' },
    ], {
      item: { id: 'a2', role: 'assistant', kind: 'assistant', text: 'short answer', done: true, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:02Z' },
      explicitId: true,
      streamId: '',
    });

    expect(merged).toHaveLength(2);
    expect(merged[1]).toMatchObject({ id: 'a1', text: 'short answer', runtime: true });
  });

  it('marks split stream items done when the accumulated text matches completion text', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'a1', role: 'assistant', kind: 'assistant', text: 'hello ', done: false, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:01Z' },
      { id: 'a2', role: 'assistant', kind: 'assistant', text: 'world', done: false, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:02Z' },
    ], {
      item: { id: 'final', role: 'assistant', kind: 'assistant', text: 'hello world', done: true, runtime: true, turnId: 'turn-1', time: '2026-06-15T01:00:03Z' },
      explicitId: false,
      streamId: '',
    });

    expect(merged.map((item) => item.done)).toEqual([undefined, true, true]);
  });
});
