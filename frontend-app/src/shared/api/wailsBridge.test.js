import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cwd } from 'node:process';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { beginTextClipboardWrite, copyTextToClipboard, normalizeRuntimeEventEnvelope } from './wailsBridge.js';

const runtimeModule = '/wails/runtime.js';
const devRuntimeShimModule = '../../../public/wails/runtime.js?test-runtime-shim';

function captureBridgeLogs(registerBridgeLogStore) {
  const logs = [];
  const write = (level) => (event, fields) => logs.push({ level, event, fields });
  registerBridgeLogStore({
    debug: write('debug'),
    error: write('error'),
    info: write('info'),
    warn: write('warn'),
  });
  return logs;
}

function waitForTraceFlush() {
  return new Promise((resolve) => {
    setTimeout(resolve, 0);
  });
}

function importFreshDevRuntimeShim() {
  vi.resetModules();
  return import(devRuntimeShimModule);
}

function resetWailsRuntimeMocks() {
  vi.resetModules();
  vi.doUnmock(runtimeModule);
}

function resetFrontendTraceEmitter() {
  resetWailsRuntimeMocks();
  delete window.__AO_FRONTEND_TRACE_DEBUG__;
  delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  window.localStorage.clear();
}

describe('wails bridge runtime loading', () => {
  it('keeps the public Wails runtime out of Vite import analysis', () => {
    const source = readFileSync(join(cwd(), 'src/shared/api/wailsBridge.js'), 'utf8');

    expect(source).toContain('nativeImportModule(WAILS_RUNTIME_MODULE)');
    expect(source).toContain('return import(/* @vite-ignore */ modulePath)');
    expect(source).not.toContain("Function('modulePath'");
    expect(source).not.toContain('import(/* @vite-ignore */ WAILS_RUNTIME_MODULE)');
  });
});

describe('wails bridge runtime event JSON parsing', () => {
  it('preserves large integer-looking text inside JSON strings', () => {
    expect(normalizeRuntimeEventEnvelope({
      name: 'runtime',
      data: '{"payload":{"message":"keep : 1234567890123456 inside string"}}',
    }).payload.message).toBe('keep : 1234567890123456 inside string');
  });

  it('converts unsafe runtime event object integers to strings', () => {
    expect(normalizeRuntimeEventEnvelope({
      name: 'runtime',
      data: '{"payload":{"requestId":9007199254740993}}',
    }).payload.requestId).toBe('9007199254740993');
  });

  it('converts unsafe runtime event array integers to strings', () => {
    expect(normalizeRuntimeEventEnvelope({
      name: 'runtime',
      data: '{"payload":{"ids":[9007199254740993]}}',
    }).payload.ids).toEqual(['9007199254740993']);
  });
});

function createTestWebSocketClass(sockets) {
  return class TestWebSocket {
    static CONNECTING = 0;

    static OPEN = 1;

    constructor(url) {
      this.url = url;
      this.readyState = TestWebSocket.CONNECTING;
      this.sent = [];
      sockets.push(this);
    }

    send(data) {
      this.sent.push(data);
    }

    open() {
      this.readyState = TestWebSocket.OPEN;
      this.onopen?.();
    }

    close(event = { code: 1006, reason: 'network lost' }) {
      this.readyState = 3;
      this.onclose?.(event);
    }

    emit(method, params) {
      this.onmessage?.({
        data: JSON.stringify({ jsonrpc: '2.0', method, params }),
      });
    }

    receive(message) {
      this.onmessage?.({
        data: JSON.stringify(message),
      });
    }
  };
}

describe('wails bridge clipboard helpers', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__WAILS_SHIM_DEBUG__;
    delete document.execCommand;
  });

  it('starts a clipboard write synchronously and commits text after async data is ready', async () => {
    let copiedText = '';
    class TestClipboardItem {
      constructor(items) {
        this.items = items;
      }

      getType(type) {
        return this.items[type];
      }
    }
    class TestBlob {
      constructor(parts, options = {}) {
        this.parts = parts;
        this.type = options.type || '';
      }
    }
    const write = vi.fn(async ([item]) => {
      const blob = await item.getType('text/plain');
      copiedText = blob.parts.join('');
    });
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { write },
    });
    vi.stubGlobal('ClipboardItem', TestClipboardItem);
    vi.stubGlobal('Blob', TestBlob);

    const prepared = beginTextClipboardWrite();

    expect(write).toHaveBeenCalledTimes(1);
    await expect(prepared.commit('thread info')).resolves.toBe(true);
    expect(copiedText).toBe('thread info');
  });

  it('surfaces prepared clipboard write failures when committing text', async () => {
    class TestClipboardItem {
      constructor(items) {
        this.items = items;
      }

      getType(type) {
        return this.items[type];
      }
    }
    class TestBlob {
      constructor(parts, options = {}) {
        this.parts = parts;
        this.type = options.type || '';
      }
    }
    const write = vi.fn(async ([item]) => {
      await item.getType('text/plain');
      throw new Error('clipboard write rejected');
    });
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { write },
    });
    vi.stubGlobal('ClipboardItem', TestClipboardItem);
    vi.stubGlobal('Blob', TestBlob);

    const prepared = beginTextClipboardWrite();

    expect(write).toHaveBeenCalledTimes(1);
    await expect(prepared.commit('thread info')).rejects.toThrow('clipboard write rejected');
  });

  it('falls back to a focused textarea copy when async clipboard is unavailable', async () => {
    window.__WAILS_SHIM_DEBUG__ = true;
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    });
    document.execCommand = vi.fn(() => true);

    await expect(copyTextToClipboard('fallback text')).resolves.toBe(true);

    expect(document.execCommand).toHaveBeenCalledWith('copy');
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('throws concrete clipboard failures instead of returning false', async () => {
    window.__WAILS_SHIM_DEBUG__ = true;
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: vi.fn().mockRejectedValue(new Error('The request is not allowed')),
      },
    });
    document.execCommand = vi.fn(() => false);

    await expect(copyTextToClipboard('failfast text')).rejects.toThrow(
      "clipboard copy failed: browser clipboard.writeText failed: The request is not allowed; document.execCommand fallback failed: document.execCommand('copy') returned false",
    );
  });
});

describe('wails bridge shared file helpers', () => {
  beforeEach(resetWailsRuntimeMocks);

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('opens shared files through the native sharedFile open RPC with a trimmed path', async () => {
    const byID = vi.fn().mockResolvedValue({ opened: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { openSharedFile } = await import('./wailsBridge.js');

    await expect(openSharedFile({ path: ' dag/video/final.mp4 ' })).resolves.toEqual({ opened: true });

    expect(byID).toHaveBeenCalledWith(expect.any(Number), 'ui/sharedFile/open', expect.objectContaining({
      path: 'dag/video/final.mp4',
    }));
    await expect(openSharedFile({ path: ' ' })).rejects.toThrow('openSharedFile path is required');
  });

  it('requests tokenized shared file previews through the native sharedFile RPC', async () => {
    const byID = vi.fn().mockResolvedValue({
      url: 'http://127.0.0.1:4511/shared-file-preview?id=sf_123',
      path: 'dag/video/final.mp4',
      contentType: 'video/mp4',
      sizeBytes: 24,
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { previewSharedFile } = await import('./wailsBridge.js');

    await expect(previewSharedFile({ path: ' dag/video/final.mp4 ' })).resolves.toEqual({
      url: 'http://127.0.0.1:4511/shared-file-preview?id=sf_123',
      path: 'dag/video/final.mp4',
      contentType: 'video/mp4',
      sizeBytes: 24,
    });

    expect(byID).toHaveBeenCalledWith(expect.any(Number), 'ui/sharedFile/open', expect.objectContaining({
      path: 'dag/video/final.mp4',
      preview: true,
    }));
    await expect(previewSharedFile({ path: '' })).rejects.toThrow('previewSharedFile path is required');
  });

  it('rejects malformed native shared file responses', async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method !== 'ui/sharedFile/open') {
        throw new Error(`unexpected method ${method}`);
      }
      return Promise.resolve(payload.preview ? { url: '' } : {});
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { openSharedFile, previewSharedFile } = await import('./wailsBridge.js');

    await expect(openSharedFile({ path: 'dag/video/final.mp4' }))
      .rejects.toThrow('ui/sharedFile/open response opened must be true');
    await expect(previewSharedFile({ path: 'dag/video/final.mp4' }))
      .rejects.toThrow('ui/sharedFile/open response url must be a non-empty string');
  });
});

describe('wails bridge warning logs', () => {
  beforeEach(resetWailsRuntimeMocks);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it('fails frontend log batch delivery when the runtime binding is unavailable', async () => {
    vi.doMock(runtimeModule, () => ({
      Call: {},
      Events: { On: vi.fn() },
    }));
    const { sendFrontendLogBatch } = await import('./wailsBridge.js');

    await expect(sendFrontendLogBatch([{ level: 'error', event: 'ui.failed' }])).rejects.toThrow(
      'frontend log bridge runtime Call.ByID is required',
    );
  });

  it('propagates frontend log batch RPC failures', async () => {
    const error = new Error('log ingest unavailable');
    const byID = vi.fn().mockRejectedValue(error);
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { sendFrontendLogBatch } = await import('./wailsBridge.js');

    await expect(sendFrontendLogBatch([{ level: 'error', event: 'ui.failed' }])).rejects.toThrow(
      'log ingest unavailable',
    );
    expect(byID).toHaveBeenCalledWith(expect.any(Number), 'ui/log', expect.objectContaining({
      entries: [{ level: 'error', event: 'ui.failed' }],
    }));
  });

  it('reports a failed backend RPC once as api.rpc.failed', async () => {
    const error = new Error('backend unavailable');
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).rejects.toThrow('backend unavailable');

    const errorEvents = logs.filter((entry) => entry.level === 'error').map((entry) => entry.event);
    expect(errorEvents).toEqual(['api.rpc.failed']);
    expect(errorEvents).not.toContain('bridge.call.failed');
  });

  it('records failed backend RPC details with a serializable error message', async () => {
    const error = new Error('backend unavailable');
    error.code = 'ECONNREFUSED';
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).rejects.toThrow('backend unavailable');

    const failure = logs.find((entry) => entry.event === 'api.rpc.failed');
    expect(failure.fields).toEqual(expect.objectContaining({
      method: 'thread/config/get',
      error: expect.objectContaining({
        message: 'backend unavailable',
        code: 'ECONNREFUSED',
      }),
    }));
    expect(JSON.stringify(failure.fields)).toContain('backend unavailable');
  });

  it('filters sensitive JSON-RPC error data from api.rpc.failed UI logs', async () => {
    const error = new Error('backend rejected request');
    error.code = -32000;
    error.data = {
      code: 'RPC_REJECTED',
      message: 'safe backend diagnostic',
      name: 'JsonRpcError',
      type: 'validation',
      status: 400,
      prompt: 'real-prompt-secret',
      params: { userPrompt: 'real-params-secret' },
      stack: 'real-stack-secret',
      secret: 'real-secret',
      token: 'real-token',
      password: 'real-password',
      apiKey: 'real-api-key',
      api_key: 'real-api-key-snake',
      auth: 'real-auth',
      credential: 'real-credential',
      authorization: 'Bearer real-authorization',
      authToken: 'real-auth-token',
      nested: {
        code: 'NESTED_CODE',
        message: 'nested diagnostic',
        content: 'real-content-secret',
        token: 'real-nested-token',
      },
    };
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/start', { prompt: 'user prompt' })).rejects.toThrow('backend rejected request');

    const failure = logs.find((entry) => entry.event === 'api.rpc.failed');
    expect(failure.fields.error).toEqual(expect.objectContaining({
      message: 'backend rejected request',
      code: -32000,
      data: expect.objectContaining({
        code: '[redacted]',
        message: '[redacted]',
        name: '[redacted]',
        type: '[redacted]',
        status: 400,
      }),
    }));
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain('real-');
    expect(serialized).not.toContain('RPC_REJECTED');
    expect(serialized).not.toContain('safe backend diagnostic');
    expect(serialized).not.toContain('JsonRpcError');
    expect(serialized).not.toContain('validation');
    expect(serialized).not.toContain('nested diagnostic');
    expect(serialized).not.toContain('"prompt"');
    expect(serialized).not.toContain('"params"');
    expect(serialized).not.toContain('"stack"');
    expect(serialized).not.toContain('"content"');
    expect(serialized).not.toContain('"secret"');
    expect(serialized).not.toContain('"token"');
    expect(serialized).not.toContain('"password"');
    expect(serialized).not.toContain('"apiKey"');
    expect(serialized).not.toContain('"api_key"');
    expect(serialized).not.toContain('"auth"');
    expect(serialized).not.toContain('"credential"');
    expect(serialized).not.toContain('"authorization"');
    expect(serialized).not.toContain('"authToken"');
  });

  it('redacts free-text JSON-RPC error data strings from api.rpc.failed UI logs', async () => {
    const error = new Error('backend rejected');
    error.code = -32000;
    error.data = {
      message: 'token=real-token password=real-password',
      code: 'token=real-code-token',
      status: 400,
    };
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/start', { prompt: 'user prompt' })).rejects.toThrow('backend rejected');

    const failure = logs.find((entry) => entry.event === 'api.rpc.failed');
    expect(failure.fields.error).toEqual(expect.objectContaining({
      message: 'backend rejected',
      code: -32000,
      data: {
        code: '[redacted]',
        message: '[redacted]',
        status: 400,
      },
    }));
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain('real-token');
    expect(serialized).not.toContain('real-password');
    expect(serialized).not.toContain('real-code-token');
    expect(serialized).not.toContain('token=');
    expect(serialized).not.toContain('password=');
  });

  it('redacts JSON-RPC error data strings when runtime rejects with a plain object', async () => {
    const error = {
      message: 'backend object rejected',
      code: -32002,
      data: {
        message: 'token=real-object-token password=real-object-password',
        code: 'token=real-object-code-token',
        status: 401,
        authorization: 'Bearer real-object-authorization',
      },
    };
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/start', { prompt: 'user prompt' })).rejects.toMatchObject({
      message: 'backend object rejected',
      code: -32002,
    });

    const failure = logs.find((entry) => entry.event === 'api.rpc.failed');
    expect(failure.fields.error).toEqual(expect.objectContaining({
      message: 'backend object rejected',
      code: -32002,
      data: {
        message: '[redacted]',
        code: '[redacted]',
        status: 401,
      },
    }));
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain('real-object-token');
    expect(serialized).not.toContain('real-object-password');
    expect(serialized).not.toContain('real-object-code-token');
    expect(serialized).not.toContain('real-object-authorization');
    expect(serialized).not.toContain('token=');
    expect(serialized).not.toContain('password=');
    expect(serialized).not.toContain('authorization');
  });

  it('redacts primitive JSON-RPC error data from dev runtime UI logs while keeping diagnostics', async () => {
    const sockets = [];
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));
    vi.doMock(runtimeModule, async () => import(devRuntimeShimModule));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    const resultPromise = callAPI('thread/config/get', { threadId: 'thread-1' });
    await waitFor(() => {
      expect(sockets).toHaveLength(1);
    });
    sockets[0].open();
    await waitFor(() => {
      expect(sockets[0].sent.some((sent) => JSON.parse(sent).method === 'thread/config/get')).toBe(true);
    });
    const request = sockets[0].sent.map((sent) => JSON.parse(sent))
      .find((message) => message.method === 'thread/config/get');
    sockets[0].receive({
      jsonrpc: '2.0',
      id: request.id,
      error: {
        code: -32001,
        message: 'backend rejected primitive data',
        data: 'real-token',
      },
    });

    await expect(resultPromise).rejects.toThrow('backend rejected primitive data');

    const failure = logs.find((entry) => entry.event === 'api.rpc.failed');
    expect(failure.fields.error).toEqual(expect.objectContaining({
      message: 'backend rejected primitive data',
      code: -32001,
      data: '[redacted]',
    }));
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain('real-token');
  });

  it('does not write successful RPC lifecycle logs to the UI store by default', async () => {
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockResolvedValue({ ok: true, tool: 'mcp__lsp__grep', result: { total: 3 } }) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('tools/call', { name: 'mcp__lsp__grep' })).resolves.toEqual({
      ok: true,
      tool: 'mcp__lsp__grep',
      result: { total: 3 },
    });

    const events = logs.map((entry) => entry.event);
    expect(events).not.toContain('api.rpc.start');
    expect(events).not.toContain('api.rpc.done');
    expect(events).not.toContain('bridge.call.start');
    expect(events).not.toContain('bridge.call.done');
  });

  it('keeps compact successful RPC diagnostics when frontend trace debug is enabled', async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockResolvedValue({ ok: true, tool: 'mcp__lsp__grep', result: { total: 3 } }) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('tools/call', { name: 'mcp__lsp__grep' })).resolves.toEqual({
      ok: true,
      tool: 'mcp__lsp__grep',
      result: { total: 3 },
    });

    const done = logs.find((entry) => entry.event === 'api.rpc.done');
    expect(done.fields).toEqual(expect.objectContaining({
      method: 'tools/call',
      result_preview: expect.stringContaining('"total":3'),
    }));
  });

  it('redacts sensitive successful RPC diagnostic previews before they reach the UI log store', async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    vi.doMock(runtimeModule, () => ({
      Call: {
        ByID: vi.fn().mockResolvedValue({
          ok: true,
          tool: 'mcp__secret__read',
          result: {
            total: 3,
            prompt: 'real-prompt-secret',
            content: 'real-content-secret',
            text: 'real-text-secret',
            body: 'token=real-body-token',
            profile: { name: 'real-profile-secret' },
            cwd: '/home/l4place/private-project',
            path: '/home/l4place/private-project/secret.txt',
            paths: ['/home/l4place/private-project/secret-a.txt'],
            nested: {
              count: 2,
              accessToken: 'real-access-token',
              message: 'real-message-secret',
            },
          },
        }),
      },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await callAPI('tools/call', { name: 'mcp__secret__read' });

    const done = logs.find((entry) => entry.event === 'api.rpc.done');
    expect(done.fields.result_preview).toContain('"total":3');
    expect(done.fields.result_preview).toContain('"count":2');
    expect(done.fields.result_preview).not.toContain('real-');
    expect(done.fields.result_preview).not.toContain('/home/l4place');
    expect(done.fields.result_preview).not.toContain('"prompt"');
    expect(done.fields.result_preview).not.toContain('"content"');
    expect(done.fields.result_preview).not.toContain('"body"');
    expect(done.fields.result_preview).not.toContain('"cwd"');
    expect(done.fields.result_preview).not.toContain('"path"');
    expect(done.fields.result_preview).not.toContain('"paths"');
    expect(done.fields.result_preview).not.toContain('"accessToken"');
  });
});

describe('wails bridge RPC trace log fields', () => {
  beforeEach(resetWailsRuntimeMocks);

  afterEach(() => {
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
  });

  it('injects W3C trace metadata into backend RPC payloads', async () => {
    const byID = vi.fn().mockResolvedValue({ ok: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).resolves.toEqual({ ok: true });

    const payload = byID.mock.calls[0][2];
    expect(payload).toEqual(expect.objectContaining({
      threadId: 'thread-1',
      _aoTraceparent: expect.stringMatching(/^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/),
      _aoTraceId: expect.stringMatching(/^[0-9a-f]{32}$/),
      _aoSpanId: expect.stringMatching(/^[0-9a-f]{16}$/),
    }));
    const [, traceId, spanId] = payload._aoTraceparent.match(/^00-([0-9a-f]{32})-([0-9a-f]{16})-01$/);
    expect(payload._aoTraceId).toBe(traceId);
    expect(payload._aoSpanId).toBe(spanId);
  });

  it('records trace identifiers in debug success logs and backend RPC failure logs', async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    let appRPCCount = 0;
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') {
        return Promise.resolve({ recorded: payload.events.length });
      }
      appRPCCount += 1;
      if (appRPCCount === 1) return Promise.resolve({ ok: true });
      return Promise.reject(new Error('backend unavailable'));
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('tools/call', { name: 'mcp__lsp__grep' })).resolves.toEqual({ ok: true });
    const appCalls = () => byID.mock.calls.filter(([, method]) => method !== 'observability/frontend/ingest');
    const successPayload = appCalls()[0][2];
    const successStart = logs.find((entry) => entry.event === 'api.rpc.start' && entry.fields.method === 'tools/call');
    const successDone = logs.find((entry) => entry.event === 'api.rpc.done' && entry.fields.method === 'tools/call');
    expect(successStart.fields).toEqual(expect.objectContaining({
      trace_id: successPayload._aoTraceId,
      span_id: successPayload._aoSpanId,
    }));
    expect(successDone.fields).toEqual(expect.objectContaining({
      trace_id: successPayload._aoTraceId,
      span_id: successPayload._aoSpanId,
    }));

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).rejects.toThrow('backend unavailable');
    const failedPayload = appCalls()[1][2];
    const failed = logs.find((entry) => entry.event === 'api.rpc.failed' && entry.fields.method === 'thread/config/get');
    expect(failed.fields).toEqual(expect.objectContaining({
      trace_id: failedPayload._aoTraceId,
      span_id: failedPayload._aoSpanId,
    }));
  });
});

describe('wails bridge file picker helpers', () => {
  beforeEach(resetWailsRuntimeMocks);

  it('passes file filters through the ui/selectFiles RPC path', async () => {
    const byID = vi.fn((methodID, method, payload) => {
      if (methodID !== 1391035622 || method !== 'ui/selectFiles') {
        throw new Error('filtered picker must use parameterized RPC path');
      }
      if (payload.filters?.[0]?.pattern !== '*.pdf;*.txt;*.text') {
        throw new Error('missing datasource filter pattern');
      }
      return Promise.resolve({ paths: ['C:\\data\\manual.pdf'] });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectFiles } = await import('./wailsBridge.js');

    await expect(selectFiles({
      filters: [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }],
    })).resolves.toEqual(['C:\\data\\manual.pdf']);
    expect(byID).toHaveBeenCalledWith(1391035622, 'ui/selectFiles', expect.objectContaining({
      filters: [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }],
    }));
  });

  it('uses a dedicated datasource import picker response with a token', async () => {
    const byID = vi.fn((methodID, method, payload) => {
      if (methodID !== 1391035622 || method !== 'ui/selectDatasourceImportFile') {
        throw new Error('datasource import picker must use its dedicated RPC path');
      }
      if (payload.filters?.[0]?.pattern !== '*.pdf;*.txt;*.text') {
        throw new Error('missing datasource filter pattern');
      }
      return Promise.resolve({ sourcePath: 'C:\\\\data\\\\manual.pdf', pickerToken: 'picker-token' });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectDatasourceImportFile } = await import('./wailsBridge.js');

    await expect(selectDatasourceImportFile({
      filters: [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }],
    })).resolves.toEqual({ sourcePath: 'C:\\\\data\\\\manual.pdf', pickerToken: 'picker-token' });
    expect(byID).toHaveBeenCalledWith(1391035622, 'ui/selectDatasourceImportFile', expect.objectContaining({
      filters: [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }],
    }));
  });

  it('parses native file helper responses only from explicit response shapes', async () => {
    const byID = vi.fn((_methodID, method) => {
      if (method === 'ui/selectProjectDirs') return Promise.resolve({ paths: ['/repo/a'] });
      if (method === 'ui/saveTextFile') return Promise.resolve({ path: '/tmp/out.txt' });
      if (method === 'ui/readDroppedTextFiles') {
        return Promise.resolve({
          files: [{
            path: '/tmp/a.txt',
            name: 'a.txt',
            text: 'hello',
            sizeBytes: 5,
          }],
        });
      }
      throw new Error(`unexpected method ${method}`);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectProjectDirs, saveTextFile, readDroppedTextFiles } = await import('./wailsBridge.js');

    await expect(selectProjectDirs()).resolves.toEqual(['/repo/a']);
    await expect(saveTextFile({ defaultFilename: 'out.txt', content: 'hello' })).resolves.toBe('/tmp/out.txt');
    await expect(readDroppedTextFiles(['/tmp/a.txt'], 'drop-1')).resolves.toEqual([{
      path: '/tmp/a.txt',
      name: 'a.txt',
      text: 'hello',
      sizeBytes: 5,
    }]);
  });

  it('rejects malformed native file helper responses instead of defaulting to empty values', async () => {
    const byID = vi.fn((_methodID, method) => {
      if (method === 'ui/selectProjectDirs') return Promise.resolve({});
      if (method === 'ui/saveTextFile') return Promise.resolve({});
      if (method === 'ui/readDroppedTextFiles') return Promise.resolve({ files: [{ path: '/tmp/a.txt', name: 'a.txt', text: 'hello' }] });
      throw new Error(`unexpected method ${method}`);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectProjectDirs, saveTextFile, readDroppedTextFiles } = await import('./wailsBridge.js');

    await expect(selectProjectDirs()).rejects.toThrow('ui/selectProjectDirs response paths must be an array');
    await expect(saveTextFile({ defaultFilename: 'out.txt', content: 'hello' }))
      .rejects.toThrow('ui/saveTextFile response path must be a string');
    await expect(readDroppedTextFiles(['/tmp/a.txt'], 'drop-1'))
      .rejects.toThrow('ui/readDroppedTextFiles response file sizeBytes must be a non-negative number');
  });

  it('rejects missing clipboard image paths instead of defaulting to an empty payload', async () => {
    const byID = vi.fn().mockResolvedValue(undefined);
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { saveClipboardImage } = await import('./wailsBridge.js');

    await expect(saveClipboardImage('base64-image')).rejects.toThrow('ui/saveClipboardImage response path must be a string');
  });

  it('rejects malformed selectFiles native responses without falling back to the RPC path', async () => {
    const byID = vi.fn().mockResolvedValue({});
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectFiles } = await import('./wailsBridge.js');

    await expect(selectFiles()).rejects.toThrow('ui/selectFiles response paths must be an array');
    expect(byID).toHaveBeenCalledTimes(1);
  });

  it('rejects malformed datasource import picker responses without defaulting a token', async () => {
    const byID = vi.fn().mockResolvedValue({ sourcePath: 'C:\\\\data\\\\manual.pdf' });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectDatasourceImportFile } = await import('./wailsBridge.js');

    await expect(selectDatasourceImportFile())
      .rejects.toThrow('ui/selectDatasourceImportFile response pickerToken must be a non-empty string');
  });
});

describe('wails bridge non-RPC binding logs', () => {
  beforeEach(resetWailsRuntimeMocks);

  it('keeps bridge.call.failed for non-RPC bridge binding failures', async () => {
    const error = new Error('native binding unavailable');
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { getBuildInfo, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(getBuildInfo()).rejects.toThrow('native binding unavailable');

    const errorEvents = logs.filter((entry) => entry.level === 'error').map((entry) => entry.event);
    expect(errorEvents).toEqual(['bridge.call.failed']);
  });
});

describe('wails bridge event callbacks', () => {
  beforeEach(resetWailsRuntimeMocks);

  it('returns a ready promise and retries when the first runtime event binding is unavailable', async () => {
    let importCount = 0;
    const on = vi.fn(() => () => {});
    vi.doMock(runtimeModule, () => {
      importCount += 1;
      if (importCount === 1) {
        throw new Error('runtime not loaded yet');
      }
      return {
        Call: { ByID: vi.fn() },
        Events: { On: on },
      };
    });
    const { onBridgeEvent, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    const first = onBridgeEvent(vi.fn());

    expect(typeof first).toBe('object');
    expect(first).toEqual({
      ready: expect.any(Promise),
      unsubscribe: expect.any(Function),
    });
    await expect(first.ready).resolves.toBe(false);
    expect(logs.find((entry) => entry.event === 'bridge.subscribe.unavailable')).toEqual(
      expect.objectContaining({ level: 'warn' }),
    );

    const second = onBridgeEvent(vi.fn());

    expect(typeof second).toBe('object');
    expect(second).toEqual({
      ready: expect.any(Promise),
      unsubscribe: expect.any(Function),
    });
    await expect(second.ready).resolves.toBe(true);
    expect(on).toHaveBeenCalledWith('bridge-event', expect.any(Function));
    second.unsubscribe();
  });

  it('rethrows bridge callback errors when escalation is requested', async () => {
    let eventCallback = null;
    const on = vi.fn((_eventName, callback) => {
      eventCallback = callback;
      return () => {};
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn() },
      Events: { On: on },
    }));
    const { onBridgeEvent, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    onBridgeEvent(() => {
      throw new Error('dag status event run identity is required');
    }, { escalateCallbackError: true });

    await waitFor(() => expect(on).toHaveBeenCalledWith('bridge-event', expect.any(Function)));
    expect(() => eventCallback({
      name: 'bridge-event',
      data: {
        method: 'task/node/statuschanged',
        payload: { dag_key: 'flow-a', node_key: 'step', new_status: 'running' },
      },
    })).toThrow('dag status event run identity is required');
    expect(logs.find((entry) => entry.event === 'bridge.callback.failed')).toEqual(
      expect.objectContaining({
        level: 'error',
        fields: expect.objectContaining({
          error: expect.objectContaining({ message: 'dag status event run identity is required' }),
        }),
      }),
    );
  });

  it('emits an explicit parse failure event for malformed bridge event JSON', async () => {
    let eventCallback = null;
    const on = vi.fn((_eventName, callback) => {
      eventCallback = callback;
      return () => {};
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn() },
      Events: { On: on },
    }));
    const { onBridgeEvent, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);
    const callback = vi.fn();

    onBridgeEvent(callback);

    await waitFor(() => expect(on).toHaveBeenCalledWith('bridge-event', expect.any(Function)));
    eventCallback({
      name: 'bridge-event',
      data: '{"method":',
    });

    expect(callback).toHaveBeenCalledWith(expect.objectContaining({
      method: 'bridge.event.parse_failed',
      payload: expect.objectContaining({
        eventName: 'bridge-event',
        rawLen: 10,
      }),
    }));
    expect(callback.mock.calls[0][0].payload).not.toHaveProperty('rawPreview');
    expect(logs.find((entry) => entry.event === 'bridge.event.parse_failed')).toEqual(
      expect.objectContaining({ level: 'error' }),
    );
  });
});

describe('wails bridge frontend trace emitter', () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it('flushes failed RPC traces through observability frontend ingest without sensitive payload fields', async () => {
    const backendError = new Error('backend unavailable');
    backendError.code = 'E_BACKEND';
    backendError.stack = 'raw stack with file contents';
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.reject(backendError);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    let thrownError;
    await expect(callAPI('thread/start', {
      prompt: 'do not persist this prompt',
      result_preview: 'do not persist preview',
    }).catch((error) => {
      thrownError = error;
      throw error;
    })).rejects.toThrow('backend unavailable');

    const rpcPayload = byID.mock.calls[0][2];
    expect(thrownError).toEqual(expect.objectContaining({
      traceId: rpcPayload._aoTraceId,
      trace_id: rpcPayload._aoTraceId,
      spanId: rpcPayload._aoSpanId,
      span_id: rpcPayload._aoSpanId,
      req_id: rpcPayload._aoRequestId,
      method: 'thread/start',
    }));
    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    const events = ingestCall[2].events;
    expect(events).toHaveLength(1);
    expect(events[0]).toEqual(expect.objectContaining({
      phase: 'frontend.rpc.failed',
      method: 'thread/start',
      status: 'error',
      error: 'E_BACKEND: backend unavailable',
    }));
    expect(events[0].trace_id).toBe(rpcPayload._aoTraceId);
    expect(events[0].span_id).toBe(rpcPayload._aoSpanId);
    expect(events[0]).not.toHaveProperty('error_name');
    expect(events[0]).not.toHaveProperty('error_code');
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain('result_preview');
    expect(serialized).not.toContain('do not persist');
    expect(serialized).not.toContain('raw stack');
    expect(serialized).not.toContain('prompt');
  });

  it('drops credential values and local paths from failed RPC trace errors', async () => {
    const backendError = new Error(
      'open /home/l4place/project/.env failed token=sk-live-secret password=hunter2 api_key=abc123',
    );
    backendError.code = 'E_SECRET';
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.reject(backendError);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    await expect(callAPI('thread/start', { prompt: 'safe prompt payload should still be stripped' }))
      .rejects.toThrow('/home/l4place/project/.env failed');

    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    expect(ingestCall[2].events[0]).toEqual(expect.objectContaining({
      phase: 'frontend.rpc.failed',
      method: 'thread/start',
      status: 'error',
      error: 'E_SECRET',
    }));
    const serialized = JSON.stringify(ingestCall[2].events);
    expect(serialized).not.toContain('/home/l4place');
    expect(serialized).not.toContain('.env');
    expect(serialized).not.toContain('sk-live-secret');
    expect(serialized).not.toContain('hunter2');
    expect(serialized).not.toContain('abc123');
    expect(serialized).not.toContain('token=');
    expect(serialized).not.toContain('password=');
    expect(serialized).not.toContain('api_key=');
  });

  it('flushes failed frontend warning traces through observability ingest', async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { emitFrontendTraceEvent } = await import('./wailsBridge.js');

    expect(emitFrontendTraceEvent({
      phase: 'frontend.warning',
      method: 'memory.badge.refresh.failed',
      trace_id: 'trace-memory-1',
      span_id: 'span-memory-1',
      thread_id: 'thread-1',
      status: 'error',
      error: '记忆中心加载超时，请检查记忆数据或后端状态。',
      metadata: { component: 'memory', req_id: 17, prompt: 'secret prompt must not leak' },
    })).toBe(true);
    expect(emitFrontendTraceEvent({
      phase: 'frontend.warning',
      method: 'memory.raw.failed',
      trace_id: 'trace-memory-2',
      span_id: 'span-memory-2',
      status: 'error',
      error: 'prompt secret must not leak',
    })).toBe(true);
    expect(emitFrontendTraceEvent({
      phase: 'frontend.warning',
      method: 'app.render.crash',
      client_route: 'app',
      status: 'error',
      error: 'TypeError:APPROVAL_SUBMIT_TIMEOUT',
      metadata: {
        component: 'react.root',
        react_phase: 'render',
        crash_fingerprint: 'crash-v1-1483443a51ffbe45',
        breadcrumb_trail: 'app.bootstrap:app:start',
        message: 'private crash message must not leak',
        stack: 'private crash stack must not leak',
        component_props: 'private props must not leak',
      },
    })).toBe(true);

    let events = [];
    await waitFor(() => {
      events = byID.mock.calls
        .filter(([, method]) => method === 'observability/frontend/ingest')
        .flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(3);
    });
    expect(events[0]).toEqual(expect.objectContaining({
      phase: 'frontend.warning',
      method: 'memory.badge.refresh.failed',
      trace_id: 'trace-memory-1',
      span_id: 'span-memory-1',
      thread_id: 'thread-1',
      status: 'error',
      error: '记忆中心加载超时，请检查记忆数据或后端状态。',
      metadata: { component: 'memory', req_id: 17 },
    }));
    expect(events[1]).not.toHaveProperty('error');
    expect(events[2]).toEqual(expect.objectContaining({
      phase: 'frontend.warning',
      method: 'app.render.crash',
      client_route: 'app',
      status: 'error',
      error: 'TypeError:APPROVAL_SUBMIT_TIMEOUT',
      metadata: {
        component: 'react.root',
        react_phase: 'render',
        crash_fingerprint: 'crash-v1-1483443a51ffbe45',
        breadcrumb_trail: 'app.bootstrap:app:start',
      },
    }));
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain('secret');
    expect(serialized).not.toContain('prompt');
    expect(serialized).not.toContain('private crash');
    expect(serialized).not.toContain('component_props');
  });

  it('rejects frontend traces with unknown statuses instead of coercing them to ok', async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { emitFrontendTraceEvent } = await import('./wailsBridge.js');

    expect(emitFrontendTraceEvent({
      phase: 'frontend.warning',
      method: 'memory.badge.refresh.failed',
      trace_id: 'trace-memory-invalid-status',
      span_id: 'span-memory-invalid-status',
      status: 'warn',
    })).toBe(false);
    await waitForTraceFlush();
    expect(byID).not.toHaveBeenCalled();
  });

  it('keeps runtime RPC telemetry metadata while dropping forbidden content', async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { emitFrontendTraceEvent } = await import('./wailsBridge.js');

    expect(emitFrontendTraceEvent({
      phase: 'runtime.rpc.pending',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-1',
      span_id: 'span-runtime-1',
      call_id: '7',
      duration_ms: 12,
      status: 'ok',
      metadata: {
        req_id: 42,
        pending_count: 3,
        attempt: 1,
        prompt: 'secret prompt must not leak',
        content: 'secret content must not leak',
      },
    })).toBe(true);

    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    expect(ingestCall[2].events).toEqual([
      expect.objectContaining({
        phase: 'runtime.rpc.pending',
        method: 'thread/config/get',
        trace_id: 'trace-runtime-1',
        span_id: 'span-runtime-1',
        call_id: '7',
        duration_ms: 12,
        status: 'ok',
        metadata: {
          req_id: 42,
          pending_count: 3,
          attempt: 1,
        },
      }),
    ]);
    const serialized = JSON.stringify(ingestCall[2].events);
    expect(serialized).not.toContain('secret');
    expect(serialized).not.toContain('prompt');
    expect(serialized).not.toContain('content');
  });

  it('pipes runtime shim telemetry into bridge logs without prompt fields', async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: 'runtime.rpc.send.done',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-2',
      span_id: 'span-runtime-2',
      call_id: '8',
      duration_ms: 2,
      status: 'ok',
      req_id: 43,
      pending_count: 1,
      attempt: 1,
      prompt: 'secret prompt must not leak',
    });

    const telemetryLog = logs.find((entry) => entry.event === 'runtime.rpc.telemetry');
    expect(telemetryLog.fields).toEqual(expect.objectContaining({
      phase: 'runtime.rpc.send.done',
      method: 'thread/config/get',
      call_id: '8',
      duration_ms: 2,
      metadata: {
        req_id: 43,
        pending_count: 1,
        attempt: 1,
      },
    }));
    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    const serialized = JSON.stringify([...logs, ingestCall[2].events]);
    expect(serialized).not.toContain('secret');
    expect(serialized).not.toContain('prompt');
  });
});

describe('wails bridge frontend debug trace emitter', () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it('keeps successful debug RPC traces on the same trace context when debug tracing is enabled', async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    const byID = vi.fn((methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true, result_preview: 'not persisted remotely' });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).resolves.toEqual({
      ok: true,
      result_preview: 'not persisted remotely',
    });

    const rpcPayload = byID.mock.calls[0][2];
    let ingestPayload;
    await waitFor(() => {
      const ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
      ingestPayload = ingestCall?.[2];
      expect(ingestPayload?.events).toHaveLength(2);
    });
    expect(ingestPayload.events.map((event) => event.phase)).toEqual([
      'frontend.rpc.start',
      'frontend.rpc.done',
    ]);
    expect(ingestPayload.events.every((event) => event.trace_id === rpcPayload._aoTraceId)).toBe(true);
    expect(ingestPayload.events.every((event) => event.span_id === rpcPayload._aoSpanId)).toBe(true);
    expect(JSON.stringify(ingestPayload.events)).not.toContain('result_preview');
  });
});

describe('wails bridge frontend trace queue', () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it('drops oldest queued frontend traces at the queue bound without leaking sensitive metadata', async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { emitFrontendTraceEvent } = await import('./wailsBridge.js');

    for (let i = 0; i < 510; i += 1) {
      expect(emitFrontendTraceEvent({
        phase: 'frontend.rpc.failed',
        method: `thread/start-${i}`,
        trace_id: `trace-${i}`,
        span_id: `span-${i}`,
        status: 'error',
        error: 'E_BACKEND',
        metadata: {
          req_id: i,
          prompt: 'secret prompt must not leak',
          text: 'secret text must not leak',
          raw_stack: 'secret stack must not leak',
        },
      }, { flush: false })).toBe(true);
    }
    expect(emitFrontendTraceEvent({
      phase: 'frontend.rpc.failed',
      method: 'trigger-flush',
      trace_id: 'trace-trigger',
      span_id: 'span-trigger',
      status: 'error',
      error: 'E_BACKEND',
      metadata: { req_id: 510, prompt: 'trigger secret must not leak' },
    })).toBe(true);

    let events = [];
    await waitFor(() => {
      const ingestCalls = byID.mock.calls.filter(([, method]) => method === 'observability/frontend/ingest');
      events = ingestCalls.flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(500);
    });
    expect(events[0].method).toBe('thread/start-11');
    expect(events.some((event) => event.method === 'thread/start-0')).toBe(false);
    expect(events.some((event) => event.method === 'thread/start-10')).toBe(false);
    expect(events[events.length - 1].method).toBe('trigger-flush');
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain('secret');
    expect(serialized).not.toContain('prompt');
    expect(serialized).not.toContain('raw_stack');
  });
});

describe('wails bridge frontend trace defaults', () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it('does not remote flush successful debug-level RPC traces by default', async () => {
    const byID = vi.fn().mockResolvedValue({ ok: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).resolves.toEqual({ ok: true });
    await waitForTraceFlush();

    expect(byID.mock.calls.some(([, method]) => method === 'observability/frontend/ingest')).toBe(false);
  });

  it('remote flushes default runtime timing traces without logging normal ok telemetry locally', async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: 'runtime.rpc.pending',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-default',
      span_id: 'span-runtime-default',
      call_id: '12',
      duration_ms: 17,
      status: 'ok',
      req_id: 55,
      pending_count: 1,
      prompt: 'secret prompt must not leak',
    });
    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: 'runtime.rpc.send.done',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-default',
      span_id: 'span-runtime-default',
      call_id: '12',
      duration_ms: 3,
      status: 'ok',
      req_id: 55,
      pending_count: 1,
      attempt: 1,
      prompt: 'secret prompt must not leak',
    });
    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: 'runtime.rpc.settled',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-default',
      span_id: 'span-runtime-default',
      call_id: '12',
      duration_ms: 24,
      status: 'ok',
      req_id: 55,
      pending_count: 0,
      content: 'secret content must not leak',
    });

    let events = [];
    await waitFor(() => {
      events = byID.mock.calls
        .filter(([, method]) => method === 'observability/frontend/ingest')
        .flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(3);
    });
    expect(events.map((event) => event.phase)).toEqual([
      'runtime.rpc.pending',
      'runtime.rpc.send.done',
      'runtime.rpc.settled',
    ]);
    expect(events.every((event) => event.status === 'ok')).toBe(true);
    expect(events[0]).toEqual(expect.objectContaining({
      phase: 'runtime.rpc.pending',
      duration_ms: 17,
      metadata: {
        req_id: 55,
        pending_count: 1,
      },
    }));
    expect(events[1].metadata).toEqual({
      req_id: 55,
      pending_count: 1,
      attempt: 1,
    });
    expect(events[2].metadata).toEqual({
      req_id: 55,
      pending_count: 0,
    });
    expect(logs.filter((entry) => entry.event === 'runtime.rpc.telemetry')).toHaveLength(0);
    expect(JSON.stringify(events)).not.toContain('secret');
    expect(JSON.stringify(events)).not.toContain('prompt');
    expect(JSON.stringify(events)).not.toContain('content');

    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: 'runtime.rpc.timeout',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-default',
      span_id: 'span-runtime-default',
      call_id: '13',
      duration_ms: 30000,
      status: 'error',
      error: 'timeout',
      req_id: 56,
      pending_count: 0,
    });

    await waitFor(() => {
      const telemetryLogs = logs.filter((entry) => entry.event === 'runtime.rpc.telemetry');
      expect(telemetryLogs).toHaveLength(1);
      expect(telemetryLogs[0]).toEqual(expect.objectContaining({
        level: 'warn',
        fields: expect.objectContaining({
          phase: 'runtime.rpc.timeout',
          status: 'error',
          error: 'timeout',
        }),
      }));
    });
  });

  it('marks slow successful RPC done traces as slow when remote flushing', async () => {
    let now = 0;
    vi.spyOn(performance, 'now').mockImplementation(() => now);
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === 'observability/frontend/ingest') return Promise.resolve({ recorded: payload.events.length });
      now = 1000;
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    await expect(callAPI('observability/recent/list', {})).resolves.toEqual({ ok: true });

    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    expect(ingestCall[2].events).toEqual([
      expect.objectContaining({
        phase: 'frontend.rpc.done',
        method: 'observability/recent/list',
        duration_ms: 1000,
        status: 'slow',
      }),
    ]);
  });

  it('rejects invalid bridge clock timestamps before emitting fake durations', async () => {
    vi.spyOn(performance, 'now').mockReturnValue(Number.NaN);
    const byID = vi.fn().mockResolvedValue({ ok: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import('./wailsBridge.js');

    await expect(callAPI('observability/recent/list', {})).rejects.toMatchObject({
      name: 'BridgeClockUnavailableError',
    });
    expect(byID).not.toHaveBeenCalled();
  });
});

describe('development Wails runtime shim events', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('reconnects existing event subscriptions after the dev WebSocket disconnects', async () => {
    vi.useFakeTimers();
    const sockets = [];
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshDevRuntimeShim();
    const received = [];

    runtime.Events.On('agent-event', (event) => received.push(event));
    expect(sockets).toHaveLength(1);
    sockets[0].open();
    sockets[0].emit('thread/messages', { threadId: 'thread-1', text: 'before reconnect' });
    await Promise.resolve();
    expect(received).toHaveLength(1);

    sockets[0].close();
    await vi.advanceTimersByTimeAsync(500);

    expect(sockets).toHaveLength(2);
    sockets[1].open();
    sockets[1].emit('thread/messages', { threadId: 'thread-1', text: 'after reconnect' });
    await Promise.resolve();

    expect(received).toHaveLength(2);
    expect(received[1].data.payload.text).toBe('after reconnect');
  });

  it('preserves trace metadata for backend correlation while stripping client meta from strict dev RPC routes', async () => {
    const sockets = [];
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshDevRuntimeShim();
    const traceId = '4bf92f3577b34da6a3ce929d0e0e4736';
    const spanId = '00f067aa0ba902b7';
    const resultPromise = runtime.Call.ByID(1391035622, 'thread/config/get', {
      threadId: 'thread-1',
      _aoTraceparent: `00-${traceId}-${spanId}-01`,
      _aoTraceId: traceId,
      _aoSpanId: spanId,
      _aoClientKind: 'web-debug-shim',
      _aoClientRoute: '/',
      _aoRequestId: 42,
    });
    expect(sockets).toHaveLength(1);
    sockets[0].open();
    await Promise.resolve();

    expect(sockets[0].sent).toHaveLength(1);
    const request = JSON.parse(sockets[0].sent[0]);
    expect(request.method).toBe('thread/config/get');
    expect(request.params).toEqual({
      threadId: 'thread-1',
      _aoTraceparent: `00-${traceId}-${spanId}-01`,
      _aoTraceId: traceId,
      _aoSpanId: spanId,
    });

    sockets[0].receive({ jsonrpc: '2.0', id: request.id, result: { ok: true } });
    await expect(resultPromise).resolves.toEqual({ ok: true });
  });
});
