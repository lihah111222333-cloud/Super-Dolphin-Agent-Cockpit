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
      sandbox: sandboxSummary(),
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
    const allowedSandboxAccessFields = new Set(['readableRoots', 'writableRoots']);
    const allowedSandboxPolicies = new Set(['workspaceWrite', 'readOnly', 'dangerFullAccess']);
    const allowedReadOnlyModes = new Set(['', 'fullAccess', 'restricted']);
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
        const params = request.params || {};
        const call = { jsonrpc: request.jsonrpc, id: request.id, method: request.method, params };
        state.calls.push({
          jsonrpc: call.jsonrpc,
          id: call.id,
          method: call.method,
          params: sanitizedCallParams(call.method, params),
        });
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

    function sanitizedCallParams(method, params = {}) {
      if (method === 'ui/preferences/set') return sanitizedPreferenceCallParams(params);
      return sanitizedEvidenceValue(params);
    }

    function sanitizedPreferenceCallParams(params = {}) {
      const payload = params && typeof params === 'object' && !Array.isArray(params) ? params : {};
      const key = String(payload.key || '');
      const summary = {
        cwd: sandboxPathSummary(payload.cwd),
        ...sanitizedPreferenceKeySummary(key),
        ...sanitizedPreferenceValueSummary(key, payload.value, Object.prototype.hasOwnProperty.call(payload, 'value')),
      };
      const unexpectedFields = Object.keys(payload).filter((field) => !allowedPreferencePayloadFields.has(field));
      if (unexpectedFields.length > 0) summary.unexpectedFields = unexpectedFields.map(sanitizedFieldName);
      return summary;
    }

    function sanitizedPreferenceKeySummary(key) {
      if (allowedProviderPreferenceKeys.has(key)) return { key };
      return { keyType: key ? 'unsupported' : 'missing' };
    }

    function sanitizedPreferenceValueSummary(key, value, hasValue) {
      if (!hasValue) return { valueType: 'missing' };
      if (key === 'settings.provider.codex.codexHome') {
        return { valueType: 'path', path: sandboxPathSummary(value) };
      }
      if (key === 'settings.provider.codex.sandbox') return sandboxPreferenceSummary(value);
      if (value === null) return { valueType: 'null' };
      if (Array.isArray(value)) return { valueType: 'array' };
      return { valueType: typeof value };
    }

    function sandboxPreferenceSummary(value) {
      if (!value || typeof value !== 'object' || Array.isArray(value)) {
        return { valueType: Array.isArray(value) ? 'array' : typeof value };
      }
      const access = value.access && typeof value.access === 'object' && !Array.isArray(value.access) ? value.access : {};
      const readableRoots = [
        ...rootsFrom(value, 'readableRoots'),
        ...rootsFrom(access, 'readableRoots'),
      ];
      const writableRoots = [
        ...rootsFrom(value, 'writableRoots'),
        ...rootsFrom(access, 'writableRoots'),
      ];
      return {
        valueType: 'object',
        sandboxPolicy: sanitizedSandboxPolicySummary(value.type),
        writableRoots: writableRoots.map(sandboxPathSummary),
        readableRoots: readableRoots.map(sandboxPathSummary),
        networkAccess: Boolean(value.networkAccess),
        readOnlyMode: sanitizedReadOnlyModeSummary(value.readOnlyMode),
      };
    }

    function sanitizedSandboxPolicySummary(value) {
      const text = String(value || '');
      return allowedSandboxPolicies.has(text) ? text : 'unsupported';
    }

    function sanitizedReadOnlyModeSummary(value) {
      const text = String(value || '');
      return allowedReadOnlyModes.has(text) ? text : 'unsupported';
    }

    function sanitizedEvidenceValue(value) {
      if (Array.isArray(value)) return value.map(sanitizedEvidenceValue);
      if (value && typeof value === 'object') {
        return Object.fromEntries(Object.entries(value).map(([field, fieldValue]) => [field, sanitizedEvidenceValue(fieldValue)]));
      }
      if (typeof value === 'string') {
        if (/sk-[a-z0-9_-]{8,}/iu.test(value)) return 'redacted';
        if (looksPathLike(value)) return sandboxPathSummary(value);
      }
      return value;
    }

    function sandboxSummary() {
      return {
        rootDir: 'sandbox',
        homeDir: 'sandbox',
        projectDir: 'sandbox',
        uploadFile: 'sandbox',
      };
    }

    function savePreference(params = {}, method) {
      assertPreferencePayloadShape(params, method);
      const cwd = String(params.cwd || '');
      const key = String(params.key || '');
      if (!cwd) throw new Error(`${method} cwd is required`);
      assertSandboxPath(method, cwd);
      if (!allowedProviderPreferenceKeys.has(key)) throw new Error(`${method} unsupported settings preference key`);
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
          throw new Error(`${method} unsupported preference payload field: ${sanitizedFieldName(field)}`);
        }
      }
    }

    function sanitizedPreferenceWrite(method, key, value) {
      if (key === 'settings.provider.codex.codexHome') {
        assertSandboxPath(method, value);
        return { valueType: 'path', path: 'sandbox' };
      }
      if (key === 'settings.provider.codex.sandbox') return sanitizedSandboxPreference(method, value);
      if (key === 'settings.provider.codex.codexInstanceKey') return { valueType: 'string', value: sanitizedScalar(method, value) };
      if (key === 'settings.provider.codex.personality') return { valueType: 'string', value: sanitizedScalar(method, value) };
      if (key === 'settings.provider.codex.model') return { valueType: 'string', value: sanitizedScalar(method, value) };
      if (key === 'settings.provider.codex.effort') return { valueType: 'string', value: sanitizedScalar(method, value) };
      throw new Error(`${method} unsupported settings preference key`);
    }

    function sanitizedSandboxPreference(method, value) {
      if (!value || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error(`${method} sandbox preference must be an object`);
      }
      const type = String(value.type || '');
      if (!allowedSandboxPolicies.has(type)) {
        throw new Error(`${method} unsupported sandbox policy`);
      }
      const readOnlyMode = String(value.readOnlyMode || '');
      if (!allowedReadOnlyModes.has(readOnlyMode)) {
        throw new Error(`${method} unsupported read-only mode`);
      }
      const access = sandboxAccess(method, value.access);
      const writableRoots = [
        ...rootsFrom(value, 'writableRoots', method),
        ...rootsFrom(access, 'writableRoots', method),
      ];
      const readableRoots = [
        ...rootsFrom(value, 'readableRoots', method),
        ...rootsFrom(access, 'readableRoots', method),
      ];
      for (const root of [...writableRoots, ...readableRoots]) assertSandboxPath(method, root);
      return {
        valueType: 'object',
        sandboxPolicy: type,
        writableRoots: writableRoots.map(() => 'sandbox'),
        readableRoots: readableRoots.map(() => 'sandbox'),
        networkAccess: Boolean(value.networkAccess),
        readOnlyMode,
      };
    }

    function sandboxAccess(method, value) {
      if (value === undefined) return {};
      if (!value || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error(`${method} sandbox access must be an object`);
      }
      for (const field of Object.keys(value)) {
        if (!allowedSandboxAccessFields.has(field)) {
          throw new Error(`${method} unsupported sandbox access field: ${sanitizedFieldName(field)}`);
        }
      }
      return value;
    }

    function rootsFrom(value, field, method) {
      if (!Object.prototype.hasOwnProperty.call(value || {}, field)) return [];
      if (!Array.isArray(value[field])) {
        if (method) throw new Error(`${method} sandbox ${field} must be an array`);
        return [];
      }
      return value[field];
    }

    function sanitizedScalar(method, value) {
      const text = String(value || '').trim();
      if (isSensitivePreferenceScalar(text)) {
        throw new Error(`${method} sensitive preference value must not be recorded`);
      }
      return text;
    }

    function sanitizedFieldName(field) {
      const text = String(field || '');
      if (/^[a-z][a-z0-9_]*$/iu.test(text)) return text;
      return 'unsupported';
    }

    function isSensitivePreferenceScalar(text) {
      return /sk-[a-z0-9_-]{8,}/iu.test(text) || looksPathLike(text);
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
        const path = sandboxPathSummary(target);
        const message = `${method} path outside sandbox: ${path}`;
        state.sandboxViolations.push({ method, path, message });
        throw new Error(message);
      }
    }

    function isInsideSandbox(value) {
      const root = normalizeSandboxPath(sandboxConfig.rootDir);
      const target = normalizeSandboxPath(value);
      return Boolean(root && (target === root || target.startsWith(`${root}/`)));
    }

    function sandboxPathSummary(value) {
      const target = String(value || '');
      if (!target) return 'missing';
      return isInsideSandbox(target) ? 'sandbox' : 'outside';
    }

    function normalizeSandboxPath(value) {
      const text = String(value || '').trim().replace(/\\/gu, '/');
      if (!text || text.includes('\0')) return '';
      const absolute = text.startsWith('/');
      const parts = [];
      for (const part of text.split('/')) {
        if (!part || part === '.') continue;
        if (part === '..') {
          if (parts.length === 0) return '';
          parts.pop();
          continue;
        }
        parts.push(part);
      }
      const normalized = `${absolute ? '/' : ''}${parts.join('/')}`.replace(/\/+$/u, '');
      return normalized || (absolute ? '/' : '');
    }

    function looksPathLike(value) {
      return value.startsWith('/') || value.includes(sandboxConfig.rootDir);
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
