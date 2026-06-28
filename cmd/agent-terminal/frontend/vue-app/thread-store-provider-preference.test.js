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

function buildSnapshot(threadId = 'thread-scoped-provider') {
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
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},
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

describe('thread store provider preference scope', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logDebug.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('prefers the launch cwd scoped active provider over the global toolbar provider', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') return 'claude';
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-scoped-provider' } };
      }
      if (method === 'ui/state/get') return buildSnapshot();
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'claude',
    }));
  });

  it('uses the Codex launch default when no active provider preference exists', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return null;
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-codex-absent-provider-default' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-codex-absent-provider-default');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'codex',
    }));
  });

  it('sends an explicit Codex provider for a clean first launch without active preferences', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return null;
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-clean-db-codex-default' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-clean-db-codex-default');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await expect(store.startThread('/repo-clean', {})).resolves.toBe('thread-clean-db-codex-default');

    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo-clean',
      provider: 'codex',
      modelProvider: 'codex',
    }));
  });

  it('falls back to the global active provider when the launch cwd has no scoped provider preference', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') return null;
        if (payload?.key === 'settings.provider.active') return 'claude';
        if (payload?.key === 'settings.provider.claude.model') return 'sonnet';
        if (payload?.key === 'settings.provider.claude.effort') return 'high';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-global-provider-fallback' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-global-provider-fallback');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'claude',
      model: 'sonnet',
      effort: 'high',
    }));
  });

  it('does not fall back to global provider when the launch cwd provider read fails', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') {
          throw new Error('scoped read failed');
        }
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'thread/start') return { thread: { id: 'thread-should-not-start' } };
      return {};
    });

    await expect(store.startThread('/repo', {})).rejects.toThrow('scoped read failed');
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('thread/start', expect.anything());
  });

  it('leaves Codex model and effort to the backend contract when no user preference is set', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-codex-contract-defaults' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-codex-contract-defaults');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-codex', {});

    expect(startPayload?.model).toBeUndefined();
    expect(startPayload?.effort).toBeUndefined();
    expect(startPayload?.config).not.toHaveProperty('codexHome');
    expect(logMock.logWarn).toHaveBeenCalledWith('thread', 'start.config.trace', expect.objectContaining({
      provider_pref_model: '',
      provider_pref_effort: '',
      model_default_source: 'backend_provider_contract',
      payload_model: '',
      payload_effort: '',
    }));
  });

  it('forwards user-selected Codex model and effort instead of contract defaults', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return 'codex';
        if (payload?.key === 'settings.provider.codex.model') return 'gpt-5.4';
        if (payload?.key === 'settings.provider.codex.effort') return 'high';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-codex-user-defaults' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-codex-user-defaults');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-codex', {});

    expect(startPayload).toEqual(expect.objectContaining({
      modelProvider: 'codex',
      model: 'gpt-5.4',
      effort: 'high',
    }));
  });

  it('resolves launch overrides before project and global provider preferences', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') return 'codex';
        if (payload?.key === 'settings.provider.active') return 'claude';
        if (payload?.key === 'settings.provider.codex.model' && payload?.cwd === '/repo') return 'project-model';
        if (payload?.key === 'settings.provider.codex.effort' && payload?.cwd === '/repo') return 'project-effort';
        if (payload?.key === 'settings.provider.codex.model') return 'global-model';
        if (payload?.key === 'settings.provider.codex.effort') return 'global-effort';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-explicit-provider-overrides' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-explicit-provider-overrides');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', { modelProvider: 'codex', model: 'override-model', effort: 'override-effort' });

    expect(startPayload).toEqual(expect.objectContaining({
      modelProvider: 'codex',
      model: 'override-model',
      effort: 'override-effort',
    }));
  });

  it('uses global provider details when project details are absent and preserves project partials', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') return null;
        if (payload?.key === 'settings.provider.active') return 'codex';
        if (payload?.key === 'settings.provider.codex.model' && payload?.cwd === '/repo') return 'project-model';
        if (payload?.key === 'settings.provider.codex.effort' && payload?.cwd === '/repo') return '';
        if (payload?.key === 'settings.provider.codex.codexHome' && payload?.cwd === '/repo') return null;
        if (payload?.key === 'settings.provider.codex.codexInstanceKey' && payload?.cwd === '/repo') return 'project-instance';
        if (payload?.key === 'settings.provider.codex.codexModelProvider' && payload?.cwd === '/repo') return undefined;
        if (payload?.key === 'settings.provider.codex.model') return 'global-model';
        if (payload?.key === 'settings.provider.codex.effort') return 'global-effort';
        if (payload?.key === 'settings.provider.codex.codexHome') return '/Users/global/.codex';
        if (payload?.key === 'settings.provider.codex.codexInstanceKey') return 'global-instance';
        if (payload?.key === 'settings.provider.codex.codexModelProvider') return 'openai-compatible';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-global-detail-fallback' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-global-detail-fallback');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload).toEqual(expect.objectContaining({
      modelProvider: 'codex',
      model: 'project-model',
      effort: 'global-effort',
    }));
    expect(startPayload?.config).toEqual(expect.objectContaining({
      codexHome: '/Users/global/.codex',
      codexInstanceKey: 'project-instance',
      codexModelProvider: 'openai-compatible',
    }));
  });

  it('project tombstones stop global fallback and omit the cleared launch fields', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') return null;
        if (payload?.key === 'settings.provider.active') return 'codex';
        if (payload?.key === 'settings.provider.codex.model' && payload?.cwd === '/repo') return { cleared: true };
        if (payload?.key === 'settings.provider.codex.effort' && payload?.cwd === '/repo') return { cleared: true };
        if (payload?.key === 'settings.provider.codex.codexHome' && payload?.cwd === '/repo') return { cleared: true };
        if (payload?.key === 'settings.provider.codex.model') return 'global-model';
        if (payload?.key === 'settings.provider.codex.effort') return 'global-effort';
        if (payload?.key === 'settings.provider.codex.codexHome') return '/Users/global/.codex';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-tombstone-cleared' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-tombstone-cleared');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload?.model).toBeUndefined();
    expect(startPayload?.effort).toBeUndefined();
    expect(startPayload?.config).not.toHaveProperty('codexHome');
  });

  it('does not add Codex identity defaults for non-Codex providers', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return 'claude';
        if (payload?.key === 'settings.provider.codex.codexHome') return '/should/not/read';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-claude-no-codex-defaults' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-claude-no-codex-defaults');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-claude', {});

    expect(startPayload).toEqual(expect.objectContaining({ cwd: '/repo-claude', modelProvider: 'claude' }));
    expect(startPayload?.config || {}).not.toHaveProperty('codexHome');
    expect(startPayload?.config || {}).not.toHaveProperty('codexInstanceKey');
    expect(startPayload?.config || {}).not.toHaveProperty('codexModelProvider');
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('ui/preferences/get', expect.objectContaining({
      key: 'settings.provider.codex.codexHome',
    }));
  });

  it('does not read or forward sentinel Codex model and effort preferences for non-Codex launches', async () => {
    const store = useThreadStore();
    let startPayload = null;
    const readKeys = [];
    const codexSentinels = {
      'settings.provider.codex.model': 'codex-sentinel-model',
      'settings.provider.codex.effort': 'codex-sentinel-effort',
      'settings.provider.codex.codexModelProvider': 'codex-sentinel-provider',
    };
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        readKeys.push(payload?.key);
        if (payload?.key === 'settings.provider.active') return 'claude';
        if (payload?.key === 'settings.provider.claude.model') return 'sonnet';
        if (payload?.key === 'settings.provider.claude.effort') return 'high';
        if (Object.prototype.hasOwnProperty.call(codexSentinels, payload?.key)) {
          return codexSentinels[payload.key];
        }
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-claude-ignore-codex-sentinels' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-claude-ignore-codex-sentinels');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-claude', {});

    expect(readKeys).not.toContain('settings.provider.codex.model');
    expect(readKeys).not.toContain('settings.provider.codex.effort');
    expect(readKeys).not.toContain('settings.provider.codex.codexModelProvider');
    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo-claude',
      modelProvider: 'claude',
      model: 'sonnet',
      effort: 'high',
    }));
    expect(startPayload?.model).not.toBe(codexSentinels['settings.provider.codex.model']);
    expect(startPayload?.effort).not.toBe(codexSentinels['settings.provider.codex.effort']);
    expect(startPayload?.config || {}).not.toHaveProperty('codexModelProvider');
  });

  it('does not advertise semantic Codex LSP tools in thread/start defaults without backend availability', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-codex-lsp-defaults' } };
      }
      if (method === 'ui/state/get') return buildSnapshot('thread-codex-lsp-defaults');
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-codex', {});

    expect(startPayload?.config?.enabledTools).toEqual(
      expect.arrayContaining(['file', 'grep', 'inspect', 'xref', 'structure', 'edit', 'completion']),
    );
    const removedRunTools = ['code' + '_run', 'code' + '_run_test'];
    expect(startPayload?.config?.enabledTools || []).not.toEqual(expect.arrayContaining(removedRunTools));
    expect(startPayload?.config?.mcpTools).toEqual(
      expect.arrayContaining([
        'mcp__lsp__file',
        'mcp__lsp__grep',
        'mcp__lsp__inspect',
        'mcp__lsp__xref',
        'mcp__lsp__structure',
        'mcp__lsp__edit',
        'mcp__lsp__completion',
      ]),
    );
    expect(startPayload?.config?.mcpTools || []).not.toEqual(
      expect.arrayContaining(removedRunTools.map((name) => `mcp__lsp__${name}`)),
    );
  });
});
