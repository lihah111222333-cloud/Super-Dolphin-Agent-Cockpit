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
      error: 'bad config',
      metadata: {
        component: 'thread',
        req_id: 'req-1',
      },
    }));
  });
});
