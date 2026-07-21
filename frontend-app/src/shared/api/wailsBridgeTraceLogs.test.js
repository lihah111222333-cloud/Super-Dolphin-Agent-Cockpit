import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  captureBridgeLogs,
  resetWailsRuntimeMocks,
  runtimeModule,
} from "./wailsBridgeTestSupport.js";

describe("wails bridge RPC trace log fields", () => {
  beforeEach(resetWailsRuntimeMocks);

  afterEach(() => {
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
  });

  it("injects W3C trace metadata into backend RPC payloads", async () => {
    const byID = vi.fn().mockResolvedValue({ ok: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI } = await import("./wailsBridge.js");

    await expect(
      callAPI("thread/config/get", { threadId: "thread-1" }),
    ).resolves.toEqual({ ok: true });

    const payload = byID.mock.calls[0][2];
    expect(payload).toEqual(
      expect.objectContaining({
        threadId: "thread-1",
        _aoTraceparent: expect.stringMatching(
          /^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/,
        ),
        _aoTraceId: expect.stringMatching(/^[0-9a-f]{32}$/),
        _aoSpanId: expect.stringMatching(/^[0-9a-f]{16}$/),
      }),
    );
    const [, traceId, spanId] = payload._aoTraceparent.match(
      /^00-([0-9a-f]{32})-([0-9a-f]{16})-01$/,
    );
    expect(payload._aoTraceId).toBe(traceId);
    expect(payload._aoSpanId).toBe(spanId);
  });

  it("records trace identifiers in debug success logs and backend RPC failure logs", async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    let appRPCCount = 0;
    const byID = vi.fn((_methodID, method, payload) => {
      if (method === "observability/frontend/ingest") {
        return Promise.resolve({ recorded: payload.events.length });
      }
      appRPCCount += 1;
      if (appRPCCount === 1) return Promise.resolve({ ok: true });
      return Promise.reject(new Error("backend unavailable"));
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("tools/call", { name: "mcp__lsp__grep" }),
    ).resolves.toEqual({ ok: true });
    const appCalls = () =>
      byID.mock.calls.filter(
        ([, method]) => method !== "observability/frontend/ingest",
      );
    const successPayload = appCalls()[0][2];
    const successStart = logs.find(
      (entry) =>
        entry.event === "api.rpc.start" && entry.fields.method === "tools/call",
    );
    const successDone = logs.find(
      (entry) =>
        entry.event === "api.rpc.done" && entry.fields.method === "tools/call",
    );
    expect(successStart.fields).toEqual(
      expect.objectContaining({
        trace_id: successPayload._aoTraceId,
        span_id: successPayload._aoSpanId,
      }),
    );
    expect(successDone.fields).toEqual(
      expect.objectContaining({
        trace_id: successPayload._aoTraceId,
        span_id: successPayload._aoSpanId,
      }),
    );

    await expect(
      callAPI("thread/config/get", { threadId: "thread-1" }),
    ).rejects.toThrow("backend unavailable");
    const failedPayload = appCalls()[1][2];
    const failed = logs.find(
      (entry) =>
        entry.event === "api.rpc.failed" &&
        entry.fields.method === "thread/config/get",
    );
    expect(failed.fields).toEqual(
      expect.objectContaining({
        trace_id: failedPayload._aoTraceId,
        span_id: failedPayload._aoSpanId,
      }),
    );
  });
});
