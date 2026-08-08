/* global process */
import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

const BUG_REPORT_DIR = path.resolve(process.cwd(), '..', '.tmp', 'agentic-e2e', 'business-flow-playwright');

test.beforeEach(async ({ page }, testInfo) => {
  await installStrictWailsMock(page);
  await installBugCapture(page, testInfo);
});

test.afterEach(async ({ page }, testInfo) => {
  await writeBusinessFlowBugReport(page, testInfo);
});

test.describe('business-read-surfaces', () => {
test('discovered business entries open stable read surfaces', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByTestId('frontend-app')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();

  await openBusinessEntry(page, { label: '插件与技能', route: /\/skills$/, assert: async () => {
    await expect(page.getByRole('heading', { name: 'MCP工具' })).toBeVisible();
    await expect(page.getByRole('region', { name: 'SQLite MCP 控制', exact: true })).toBeVisible();
    await expect(page.getByRole('region', { name: 'Playwright MCP 控制', exact: true })).toBeVisible();
  } });

  await openBusinessEntry(page, { label: '自动化', route: /\/dags$/, assert: async () => {
    const overview = page.getByRole('region', { name: '自动化资产', exact: true });
    await expect(overview).toBeVisible();
    await expect(overview.getByRole('heading', { name: '自动化和运行状态', exact: true })).toBeVisible();
  } });

  await openBusinessEntry(page, { label: '提示词', route: /\/prompts$/, assert: async () => {
    const overview = page.getByRole('region', { name: '个性化概览', exact: true });
    await expect(overview).toBeVisible();
    await expect(overview.getByRole('heading', { name: '定制角色、知识和记忆', exact: true })).toBeVisible();
  } });

  await openBusinessEntry(page, { label: '共享文件', route: /\/files$/, assert: async () => {
    const overview = page.getByRole('region', { name: '共享文件状态', exact: true });
    await expect(overview).toBeVisible();
    await expect(overview.getByRole('heading', { name: '共享文件和最终产物', exact: true })).toBeVisible();
  } });

  await openBusinessEntry(page, { label: '记忆中心', route: /\/memory$/, assert: async () => {
    await expect(page.getByRole('heading', { name: '记忆中心', exact: true })).toBeVisible();
  }, navTestId: 'sidebar-nav' });

  await openBusinessEntry(page, { label: '链路追踪', route: /\/observability$/, assert: async () => {
    await expect(page.getByTestId('observability-page')).toBeVisible();
    await page.getByRole('button', { name: '查询最新日志' }).click();
    await expect(page.getByTestId('observability-recent-logs')).toBeVisible();
  }, navTestId: 'sidebar-nav' });

  await page.getByRole('button', { name: '设置' }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.getByTestId('settings-page')).toBeVisible();

  const runtime = await runtimeSnapshot(page);
  const readinessCalls = runtime.calls.filter((call) => call.method === 'ui/frontend/readiness');
  const readinessProbeEpochs = readinessCalls.filter((call) => call.params.phase === 'probe').map((call) => call.response.epoch);
  const readinessCommitEpochs = readinessCalls.filter((call) => call.params.phase === 'commit').map((call) => call.params.epoch);
  expect(readinessProbeEpochs).not.toEqual([]);
  expect(readinessCommitEpochs).toEqual(readinessProbeEpochs);
  expect(runtime.unhandledRPC).toEqual([]);
  expect(runtime.failures).toEqual([]);
  expect(runtime.runtimeTelemetry.filter((item) => item.status === 'error' || String(item.phase || '').endsWith('.failed') || String(item.phase || '').endsWith('.timeout'))).toEqual([]);
});
});

test.describe('business-chat-bridge', () => {
test('high-risk chat send creates a thread then starts a turn through the bridge', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByTestId('chat-page')).toBeVisible();

  const prompt = 'Playwright 高风险链路：请返回一条短回复';
  await page.getByTestId('composer-input').fill(prompt);
  await page.getByRole('button', { name: '发送消息' }).click();

  await expect(page.getByTestId('composer-input')).toHaveValue('');
  await expect(page.getByTestId('chat-timeline')).toContainText(prompt);
  await expect(page.getByTestId('thread-rail')).toContainText(prompt);

  const runtime = await runtimeSnapshot(page);
  const calls = runtime.calls;
  const threadStartIndex = calls.findIndex((call) => call.method === 'thread/start');
  const turnStartIndex = calls.findIndex((call) => call.method === 'turn/start');
  expect(threadStartIndex).toBeGreaterThanOrEqual(0);
  expect(turnStartIndex).toBeGreaterThan(threadStartIndex);

  const threadStart = calls[threadStartIndex];
  const turnStart = calls[turnStartIndex];
  expect(threadStart).toEqual(expect.objectContaining({ jsonrpc: '2.0', id: expect.any(Number) }));
  expect(turnStart).toEqual(expect.objectContaining({ jsonrpc: '2.0', id: expect.any(Number) }));
  expect(threadStart.params).toEqual(expect.objectContaining({
    cwd: '/repo/app',
    defer_spawn: true,
    name: prompt,
    provider: 'codex',
  }));
  expect(threadStart.params).not.toHaveProperty('deferSpawn');
  expect(threadStart.params).not.toHaveProperty('codexModelProvider');
  expect(threadStart.params).not.toHaveProperty('modelProvider');
  expect(threadStart.params).toEqual(expect.objectContaining({
    _aoTraceId: expect.stringMatching(/^[0-9a-f]{32}$/),
    _aoSpanId: expect.stringMatching(/^[0-9a-f]{16}$/),
    _aoTraceparent: expect.stringMatching(/^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/),
  }));
  expect(threadStart.params).not.toHaveProperty('_aoClientKind');
  expect(threadStart.params).not.toHaveProperty('_aoClientRoute');
  expect(threadStart.params).not.toHaveProperty('_aoRequestId');
  expect(threadStart.params.config).toEqual(expect.objectContaining({
    codexHome: '~/.codex',
    codexInstanceKey: 'default',
    codexModelProvider: 'openai',
  }));
  expect(threadStart.params.launchIntentId).toEqual(expect.stringMatching(/^launch_/));
  expect(turnStart.params).toEqual(expect.objectContaining({
    cwd: '/repo/app',
    threadId: 'thread-business-e2e',
    manualSkillSelection: false,
  }));
  expect(turnStart.params.input).toEqual([{ type: 'text', text: prompt }]);
  expect(turnStart.params).not.toHaveProperty('prompt');
  expect(turnStart.params).not.toHaveProperty('attachments');
  expect(runtime.runtimeTelemetry).toEqual(expect.arrayContaining([
    expect.objectContaining({ method: 'thread/start', phase: 'runtime.rpc.pending', status: 'ok' }),
    expect.objectContaining({ method: 'thread/start', phase: 'runtime.rpc.send.done', status: 'ok' }),
    expect.objectContaining({ method: 'thread/start', phase: 'runtime.rpc.settled', status: 'ok' }),
    expect.objectContaining({ method: 'turn/start', phase: 'runtime.rpc.pending', status: 'ok' }),
    expect.objectContaining({ method: 'turn/start', phase: 'runtime.rpc.send.done', status: 'ok' }),
    expect.objectContaining({ method: 'turn/start', phase: 'runtime.rpc.settled', status: 'ok' }),
  ]));
  const readinessCalls = runtime.calls.filter((call) => call.method === 'ui/frontend/readiness');
  const readinessProbeEpochs = readinessCalls.filter((call) => call.params.phase === 'probe').map((call) => call.response.epoch);
  const readinessCommitEpochs = readinessCalls.filter((call) => call.params.phase === 'commit').map((call) => call.params.epoch);
  expect(readinessProbeEpochs).not.toEqual([]);
  expect(readinessCommitEpochs).toEqual(readinessProbeEpochs);
  expect(runtime.unhandledRPC).toEqual([]);
  expect(runtime.failures).toEqual([]);
  expect(runtime.runtimeTelemetry.filter((item) => item.status === 'error' || String(item.phase || '').endsWith('.failed') || String(item.phase || '').endsWith('.timeout'))).toEqual([]);
});
});

async function openBusinessEntry(page, { label, route, assert, navTestId = 'sidebar-nav' }) {
  await page.getByTestId(navTestId).getByRole('button', { name: label }).click();
  await expect(page).toHaveURL(route);
  await assert();
}

async function installBugCapture(page, testInfo) {
  const pageErrors = [];
  const consoleErrors = [];
  const failedRequests = [];
  const httpErrors = [];
  await page.addInitScript(() => {
    window.__BUSINESS_FLOW_CAPTURE__ = { runtimeTelemetry: [] };
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = (detail) => {
      window.__BUSINESS_FLOW_CAPTURE__.runtimeTelemetry.push(detail);
    };
  });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('requestfailed', (request) => {
    failedRequests.push({
      method: request.method(),
      url: request.url(),
      failure: request.failure()?.errorText || 'request failed',
    });
  });
  page.on('response', (response) => {
    if (response.status() < 400) return;
    httpErrors.push({
      status: response.status(),
      url: response.url(),
      requestMethod: response.request().method(),
    });
  });
  testInfo._businessFlowBugs = { pageErrors, consoleErrors, failedRequests, httpErrors };
}

async function writeBusinessFlowBugReport(page, testInfo) {
  const runtime = await runtimeSnapshot(page);
  const runtimeTelemetryFailures = runtime.runtimeTelemetry.filter((item) => item.status === 'error' || String(item.phase || '').endsWith('.failed') || String(item.phase || '').endsWith('.timeout'));
  const runtimeTelemetryMissing = runtime.calls.length > 0 && runtime.runtimeTelemetry.length === 0
    ? ['runtime telemetry hook did not receive events']
    : [];
  const unexpectedNonWailsSockets = runtime.nonWailsSockets.filter((url) => !isAllowedNonWailsSocket(url));
  const bugs = {
    test: testInfo.title,
    status: testInfo.status,
    expectedStatus: testInfo.expectedStatus,
    pageErrors: testInfo._businessFlowBugs?.pageErrors || [],
    consoleErrors: testInfo._businessFlowBugs?.consoleErrors || [],
    failedRequests: testInfo._businessFlowBugs?.failedRequests || [],
    httpErrors: testInfo._businessFlowBugs?.httpErrors || [],
    runtimeTelemetryFailures,
    runtimeTelemetryMissing,
    unhandledRPC: runtime.unhandledRPC,
    rpcFailures: runtime.failures,
    nonWailsSockets: runtime.nonWailsSockets,
    unexpectedNonWailsSockets,
    eventNotifications: runtime.eventNotifications,
    runtimeTelemetryCount: runtime.runtimeTelemetry.length,
    callCount: runtime.calls.length,
  };
  bugs.capturedBugFailure = hasCapturedBugs(bugs);
  await mkdir(BUG_REPORT_DIR, { recursive: true });
  await writeFile(
    path.join(BUG_REPORT_DIR, `${safeReportName(testInfo.title)}.json`),
    `${JSON.stringify(bugs, null, 2)}\n`,
    'utf8',
  );

  expect(bugs.pageErrors).toEqual([]);
  expect(bugs.consoleErrors).toEqual([]);
  expect(bugs.failedRequests).toEqual([]);
  expect(bugs.httpErrors).toEqual([]);
  expect(bugs.runtimeTelemetryFailures).toEqual([]);
  expect(bugs.runtimeTelemetryMissing).toEqual([]);
  expect(bugs.unhandledRPC).toEqual([]);
  expect(bugs.rpcFailures).toEqual([]);
  expect(bugs.unexpectedNonWailsSockets).toEqual([]);
}

function hasCapturedBugs(bugs) {
  return bugs.pageErrors.length > 0 ||
    bugs.consoleErrors.length > 0 ||
    bugs.failedRequests.length > 0 ||
    bugs.httpErrors.length > 0 ||
    bugs.runtimeTelemetryFailures.length > 0 ||
    bugs.runtimeTelemetryMissing.length > 0 ||
    bugs.unhandledRPC.length > 0 ||
    bugs.rpcFailures.length > 0 ||
    bugs.unexpectedNonWailsSockets.length > 0;
}

async function runtimeSnapshot(page) {
  return page.evaluate(() => ({
    ...(window.__BUSINESS_FLOW_E2E__ || { calls: [], failures: [], unhandledRPC: [], nonWailsSockets: [], eventNotifications: 0 }),
    runtimeTelemetry: window.__BUSINESS_FLOW_CAPTURE__?.runtimeTelemetry || [],
  })).catch(() => ({
    calls: [],
    failures: [],
    unhandledRPC: ['runtime snapshot unavailable'],
    nonWailsSockets: [],
    eventNotifications: 0,
    runtimeTelemetry: [],
  }));
}

function safeReportName(value) {
  return value.toLowerCase().replace(/[^a-z0-9\u4e00-\u9fff]+/giu, '-').replace(/^-+|-+$/g, '') || 'report';
}

function isAllowedNonWailsSocket(value) {
  try {
    const url = new URL(value);
    return (url.protocol === 'ws:' || url.protocol === 'wss:') &&
      (url.hostname === '127.0.0.1' || url.hostname === 'localhost') &&
      url.pathname === '/' &&
      url.searchParams.has('token');
  }
  catch {
    return false;
  }
}

async function installStrictWailsMock(page) {
  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    const state = {
      calls: [],
      failures: [],
      unhandledRPC: [],
      nonWailsSockets: [],
      eventNotifications: 0,
      nextThreadId: 'thread-business-e2e',
      frontendReadiness: { epoch: 1, committedEpoch: 0 },
      recentEvents: [{
        trace_id: 'business-flow-trace-1',
        span_id: 'span-1',
        method: 'thread/start',
        component: 'frontend',
        status: 'ok',
        duration_ms: 12,
        ts: '2026-07-04T00:00:00Z',
      }],
    };
    window.__BUSINESS_FLOW_E2E__ = state;

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
          const result = responseForRPC(call.method, call.params);
          if (call.method === 'ui/frontend/readiness' && call.params.phase === 'probe') call.response = result;
          this.onmessage?.({
            data: JSON.stringify({
              jsonrpc: '2.0',
              id: call.id,
              result,
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
        this.onclose?.({ target: this, code: 1000, reason: 'closed by test' });
      }

      addEventListener(type, listener) {
        this[`on${type}`] = listener;
      }

      removeEventListener(type, listener) {
        if (this[`on${type}`] === listener) this[`on${type}`] = null;
      }
    }

    function responseForRPC(method, params = {}) {
      if (method === 'ui/frontend/readiness') return frontendReadinessResponse(params);
      if (method === 'ui/log') return { ok: true };
      if (method === 'observability/frontend/ingest') return frontendTraceIngestResponse(params);
      if (method === 'ui/buildInfo') return { version: 'business-flow-e2e' };
      if (method === 'config/read') return {
        model: 'gpt-5.5',
        modelProvider: null,
        cwd: '/repo/app',
        approvalPolicy: 'on-failure',
        sandbox: 'workspace-write',
        config: null,
        baseInstructions: null,
        developerInstructions: null,
        personality: null,
        toolRouting: {
          mode: 'legacy',
          routerModel: '',
          routerProvider: 'openai_compatible',
          routerBaseURL: '',
          routerHasAPIKey: false,
          confidenceThreshold: 0.65,
          timeoutSec: 8,
        },
      };
      if (method === 'ui/windowBootstrap/get') return { snapshot: null };
      if (method === 'ui/preferences/get') return preferenceFor(params);
      if (method === 'ui/preferences/getAll') return { preferences: {} };
      if (method === 'ui/projects/get') return { projects: ['/repo/app'], active: '/repo/app' };
      if (method === 'ui/sidebar/get') return sidebarSnapshot();
      if (method === 'ui/state/get') return threadState(params.threadId || params.thread_id || state.nextThreadId);
      if (method === 'thread/messages') return { messages: [] };
      if (method === 'thread/config/get') return threadConfig(params.threadId || params.thread_id || state.nextThreadId);
      if (method === 'thread/start') return {
        threadId: state.nextThreadId,
        thread_id: state.nextThreadId,
        thread: { id: state.nextThreadId, agentId: 'agent-business-e2e', provider: 'codex' },
      };
      if (method === 'turn/start') return { turn_id: 'turn-business-e2e' };
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
      if (method === 'config/lspPromptHint/read') return { hint: 'business flow prompt hint', defaultHint: 'business flow prompt hint', overrideHint: '', usingDefault: true };
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
      throw new Error(`unhandled business-flow RPC: ${method}`);
    }

    function frontendReadinessResponse(params) {
      if (!params || typeof params !== 'object' || Array.isArray(params)) {
        throw new Error('wails frontend readiness: request must contain one JSON object');
      }
      const unknownField = Object.keys(params).find((field) => field !== 'phase' && field !== 'epoch');
      if (unknownField) throw new Error(`wails frontend readiness: decode request: json: unknown field "${unknownField}"`);

      const phase = typeof params.phase === 'string' ? params.phase : '';
      if (!phase) throw new Error('wails frontend readiness: phase is required');
      const hasEpoch = Object.prototype.hasOwnProperty.call(params, 'epoch');
      if (phase === 'probe') {
        if (hasEpoch) throw new Error('wails frontend readiness: probe must not include an epoch');
        return { epoch: state.frontendReadiness.epoch };
      }
      if (phase === 'commit') {
        if (!hasEpoch || !Number.isSafeInteger(params.epoch) || params.epoch <= 0) {
          throw new Error('wails frontend readiness: commit epoch is required');
        }
        if (params.epoch !== state.frontendReadiness.epoch) {
          throw new Error('wails frontend readiness: epoch does not match current activation');
        }
        state.frontendReadiness.committedEpoch = params.epoch;
        return { epoch: state.frontendReadiness.epoch };
      }
      throw new Error('wails frontend readiness: phase must be probe or commit');
    }

    function frontendTraceIngestResponse(params = {}) {
      if (!params || typeof params !== 'object' || Array.isArray(params) || !Array.isArray(params.events)) {
        throw new Error('wails frontend trace ingest: events must be an array');
      }
      return { enabled: true, recorded: params.events.length, dropped: 0 };
    }

    function preferenceFor(params = {}) {
      const key = String(params.key || '');
      if (key.includes('provider.active')) return 'codex';
      if (key.includes('.effort')) return 'medium';
      if (key.includes('codexModelProvider')) return 'openai';
      if (key.includes('codexHome')) return '~/.codex';
      if (key.includes('codexInstanceKey')) return 'default';
      if (key.includes('.sandbox')) return 'workspace-write';
      if (key.includes('.approvalPolicy')) return 'on-failure';
      if (key.includes('.personality')) return 'friendly';
      if (key.includes('.summary')) return 'auto';
      return '';
    }

    function sidebarSnapshot() {
      return {
        threads: [],
        agents: [],
        recent_turns: [],
        workspace: { runs: [] },
        token_usage: {
          inputTokens: 0,
          outputTokens: 0,
          totalTokens: 0,
          usedTokens: 0,
          contextWindowTokens: 0,
          usedPercent: 0,
        },
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
          dir: '/repo/app/.agents/skills/backend',
          description: '业务链路测试技能夹具',
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
          projectRoot: '/repo/app',
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
  });
}
