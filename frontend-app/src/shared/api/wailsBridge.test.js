import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { beginTextClipboardWrite, copyTextToClipboard } from './wailsBridge.js';

const runtimeModule = 'http://127.0.0.1:5175/wails/runtime.js';

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
  beforeEach(() => {
    vi.resetModules();
    vi.doUnmock(runtimeModule);
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

describe('wails bridge frontend trace emitter', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.doUnmock(runtimeModule);
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    window.localStorage.clear();
  });

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

    await expect(callAPI('thread/start', {
      prompt: 'do not persist this prompt',
      result_preview: 'do not persist preview',
    })).rejects.toThrow('backend unavailable');
    await waitForTraceFlush();
    await waitForTraceFlush();

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
    expect(events[0].trace_id).toBe(byID.mock.calls[0][2]._aoTraceId);
    expect(events[0].span_id).toBe(byID.mock.calls[0][2]._aoSpanId);
    expect(events[0]).not.toHaveProperty('error_name');
    expect(events[0]).not.toHaveProperty('error_code');
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain('result_preview');
    expect(serialized).not.toContain('do not persist');
    expect(serialized).not.toContain('raw stack');
    expect(serialized).not.toContain('prompt');
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
});

describe('development Wails runtime shim events', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('reconnects existing event subscriptions after the dev WebSocket disconnects', async () => {
    vi.useFakeTimers();
    const sockets = [];
    class TestWebSocket {
      static CONNECTING = 0;

      static OPEN = 1;

      constructor(url) {
        this.url = url;
        this.readyState = TestWebSocket.CONNECTING;
        sockets.push(this);
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
    }
    vi.stubGlobal('WebSocket', TestWebSocket);

    const runtimeSource = readFileSync('public/wails/runtime.js', 'utf8')
      .replace('export const Call =', 'const Call =')
      .replace('export const Events =', 'const Events =');
    const runtime = new Function(`${runtimeSource}\nreturn { Call, Events };`)();
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
});
