import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import {
  captureBridgeLogs,
  resetFrontendTraceEmitter,
  runtimeModule,
  waitForTraceFlush,
} from "./wailsBridgeTestSupport.js";

describe("wails bridge frontend trace defaults", () => {
  beforeEach(resetFrontendTraceEmitter);

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it("does not remote flush successful debug-level RPC traces by default", async () => {
    const byID = vi.fn().mockResolvedValue({ ok: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    await expect(
      callAPI("thread/config/get", { threadId: "thread-1" }),
    ).resolves.toEqual({ ok: true });
    await waitForTraceFlush();

    expect(
      byID.mock.calls.some(
        ([, method]) => method === "observability/frontend/ingest",
      ),
    ).toBe(false);
  });

  it("remote flushes default runtime timing traces without logging normal ok telemetry locally", async () => {
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
      phase: "runtime.rpc.pending",
      method: "thread/config/get",
      trace_id: "trace-runtime-default",
      span_id: "span-runtime-default",
      call_id: "12",
      duration_ms: 17,
      status: "ok",
      req_id: 55,
      pending_count: 1,
      prompt: "secret prompt must not leak",
    });
    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: "runtime.rpc.send.done",
      method: "thread/config/get",
      trace_id: "trace-runtime-default",
      span_id: "span-runtime-default",
      call_id: "12",
      duration_ms: 3,
      status: "ok",
      req_id: 55,
      pending_count: 1,
      attempt: 1,
      prompt: "secret prompt must not leak",
    });
    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: "runtime.rpc.settled",
      method: "thread/config/get",
      trace_id: "trace-runtime-default",
      span_id: "span-runtime-default",
      call_id: "12",
      duration_ms: 24,
      status: "ok",
      req_id: 55,
      pending_count: 0,
      content: "secret content must not leak",
    });

    let events = [];
    await waitFor(() => {
      events = byID.mock.calls
        .filter(([, method]) => method === "observability/frontend/ingest")
        .flatMap(([, , payload]) => payload.events);
      expect(events).toHaveLength(3);
    });
    expect(events.map((event) => event.phase)).toEqual([
      "runtime.rpc.pending",
      "runtime.rpc.send.done",
      "runtime.rpc.settled",
    ]);
    expect(events.every((event) => event.status === "ok")).toBe(true);
    expect(events[0]).toEqual(
      expect.objectContaining({
        phase: "runtime.rpc.pending",
        duration_ms: 17,
        metadata: {
          req_id: 55,
          pending_count: 1,
        },
      }),
    );
    expect(events[1].metadata).toEqual({
      req_id: 55,
      pending_count: 1,
      attempt: 1,
    });
    expect(events[2].metadata).toEqual({
      req_id: 55,
      pending_count: 0,
    });
    expect(
      logs.filter((entry) => entry.event === "runtime.rpc.telemetry"),
    ).toHaveLength(0);
    expect(JSON.stringify(events)).not.toContain("secret");
    expect(JSON.stringify(events)).not.toContain("prompt");
    expect(JSON.stringify(events)).not.toContain("content");

    window.__AO_WAILS_RUNTIME_TELEMETRY__({
      phase: "runtime.rpc.timeout",
      method: "thread/config/get",
      trace_id: "trace-runtime-default",
      span_id: "span-runtime-default",
      call_id: "13",
      duration_ms: 30000,
      status: "error",
      error: "timeout",
      req_id: 56,
      pending_count: 0,
    });

    await waitFor(() => {
      const telemetryLogs = logs.filter(
        (entry) => entry.event === "runtime.rpc.telemetry",
      );
      expect(telemetryLogs).toHaveLength(1);
      expect(telemetryLogs[0]).toEqual(
        expect.objectContaining({
          level: "warn",
          fields: expect.objectContaining({
            phase: "runtime.rpc.timeout",
            status: "error",
            error: "timeout",
          }),
        }),
      );
    });
  });

  it("marks slow successful RPC done traces as slow when remote flushing", async () => {
    let now = 0;
    vi.spyOn(performance, "now").mockImplementation(() => now);
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === "observability/frontend/ingest")
        return Promise.resolve({ recorded: payload.events.length });
      now = 1000;
      return Promise.resolve({ ok: true });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    await expect(callAPI("observability/recent/list", {})).resolves.toEqual({
      ok: true,
    });

    let ingestCall;
    await waitFor(() => {
      ingestCall = byID.mock.calls.find(
        ([, method]) => method === "observability/frontend/ingest",
      );
      expect(ingestCall?.[2]?.events).toHaveLength(1);
    });
    expect(ingestCall[2].events).toEqual([
      expect.objectContaining({
        phase: "frontend.rpc.done",
        method: "observability/recent/list",
        duration_ms: 1000,
        status: "slow",
      }),
    ]);
  });

  it("rejects invalid bridge clock timestamps before emitting fake durations", async () => {
    vi.spyOn(performance, "now").mockReturnValue(Number.NaN);
    const byID = vi.fn().mockResolvedValue({ ok: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    await expect(
      callAPI("observability/recent/list", {}),
    ).rejects.toMatchObject({
      name: "BridgeClockUnavailableError",
    });
    expect(byID).not.toHaveBeenCalled();
  });
});
