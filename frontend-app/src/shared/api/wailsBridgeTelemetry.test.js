import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import {
  captureBridgeLogs,
  resetFrontendTraceEmitter,
  runtimeModule,
  waitForTraceFlush,
} from "./wailsBridgeTestSupport.js";

describe("wails bridge frontend trace emitter", () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it("sanitizes and remotely flushes all frontend performance pressure phases", async () => {
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
    const events = [
      {
        phase: "frontend.performance.long_task_pressure",
        status: "slow",
        duration_ms: 420,
        metadata: {
          count: 2,
          total_ms: 620,
          max_ms: 420,
          build: "production",
          prompt: "secret prompt",
        },
        timestamp: "forbidden timestamp",
        stack: "forbidden stack",
      },
      {
        phase: "frontend.performance.event_loop_pressure",
        status: "slow",
        duration_ms: 150,
        metadata: { lag_bucket: "150_299", path: "/Users/private" },
        code: "forbidden code",
      },
      {
        phase: "frontend.performance.heap_pressure",
        status: "slow",
        metadata: {
          heap_ratio_bucket: "0.85_0.89",
          dom_text: "private DOM text",
        },
      },
      {
        phase: "frontend.performance.capability_absent",
        status: "ok",
        metadata: { capability: "heap", reason: "free text is forbidden" },
      },
    ];

    events.forEach((event) => expect(emitFrontendTraceEvent(event)).toBe(true));

    let flushed = [];
    await waitFor(() => {
      flushed = byID.mock.calls
        .filter(([, method]) => method === "observability/frontend/ingest")
        .flatMap(([, , payload]) => payload.events);
      expect(flushed).toHaveLength(4);
    });
    expect(flushed).toEqual([
      {
        phase: "frontend.performance.long_task_pressure",
        status: "slow",
        ts: expect.any(String),
        duration_ms: 420,
        metadata: { count: 2, total_ms: 620, max_ms: 420, build: "production" },
      },
      {
        phase: "frontend.performance.event_loop_pressure",
        status: "slow",
        ts: expect.any(String),
        duration_ms: 150,
        metadata: { lag_bucket: "150_299" },
      },
      {
        phase: "frontend.performance.heap_pressure",
        status: "slow",
        ts: expect.any(String),
        metadata: { heap_ratio_bucket: "0.85_0.89" },
      },
      {
        phase: "frontend.performance.capability_absent",
        status: "ok",
        ts: expect.any(String),
        metadata: { capability: "heap" },
      },
    ]);
    const serialized = JSON.stringify(flushed);
    [
      "prompt",
      "timestamp",
      "stack",
      "path",
      "dom_text",
      "reason",
      "free text",
      "/Users/private",
    ].forEach((forbidden) => expect(serialized).not.toContain(forbidden));
    expect(
      emitFrontendTraceEvent({
        phase: "frontend.performance.capability_absent",
        status: "slow",
        metadata: { capability: "heap" },
      }),
    ).toBe(false);
    expect(
      emitFrontendTraceEvent({
        phase: "frontend.performance.heap_pressure",
        status: "ok",
        metadata: { heap_ratio_bucket: "0.85_0.89" },
      }),
    ).toBe(false);
  });

  it("flushes failed RPC traces through observability frontend ingest without sensitive payload fields", async () => {
    const backendError = new Error("backend unavailable");
    backendError.code = "E_BACKEND";
    backendError.stack = "raw stack with file contents";
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === "observability/frontend/ingest")
        return Promise.resolve({ recorded: payload.events.length });
      return Promise.reject(backendError);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    let thrownError;
    await expect(
      callAPI("thread/start", {
        prompt: "do not persist this prompt",
        result_preview: "do not persist preview",
      }).catch((error) => {
        thrownError = error;
        throw error;
      }),
    ).rejects.toThrow("backend unavailable");

    const rpcPayload = byID.mock.calls[0][2];
    expect(thrownError).toEqual(
      expect.objectContaining({
        traceId: rpcPayload._aoTraceId,
        trace_id: rpcPayload._aoTraceId,
        spanId: rpcPayload._aoSpanId,
        span_id: rpcPayload._aoSpanId,
        req_id: rpcPayload._aoRequestId,
        method: "thread/start",
      }),
    );
    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(
        ([, method]) => method === "observability/frontend/ingest",
      );
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    const events = ingestCall[2].events;
    expect(events).toHaveLength(1);
    expect(events[0]).toEqual(
      expect.objectContaining({
        phase: "frontend.rpc.failed",
        method: "thread/start",
        status: "error",
        error: "E_BACKEND: backend unavailable",
      }),
    );
    expect(events[0].trace_id).toBe(rpcPayload._aoTraceId);
    expect(events[0].span_id).toBe(rpcPayload._aoSpanId);
    expect(events[0]).not.toHaveProperty("error_name");
    expect(events[0]).not.toHaveProperty("error_code");
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain("result_preview");
    expect(serialized).not.toContain("do not persist");
    expect(serialized).not.toContain("raw stack");
    expect(serialized).not.toContain("prompt");
  });

  it("drops credential values and local paths from failed RPC trace errors", async () => {
    const backendError = new Error(
      "open /home/l4place/project/.env failed token=sk-live-secret password=hunter2 api_key=abc123",
    );
    backendError.code = "E_SECRET";
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === "observability/frontend/ingest")
        return Promise.resolve({ recorded: payload.events.length });
      return Promise.reject(backendError);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    await expect(
      callAPI("thread/start", {
        prompt: "safe prompt payload should still be stripped",
      }),
    ).rejects.toThrow("/home/l4place/project/.env failed");

    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(
        ([, method]) => method === "observability/frontend/ingest",
      );
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    expect(ingestCall[2].events[0]).toEqual(
      expect.objectContaining({
        phase: "frontend.rpc.failed",
        method: "thread/start",
        status: "error",
        error: "E_SECRET",
      }),
    );
    const serialized = JSON.stringify(ingestCall[2].events);
    expect(serialized).not.toContain("/home/l4place");
    expect(serialized).not.toContain(".env");
    expect(serialized).not.toContain("sk-live-secret");
    expect(serialized).not.toContain("hunter2");
    expect(serialized).not.toContain("abc123");
    expect(serialized).not.toContain("token=");
    expect(serialized).not.toContain("password=");
    expect(serialized).not.toContain("api_key=");
  });

  it("flushes failed frontend warning traces through observability ingest", async () => {
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

    expect(
      emitFrontendTraceEvent({
        phase: "frontend.warning",
        method: "memory.badge.refresh.failed",
        trace_id: "trace-memory-1",
        span_id: "span-memory-1",
        thread_id: "thread-1",
        status: "error",
        error: "记忆中心加载超时，请检查记忆数据或后端状态。",
        metadata: {
          component: "memory",
          req_id: 17,
          prompt: "secret prompt must not leak",
        },
      }),
    ).toBe(true);
    expect(
      emitFrontendTraceEvent({
        phase: "frontend.warning",
        method: "memory.raw.failed",
        trace_id: "trace-memory-2",
        span_id: "span-memory-2",
        status: "error",
        error: "prompt secret must not leak",
      }),
    ).toBe(true);
    expect(
      emitFrontendTraceEvent({
        phase: "frontend.warning",
        method: "app.render.crash",
        client_route: "app",
        status: "error",
        error: "TypeError:APPROVAL_SUBMIT_TIMEOUT",
        metadata: {
          component: "react.root",
          react_phase: "render",
          crash_fingerprint: "crash-v1-1483443a51ffbe45",
          breadcrumb_trail: "app.bootstrap:app:start",
          message: "private crash message must not leak",
          stack: "private crash stack must not leak",
          component_props: "private props must not leak",
        },
      }),
    ).toBe(true);

    let events = [];
    await waitFor(() => {
      events = byID.mock.calls
        .filter(([, method]) => method === "observability/frontend/ingest")
        .flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(3);
    });
    expect(events[0]).toEqual(
      expect.objectContaining({
        phase: "frontend.warning",
        method: "memory.badge.refresh.failed",
        trace_id: "trace-memory-1",
        span_id: "span-memory-1",
        thread_id: "thread-1",
        status: "error",
        error: "记忆中心加载超时，请检查记忆数据或后端状态。",
        metadata: { component: "memory", req_id: 17 },
      }),
    );
    expect(events[1]).not.toHaveProperty("error");
    expect(events[2]).toEqual(
      expect.objectContaining({
        phase: "frontend.warning",
        method: "app.render.crash",
        client_route: "app",
        status: "error",
        error: "TypeError:APPROVAL_SUBMIT_TIMEOUT",
        metadata: {
          component: "react.root",
          react_phase: "render",
          crash_fingerprint: "crash-v1-1483443a51ffbe45",
          breadcrumb_trail: "app.bootstrap:app:start",
        },
      }),
    );
    const serialized = JSON.stringify(events);
    expect(serialized).not.toContain("secret");
    expect(serialized).not.toContain("prompt");
    expect(serialized).not.toContain("private crash");
    expect(serialized).not.toContain("component_props");
  });

  it("rejects frontend traces with unknown statuses instead of coercing them to ok", async () => {
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

    expect(
      emitFrontendTraceEvent({
        phase: "frontend.warning",
        method: "memory.badge.refresh.failed",
        trace_id: "trace-memory-invalid-status",
        span_id: "span-memory-invalid-status",
        status: "warn",
      }),
    ).toBe(false);
    await waitForTraceFlush();
    expect(byID).not.toHaveBeenCalled();
  });

  it("keeps runtime RPC telemetry metadata while dropping forbidden content", async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
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

    expect(
      emitFrontendTraceEvent({
        phase: "runtime.rpc.pending",
        method: "thread/config/get",
        trace_id: "trace-runtime-1",
        span_id: "span-runtime-1",
        call_id: "7",
        duration_ms: 12,
        status: "ok",
        metadata: {
          req_id: 42,
          pending_count: 3,
          attempt: 1,
          prompt: "secret prompt must not leak",
          content: "secret content must not leak",
        },
      }),
    ).toBe(true);

    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(
        ([, method]) => method === "observability/frontend/ingest",
      );
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    expect(ingestCall[2].events).toEqual([
      expect.objectContaining({
        phase: "runtime.rpc.pending",
        method: "thread/config/get",
        trace_id: "trace-runtime-1",
        span_id: "span-runtime-1",
        call_id: "7",
        duration_ms: 12,
        status: "ok",
        metadata: {
          req_id: 42,
          pending_count: 3,
          attempt: 1,
        },
      }),
    ]);
    const serialized = JSON.stringify(ingestCall[2].events);
    expect(serialized).not.toContain("secret");
    expect(serialized).not.toContain("prompt");
    expect(serialized).not.toContain("content");
  });

  it("pipes runtime shim telemetry into bridge logs without prompt fields", async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === "observability/frontend/ingest")
        return Promise.resolve({ recorded: payload.events.length });
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { registerBridgeLogStore } = await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: "runtime.rpc.send.done",
      method: "thread/config/get",
      trace_id: "trace-runtime-2",
      span_id: "span-runtime-2",
      call_id: "8",
      duration_ms: 2,
      status: "ok",
      req_id: 43,
      pending_count: 1,
      attempt: 1,
      prompt: "secret prompt must not leak",
    });

    const telemetryLog = logs.find(
      (entry) => entry.event === "runtime.rpc.telemetry",
    );
    expect(telemetryLog.fields).toEqual(
      expect.objectContaining({
        phase: "runtime.rpc.send.done",
        method: "thread/config/get",
        call_id: "8",
        duration_ms: 2,
        metadata: {
          req_id: 43,
          pending_count: 1,
          attempt: 1,
        },
      }),
    );
    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(
        ([, method]) => method === "observability/frontend/ingest",
      );
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    const serialized = JSON.stringify([...logs, ingestCall[2].events]);
    expect(serialized).not.toContain("secret");
    expect(serialized).not.toContain("prompt");
  });
});
