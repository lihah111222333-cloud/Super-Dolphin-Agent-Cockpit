import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { createTestWebSocketClass } from "./test-support/wailsBridgeTestWebSocket.js";
import {
  captureBridgeLogs,
  devRuntimeShimModule,
  resetWailsRuntimeMocks,
  runtimeModule,
} from "./wailsBridgeTestSupport.js";

describe("wails bridge warning logs", () => {
  beforeEach(resetWailsRuntimeMocks);

  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__AO_FRONTEND_TRACE_DEBUG__;
    delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  });

  it("fails frontend log batch delivery when the runtime binding is unavailable", async () => {
    vi.doMock(runtimeModule, () => ({
      Call: {},
      Events: { On: vi.fn() },
    }));
    const { sendFrontendLogBatch } = await import("./wailsBridge.js");

    await expect(
      sendFrontendLogBatch([{ level: "error", event: "ui.failed" }]),
    ).rejects.toThrow("frontend log bridge runtime Call.ByID is required");
  });

  it("propagates frontend log batch RPC failures", async () => {
    const error = new Error("log ingest unavailable");
    const byID = vi.fn().mockRejectedValue(error);
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { sendFrontendLogBatch } = await import("./wailsBridge.js");

    await expect(
      sendFrontendLogBatch([{ level: "error", event: "ui.failed" }]),
    ).rejects.toThrow("log ingest unavailable");
    expect(byID).toHaveBeenCalledWith(
      expect.any(Number),
      "ui/log",
      expect.objectContaining({
        entries: [{ level: "error", event: "ui.failed" }],
      }),
    );
  });

  it("reports a failed backend RPC once as api.rpc.failed", async () => {
    const error = new Error("backend unavailable");
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    const rejected = await callAPI("thread/config/get", {
      threadId: "thread-1",
    }).catch((cause) => cause);
    expect(rejected).toBe(error);
    expect(rejected).toMatchObject({
      traceId: expect.stringMatching(/^[0-9a-f]{32}$/),
      trace_id: expect.stringMatching(/^[0-9a-f]{32}$/),
      reqId: expect.any(Number),
      req_id: expect.any(Number),
    });

    const errorEvents = logs
      .filter((entry) => entry.level === "error")
      .map((entry) => entry.event);
    expect(errorEvents).toEqual(["api.rpc.failed"]);
    expect(errorEvents).not.toContain("bridge.call.failed");
    const failure = logs.find((entry) => entry.event === "api.rpc.failed");
    expect(failure.fields).toEqual(
      expect.objectContaining({
        trace_id: rejected.traceId,
        req_id: rejected.reqId,
      }),
    );
  });

  it("uses the logged RPC trace for visible and Health diagnostics without exposing the provider cause", async () => {
    const rawCause = "provider token=secret";
    const error = new Error(rawCause);
    const healthSink = vi.fn();
    const visibleFailureSink = vi.fn();
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const { runUIAction } = await import("../ui/runUIAction.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    const result = runUIAction(
      "fixture.bridge-correlation",
      () => callAPI("thread/config/get", { threadId: "thread-1" }),
      {
        healthSink,
        visibleFailureSink,
      },
    );
    await expect(result).rejects.toBe(error);
    await waitFor(() => expect(visibleFailureSink).toHaveBeenCalledTimes(1));

    const rpcFailure = logs.find((entry) => entry.event === "api.rpc.failed");
    const visibleFailure = visibleFailureSink.mock.calls[0][0].publicError;
    const healthFailure = healthSink.mock.calls[0][0].publicError;
    expect(visibleFailure.diagnosticId).toBe(rpcFailure.fields.trace_id);
    expect(healthFailure.diagnosticId).toBe(rpcFailure.fields.trace_id);
    expect(JSON.stringify([visibleFailure, healthFailure])).not.toContain(
      rawCause,
    );
  });

  it.each(["thread/messages", "ui/state/get"])(
    "fails a stalled %s history sync RPC instead of remaining pending forever",
    async (method) => {
      vi.useFakeTimers();
      try {
        const byID = vi.fn(() => new Promise(() => {}));
        vi.doMock(runtimeModule, () => ({
          Call: { ByID: byID },
          Events: { On: vi.fn() },
        }));
        const { callAPI, registerBridgeLogStore } =
          await import("./wailsBridge.js");
        const { frontendDiagnosticCorrelationForError } =
          await import("../diagnostics/frontendDiagnosticCorrelation.js");
        const logs = captureBridgeLogs(registerBridgeLogStore);

        const rpcOutcome = callAPI(method, {
          cwd: "/workspace",
          threadId: "thread-1",
        }).then(
          () => ({ status: "resolved" }),
          (error) => ({ status: "rejected", error }),
        );
        const testDeadline = new Promise((resolve) => {
          setTimeout(() => resolve({ status: "still-pending" }), 10_001);
        });
        const outcome = Promise.race([rpcOutcome, testDeadline]);

        await vi.advanceTimersByTimeAsync(10_001);

        const resolvedOutcome = await outcome;
        expect(resolvedOutcome).toMatchObject({
          status: "rejected",
          error: {
            name: "BridgeRPCTimeoutError",
            code: "BRIDGE_RPC_TIMEOUT",
            method,
            timeoutMs: 10_000,
          },
        });
        const rejected = resolvedOutcome.error;
        const failure = logs.find((entry) => entry.event === "api.rpc.failed");
        expect(failure.fields).toEqual(expect.objectContaining({
          method,
          trace_id: rejected.traceId,
          error: expect.objectContaining({
            name: "BridgeRPCTimeoutError",
            code: "BRIDGE_RPC_TIMEOUT",
            message: `${method} timed out after 10000ms`,
          }),
        }));
        expect(frontendDiagnosticCorrelationForError(rejected)).toBe(rejected.traceId);
        expect(rejected).toMatchObject({ method, timeoutMs: 10_000 });
        expect(byID.mock.calls.filter(([, calledMethod]) => calledMethod === method)).toHaveLength(1);
      }
      finally {
        vi.useRealTimers();
      }
    },
  );

  it("records failed backend RPC details with a serializable error message", async () => {
    const error = new Error("backend unavailable");
    error.code = "ECONNREFUSED";
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("thread/config/get", { threadId: "thread-1" }),
    ).rejects.toThrow("backend unavailable");

    const failure = logs.find((entry) => entry.event === "api.rpc.failed");
    expect(failure.fields).toEqual(
      expect.objectContaining({
        method: "thread/config/get",
        error: expect.objectContaining({
          message: "backend unavailable",
          code: "ECONNREFUSED",
        }),
      }),
    );
    expect(JSON.stringify(failure.fields)).toContain("backend unavailable");
  });

  it("filters sensitive JSON-RPC error data from api.rpc.failed UI logs", async () => {
    const error = new Error("backend rejected request");
    error.code = -32000;
    error.data = {
      code: "RPC_REJECTED",
      message: "safe backend diagnostic",
      name: "JsonRpcError",
      type: "validation",
      status: 400,
      prompt: "real-prompt-secret",
      params: { userPrompt: "real-params-secret" },
      stack: "real-stack-secret",
      secret: "real-secret",
      token: "real-token",
      password: "real-password",
      apiKey: "real-api-key",
      api_key: "real-api-key-snake",
      auth: "real-auth",
      credential: "real-credential",
      authorization: "Bearer real-authorization",
      authToken: "real-auth-token",
      nested: {
        code: "NESTED_CODE",
        message: "nested diagnostic",
        content: "real-content-secret",
        token: "real-nested-token",
      },
    };
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("thread/start", { prompt: "user prompt" }),
    ).rejects.toThrow("backend rejected request");

    const failure = logs.find((entry) => entry.event === "api.rpc.failed");
    expect(failure.fields.error).toEqual(
      expect.objectContaining({
        message: "backend rejected request",
        code: -32000,
        data: expect.objectContaining({
          code: "[redacted]",
          message: "[redacted]",
          name: "[redacted]",
          type: "[redacted]",
          status: 400,
        }),
      }),
    );
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain("real-");
    expect(serialized).not.toContain("RPC_REJECTED");
    expect(serialized).not.toContain("safe backend diagnostic");
    expect(serialized).not.toContain("JsonRpcError");
    expect(serialized).not.toContain("validation");
    expect(serialized).not.toContain("nested diagnostic");
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

  it("redacts free-text JSON-RPC error data strings from api.rpc.failed UI logs", async () => {
    const error = new Error("backend rejected");
    error.code = -32000;
    error.data = {
      message: "token=real-token password=real-password",
      code: "token=real-code-token",
      status: 400,
    };
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("thread/start", { prompt: "user prompt" }),
    ).rejects.toThrow("backend rejected");

    const failure = logs.find((entry) => entry.event === "api.rpc.failed");
    expect(failure.fields.error).toEqual(
      expect.objectContaining({
        message: "backend rejected",
        code: -32000,
        data: {
          code: "[redacted]",
          message: "[redacted]",
          status: 400,
        },
      }),
    );
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain("real-token");
    expect(serialized).not.toContain("real-password");
    expect(serialized).not.toContain("real-code-token");
    expect(serialized).not.toContain("token=");
    expect(serialized).not.toContain("password=");
  });

  it("redacts JSON-RPC error data strings when runtime rejects with a plain object", async () => {
    const error = {
      message: "backend object rejected",
      code: -32002,
      data: {
        message: "token=real-object-token password=real-object-password",
        code: "token=real-object-code-token",
        status: 401,
        authorization: "Bearer real-object-authorization",
      },
    };
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("thread/start", { prompt: "user prompt" }),
    ).rejects.toMatchObject({
      message: "backend object rejected",
      code: -32002,
    });

    const failure = logs.find((entry) => entry.event === "api.rpc.failed");
    expect(failure.fields.error).toEqual(
      expect.objectContaining({
        message: "backend object rejected",
        code: -32002,
        data: {
          message: "[redacted]",
          code: "[redacted]",
          status: 401,
        },
      }),
    );
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain("real-object-token");
    expect(serialized).not.toContain("real-object-password");
    expect(serialized).not.toContain("real-object-code-token");
    expect(serialized).not.toContain("real-object-authorization");
    expect(serialized).not.toContain("token=");
    expect(serialized).not.toContain("password=");
    expect(serialized).not.toContain("authorization");
  });

  it("redacts primitive JSON-RPC error data from dev runtime UI logs while keeping diagnostics", async () => {
    const sockets = [];
    vi.stubGlobal("WebSocket", createTestWebSocketClass(sockets));
    vi.doMock(runtimeModule, async () => import(devRuntimeShimModule));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    const resultPromise = callAPI("thread/config/get", {
      threadId: "thread-1",
    });
    await waitFor(() => {
      expect(sockets).toHaveLength(1);
    });
    sockets[0].open();
    await waitFor(() => {
      expect(
        sockets[0].sent.some(
          (sent) => JSON.parse(sent).method === "thread/config/get",
        ),
      ).toBe(true);
    });
    const request = sockets[0].sent
      .map((sent) => JSON.parse(sent))
      .find((message) => message.method === "thread/config/get");
    sockets[0].receive({
      jsonrpc: "2.0",
      id: request.id,
      error: {
        code: -32001,
        message: "backend rejected primitive data",
        data: "real-token",
      },
    });

    await expect(resultPromise).rejects.toThrow(
      "backend rejected primitive data",
    );

    const failure = logs.find((entry) => entry.event === "api.rpc.failed");
    expect(failure.fields.error).toEqual(
      expect.objectContaining({
        message: "backend rejected primitive data",
        code: -32001,
        data: "[redacted]",
      }),
    );
    const serialized = JSON.stringify(failure.fields);
    expect(serialized).not.toContain("real-token");
  });

  it("does not write successful RPC lifecycle logs to the UI store by default", async () => {
    vi.doMock(runtimeModule, () => ({
      Call: {
        ByID: vi.fn().mockResolvedValue({
          ok: true,
          tool: "mcp__lsp__grep",
          result: { total: 3 },
        }),
      },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("tools/call", { name: "mcp__lsp__grep" }),
    ).resolves.toEqual({
      ok: true,
      tool: "mcp__lsp__grep",
      result: { total: 3 },
    });

    const events = logs.map((entry) => entry.event);
    expect(events).not.toContain("api.rpc.start");
    expect(events).not.toContain("api.rpc.done");
    expect(events).not.toContain("bridge.call.start");
    expect(events).not.toContain("bridge.call.done");
  });

  it("keeps compact successful RPC diagnostics when frontend trace debug is enabled", async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    vi.doMock(runtimeModule, () => ({
      Call: {
        ByID: vi.fn().mockResolvedValue({
          ok: true,
          tool: "mcp__lsp__grep",
          result: { total: 3 },
        }),
      },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(
      callAPI("tools/call", { name: "mcp__lsp__grep" }),
    ).resolves.toEqual({
      ok: true,
      tool: "mcp__lsp__grep",
      result: { total: 3 },
    });

    const done = logs.find((entry) => entry.event === "api.rpc.done");
    expect(done.fields).toEqual(
      expect.objectContaining({
        method: "tools/call",
        result_preview: expect.stringContaining('"total":3'),
      }),
    );
  });

  it("redacts sensitive successful RPC diagnostic previews before they reach the UI log store", async () => {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    vi.doMock(runtimeModule, () => ({
      Call: {
        ByID: vi.fn().mockResolvedValue({
          ok: true,
          tool: "mcp__secret__read",
          result: {
            total: 3,
            prompt: "real-prompt-secret",
            content: "real-content-secret",
            text: "real-text-secret",
            body: "token=real-body-token",
            profile: { name: "real-profile-secret" },
            cwd: "/home/l4place/private-project",
            path: "/home/l4place/private-project/secret.txt",
            paths: ["/home/l4place/private-project/secret-a.txt"],
            nested: {
              count: 2,
              accessToken: "real-access-token",
              message: "real-message-secret",
            },
          },
        }),
      },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await callAPI("tools/call", { name: "mcp__secret__read" });

    const done = logs.find((entry) => entry.event === "api.rpc.done");
    expect(done.fields.result_preview).toContain('"total":3');
    expect(done.fields.result_preview).toContain('"count":2');
    expect(done.fields.result_preview).not.toContain("real-");
    expect(done.fields.result_preview).not.toContain("/home/l4place");
    expect(done.fields.result_preview).not.toContain('"prompt"');
    expect(done.fields.result_preview).not.toContain('"content"');
    expect(done.fields.result_preview).not.toContain('"body"');
    expect(done.fields.result_preview).not.toContain('"cwd"');
    expect(done.fields.result_preview).not.toContain('"path"');
    expect(done.fields.result_preview).not.toContain('"paths"');
    expect(done.fields.result_preview).not.toContain('"accessToken"');
  });
});
