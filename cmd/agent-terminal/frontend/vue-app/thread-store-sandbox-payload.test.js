// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({
  logDebug: logMock.logDebug,
  logInfo: logMock.logInfo,
  logWarn: logMock.logWarn,
}));

import { useThreadStore } from './stores/threads.js';

function buildSnapshot(threadId) {
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

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    threads: [],
    statuses: {},
    interruptibleByThread: {},
    statusHeadersByThread: {},
    statusDetailsByThread: {},
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: {},
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
  });
}

async function startWithSandbox(sandbox) {
  const store = useThreadStore();
  let startPayload = null;
  apiMock.callAPI.mockImplementation(async (method, payload) => {
    if (method === 'ui/preferences/get') {
      if (payload?.key === 'settings.provider.active') return 'codex';
      if (payload?.key === 'settings.provider.codex.sandbox') return sandbox;
      return undefined;
    }
    if (method === 'config/builtinTools/read') return { tools: [] };
    if (method === 'thread/start') {
      startPayload = payload;
      return { thread: { id: 'thread-codex-sandbox' } };
    }
    if (method === 'ui/state/get') return buildSnapshot('thread-codex-sandbox');
    if (method === 'ui/preferences/set') return {};
    return {};
  });

  await store.startThread('/repo', {});
  return startPayload;
}

describe('thread store Codex sandbox payload', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logDebug.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('omits config.sandbox when the Codex sandbox preference is undefined', async () => {
    const startPayload = await startWithSandbox(undefined);
    expect(startPayload?.config).not.toHaveProperty('sandbox');
  });

  it('omits config.sandbox when the Codex sandbox preference is a stringified undefined sentinel', async () => {
    const startPayload = await startWithSandbox('undefined');
    expect(startPayload?.config).not.toHaveProperty('sandbox');
  });

  it('forwards canonical snake_case workspace-write sandbox fields', async () => {
    const startPayload = await startWithSandbox({
      type: 'workspaceWrite',
      writableRoots: ['/repo', '/tmp/cache'],
      networkAccess: true,
    });
    expect(startPayload?.config?.sandbox).toEqual({
      mode: 'workspace-write',
      writable_roots: ['/repo', '/tmp/cache'],
      network_access: true,
    });
  });

  it('forwards canonical restricted read-only sandbox access fields', async () => {
    const startPayload = await startWithSandbox({
      type: 'readOnly',
      access: { type: 'restricted', readableRoots: ['/repo'], includePlatformDefaults: true },
    });
    expect(startPayload?.config?.sandbox).toEqual({
      mode: 'read-only',
      access: {
        type: 'restricted',
        readable_roots: ['/repo'],
        include_platform_defaults: true,
      },
    });
  });
});
