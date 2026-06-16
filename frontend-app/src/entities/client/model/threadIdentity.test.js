import { describe, expect, it } from 'vitest';
import {
  firstBackendThreadId,
  firstRuntimeAgentId,
  isAgentRuntimeId,
  isLaunchIntentId,
  normalizeBackendThreadId,
  normalizeThreadId,
  normalizeThreadIdentity,
} from './threadIdentity.js';

describe('threadIdentity', () => {
  it('normalizes thread ids and filters launch placeholders from backend ids', () => {
    expect(normalizeThreadId(' thread-1 ')).toBe('thread-1');
    expect(isLaunchIntentId('launch_123')).toBe(true);
    expect(isLaunchIntentId('launch-123')).toBe(true);
    expect(normalizeBackendThreadId('launch_123')).toBe('');
    expect(normalizeBackendThreadId(' thread-1 ')).toBe('thread-1');
    expect(firstBackendThreadId('', 'launch-1', 'thread-2')).toBe('thread-2');
  });

  it('detects runtime agent ids separately from backend thread ids', () => {
    expect(isAgentRuntimeId('agent_123')).toBe(true);
    expect(isAgentRuntimeId('agent-123')).toBe(true);
    expect(isAgentRuntimeId('thread-123')).toBe(false);
    expect(firstRuntimeAgentId('', 'thread-1', 'agent-1')).toBe('agent-1');
  });

  it('normalizes root and nested thread identity fields', () => {
    expect(normalizeThreadIdentity({
      thread_id: '',
      id: 'agent_ignored_as_backend_fallback',
      agent_id: 'agent-root',
      provider_thread_id: 'provider-1',
      session_id: 'session-1',
      thread: {
        codex_thread_id: 'thread-nested',
        agent_id: 'agent-nested',
        providerThreadId: 'provider-nested',
        sessionId: 'session-nested',
      },
    })).toEqual({
      threadId: 'thread-nested',
      agentId: 'agent-root',
      providerThreadId: 'provider-1',
      sessionId: 'session-1',
    });
  });

  it('falls back to agent runtime id when no explicit agent field exists', () => {
    expect(normalizeThreadIdentity({
      id: 'agent-runtime',
    })).toEqual({
      threadId: 'agent-runtime',
      agentId: 'agent-runtime',
      providerThreadId: '',
      sessionId: '',
    });
  });
});
