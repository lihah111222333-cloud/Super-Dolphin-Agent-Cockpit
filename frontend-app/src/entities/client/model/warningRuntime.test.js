import { describe, expect, it, vi } from 'vitest';
import {
  attachWarningRuntime,
  mergeWarningEntries,
  warningTraceComponent,
  warningTraceStatus,
} from './warningRuntime.js';

const cleanObject = (payload) => Object.fromEntries(
  Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
);
const normalizeString = (value) => (value || '').toString().trim();
const normalizeThreadId = normalizeString;
const runtimeThreadIdentifier = (fields = {}) => fields.threadId || fields.thread_id || '';

describe('warning runtime helpers', () => {
  it('coalesces repeated warnings while keeping newest entry first', () => {
    const first = { id: 'first', timestamp: 't1', signature: 'same', occurrenceCount: 1, fields: { error: 'old' } };
    const second = { id: 'second', timestamp: 't2', signature: 'same', occurrenceCount: 1, fields: { error: 'new' } };

    expect(mergeWarningEntries([first], second, second.fields)).toEqual([{
      ...first,
      id: 'second',
      timestamp: 't2',
      fields: { error: 'new' },
      occurrenceCount: 2,
    }]);
  });

  it('derives warning trace component and status from event names', () => {
    expect(warningTraceComponent('thread.config.failed')).toBe('thread');
    expect(warningTraceStatus('warn', 'thread.config.failed')).toBe('error');
    expect(warningTraceStatus('warn', 'memory.changed')).toBe('ok');
    expect(warningTraceStatus('error', 'memory.changed')).toBe('error');
  });

  it('attaches addWarning to runtime and emits frontend traces', () => {
    let state = { warningEntries: [] };
    const emitFrontendTraceEvent = vi.fn();
    const runtime = {
      set: (updater) => {
        state = { ...state, ...updater(state) };
      },
    };

    attachWarningRuntime(runtime, {
      cleanObject,
      emitFrontendTraceEvent,
      normalizeString,
      normalizeThreadId,
      runtimeThreadIdentifier,
    });

    runtime.addWarning('error', 'thread.config.failed', {
      threadId: 'thread-1',
      error: new Error('bad config'),
      reqId: 'req-1',
    });

    expect(state.warningEntries).toHaveLength(1);
    expect(state.warningEntries[0]).toEqual(expect.objectContaining({
      level: 'error',
      event: 'thread.config.failed',
      threadId: 'thread-1',
      occurrenceCount: 1,
    }));
    expect(emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.warning',
      method: 'thread.config.failed',
      thread_id: 'thread-1',
      status: 'error',
      error: '[redacted]',
      metadata: {
        component: 'thread',
        req_id: 'req-1',
      },
    }));
  });

  it('redacts sensitive warning fields before storing, signing, and tracing', () => {
    let state = { warningEntries: [] };
    const emitFrontendTraceEvent = vi.fn();
    const runtime = {
      set: (updater) => {
        state = { ...state, ...updater(state) };
      },
    };

    attachWarningRuntime(runtime, {
      cleanObject,
      emitFrontendTraceEvent,
      normalizeString,
      normalizeThreadId,
      runtimeThreadIdentifier,
    });

    runtime.addWarning('error', 'api.rpc.failed', {
      threadId: 'thread-1',
      method: 'thread/messages',
      req_id: 9,
      path: '/home/l4place/private-project/secret.txt',
      prompt: 'private prompt body',
      api_key: 'sk-live-secret',
      error: 'failed at /home/l4place/private-project/secret.txt with sk-live-secret',
      rawPreview: {
        content: 'private prompt body',
      },
    });

    const entry = state.warningEntries[0];
    const serializedFields = JSON.stringify(entry.fields);
    expect(serializedFields).toContain('thread/messages');
    expect(serializedFields).toContain('"req_id":9');
    expect(serializedFields).not.toContain('/home/l4place');
    expect(serializedFields).not.toContain('secret.txt');
    expect(serializedFields).not.toContain('private prompt body');
    expect(serializedFields).not.toContain('sk-live-secret');
    expect(entry.signature).not.toContain('/home/l4place');
    expect(entry.signature).not.toContain('sk-live-secret');

    const trace = emitFrontendTraceEvent.mock.calls[0][0];
    const serializedTrace = JSON.stringify(trace);
    expect(serializedTrace).not.toContain('/home/l4place');
    expect(serializedTrace).not.toContain('secret.txt');
    expect(serializedTrace).not.toContain('private prompt body');
    expect(serializedTrace).not.toContain('sk-live-secret');
  });
});
