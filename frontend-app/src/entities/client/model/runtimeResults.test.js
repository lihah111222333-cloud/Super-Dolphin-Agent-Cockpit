import { describe, expect, it } from 'vitest';
import { createRuntimeResultHelpers } from './runtimeResults.js';

const helpers = createRuntimeResultHelpers({
  normalizeString: (value) => (value || '').toString().trim(),
  normalizeTimestamp: (value) => Date.parse(value) || 0,
  normalizeThreadId: (value) => (value || '').toString().trim(),
  runtimeThreadIdentifier: (fields) => fields.threadId || fields.thread_id || '',
  nowISO: () => '2026-06-15T10:00:00.000Z',
  nowMillis: () => 1781517600000,
  randomHex: () => 'abc123',
});

describe('runtime result helpers', () => {
  it('turns terminal tool timeline items into runtime result entries', () => {
    const entries = helpers.runtimeResultEntriesFromTimelineItems([
      {
        id: 'tool-grep',
        kind: 'tool',
        tool: 'mcp__lsp__grep',
        status: 'completed',
        output: 'src/App.jsx: found runtime log',
        ts: '2026-05-30T08:00:00Z',
      },
      {
        id: 'assistant-1',
        kind: 'assistant',
        text: 'hello',
      },
    ], 'thread-1');

    expect(entries).toEqual([
      expect.objectContaining({
        id: 'tool-grep',
        timestamp: '2026-05-30T08:00:00Z',
        level: 'info',
        event: 'tool.result',
        threadId: 'thread-1',
        message: expect.stringContaining('grep 返回'),
        detail: '[redacted]',
        signature: 'tool.result|thread-1|tool-grep|[redacted]',
      }),
    ]);
    expect(entries[0].message).not.toContain('src/App.jsx');
    expect(JSON.stringify(entries[0].fields)).not.toContain('src/App.jsx');
  });

  it('records failed tool results even when the detail comes from error', () => {
    const [entry] = helpers.runtimeResultEntriesFromTimelineItems([
      {
        kind: 'tool',
        name: 'functions.search',
        status: 'failed',
        error: 'backend unavailable',
      },
    ], 'thread-2');

    expect(entry).toEqual(expect.objectContaining({
      id: 'tool-result-thread-2-0-1781517600000',
      level: 'error',
      message: expect.stringContaining('search 失败'),
      detail: '[redacted]',
    }));
    expect(entry.message).not.toContain('backend unavailable');
    expect(JSON.stringify(entry.fields)).not.toContain('backend unavailable');
  });

  it('creates and coalesces backend rpc return entries by signature', () => {
    const first = helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 1,
      result_preview: '{"messages":[{"id":1}]}',
    });
    const second = helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 2,
      result_preview: '{"messages":[{"id":1}]}',
    });

    expect(first).toEqual(expect.objectContaining({
      id: 'api.rpc.done-1-abc123',
      timestamp: '2026-06-15T10:00:00.000Z',
      message: 'thread/messages 返回 · {"messages":[{"id":1}]}',
    }));
    expect(helpers.mergeRuntimeResultEntries([first], [second])).toEqual([
      expect.objectContaining({
        id: 'api.rpc.done-2-abc123',
        occurrenceCount: 2,
        fields: expect.objectContaining({ req_id: 2 }),
      }),
    ]);
  });

  it('redacts sensitive legacy RPC result previews before display', () => {
    const entry = helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 3,
      result_preview: JSON.stringify({
        messages: [{
          id: 1,
          content: 'real-message-secret',
          path: '/home/l4place/private-project/secret.txt',
          count: 2,
        }],
        total: 1,
      }),
    });

    expect(entry.detail).toContain('"total":1');
    expect(entry.detail).toContain('"count":2');
    expect(entry.detail).not.toContain('real-message-secret');
    expect(entry.detail).not.toContain('/home/l4place');
    expect(entry.message).not.toContain('real-message-secret');
  });

  it('redacts sensitive tool timeline result details before display', () => {
    const [entry] = helpers.runtimeResultEntriesFromTimelineItems([
      {
        id: 'tool-secret',
        kind: 'tool',
        tool: 'mcp__lsp__file',
        status: 'completed',
        result: JSON.stringify({
          content: 'private prompt body',
          path: '/home/l4place/private-project/secret.txt',
          api_key: 'sk-live-secret',
          total: 7,
        }),
      },
    ], 'thread-1');

    const serializedFields = JSON.stringify(entry.fields);
    expect(entry.detail).toContain('"total":7');
    expect(entry.detail).not.toContain('private prompt body');
    expect(entry.detail).not.toContain('/home/l4place');
    expect(entry.detail).not.toContain('sk-live-secret');
    expect(entry.message).not.toContain('private prompt body');
    expect(serializedFields).not.toContain('private prompt body');
    expect(serializedFields).not.toContain('/home/l4place');
    expect(serializedFields).not.toContain('sk-live-secret');
  });

  it('compacts oversized result detail before storing it', () => {
    const entry = helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      result: { values: Array.from({ length: 900 }, (_, index) => index) },
    });

    expect(entry.detail).toHaveLength(1603);
    expect(entry.detail.endsWith('...')).toBe(true);
  });
});
