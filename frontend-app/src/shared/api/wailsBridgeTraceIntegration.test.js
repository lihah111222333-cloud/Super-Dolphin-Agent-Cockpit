import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";

const runtimeModule = "/wails/runtime.js";

function resetFrontendTraceEmitter() {
  vi.resetModules();
  vi.doUnmock(runtimeModule);
  delete window.__AO_FRONTEND_TRACE_DEBUG__;
  delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  window.localStorage.clear();
}

describe("wails bridge frontend debug trace emitter", () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it("keeps successful debug RPC traces on the same trace context when debug tracing is enabled", async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    const byID = vi.fn((methodID, method, payload) => {
      if (method === "observability/frontend/ingest")
        return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({
        ok: true,
        result_preview: "not persisted remotely",
      });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    await expect(
      callAPI("thread/config/get", { threadId: "thread-1" }),
    ).resolves.toEqual({
      ok: true,
      result_preview: "not persisted remotely",
    });

    const rpcPayload = byID.mock.calls[0][2];
    let ingestPayload;
    await waitFor(() => {
      const ingestCall = byID.mock.calls.find(
        ([, method]) => method === "observability/frontend/ingest",
      );
      ingestPayload = ingestCall?.[2];
      expect(ingestPayload?.events).toHaveLength(2);
    });
    expect(ingestPayload.events.map((event) => event.phase)).toEqual([
      "frontend.rpc.start",
      "frontend.rpc.done",
    ]);
    expect(
      ingestPayload.events.every(
        (event) => event.trace_id === rpcPayload._aoTraceId,
      ),
    ).toBe(true);
    expect(
      ingestPayload.events.every(
        (event) => event.span_id === rpcPayload._aoSpanId,
      ),
    ).toBe(true);
    expect(JSON.stringify(ingestPayload.events)).not.toContain(
      "result_preview",
    );
  });
});

describe("wails bridge frontend trace queue", () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it("drops oldest queued frontend traces at the queue bound without leaking sensitive metadata", async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === "observability/frontend/ingest")
        return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { emitFrontendTraceEvent } = await import("./wailsBridge.js");

    for (let i = 0; i < 510; i += 1) {
      expect(
        emitFrontendTraceEvent(
          {
            phase: "frontend.rpc.failed",
            method: `thread/start-${i}`,
            trace_id: `trace-${i}`,
            span_id: `span-${i}`,
            status: "error",
            error: "E_BACKEND",
            metadata: {
              req_id: i,
              prompt: "secret prompt must not leak",
              text: "secret text must not leak",
              raw_stack: "secret stack must not leak",
            },
          },
          { flush: false },
        ),
      ).toBe(true);
    }
    expect(
      emitFrontendTraceEvent({
        phase: "frontend.rpc.failed",
        method: "trigger-flush",
        trace_id: "trace-trigger",
        span_id: "span-trigger",
        status: "error",
        error: "E_BACKEND",
        metadata: { req_id: 510, prompt: "trigger secret must not leak" },
      }),
    ).toBe(true);

    let events = [];
    await waitFor(() => {
      const ingestCalls = byID.mock.calls.filter(
        ([, method]) => method === "observability/frontend/ingest",
      );
      events = ingestCalls.flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(500);
    });
    expect(events[0].method).toBe("thread/start-11");
    expect(events.some((event) => event.method === "thread/start-0")).toBe(
      false,
    );
    expect(events.some((event) => event.method === "thread/start-10")).toBe(
      false,
    );
    expect(events[events.length - 1].method).toBe("trigger-flush");
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain("secret");
    expect(serialized).not.toContain("prompt");
    expect(serialized).not.toContain("raw_stack");
  });
});
