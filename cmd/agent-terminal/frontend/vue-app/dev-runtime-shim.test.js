// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const METHOD_IDS = Object.freeze({
  CALL_API: 2963398832,
  SAVE_CLIPBOARD_IMAGE: 3733550318,
  SELECT_FILES: 4126105303,
});

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 3;

  static instances = [];

  constructor(url) {
    this.url = url;
    this.readyState = FakeWebSocket.CONNECTING;
    this.sent = [];
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.({});
    });
  }

  send(data) {
    this.sent.push(data);
  }

  receive(message) {
    this.onmessage?.({ data: JSON.stringify(message) });
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code: 1000, reason: 'test close' });
  }
}

async function flushMicrotasks() {
  await Promise.resolve();
  await Promise.resolve();
}

async function loadRuntime() {
  vi.resetModules();
  globalThis.WebSocket = FakeWebSocket;
  globalThis.window = {
    location: { protocol: 'http:', host: 'localhost:5173' },
  };
  return import('../wails/runtime.js');
}

describe('dev Wails runtime shim', () => {
  beforeEach(() => {
    FakeWebSocket.instances.length = 0;
  });

  afterEach(() => {
    delete globalThis.WebSocket;
    delete globalThis.window;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('bridges Call.ByID(CALL_API) to JSON-RPC over /wails/ws', async () => {
    const runtime = await loadRuntime();

    const resultPromise = runtime.Call.ByID(METHOD_IDS.CALL_API, 'thread/list', {
      cwd: '/tmp/project',
      _aoClientKind: 'web-debug-shim',
      _aoClientRoute: '/',
    });
    await flushMicrotasks();

    const ws = FakeWebSocket.instances[0];
    expect(ws.url).toBe('ws://localhost:5173/wails/ws');
    expect(ws.sent).toHaveLength(1);
    const request = JSON.parse(ws.sent[0]);
    expect(request).toMatchObject({
      jsonrpc: '2.0',
      method: 'thread/list',
      params: { cwd: '/tmp/project' },
    });

    ws.receive({ jsonrpc: '2.0', id: request.id, result: { ok: true } });
    await expect(resultPromise).resolves.toEqual({ ok: true });
    expect(globalThis.window.__WAILS_SHIM_DEBUG__).toBe(true);
  });

  it('preserves frontend client metadata for ui/log while stripping it for strict routes', async () => {
    const runtime = await loadRuntime();

    const logPromise = runtime.Call.ByID(METHOD_IDS.CALL_API, 'ui/log', {
      entries: [],
      _aoClientKind: 'web-debug-shim',
      _aoClientRoute: '/chat',
    });
    await flushMicrotasks();

    const ws = FakeWebSocket.instances[0];
    const logRequest = JSON.parse(ws.sent[0]);
    expect(logRequest.method).toBe('ui/log');
    expect(logRequest.params).toMatchObject({
      entries: [],
      _aoClientKind: 'web-debug-shim',
      _aoClientRoute: '/chat',
    });
    ws.receive({ jsonrpc: '2.0', id: logRequest.id, result: { ok: true } });
    await expect(logPromise).resolves.toEqual({ ok: true });

    const strictPromise = runtime.Call.ByID(METHOD_IDS.CALL_API, 'thread/list', {
      cwd: '/tmp/project',
      _aoClientKind: 'web-debug-shim',
      _aoClientRoute: '/chat',
    });
    await flushMicrotasks();
    const strictRequest = JSON.parse(ws.sent[1]);
    expect(strictRequest.params).toEqual({ cwd: '/tmp/project' });
    ws.receive({ jsonrpc: '2.0', id: strictRequest.id, result: [] });
    await expect(strictPromise).resolves.toEqual([]);
  });

  it('maps native method IDs used by services/api.js to debug RPC routes', async () => {
    const runtime = await loadRuntime();

    const savePromise = runtime.Call.ByID(METHOD_IDS.SAVE_CLIPBOARD_IMAGE, 'aGVsbG8=');
    await flushMicrotasks();
    const ws = FakeWebSocket.instances[0];
    const saveRequest = JSON.parse(ws.sent[0]);
    expect(saveRequest.method).toBe('ui/saveClipboardImage');
    expect(saveRequest.params).toEqual({ base64Payload: 'aGVsbG8=' });
    ws.receive({ jsonrpc: '2.0', id: saveRequest.id, result: { path: '/tmp/clipboard.png' } });
    await expect(savePromise).resolves.toBe('/tmp/clipboard.png');

    const filesPromise = runtime.Call.ByID(METHOD_IDS.SELECT_FILES);
    await flushMicrotasks();
    const filesRequest = JSON.parse(ws.sent[1]);
    expect(filesRequest.method).toBe('ui/selectFiles');
    ws.receive({ jsonrpc: '2.0', id: filesRequest.id, result: { paths: ['/tmp/a.txt'] } });
    await expect(filesPromise).resolves.toEqual(['/tmp/a.txt']);
  });

  it('keeps prompt intent drafting on the long RPC timeout budget', async () => {
    vi.useFakeTimers();
    const runtime = await loadRuntime();

    const resultPromise = runtime.Call.ByID(METHOD_IDS.CALL_API, 'prompt-intents/draft', {
      cwd: '/tmp/project',
      kind: 'recall',
      raw_input: 'large product price sheet',
    });
    await flushMicrotasks();

    const ws = FakeWebSocket.instances[0];
    const request = JSON.parse(ws.sent[0]);
    expect(request.method).toBe('prompt-intents/draft');

    let settled = false;
    resultPromise.then(
      () => { settled = true; },
      () => { settled = true; },
    );
    await vi.advanceTimersByTimeAsync(120_000);
    await flushMicrotasks();
    expect(settled).toBe(false);

    ws.receive({ jsonrpc: '2.0', id: request.id, result: { draft_key: 'draft-1' } });
    await expect(resultPromise).resolves.toEqual({ draft_key: 'draft-1' });
  });

  it('adapts backend notifications into Wails-style bridge and agent events', async () => {
    const runtime = await loadRuntime();
    const bridgeEvents = [];
    const agentEvents = [];

    const offBridge = runtime.Events.On('bridge-event', (event) => bridgeEvents.push(event));
    const offAgent = runtime.Events.On('agent-event', (event) => agentEvents.push(event));
    await flushMicrotasks();

    const ws = FakeWebSocket.instances[0];
    ws.receive({
      jsonrpc: '2.0',
      method: 'ui/thread/patch',
      params: { threadId: 'thread-1', sequence: 7 },
    });
    await flushMicrotasks();

    expect(bridgeEvents).toEqual([{
      name: 'bridge-event',
      data: {
        type: 'ui/thread/patch',
        method: 'ui/thread/patch',
        payload: { threadId: 'thread-1', sequence: 7 },
      },
    }]);
    expect(agentEvents).toEqual([{
      name: 'agent-event',
      data: {
        agent_id: 'thread-1',
        type: 'ui/thread/patch',
        payload: { threadId: 'thread-1', sequence: 7 },
      },
    }]);

    offBridge();
    offAgent();
  });
});
