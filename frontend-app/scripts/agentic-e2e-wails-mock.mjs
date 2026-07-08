export async function installAgenticE2EMockWails(page, options = {}) {
  const sandbox = normalizeMockSandbox(options.sandbox);
  await page.addInitScript((mockOptions) => {
    const NativeWebSocket = window.WebSocket;
    const sandboxConfig = mockOptions.sandbox;
    const state = {
      calls: [],
      failures: [],
      unhandledRPC: [],
      nonWailsSockets: [],
      sandbox: sandboxConfig,
      sandboxViolations: [],
      settingsWrites: [],
      eventNotifications: 0,
      nextThreadId: 'thread-agentic-e2e',
      recentEvents: [{
        trace_id: 'agentic-e2e-trace-1',
        span_id: 'span-1',
        method: 'thread/start',
        component: 'frontend',
        status: 'ok',
        duration_ms: 12,
        ts: '2026-07-04T00:00:00Z',
      }],
    };
    const allowedProviderPreferenceKeys = new Set([
      'settings.provider.codex.personality',
      'settings.provider.codex.sandbox',
      'settings.provider.codex.model',
      'settings.provider.codex.effort',
      'settings.provider.codex.codexHome',
      'settings.provider.codex.codexInstanceKey',
    ]);
    const allowedPreferencePayloadFields = new Set(['cwd', 'key', 'value']);
    window.__AGENTIC_E2E_MOCK_WAILS__ = state;

    class StrictMockWebSocket {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      onopen = null;
      onmessage = null;
      onerror = null;
      onclose = null;

      constructor(url, protocols) {
        if (!String(url || '').endsWith('/wails/ws')) {
          state.nonWailsSockets.push(String(url || ''));
          return protocols === undefined ? new NativeWebSocket(url) : new NativeWebSocket(url, protocols);
        }
        this.url = url;
        this.protocol = '';
        this.extensions = '';
        this.bufferedAmount = 0;
        this.binaryType = 'blob';
        this.readyState = StrictMockWebSocket.CONNECTING;
        queueMicrotask(() => {
          this.readyState = StrictMockWebSocket.OPEN;
          this.onopen?.({ target: this });
        });
      }

      send(raw) {
        const request = JSON.parse(raw);
        const call = { jsonrpc: request.jsonrpc, id: request.id, method: request.method, params: request.params || {} };
        state.calls.push(call);
        queueMicrotask(() => this.respond(call));
      }

      respond(call) {
        try {
          this.onmessage?.({
            data: JSON.stringify({
              jsonrpc: '2.0',
              id: call.id,
              result: responseForRPC(call.method, call.params),
            }),
          });
          if (call.method === 'turn/start') this.emitNotification('thread/tokenUsage/updated', {
            threadId: state.nextThreadId,
            usedTokens: 1,
            contextWindowTokens: 1000,
            usedPercent: 0.1,
          });
        }
        catch (error) {
          const message = error?.message || String(error);
          state.failures.push({ method: call.method, message });
          this.onmessage?.({
            data: JSON.stringify({
              jsonrpc: '2.0',
              id: call.id,
              error: { code: -32000, message },
            }),
          });
        }
      }

      emitNotification(method, params) {
        state.eventNotifications += 1;
        this.onmessage?.({
          data: JSON.stringify({ jsonrpc: '2.0', method, params }),
        });
      }

      close() {
        this.readyState = StrictMockWebSocket.CLOSED;
        this.onclose?.({ target: this, code: 1000, reason: 'closed by agentic e2e mock' });
      }

      addEventListener(type, listener) {
        this[`on${type}`] = listener;
      }

      removeEventListener(type, listener) {
        if (this[`on${type}`] === listener) this[`on${type}`] = null;
      }
    }

    function responseForRPC(method, params = {}) {
      if (method === 'ui/log' || method === 'observability/frontend/ingest') return { ok: true };
      if (method === 'ui/buildInfo') return { version: 'agentic-e2e-mock' };
      if (method === 'config/read') return { cwd: sandboxConfig.projectDir };
      if (method === 'ui/windowBootstrap/get') return { snapshot: null };
      if (method === 'ui/preferences/get') return preferenceFor(params);
      if (method === 'ui/preferences/getAll') return { preferences: {} };
      if (method === 'ui/preferences/set') return savePreference(params, method);
      if (method === 'ui/projects/get') return projectList();
      if (method === 'ui/selectProjectDir') return selectProjectDir(params);
      if (method === 'ui/selectFiles') return { paths: [sandboxConfig.uploadFile] };
      if (method === 'ui/projects/add') return addProject(params, method);
      if (method === 'ui/projects/setActive') return setActiveProject(params, method);
      if (method === 'ui/projects/remove') return projectList();
      if (method === 'ui/sidebar/get') return sidebarSnapshot();
      if (method === 'ui/state/get') return threadState(params.threadId || params.thread_id || state.nextThreadId);
      if (method === 'thread/messages') return { messages: [] };
      if (method === 'thread/config/get') return threadConfig(params.threadId || params.thread_id || state.nextThreadId);
      if (method === 'thread/start') return startThread(params, method);
      if (method === 'turn/start') return startTurn(params, method);
      if (method === 'thread/delete') return { ok: true };
      if (method === 'app/update/check') return { enabled: false, available: false };
      if (method === 'ui/dashboard/get') return dashboardPage(params.page);
      if (method === 'ui/memory/get') return memorySnapshot();
      if (method === 'dashboard/sharedFiles') return sharedFilesDashboard();
      if (method === 'observability/status') return { enabled: true, schema_version: 1, index_trace_keys: 1, sink_events_written: 1, sink_write_errors: 0 };
      if (method === 'observability/recent/list') return observabilityResult();
      if (method === 'observability/slow/list' || method === 'observability/error/list' || method === 'observability/thread/recent' || method === 'observability/trace/get') {
        return observabilityResult();
      }
      if (method === 'prompt-assets/list') return { prompts: [] };
      if (method === 'dashboard/prompts') return { prompts: [] };
      if (method === 'prompt-sections/list') return { sections: [] };
      if (method === 'personalization/profile/get') return { profile: {} };
      if (method === 'modelProviders/list') return modelProviders();
      if (method === 'ui/video/getApiKey') return { configured: false, masked: '' };
      if (method === 'ui/video/setApiKey') return saveVideoApiKey(params, method);
      if (method === 'config/lspPromptHint/read') return { hint: 'agentic e2e prompt hint', defaultHint: 'agentic e2e prompt hint', overrideHint: '', usingDefault: true };
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'dashboard/dags') return { dags: [] };
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'workflowTemplates/list') return { templates: [] };
      if (method === 'cronjob/list') return { jobs: [] };
      if (method === 'skills/resolution_list') return { items: [] };
      if (method === 'skills/tools/list') return { tools: [] };
      if (method === 'mcpServer/list') return { mcpServers: { sqlite: { enabled: false }, playwright: { enabled: false } } };
      if (method === 'datasourceV2/list') return { documents: [] };
      state.unhandledRPC.push(method);
      throw new Error(`unhandled agentic e2e mock RPC: ${method}`);
    }

    function projectList(active = sandboxConfig.projectDir) {
      return { projects: [sandboxConfig.projectDir], active };
    }

    function selectProjectDir(params = {}) {
      const seed = String(params.defaultPath || '');
      if (seed) assertSandboxPath('ui/selectProjectDir', seed);
      return { path: sandboxConfig.projectDir };
    }

    function addProject(params = {}, method) {
      assertSandboxPath(method, params.cwd);
      assertSandboxPath(method, params.path);
      return projectList(String(params.path || sandboxConfig.projectDir));
    }

    function setActiveProject(params = {}, method) {
      assertSandboxPath(method, params.cwd);
      assertSandboxPath(method, params.path);
      return projectList(String(params.path || sandboxConfig.projectDir));
    }

    function startThread(params = {}, method) {
      assertSandboxPath(method, params.cwd);
      return {
        threadId: state.nextThreadId,
        thread_id: state.nextThreadId,
        thread: { id: state.nextThreadId, agentId: 'agent-agentic-e2e', provider: 'codex' },
      };
    }

    function startTurn(params = {}, method) {
      assertSandboxPath(method, params.cwd);
      return { turn_id: 'turn-agentic-e2e' };
    }

    function savePreference(params = {}, method) {
      assertPreferencePayloadShape(params, method);
      const cwd = String(params.cwd || '');
      const key = String(params.key || '');
      if (!cwd) throw new Error(`${method} cwd is required`);
      assertSandboxPath(method, cwd);
      if (!allowedProviderPreferenceKeys.has(key)) throw new Error(`${method} unsupported settings preference key: ${key}`);
      if (!Object.prototype.hasOwnProperty.call(params, 'value')) throw new Error(`${method} value is required`);
      const summary = sanitizedPreferenceWrite(method, key, params.value);
      state.settingsWrites.push({
        method,
        key,
        cwd: 'sandbox',
        ...summary,
      });
      return { ok: true };
    }

    function assertPreferencePayloadShape(params, method) {
      for (const field of Object.keys(params || {})) {
        if (!allowedPreferencePayloadFields.has(field)) {
          throw new Error(`${method} unsupported preference payload field: ${field}`);
        }
      }
    }

    function sanitizedPreferenceWrite(method, key, value) {
      if (key === 'settings.provider.codex.codexHome') {
        assertSandboxPath(method, value);
        return { valueType: 'path', path: 'sandbox' };
      }
      if (key === 'settings.provider.codex.sandbox') return sanitizedSandboxPreference(method, value);
      if (key === 'settings.provider.codex.codexInstanceKey') return { valueType: 'string', value: sanitizedScalar(value) };
      if (key === 'settings.provider.codex.personality') return { valueType: 'string', value: sanitizedScalar(value) };
      if (key === 'settings.provider.codex.model') return { valueType: 'string', value: sanitizedScalar(value) };
      if (key === 'settings.provider.codex.effort') return { valueType: 'string', value: sanitizedScalar(value) };
      throw new Error(`${method} unsupported settings preference key: ${key}`);
    }

    function sanitizedSandboxPreference(method, value) {
      if (!value || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error(`${method} sandbox preference must be an object`);
      }
      const type = String(value.type || '');
      if (!['workspaceWrite', 'readOnly', 'dangerFullAccess'].includes(type)) {
        throw new Error(`${method} unsupported sandbox policy: ${type}`);
      }
      const writableRoots = Array.isArray(value.writableRoots) ? value.writableRoots : [];
      const readableRoots = Array.isArray(value.readableRoots) ? value.readableRoots : [];
      for (const root of [...writableRoots, ...readableRoots]) assertSandboxPath(method, root);
      return {
        valueType: 'object',
        sandboxPolicy: type,
        writableRoots: writableRoots.map(() => 'sandbox'),
        readableRoots: readableRoots.map(() => 'sandbox'),
        networkAccess: Boolean(value.networkAccess),
        readOnlyMode: String(value.readOnlyMode || ''),
      };
    }

    function sanitizedScalar(value) {
      const text = String(value || '').trim();
      if (/sk-[a-z0-9_-]{8,}/iu.test(text)) throw new Error('secret-like preference value must not be recorded');
      return text;
    }

    function saveVideoApiKey(params = {}, method) {
      const apiKey = String(params.apiKey || params.api_key || '');
      if (!apiKey) throw new Error(`${method} apiKey is required`);
      state.settingsWrites.push({ method, apiKeyLength: apiKey.length });
      return { ok: true };
    }

    function assertSandboxPath(method, value) {
      const target = String(value || '');
      if (!target || !isInsideSandbox(target)) {
        const message = `${method} path outside sandbox: ${target}`;
        state.sandboxViolations.push({ method, path: target, message });
        throw new Error(message);
      }
    }

    function isInsideSandbox(value) {
      const root = String(sandboxConfig.rootDir || '').replace(/\/+$/u, '');
      const target = String(value || '').replace(/\/+$/u, '');
      return Boolean(root && (target === root || target.startsWith(`${root}/`)));
    }

    function preferenceFor(params = {}) {
      const key = String(params.key || '');
      if (key.includes('provider.active')) return 'codex';
      if (key.includes('codexModelProvider')) return 'openai';
      if (key.includes('codexHome')) return sandboxConfig.homeDir;
      if (key.includes('codexInstanceKey')) return 'default';
      return '';
    }

    function sidebarSnapshot() {
      return {
        activeThreadId: '',
        threads: [],
        active_turn: null,
        tokenUsageByThread: {},
        activityStatsByThread: {},
      };
    }

    function threadState(threadId) {
      return {
        activeThreadId: threadId,
        timelinesByThread: { [threadId]: [] },
        diffTextByThread: {},
      };
    }

    function threadConfig(threadId) {
      return {
        threadId,
        provider: 'codex',
        supportsThreadOverride: true,
        override: { model: '', effort: '' },
        effective: { model: '', effort: '' },
      };
    }

    function dashboardPage(page) {
      if (page === 'skills') return {
        skills: [{
          name: 'backend',
          display_name: '后端',
          dir: `${sandboxConfig.projectDir}/.agents/skills/e2e-fixture`,
          description: 'agentic e2e skill fixture',
          trigger_words: ['go'],
          scope: 'project',
        }],
      };
      if (page === 'memory') return memorySnapshot();
      if (page === 'dags') return { dags: [] };
      return {};
    }

    function memorySnapshot() {
      return {
        overview: {
          enabled: true,
          autoDreamEnabled: false,
          autoDreamIntent: null,
          projectRoot: sandboxConfig.projectDir,
          health: { preferenceCount: 0, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
        },
        private: { entries: [] },
        team: { entries: [] },
        finalOutputRefs: [],
        sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
      };
    }

    function sharedFilesDashboard() {
      return {
        files: [],
        finalOutputRefs: [],
        sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
      };
    }

    function observabilityResult() {
      return {
        source: 'mock',
        events: state.recentEvents,
        slowest_events: [],
        errors: [],
        total_duration_ms: 12,
        truncated: false,
      };
    }

    function modelProviders() {
      return {
        activeVendorId: 'openai',
        vendors: [{
          id: 'openai',
          label: 'OpenAI',
          enabled: true,
          baseURL: 'https://api.openai.com/v1',
          envKey: 'OPENAI_API_KEY',
          codexModelProvider: 'openai',
          defaultModel: 'gpt-5',
          configured: true,
          maskedEnv: '********',
          envStatus: 'configured',
          budget: {},
          tokenPool: { priority: 10 },
        }],
      };
    }

    window.WebSocket = StrictMockWebSocket;
  }, { sandbox });
}

export async function readAgenticE2EMockWailsState(page) {
  return page.evaluate(() => window.__AGENTIC_E2E_MOCK_WAILS__ || null).catch(() => null);
}

export function assertAgenticE2EMockWailsClean(state) {
  if (!state) return;
  const failures = [
    ...(state.unhandledRPC || []).map((method) => `unhandled RPC ${method}`),
    ...(state.failures || []).map((failure) => `${failure.method}: ${failure.message}`),
    ...(state.sandboxViolations || []).map((violation) => `${violation.method}: ${violation.message || `outside sandbox path ${violation.path}`}`),
  ];
  if (failures.length > 0) {
    throw new Error(`agentic e2e mock Wails failures: ${failures.join('; ')}`);
  }
}

function normalizeMockSandbox(sandbox) {
  if (!sandbox || typeof sandbox !== 'object' || Array.isArray(sandbox)) {
    throw new Error('agentic e2e mock Wails sandbox config is required');
  }
  const normalized = {};
  for (const field of ['rootDir', 'homeDir', 'projectDir', 'uploadFile']) {
    const value = String(sandbox[field] || '').trim();
    if (!value) throw new Error(`agentic e2e mock Wails sandbox ${field} is required`);
    normalized[field] = value;
  }
  return Object.freeze(normalized);
}
