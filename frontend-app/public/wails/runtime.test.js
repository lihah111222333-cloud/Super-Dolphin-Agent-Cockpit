import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cwd } from 'node:process';
import { afterEach, describe, expect, it, vi } from 'vitest';

function createTestWebSocketClass(sockets) {
  return class TestWebSocket {
    static CONNECTING = 0;

    static OPEN = 1;

    onopen;

    onclose;

    onerror;

    onmessage;

    constructor(url) {
      this.url = url;
      this.readyState = TestWebSocket.CONNECTING;
      this.sent = [];
      this.onopen = null;
      this.onclose = null;
      this.onerror = null;
      this.onmessage = null;
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

    error(error = new Error('network fault')) {
      this.onerror?.(error);
    }

    receive(message) {
      this.onmessage?.({
        data: JSON.stringify(message),
      });
    }
  };
}

async function importFreshRuntimeShim() {
  vi.resetModules();
  return import('./runtime.js?test-runtime-telemetry');
}

describe('development Wails runtime shim', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it('keeps the websocket bridge entrypoint in the served runtime', () => {
    const source = readFileSync(join(cwd(), 'public/wails/runtime.js'), 'utf8');
    expect(source).toContain('/wails/ws');
    expect(source).toContain('__WAILS_SHIM_DEBUG__');
  });

  it('emits wails:loaded once per reconnect cycle but not on the first normal open', async () => {
    vi.useFakeTimers();
    const sockets = [];
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const loaded = vi.fn();
    const unsubscribe = runtime.Events.On('wails:loaded', loaded);
    expect(sockets).toHaveLength(1);

    sockets[0].open();
    sockets[0].open();
    expect(loaded).not.toHaveBeenCalled();

    sockets[0].close();
    await vi.advanceTimersByTimeAsync(500);
    expect(sockets).toHaveLength(2);
    sockets[1].open();
    sockets[1].open();
    expect(loaded).toHaveBeenCalledTimes(1);
    expect(loaded).toHaveBeenLastCalledWith({ name: 'wails:loaded', data: {} });

    sockets[1].close();
    await vi.advanceTimersByTimeAsync(500);
    expect(sockets).toHaveLength(3);
    sockets[2].open();
    expect(loaded).toHaveBeenCalledTimes(2);

    unsubscribe();
  });

  it('emits wails:loaded when an initially failed connection recovers', async () => {
    vi.useFakeTimers();
    const sockets = [];
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const loaded = vi.fn();
    runtime.Events.On('wails:loaded', loaded);
    sockets[0].error(new Error('ECONNREFUSED'));
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(500);
    expect(sockets).toHaveLength(2);

    sockets[1].open();
    expect(loaded).toHaveBeenCalledTimes(1);
  });

  it('does not reconnect or emit wails:loaded after the listener unsubscribes', async () => {
    vi.useFakeTimers();
    const sockets = [];
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const loaded = vi.fn();
    const unsubscribe = runtime.Events.On('wails:loaded', loaded);
    sockets[0].open();
    sockets[0].close();
    unsubscribe();
    await vi.advanceTimersByTimeAsync(500);

    expect(sockets).toHaveLength(1);
    expect(loaded).not.toHaveBeenCalled();
  });

  it('emits pending send and settle telemetry with request correlation on resolve', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000);
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const resultPromise = runtime.Call.ByID(2963398832, 'thread/config/get', {
      threadId: 'thread-1',
      _aoTraceId: 'trace-runtime-resolve',
      _aoSpanId: 'span-runtime-resolve',
      _aoRequestId: 42,
    });
    expect(sockets).toHaveLength(1);
    vi.setSystemTime(1_037);
    sockets[0].open();
    await Promise.resolve();

    expect(sockets[0].sent).toHaveLength(1);
    const request = JSON.parse(sockets[0].sent[0]);
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.pending',
      method: 'thread/config/get',
      trace_id: 'trace-runtime-resolve',
      span_id: 'span-runtime-resolve',
      call_id: String(request.id),
      req_id: 42,
      pending_count: 1,
      duration_ms: 37,
      status: 'ok',
    }));
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.send.done',
      method: 'thread/config/get',
      call_id: String(request.id),
      req_id: 42,
      pending_count: 1,
      attempt: 1,
      status: 'ok',
    }));

    sockets[0].receive({ jsonrpc: '2.0', id: request.id, result: { ok: true } });
    await expect(resultPromise).resolves.toEqual({ ok: true });

    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.settled',
      method: 'thread/config/get',
      call_id: String(request.id),
      req_id: 42,
      pending_count: 0,
      status: 'ok',
    }));
    const serialized = JSON.stringify(telemetry.mock.calls);
    expect(serialized).not.toContain('thread-1');
  });

  it('emits timeout telemetry and removes timed out pending calls', async () => {
    vi.useFakeTimers();
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const timedOut = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-timeout',
      _aoSpanId: 'span-runtime-timeout',
      _aoRequestId: 77,
    });
    const timedOutAssertion = expect(timedOut).rejects.toThrow('runtime shim: rpc call timeout');
    sockets[0].open();
    await Promise.resolve();
    const timedOutRequest = JSON.parse(sockets[0].sent[0]);

    await vi.advanceTimersByTimeAsync(30_000);
    await timedOutAssertion;
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.timeout',
      method: 'thread/config/get',
      call_id: String(timedOutRequest.id),
      req_id: 77,
      pending_count: 0,
      status: 'error',
      error: 'timeout',
    }));

    const nextCall = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-after-timeout',
      _aoSpanId: 'span-runtime-after-timeout',
      _aoRequestId: 78,
    });
    await Promise.resolve();
    const nextRequest = JSON.parse(sockets[0].sent[1]);
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.pending',
      call_id: String(nextRequest.id),
      req_id: 78,
      pending_count: 1,
    }));
    sockets[0].receive({ jsonrpc: '2.0', id: nextRequest.id, result: { ok: true } });
    await expect(nextCall).resolves.toEqual({ ok: true });
  });

  it('uses a longer timeout for update download and installLatest RPCs', async () => {
    vi.useFakeTimers();
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const installPromise = runtime.Call.ByID(2963398832, 'app/update/installLatest', {
      _aoTraceId: 'trace-update-install',
      _aoSpanId: 'span-update-install',
      _aoRequestId: 88,
    });
    sockets[0].open();
    await Promise.resolve();
    const request = JSON.parse(sockets[0].sent[0]);
    const installTimeoutAssertion = expect(installPromise).rejects.toThrow('runtime shim: rpc call timeout (900s) for app/update/installLatest');

    await vi.advanceTimersByTimeAsync(30_001);
    expect(telemetry).not.toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.timeout',
      method: 'app/update/installLatest',
    }));

    await vi.advanceTimersByTimeAsync(869_999);
    await installTimeoutAssertion;
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.timeout',
      method: 'app/update/installLatest',
      call_id: String(request.id),
      req_id: 88,
      pending_count: 0,
      status: 'error',
      error: 'timeout',
    }));

    const downloadPromise = runtime.Call.ByID(2963398832, 'app/update/download', {
      _aoTraceId: 'trace-update-download',
      _aoSpanId: 'span-update-download',
      _aoRequestId: 89,
    });
    await Promise.resolve();
    const downloadRequest = JSON.parse(sockets[0].sent[1]);
    await vi.advanceTimersByTimeAsync(60_000);
    sockets[0].receive({ jsonrpc: '2.0', id: downloadRequest.id, result: { ok: true } });
    await expect(downloadPromise).resolves.toEqual({ ok: true });
  });

  it('keeps interactive file selection pending beyond the short RPC timeout', async () => {
    vi.useFakeTimers();
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const selectionPromise = runtime.Call.ByID(4126105303);
    sockets[0].open();
    await Promise.resolve();
    const request = JSON.parse(sockets[0].sent[0]);

    await vi.advanceTimersByTimeAsync(30_001);
    expect(telemetry).not.toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.timeout',
      method: 'ui/selectFiles',
    }));

    sockets[0].receive({ jsonrpc: '2.0', id: request.id, result: { paths: ['/tmp/selected.txt'] } });
    await expect(selectionPromise).resolves.toEqual(['/tmp/selected.txt']);
  });

  it('emits failure telemetry and clears pending calls on websocket close', async () => {
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const resultPromise = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-close',
      _aoSpanId: 'span-runtime-close',
      _aoRequestId: 88,
    });
    sockets[0].open();
    await Promise.resolve();
    const request = JSON.parse(sockets[0].sent[0]);

    sockets[0].close();
    await expect(resultPromise).rejects.toThrow('runtime shim: websocket closed');
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.failed',
      method: 'thread/config/get',
      call_id: String(request.id),
      req_id: 88,
      pending_count: 0,
      status: 'error',
      error: 'websocket_closed',
    }));
  });

  it('retries event subscriptions when the initial websocket connect is refused', async () => {
    vi.useFakeTimers();
    const sockets = [];
    const callback = vi.fn();
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    runtime.Events.On('bridge-event', callback);
    expect(sockets).toHaveLength(1);

    sockets[0].error(new Error('connect ECONNREFUSED'));
    await Promise.resolve();
    expect(warn).toHaveBeenCalledWith(
      expect.stringContaining('event bridge unavailable'),
      expect.objectContaining({ message: expect.stringContaining('failed to connect') }),
    );

    await vi.advanceTimersByTimeAsync(500);
    expect(sockets).toHaveLength(2);
    sockets[1].open();
    sockets[1].receive({
      jsonrpc: '2.0',
      method: 'thread/status/changed',
      params: { threadId: 'thread-1', status: 'idle' },
    });
    await Promise.resolve();

    expect(callback).toHaveBeenCalledWith(expect.objectContaining({
      name: 'bridge-event',
      data: expect.objectContaining({
        method: 'thread/status/changed',
        payload: expect.objectContaining({ threadId: 'thread-1' }),
      }),
    }));
  });

  it('emits failure telemetry and clears pending calls on websocket error after open', async () => {
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    let rejectedError;
    const resultPromise = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-open-error',
      _aoSpanId: 'span-runtime-open-error',
      _aoRequestId: 89,
    }).catch((error) => {
      rejectedError = error;
    });
    sockets[0].open();
    await Promise.resolve();
    const request = JSON.parse(sockets[0].sent[0]);

    sockets[0].error(new Error('socket fault after open'));
    await new Promise((resolve) => { setTimeout(resolve, 0); });

    try {
      expect(rejectedError).toEqual(expect.objectContaining({
        message: 'socket fault after open',
      }));
      expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'runtime.rpc.failed',
        method: 'thread/config/get',
        call_id: String(request.id),
        req_id: 89,
        pending_count: 0,
        status: 'error',
        error: 'websocket_error',
      }));
    }
    finally {
      if (!rejectedError) {
        sockets[0].close();
      }
      await resultPromise;
    }
  });

  it('emits send failure telemetry and clears pending calls when WebSocket.send throws', async () => {
    const sockets = [];
    const telemetry = vi.fn();
    let failedRequestText = '';
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const resultPromise = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-send-throw',
      _aoSpanId: 'span-runtime-send-throw',
      _aoRequestId: 90,
    });
    sockets[0].send = vi.fn((data) => {
      failedRequestText = data;
      throw new Error('send exploded');
    });
    sockets[0].open();

    await expect(resultPromise).rejects.toThrow('send exploded');
    const failedRequest = JSON.parse(failedRequestText);
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.send.failed',
      method: 'thread/config/get',
      call_id: String(failedRequest.id),
      req_id: 90,
      pending_count: 0,
      status: 'error',
      error: 'send_failed',
      attempt: 1,
    }));

    sockets[0].send = function send(data) {
      this.sent.push(data);
    };
    const nextCall = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-after-send-throw',
      _aoSpanId: 'span-runtime-after-send-throw',
      _aoRequestId: 91,
    });
    await Promise.resolve();
    expect(sockets[0].sent).toHaveLength(1);
    const nextRequest = JSON.parse(sockets[0].sent[0]);
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.pending',
      call_id: String(nextRequest.id),
      req_id: 91,
      pending_count: 1,
    }));

    sockets[0].receive({ jsonrpc: '2.0', id: failedRequest.id, result: { stale: true } });
    sockets[0].receive({ jsonrpc: '2.0', id: nextRequest.id, result: { ok: true } });
    await expect(nextCall).resolves.toEqual({ ok: true });
  });

  it('emits rpc error settlement telemetry and clears pending calls on JSON-RPC error responses', async () => {
    const sockets = [];
    const telemetry = vi.fn();
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = telemetry;
    vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));

    const runtime = await importFreshRuntimeShim();
    const resultPromise = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-rpc-error',
      _aoSpanId: 'span-runtime-rpc-error',
      _aoRequestId: 92,
    });
    sockets[0].open();
    await Promise.resolve();
    const failedRequest = JSON.parse(sockets[0].sent[0]);

    sockets[0].receive({
      jsonrpc: '2.0',
      id: failedRequest.id,
      error: {
        code: -32000,
        message: 'backend rejected request',
        data: { prompt: 'secret prompt must not leak' },
      },
    });
    await expect(resultPromise).rejects.toThrow('backend rejected request');
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.settled',
      method: 'thread/config/get',
      call_id: String(failedRequest.id),
      req_id: 92,
      pending_count: 0,
      status: 'error',
      error: 'rpc_error',
    }));

    const nextCall = runtime.Call.ByID(2963398832, 'thread/config/get', {
      _aoTraceId: 'trace-runtime-after-rpc-error',
      _aoSpanId: 'span-runtime-after-rpc-error',
      _aoRequestId: 93,
    });
    await Promise.resolve();
    const nextRequest = JSON.parse(sockets[0].sent[1]);
    expect(telemetry).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'runtime.rpc.pending',
      call_id: String(nextRequest.id),
      req_id: 93,
      pending_count: 1,
    }));
    sockets[0].receive({ jsonrpc: '2.0', id: nextRequest.id, result: { ok: true } });
    await expect(nextCall).resolves.toEqual({ ok: true });
    expect(JSON.stringify(telemetry.mock.calls)).not.toContain('secret prompt');
  });
});
