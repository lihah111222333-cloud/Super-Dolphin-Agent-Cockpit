import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { beginTextClipboardWrite, copyTextToClipboard } from './wailsBridge.js';

const runtimeModule = 'http://127.0.0.1:5175/wails/runtime.js';
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
  window.localStorage.clear();
}

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

describe('wails bridge warning logs', () => {
  beforeEach(resetWailsRuntimeMocks);

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

  it('records a compact backend RPC return preview on successful calls', async () => {
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
});

describe('wails bridge RPC trace log fields', () => {
  beforeEach(resetWailsRuntimeMocks);

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

  it('records trace identifiers in backend RPC start done and failed logs', async () => {
    const byID = vi.fn()
      .mockResolvedValueOnce({ ok: true })
      .mockRejectedValueOnce(new Error('backend unavailable'));
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('tools/call', { name: 'mcp__lsp__grep' })).resolves.toEqual({ ok: true });
    const successPayload = byID.mock.calls[0][2];
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
    const failedPayload = byID.mock.calls[1][2];
    const failed = logs.find((entry) => entry.event === 'api.rpc.failed' && entry.fields.method === 'thread/config/get');
    expect(failed.fields).toEqual(expect.objectContaining({
      trace_id: failedPayload._aoTraceId,
      span_id: failedPayload._aoSpanId,
    }));
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
});

describe('wails bridge frontend trace emitter', () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
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
    await waitForTraceFlush();
    await waitForTraceFlush();

    const rpcPayload = byID.mock.calls[0][2];
    expect(thrownError).toEqual(expect.objectContaining({
      traceId: rpcPayload._aoTraceId,
      trace_id: rpcPayload._aoTraceId,
      spanId: rpcPayload._aoSpanId,
      span_id: rpcPayload._aoSpanId,
      req_id: rpcPayload._aoRequestId,
      method: 'thread/start',
    }));
    const ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
    expect(ingestCall).toBeTruthy();
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
    await waitForTraceFlush();
    await waitForTraceFlush();

    let events = [];
    await waitFor(() => {
      events = byID.mock.calls
        .filter(([, method]) => method === 'observability/frontend/ingest')
        .flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(2);
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
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain('secret');
    expect(serialized).not.toContain('prompt');
  });
});

describe('wails bridge frontend debug trace emitter', () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
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
    await waitForTraceFlush();
    await waitForTraceFlush();

    const rpcPayload = byID.mock.calls[0][2];
    const ingestPayload = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest')[2];
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

  it('marks slow successful RPC done traces as slow when remote flushing', async () => {
    let now = 0;
    vi.spyOn(Date, 'now').mockImplementation(() => now);
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
    await waitForTraceFlush();
    await waitForTraceFlush();

    const ingestCall = byID.mock.calls.find(([, method]) => method === 'observability/frontend/ingest');
    expect(ingestCall).toBeTruthy();
    expect(ingestCall[2].events).toEqual([
      expect.objectContaining({
        phase: 'frontend.rpc.done',
        method: 'observability/recent/list',
        duration_ms: 1000,
        status: 'slow',
      }),
    ]);
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
    const resultPromise = runtime.Call.ByID(2963398832, 'thread/config/get', {
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
