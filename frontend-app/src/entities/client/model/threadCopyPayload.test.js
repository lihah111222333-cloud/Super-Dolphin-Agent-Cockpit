import { describe, expect, it } from 'vitest';
import {
  buildCwdLogPath,
  buildThreadCopyPayload,
  firstThreadCopyText,
  formatUTC8HumanReadable,
  positiveThreadCopyPort,
} from './threadCopyPayload.js';

describe('threadCopyPayload', () => {
  it('filters non-copyable text and ports', () => {
    expect(firstThreadCopyText('', '.', false, '[object Object]', 12, 'fallback')).toBe('12');
    expect(firstThreadCopyText('', '.', false, '[object Object]', 'fallback')).toBe('fallback');
    expect(positiveThreadCopyPort('', -1, '0', '4512')).toBe(4512);
    expect(positiveThreadCopyPort('', -1, '0')).toBeNull();
  });

  it('builds cwd log paths only for project-like paths', () => {
    expect(buildCwdLogPath('D:/project/app')).toBe('~/.multi-agent/log/app/');
    expect(buildCwdLogPath('D:/project/app/')).toBe('~/.multi-agent/log/app/');
    expect(buildCwdLogPath('D:')).toBeNull();
    expect(buildCwdLogPath('/')).toBeNull();
    expect(buildCwdLogPath('.')).toBeNull();
  });

  it('formats copiedAt in UTC+8', () => {
    expect(formatUTC8HumanReadable(new Date('2026-06-15T00:01:02Z'))).toBe('2026-06-15 08:01:02 UTC+8');
    expect(formatUTC8HumanReadable('not a date')).toBe('');
  });

  it('builds thread copy payload from identity, thread, config, and state fallbacks', () => {
    expect(buildThreadCopyPayload({
      state: {
        provider: 'codex',
        activeProject: 'D:/project/app',
        cwd: 'D:/fallback',
        statuses: { 'thread-1': 'running' },
        providerConfig: { model: 'state-model', effort: 'state-effort' },
      },
      threadId: 'thread-1',
      thread: {
        name: 'Thread name',
        providerThreadId: 'provider-thread',
        port: '4512',
      },
      identity: {
        agent_id: 'agent-1',
        session_id: 'session-1',
        effective: { model: 'identity-model' },
        reasoning_effort: 'high',
      },
      threadConfig: {
        effective: { model: 'config-model', effort: 'config-effort' },
      },
      copiedAt: new Date('2026-06-15T00:00:00Z'),
    })).toEqual({
      agentId: 'agent-1',
      providerThreadId: 'provider-thread',
      uuid: 'session-1',
      name: 'Thread name',
      status: 'running',
      provider: 'codex',
      model: 'identity-model',
      effort: 'high',
      port: 4512,
      cwd: 'D:/project/app',
      'log-path': '~/.multi-agent/log/app/',
      copiedAt: '2026-06-15 08:00:00 UTC+8',
    });
  });
});
