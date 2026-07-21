import { afterEach, describe, expect, it, vi } from "vitest";
import { createTestWebSocketClass } from "./test-support/wailsBridgeTestWebSocket.js";
import { importFreshDevRuntimeShim } from "./wailsBridgeTestSupport.js";

describe("development Wails runtime shim events", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("reconnects existing event subscriptions after the dev WebSocket disconnects", async () => {
    vi.useFakeTimers();
    const sockets = [];
    vi.stubGlobal("WebSocket", createTestWebSocketClass(sockets));

    const runtime = await importFreshDevRuntimeShim();
    const received = [];

    runtime.Events.On("agent-event", (event) => received.push(event));
    expect(sockets).toHaveLength(1);
    sockets[0].open();
    sockets[0].emit("thread/messages", {
      threadId: "thread-1",
      text: "before reconnect",
    });
    await Promise.resolve();
    expect(received).toHaveLength(1);

    sockets[0].close();
    await vi.advanceTimersByTimeAsync(500);

    expect(sockets).toHaveLength(2);
    sockets[1].open();
    sockets[1].emit("thread/messages", {
      threadId: "thread-1",
      text: "after reconnect",
    });
    await Promise.resolve();

    expect(received).toHaveLength(2);
    expect(received[1].data.payload.text).toBe("after reconnect");
  });

  it("preserves trace metadata for backend correlation while stripping client meta from strict dev RPC routes", async () => {
    const sockets = [];
    vi.stubGlobal("WebSocket", createTestWebSocketClass(sockets));

    const runtime = await importFreshDevRuntimeShim();
    const traceId = "4bf92f3577b34da6a3ce929d0e0e4736";
    const spanId = "00f067aa0ba902b7";
    const resultPromise = runtime.Call.ByID(1391035622, "thread/config/get", {
      threadId: "thread-1",
      _aoTraceparent: `00-${traceId}-${spanId}-01`,
      _aoTraceId: traceId,
      _aoSpanId: spanId,
      _aoClientKind: "web-debug-shim",
      _aoClientRoute: "/",
      _aoRequestId: 42,
    });
    expect(sockets).toHaveLength(1);
    sockets[0].open();
    await Promise.resolve();

    expect(sockets[0].sent).toHaveLength(1);
    const request = JSON.parse(sockets[0].sent[0]);
    expect(request.method).toBe("thread/config/get");
    expect(request.params).toEqual({
      threadId: "thread-1",
      _aoTraceparent: `00-${traceId}-${spanId}-01`,
      _aoTraceId: traceId,
      _aoSpanId: spanId,
    });

    sockets[0].receive({
      jsonrpc: "2.0",
      id: request.id,
      result: { ok: true },
    });
    await expect(resultPromise).resolves.toEqual({ ok: true });
  });
});
