import { describe, expect, it } from 'vitest';

import {
  createMCPFrameReader,
  encodeMCPFrame,
  MCPFrameError,
  parseMCPFrame,
} from './ui-test-mcp-framing.mjs';
import {
  browserLaunchOptions,
  createToolDefinitions,
  createUITestMCPServer,
  validateBaseURL,
} from './ui-test-mcp-server.mjs';

const CONTRACT = Object.freeze({
  UI_TEST_TOOLS: ['ui_snapshot', 'ui_action', 'ui_diagnostics', 'ui_frontend_logs'],
  UI_TEST_ACTIONS: ['navigate', 'fill_composer', 'submit_composer', 'wait_for'],
  UI_TEST_TARGETS: ['composer_input', 'composer_submit'],
  UI_TEST_ROUTES: { chat: '/', settings: '/settings', observability: '/observability' },
  UI_TEST_WAIT_STATES: ['frontend_ready', 'composer_text_length', 'route'],
  UI_TEST_LIMITS: {
    defaultLimit: 100,
    maxLimit: 100,
    maxTextLength: 4000,
    maxStringLength: 500,
    maxFieldDepth: 4,
    maxFieldCount: 50,
    defaultTimeoutMs: 5000,
    maxTimeoutMs: 30000,
    pollIntervalMs: 1,
    maxFrameBytes: 1024,
    maxHeaderBytes: 128,
    maxLineBytes: 1024,
  },
  assertKnownToolName(name) {
    if (!this.UI_TEST_TOOLS.includes(name)) throw new Error(`unknown tool: ${name}`);
  },
  assertKnownActionName(name) {
    if (!this.UI_TEST_ACTIONS.includes(name)) throw new Error(`unknown action: ${name}`);
  },
  assertKnownTargetName(name) {
    if (!this.UI_TEST_TARGETS.includes(name)) throw new Error(`unknown target: ${name}`);
  },
  normalizeLimit(limit) {
    if (limit == null) return this.UI_TEST_LIMITS.defaultLimit;
    if (!Number.isSafeInteger(limit) || limit < 1 || limit > this.UI_TEST_LIMITS.maxLimit) {
      throw new Error('invalid limit');
    }
    return limit;
  },
  normalizeTimeoutMs(timeoutMs) {
    if (timeoutMs == null) return this.UI_TEST_LIMITS.defaultTimeoutMs;
    if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > this.UI_TEST_LIMITS.maxTimeoutMs) {
      throw new Error('invalid timeoutMs');
    }
    return timeoutMs;
  },
  validateExactKeys(value, allowedKeys, label) {
    const extra = Object.keys(value).filter((key) => !allowedKeys.includes(key));
    if (extra.length > 0) throw new Error(`${label} contains unknown field: ${extra[0]}`);
  },
});

describe('MCP framing', () => {
  it('round trips NDJSON and Content-Length frames while preserving mode', () => {
    const message = { jsonrpc: '2.0', id: 1, result: { ok: true } };

    const ndjson = parseMCPFrame(encodeMCPFrame(message, 'ndjson'));
    expect(ndjson).toEqual({
      message,
      consumed: Buffer.byteLength(JSON.stringify(message)) + 1,
      mode: 'ndjson',
    });

    const framed = parseMCPFrame(encodeMCPFrame(message, 'content-length'));
    expect(framed.message).toEqual(message);
    expect(framed.mode).toBe('content-length');
    expect(encodeMCPFrame(message, framed.mode).toString('utf8')).toMatch(/^Content-Length: \d+\r\n\r\n/);
  });

  it('supports chunked frames and bounded parse errors', async () => {
    const messages = [];
    const errors = [];
    const reader = createMCPFrameReader({
      limits: CONTRACT.UI_TEST_LIMITS,
      onMessage: (message, mode) => messages.push({ message, mode }),
      onError: (error, mode) => errors.push({ error, mode }),
    });

    const frame = encodeMCPFrame({ jsonrpc: '2.0', id: 2, method: 'ping' }, 'content-length');
    await reader.push(frame.subarray(0, 8));
    await reader.push(frame.subarray(8));
    await reader.push(Buffer.from('{bad json}\n'));

    expect(messages).toEqual([
      { message: { jsonrpc: '2.0', id: 2, method: 'ping' }, mode: 'content-length' },
    ]);
    expect(errors).toHaveLength(1);
    expect(errors[0].error).toBeInstanceOf(MCPFrameError);
    expect(errors[0].mode).toBe('ndjson');

    expect(() => parseMCPFrame(Buffer.from(`${'x'.repeat(10)}\n`), { ...CONTRACT.UI_TEST_LIMITS, maxLineBytes: 4 }))
      .toThrow('maxLineBytes');
    expect(() => parseMCPFrame(Buffer.from(`Content-Length: 2048\r\n\r\n{}`), CONTRACT.UI_TEST_LIMITS))
      .toThrow('maxFrameBytes');
    expect(() => parseMCPFrame(Buffer.from(`Content-Length: 1\r\n${'x'.repeat(200)}`), CONTRACT.UI_TEST_LIMITS))
      .toThrow('maxHeaderBytes');
  });
});

describe('UI test MCP server lifecycle and protocol', () => {
  it('implements initialize, notifications, ping, shutdown, and id-bearing exit', async () => {
    const server = createServer();

    expect(await server.handleMessage(request(1, 'tools/list'))).toMatchObject({
      id: 1,
      error: { code: -32000 },
    });

    expect(await server.handleMessage(request(2, 'initialize', {
      protocolVersion: '2024-11-05',
      capabilities: {},
      clientInfo: { name: 'vitest', version: '1' },
    }))).toEqual({
      jsonrpc: '2.0',
      id: 2,
      result: {
        protocolVersion: '2024-11-05',
        capabilities: { tools: {} },
        serverInfo: { name: 'super-dolphin-ui-test-mcp', version: '0.1.0' },
      },
    });
    expect(await server.handleMessage({ jsonrpc: '2.0', method: 'notifications/initialized' })).toBeNull();
    expect(await server.handleMessage(request(3, 'ping'))).toEqual({ jsonrpc: '2.0', id: 3, result: {} });
    expect(await server.handleMessage(request(4, 'shutdown'))).toEqual({ jsonrpc: '2.0', id: 4, result: {} });

    const exitServer = createServer();
    await exitServer.handleMessage(request(1, 'initialize'));
    expect(await exitServer.handleMessage(request('exit-id', 'exit'))).toEqual({
      jsonrpc: '2.0',
      id: 'exit-id',
      result: {},
    });
    expect(exitServer.isStopped()).toBe(true);

    const notificationExitServer = createServer();
    await notificationExitServer.handleMessage(request(1, 'initialize'));
    expect(await notificationExitServer.handleMessage({ jsonrpc: '2.0', method: 'exit' })).toBeNull();
    expect(notificationExitServer.isStopped()).toBe(true);
  });

  it('applies the pinned JSON-RPC error boundaries', async () => {
    const server = createServer();

    expect(await server.handleMessage('nope')).toMatchObject({ id: null, error: { code: -32600 } });
    expect(await server.handleMessage({ jsonrpc: '1.0', id: 'keep', method: 'ping' })).toMatchObject({
      id: 'keep',
      error: { code: -32600 },
    });
    expect(await server.handleMessage({ jsonrpc: '2.0', id: { bad: true }, method: 17 })).toMatchObject({
      id: null,
      error: { code: -32600 },
    });
    expect(await server.handleMessage(request(1, 'initialize', { extra: true }))).toMatchObject({
      id: 1,
      error: { code: -32602 },
    });

    await server.handleMessage(request(2, 'initialize'));
    expect(await server.handleMessage(request(3, 'unknown/method'))).toMatchObject({
      id: 3,
      error: { code: -32601 },
    });
    expect(await server.handleMessage(request(4, 'tools/call', {
      name: 'ui_action',
      arguments: { action: 'fill_composer', text: 'x', extra: true },
    }))).toMatchObject({
      id: 4,
      error: { code: -32602 },
    });
  });

  it('lists exact tools with strict input schemas from the contract', async () => {
    const server = createServer();
    await server.handleMessage(request(1, 'initialize'));

    const response = await server.handleMessage(request(2, 'tools/list'));
    expect(response.result.tools.map((tool) => tool.name)).toEqual(CONTRACT.UI_TEST_TOOLS);
    for (const tool of response.result.tools) {
      expect(tool.inputSchema).toMatchObject({ type: 'object', additionalProperties: false });
    }
    expect(response.result.tools.find((tool) => tool.name === 'ui_action').inputSchema.properties.action.enum)
      .toEqual(CONTRACT.UI_TEST_ACTIONS);
  });

  it('returns tool-shaped success and failure results without top-level tool execution errors', async () => {
    const fake = createFakeBrowser();
    const server = createServer({ browserFactory: async () => fake.browser });
    await server.handleMessage(request(1, 'initialize'));

    const snapshot = await server.handleMessage(request(2, 'tools/call', { name: 'ui_snapshot', arguments: {} }));
    expect(snapshot.result).toMatchObject({
      isError: false,
      structuredContent: { tool: 'ui_snapshot', snapshot: { route: '/', inputTextLength: 0 } },
    });

    const fill = await server.handleMessage(request(3, 'tools/call', {
      name: 'ui_action',
      arguments: { action: 'fill_composer', text: 'MCP UI test input' },
    }));
    expect(fill.result).toMatchObject({
      isError: false,
      structuredContent: { action: 'fill_composer', target: 'composer_input', textLength: 17 },
    });
    expect(fake.page.filledText).toBe('MCP UI test input');
    expect(fake.page.recordedLogs).toContainEqual(expect.objectContaining({
      source: 'ui_test_mcp',
      message: 'fill_composer',
    }));

    const submit = await server.handleMessage(request(4, 'tools/call', {
      name: 'ui_action',
      arguments: { action: 'submit_composer' },
    }));
    expect(submit).not.toHaveProperty('error');
    expect(submit.result).toMatchObject({
      isError: true,
      structuredContent: {
        error: {
          tool: 'ui_action',
          action: 'submit_composer',
          target: 'composer_submit',
          reason: expect.stringContaining('isolated acceptance'),
        },
      },
    });
  });

  it('gates isolated submit behind flag, token, ownership, and page verification', async () => {
    const fake = createFakeBrowser({
      snapshot: {
        route: '/',
        inputTextLength: 4,
        availableActions: [{ action: 'submit_composer', enabled: true }],
      },
    });
    const server = createServer({
      browserFactory: async () => fake.browser,
      env: {
        SUPER_DOLPHIN_UI_TEST_MCP: '1',
        SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT: '1',
        SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI: '1',
        SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TOKEN: 'token-1',
      },
    });
    await server.handleMessage(request(1, 'initialize'));

    const response = await server.handleMessage(request(2, 'tools/call', {
      name: 'ui_action',
      arguments: { action: 'submit_composer', target: 'composer_submit' },
    }));

    expect(response.result).toMatchObject({
      isError: false,
      structuredContent: { action: 'submit_composer', target: 'composer_submit', result: { submitted: true } },
    });
    expect(fake.page.initScripts).toHaveLength(1);
    expect(fake.page.submittedToken).toBe('token-1');
  });

  it('rejects unsafe base URLs and production mode without explicit enablement', () => {
    expect(() => validateBaseURL('http://example.com')).toThrow('127.0.0.1');
    expect(() => validateBaseURL('ftp://localhost')).toThrow('http or https');
    expect(() => validateBaseURL('http://user:pass@127.0.0.1:5175')).toThrow('credentials');
    expect(() => validateBaseURL('http://[::1]:5175')).not.toThrow();
    expect(() => createServer({ env: { NODE_ENV: 'production', SUPER_DOLPHIN_UI_TEST_MCP: '0' } }))
      .toThrow('SUPER_DOLPHIN_UI_TEST_MCP=1');
  });

  it('preserves request framing mode and keeps stdout protocol-only', async () => {
    const stdout = createWriter();
    const stderr = createWriter();
    const server = createServer({ stdout, stderr });

    await server.processChunk(encodeMCPFrame(request(1, 'initialize'), 'content-length'));
    expect(stdout.text()).toMatch(/^Content-Length: \d+\r\n\r\n/);
    expect(stderr.text()).toBe('');

    stdout.clear();
    await server.processChunk(encodeMCPFrame(request(2, 'ping'), 'ndjson'));
    expect(stdout.text()).toMatch(/\n$/);
    expect(JSON.parse(stdout.text())).toEqual({ jsonrpc: '2.0', id: 2, result: {} });

    stdout.clear();
    await server.processChunk(Buffer.from('{bad}\n'));
    expect(JSON.parse(stdout.text())).toMatchObject({
      id: null,
      error: { code: -32700 },
    });
  });

  it('cleans up Playwright resources on shutdown, exit, EOF, signals, and startup failure', async () => {
    const shutdownFake = createFakeBrowser();
    const shutdownServer = createServer({ browserFactory: async () => shutdownFake.browser });
    await shutdownServer.handleMessage(request(1, 'initialize'));
    await shutdownServer.handleMessage(request(2, 'tools/call', { name: 'ui_snapshot', arguments: {} }));
    await shutdownServer.handleMessage(request(3, 'shutdown'));
    expect(shutdownFake.page.closed).toBe(true);
    expect(shutdownFake.browser.closed).toBe(true);

    const eofFake = createFakeBrowser();
    const eofServer = createServer({ browserFactory: async () => eofFake.browser });
    await eofServer.handleMessage(request(1, 'initialize'));
    await eofServer.handleMessage(request(2, 'tools/call', { name: 'ui_snapshot', arguments: {} }));
    await eofServer.endInput();
    expect(eofFake.page.closed).toBe(true);
    expect(eofFake.browser.closed).toBe(true);

    const signalFake = createFakeBrowser();
    const signalServer = createServer({ browserFactory: async () => signalFake.browser });
    await signalServer.handleMessage(request(1, 'initialize'));
    await signalServer.handleMessage(request(2, 'tools/call', { name: 'ui_snapshot', arguments: {} }));
    await signalServer.handleSignal('SIGTERM');
    expect(signalFake.page.closed).toBe(true);
    expect(signalFake.browser.closed).toBe(true);

    const failing = createFakeBrowser({ failNewPage: true });
    const failingServer = createServer({ browserFactory: async () => failing.browser });
    await failingServer.handleMessage(request(1, 'initialize'));
    const response = await failingServer.handleMessage(request(2, 'tools/call', { name: 'ui_snapshot', arguments: {} }));
    expect(response.result.isError).toBe(true);
    expect(failing.browser.closed).toBe(true);
  });
});

it('exports tool definitions without mutating contract order', () => {
  expect(createToolDefinitions(CONTRACT).map((tool) => tool.name)).toEqual(CONTRACT.UI_TEST_TOOLS);
  const moduleNamespaceContract = Object.assign(Object.create(null), CONTRACT);
  expect(createToolDefinitions(moduleNamespaceContract).map((tool) => tool.name)).toEqual(CONTRACT.UI_TEST_TOOLS);
});

it('uses an explicit browser executable when provided by env', () => {
  expect(browserLaunchOptions({})).toEqual({});
  expect(browserLaunchOptions({ SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE_PATH: '/tmp/chrome' })).toEqual({
    executablePath: '/tmp/chrome',
  });
  expect(browserLaunchOptions({ SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE: '/tmp/chromium' })).toEqual({
    executablePath: '/tmp/chromium',
  });
});

function createServer(options = {}) {
  return createUITestMCPServer({
    contract: CONTRACT,
    env: { SUPER_DOLPHIN_UI_TEST_MCP: '1', ...options.env },
    stdout: options.stdout || createWriter(),
    stderr: options.stderr || createWriter(),
    browserFactory: options.browserFactory || (async () => createFakeBrowser().browser),
  });
}

function request(id, method, params) {
  const message = { jsonrpc: '2.0', id, method };
  if (params !== undefined) message.params = params;
  return message;
}

function createWriter() {
  const chunks = [];
  return {
    chunks,
    write(chunk) {
      chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(String(chunk)));
      return true;
    },
    text() {
      return Buffer.concat(chunks).toString('utf8');
    },
    clear() {
      chunks.length = 0;
    },
  };
}

function createFakeBrowser(options = {}) {
  const page = {
    closed: false,
    filledText: '',
    initScripts: [],
    recordedLogs: [],
    submittedToken: null,
    snapshot: options.snapshot || {
      route: '/',
      inputTextLength: 0,
      availableActions: [],
    },
    async addInitScript(fn, arg) {
      this.initScripts.push({ fn, arg });
    },
    async goto(url) {
      this.url = url;
    },
    locator(selector) {
      if (selector !== '[data-testid="composer-input"]') throw new Error(`unexpected selector: ${selector}`);
      return {
        fill: async (text) => {
          this.filledText = text;
          this.snapshot = { ...this.snapshot, inputTextLength: text.length };
        },
      };
    },
    async evaluate(fn, arg) {
      const source = fn.toString();
      if (source.includes('.snapshot()')) return this.snapshot;
      if (source.includes('.diagnostics()')) return { consoleErrors: [], bridgeErrors: [], unhandledErrors: [] };
      if (source.includes('.frontendLogs(input)')) return [];
      if (source.includes('.recordLog(entry)')) {
        this.recordedLogs.push(arg);
        return arg;
      }
      if (source.includes('.verifyIsolatedAcceptance(input)')) {
        return { isolated: true, tokenMatched: arg.token === 'token-1' };
      }
      if (source.includes('.submitComposerInIsolation(input)')) {
        this.submittedToken = arg.token;
        return { submitted: true };
      }
      throw new Error(`unexpected evaluate: ${source}`);
    },
    async close() {
      this.closed = true;
    },
  };

  const browser = {
    closed: false,
    async newPage() {
      if (options.failNewPage) throw new Error('newPage failed');
      return page;
    },
    async close() {
      this.closed = true;
    },
  };

  return { browser, page };
}
