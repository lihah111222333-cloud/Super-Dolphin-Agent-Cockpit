import { optionalTextField, normalizeOptionalTextField, parseRequiredTimestamp } from './contractStoreModel.js';
import { describe, expect, it } from 'vitest';
import { createRuntimeResultHelpers, ContractError } from './runtimeResults.js';
import { compactSafeDiagnosticPreview } from '../../../shared/api/support/safeDiagnosticPreview.js';

const helpers = createRuntimeResultHelpers({
  normalizeString: (value) => normalizeOptionalTextField(value),
  normalizeTimestamp: (value) => parseRequiredTimestamp(value) || 0,
  normalizeThreadId: (value) => normalizeOptionalTextField(value),
  runtimeThreadIdentifier: (fields) => fields.threadId || fields.thread_id || optionalTextField(),
  nowISO: () => '2026-06-15T10:00:00.000Z',
  nowMillis: () => 1781517600000,
  randomHex: () => 'abc123',
});

describe('required timestamp contract', () => {
  it.each([
    '2026-02-30T00:00:00Z',
    '2025-02-29T00:00:00Z',
    '2026-04-31T00:00:00Z',
    '1969-12-31T23:59:59Z',
    '0000-01-01T00:00:00Z',
  ])('rejects impossible UTC calendar date %s', (value) => {
    expect(() => parseRequiredTimestamp(value, 'timestamp')).toThrow('timestamp');
  });

  it('accepts a real leap day', () => {
    expect(parseRequiredTimestamp('2024-02-29T00:00:00Z', 'timestamp')).toBe(1709164800000);
  });
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

  it('redacts sensitive legacy RPC results before display', () => {
    const entry = helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 3,
      result: JSON.stringify({
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
    const serializedFields = JSON.stringify(entry.fields);
    expect(serializedFields).not.toContain('real-message-secret');
    expect(serializedFields).not.toContain('/home/l4place');
    expect(serializedFields).not.toContain('secret.txt');
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

  it('does not throw when result_preview exceeds 1200 characters and gets truncated, while sensitive fields remain redacted', () => {
    const resultObj = {
      api_key: 'sk-live-secret-key-1234567890-very-long-secret-to-exceed-limit-and-cause-truncation',
      normal_field: Array.from({ length: 900 }, (_, index) => index),
    };

    const truncatedPreview = compactSafeDiagnosticPreview(resultObj, 1200);

    expect(truncatedPreview).toHaveLength(1203);
    expect(truncatedPreview.endsWith('...')).toBe(true);
    expect(truncatedPreview).not.toContain('sk-live-secret-key');

    const entry = helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 4,
      result_preview: truncatedPreview,
    });

    expect(entry).toBeDefined();
    expect(entry.detail).toBe(truncatedPreview);
    expect(entry.detail).not.toContain('sk-live-secret-key');
  });

  it('throws ContractError when result_preview is present but is not a non-empty string', () => {
    // null preview
    expect(() => helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      result_preview: null,
    })).toThrow(ContractError);

    // empty string preview
    expect(() => helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      result_preview: '   ',
    })).toThrow(ContractError);

    // object preview
    expect(() => helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      result_preview: { messages: [] },
    })).toThrow(ContractError);

    // array preview
    expect(() => helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
      method: 'thread/messages',
      result_preview: [],
    })).toThrow(ContractError);
  });

  it('does not expose a controllable constructor name in ContractError diagnostics', () => {
    const syntheticSecret = 'synthetic-constructor-secret';
    const preview = Object.create({ constructor: { name: syntheticSecret } });

    let error;
    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
        result_preview: preview,
      });
      expect.fail('should throw');
    } catch (caught) {
      error = caught;
    }

    expect(error).toBeInstanceOf(ContractError);
    expect(error.message).toBe('result_preview must be a non-empty string, but got type: object');
    expect(error.message).not.toContain(syntheticSecret);
    expect(JSON.stringify(error)).not.toContain(syntheticSecret);
  });

  it('throws ContractError on invalid preview types without leaking secrets or crashing', () => {
    // 1. secret object preview
    const secretObj = { api_key: 'super-secret-password-123' };
    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
        result_preview: secretObj,
      });
      expect.fail('should throw');
    } catch (e) {
      expect(e).toBeInstanceOf(ContractError);
      expect(e.message).not.toContain('super-secret-password-123');
      expect(e.message).toContain('object');
    }

    // 2. circular object preview
    const circularSecret = 'circular-preview-secret';
    const circular = { secret: circularSecret };
    circular.self = circular;
    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
        result_preview: circular,
      });
      expect.fail('should throw');
    } catch (e) {
      expect(e).toBeInstanceOf(ContractError);
      expect(e.message).toContain('object');
      expect(e.message).not.toContain(circularSecret);
    }

    // 3. BigInt preview
    const bigIntPreview = 123456789n;
    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
        result_preview: bigIntPreview,
      });
      expect.fail('should throw');
    } catch (e) {
      expect(e).toBeInstanceOf(ContractError);
      expect(e.message).toContain('bigint');
      expect(e.message).not.toContain(bigIntPreview.toString());
    }

    // 4. Custom toJSON preview
    let toJSONCalled = false;
    const customObj = {
      toJSON() {
        toJSONCalled = true;
        return 'secret-json-value';
      }
    };
    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
        result_preview: customObj,
      });
      expect.fail('should throw');
    } catch (e) {
      expect(e).toBeInstanceOf(ContractError);
      expect(toJSONCalled).toBe(false);
      expect(e.message).not.toContain('secret-json-value');
    }
  });

  it('throws SafeDiagnosticPreviewJSONParseError when result_preview is inherited (prototype-only) and result is invalid JSON', () => {
    const parentProto = { result_preview: 'some-inherited-preview' };
    const fields = Object.create(parentProto);
    fields.method = 'thread/messages';
    fields.result = '{invalid JSON';

    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', fields);
      expect.fail('should have thrown');
    } catch (e) {
      expect(e.name).toBe('SafeDiagnosticPreviewJSONParseError');
    }
  });

  it('uses only an own result_preview when Object.prototype is polluted', () => {
    const previousDescriptor = Object.getOwnPropertyDescriptor(Object.prototype, 'result_preview');
    Object.defineProperty(Object.prototype, 'result_preview', {
      configurable: true,
      value: 'inherited-preview-must-not-be-used',
    });

    try {
      expect(helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
      })).toBeNull();

      let error;
      try {
        helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
          method: 'thread/messages',
          result: '{invalid JSON',
        });
        expect.fail('should have thrown');
      } catch (caught) {
        error = caught;
      }
      expect(error.name).toBe('SafeDiagnosticPreviewJSONParseError');
    } finally {
      if (previousDescriptor) {
        Object.defineProperty(Object.prototype, 'result_preview', previousDescriptor);
      } else {
        delete Object.prototype.result_preview;
      }
    }
  });

  it('throws SafeDiagnosticPreviewJSONParseError when result_preview is absent and result is invalid JSON', () => {
    try {
      helpers.runtimeResultEntryFromRPCDone('api.rpc.done', {
        method: 'thread/messages',
        result: '{invalid JSON',
      });
      expect.fail('should have thrown');
    } catch (e) {
      expect(e.name).toBe('SafeDiagnosticPreviewJSONParseError');
    }
  });
});
