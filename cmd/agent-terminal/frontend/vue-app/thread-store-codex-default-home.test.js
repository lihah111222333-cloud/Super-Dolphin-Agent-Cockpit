// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

import { useThreadStore } from './stores/threads.js';

function snapshot(threadId) {
  return {
    threads: [{ id: threadId, name: threadId, state: 'idle' }],
    statuses: { [threadId]: 'idle' },
    interruptibleByThread: { [threadId]: false },
    statusHeadersByThread: { [threadId]: '' },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: [] },
    diffTextByThread: {},
    diffRevisionByThread: { [threadId]: 0 },
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId: '',
    activeCmdThreadId: '',
  };
}

function resetStore() {
  const store = useThreadStore();
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, snapshot(''));
  store.state.threads = [];
  return store;
}

function codexPreference(key) {
  if (key === 'settings.provider.active') return 'codex';
  if (key === 'settings.provider.codex.codexInstanceKey') return undefined;
  if (key === 'settings.provider.codex.codexModelProvider') return undefined;
  if (key === 'settings.provider.codex.codexHome') return undefined;
  if (key.endsWith('.model')) return 'gpt-5.5';
  if (key.endsWith('.effort')) return 'xhigh';
  return undefined;
}

describe('thread store Codex default home', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    globalThis.window = { ...(globalThis.window || {}), alert: vi.fn() };
    resetStore();
  });

  it('does not forward a Codex home when no Codex home preference is set', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') return codexPreference(payload?.key || '');
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-codex-default-home' } };
      }
      if (method === 'ui/state/get') return snapshot('thread-codex-default-home');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload?.config).not.toHaveProperty('codexInstanceKey');
    expect(startPayload?.config).not.toHaveProperty('codexModelProvider');
    expect(startPayload?.config).not.toHaveProperty('codexHome');
    expect(JSON.stringify(startPayload)).not.toContain('super-dolphin-relay');
    expect(JSON.stringify(startPayload)).not.toContain('Library/Application Support/Super Dolphin/providers/codex');
  });
});
