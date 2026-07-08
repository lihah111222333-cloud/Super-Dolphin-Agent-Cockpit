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
  runtimeTurnId } from './runtimeAssistantTimeline.js';

describe('runtimeAssistantTimeline', () => {
  it('normalizes runtime assistant ids and fallback ids', () => {
    expect(runtimeTurnId({ turn_id: 'turn-1' })).toBe('turn-1');
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
      turn_id: 'turn-1',
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
      },
      explicitId: false,
      streamId: 'assistant-stream-turn-1',
    });

    expect(runtimeAssistantCompletion({
      item: { type: 'tool', text: 'not assistant' },
    })).toBeNull();
    expect(runtimeAssistantCompletion({ item: { type: 'assistant' } })).toBeNull();
  });

  it('appends deltas without duplicating overlapping text', () => {
    expect(appendAssistantDeltaText('', 'hello')).toBe('hello');
    expect(appendAssistantDeltaText('hello', '')).toBe('hello');
    expect(appendAssistantDeltaText('hello world', 'world')).toBe('hello world');
    expect(appendAssistantDeltaText('hello', 'hello world')).toBe('hello world');
    expect(appendAssistantDeltaText('hello wor', 'world')).toBe('hello world');
    expect(assistantDeltaBufferKey('thread', 'item')).toBe('thread\u0000item');
  });

  it('merges final completion into a matching stream item', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'assistant-stream-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: false, runtime: true, time: '2026-06-15T01:00:01Z' },
    ], {
      item: { id: 'assistant-final-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: true, runtime: true, time: '2026-06-15T01:00:02Z' },
      explicitId: false,
      streamId: 'assistant-stream-turn-1',
    });

    expect(merged).toEqual([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'assistant-final-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: true, runtime: true, time: '2026-06-15T01:00:02Z' },
    ]);
  });

  it('deduplicates a later runtime completion against an existing done assistant item', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'a1', role: 'assistant', kind: 'assistant', text: 'short answer', done: true, runtime: true, time: '2026-06-15T01:00:01Z' },
    ], {
      item: { id: 'a2', role: 'assistant', kind: 'assistant', text: 'short answer', done: true, runtime: true, time: '2026-06-15T01:00:02Z' },
      explicitId: true,
      streamId: '',
    });

    expect(merged).toHaveLength(2);
    expect(merged[1]).toMatchObject({ id: 'a1', text: 'short answer', runtime: true });
  });

  it('marks split stream items done when the accumulated text matches completion text', () => {
    const merged = mergeRuntimeAssistantCompletion([
      { id: 'u1', role: 'user', kind: 'user', text: 'question', time: '2026-06-15T01:00:00Z' },
      { id: 'a1', role: 'assistant', kind: 'assistant', text: 'hello ', done: false, runtime: true, time: '2026-06-15T01:00:01Z' },
      { id: 'a2', role: 'assistant', kind: 'assistant', text: 'world', done: false, runtime: true, time: '2026-06-15T01:00:02Z' },
    ], {
      item: { id: 'final', role: 'assistant', kind: 'assistant', text: 'hello world', done: true, runtime: true, time: '2026-06-15T01:00:03Z' },
      explicitId: false,
      streamId: '',
    });

    expect(merged.map((item) => item.done)).toEqual([undefined, true, true]);
  });
});
