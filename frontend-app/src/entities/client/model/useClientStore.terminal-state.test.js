import { vi } from "vitest";

const runtime = vi.hoisted(() => ({
  backend: null,
  bridgeCallback: null,
  bridgeOptions: null,
  runtimeReconnectCallback: null,
}));

vi.mock("../../../shared/api/backendApi.js", async (importOriginal) => {
  const { createClientStoreBackendMock } = await import("./useClientStore.testMockFactory.js");
  return createClientStoreBackendMock({ importOriginal, runtime });
});

import * as backendApi from "../../../shared/api/backendApi.js";
import {
  expect,
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("normalizes Codex raw tokenUsage.last ahead of cumulative tokenUsage.total", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Demo", provider: "codex" }],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "thread/tokenUsage/updated",
    payload: {
      threadId: "thread-1",
      tokenUsage: {
        total: { inputTokens: 4000000, outputTokens: 465418, totalTokens: 4465418 },
        last: { inputTokens: 88502, outputTokens: 557 },
      },
      modelContextWindow: 258400,
    },
  });

  const usage = useClientStore.getState().tokenUsageByThread["thread-1"];
  expect(usage.usedTokens).toBe(89059);
  expect(usage.contextWindowTokens).toBe(258400);
  expect(usage.usedPercent).toBeCloseTo((89059 / 258400) * 100, 6);
});

it("normalizes Codex info.last_token_usage ahead of info.total_token_usage", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Demo", provider: "codex" }],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "thread/tokenUsage/updated",
    payload: {
      threadId: "thread-1",
      info: {
        total_token_usage: { input_tokens: 4000000, output_tokens: 465418, total_tokens: 4465418 },
        last_token_usage: { input_tokens: 88502, output_tokens: 557, total_tokens: 89059 },
        model_context_window: 258400,
      },
    },
  });

  const usage = useClientStore.getState().tokenUsageByThread["thread-1"];
  expect(usage.usedTokens).toBe(89059);
  expect(usage.contextWindowTokens).toBe(258400);
  expect(usage.usedPercent).toBeCloseTo((89059 / 258400) * 100, 6);
});

it("caps legacy token usage percentages without replacing current totals with cumulative totals", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Demo", provider: "codex" }],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "thread/tokenUsage/updated",
    payload: {
      threadId: "thread-1",
      input: 900000,
      output: 50000,
      total_tokens: 950000,
      context_window: 872000,
    },
  });

  expect(useClientStore.getState().tokenUsageByThread["thread-1"]).toEqual({
    usedTokens: 950000,
    contextWindowTokens: 872000,
    usedPercent: 100,
  });
});

it("deduplicates repeated terminal tool ids from one bridge patch", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "9007199254740993124",
      timelineItems: [
        {
          id: "tool:21:file",
          kind: "tool",
          tool: "file",
          status: "completed",
          preview: '{"success":true}',
          output: "stale duplicate result",
          ts: "2026-06-02T08:00:01Z",
        },
        {
          id: "tool:21:file",
          kind: "tool",
          tool: "file",
          status: "completed",
          output: "package codexapp",
          ts: "2026-06-02T08:00:01Z",
        },
      ],
    },
  });

  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  expect(timeline.filter((item) => item.id === "tool:21:file")).toHaveLength(1);
  expect(timeline[0]).toEqual(
    expect.objectContaining({
      id: "tool:21:file",
      status: "completed",
      text: expect.stringContaining("package codexapp"),
    }),
  );
});

it("keys bridge diff patches by nested thread id before agent id", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      agentId: "agent-1",
      thread: { threadId: "thread-1", agentId: "agent-1" },
      diffText: "diff --git a/src/App.jsx b/src/App.jsx",
    },
  });

  const state = useClientStore.getState();
  expect(state.diffTextByThread["thread-1"]).toContain("diff --git");
  expect(state.diffTextByThread["agent-1"]).toBeUndefined();
  expect(state.warningEntries).not.toEqual([expect.objectContaining({ event: "thread.patch.unknown_thread" })]);
});

it("allows matching activeThreadId even when the payload threadId has agent runtime id format", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_1780669491412230000",
    threads: [],
    timelinesByThread: {},
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "agent_1780669491412230000",
      diffText: "some diff text",
    },
  });

  const state = useClientStore.getState();
  expect(state.diffTextByThread["agent_1780669491412230000"]).toBe("some diff text");
  expect(state.warningEntries).not.toEqual([expect.objectContaining({ event: "thread.patch.unknown_thread" })]);
});

it("records tool result timeline items for the runtime log while preserving warnings", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "9007199254740993124",
      timelineItems: [
        {
          id: "tool-grep",
          kind: "tool",
          tool: "mcp__lsp__grep",
          status: "completed",
          preview: '{"total":3,"files":{"src/App.jsx":2}}',
          output: "src/App.jsx: found runtime log",
          ts: "2026-05-30T08:00:00Z",
        },
      ],
    },
  });
  runtime.bridgeCallback({
    type: "api.rpc.failed",
    payload: { method: "thread/config/get", threadId: "thread-1", error: "backend unavailable" },
  });

  const state = useClientStore.getState();
  expect(state.warningEntries).toEqual([expect.objectContaining({ event: "api.rpc.failed" })]);
  expect(state.runtimeResultEntries).toEqual([
    expect.objectContaining({
      event: "tool.result",
      threadId: "thread-1",
      message: expect.stringContaining("grep"),
      detail: "[redacted]",
    }),
  ]);
  expect(state.runtimeResultEntries[0].message).not.toContain("src/App.jsx");
  expect(JSON.stringify(state.runtimeResultEntries[0].fields)).not.toContain("src/App.jsx");
});

it("preserves backend thinking start and duration fields for elapsed-time display", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "9007199254740993125",
      timelineItems: [
        {
          id: "thinking-started-at",
          kind: "thinking",
          text: "grep",
          started_at: "2026-05-30T08:00:00Z",
          duration_ms: 2300,
          done: true,
        },
      ],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({
      id: "thinking-started-at",
      kind: "thinking",
      time: "2026-05-30T08:00:00Z",
      elapsedMs: 2300,
    }),
  ]);
});

it("does not surface stale or sparse thread patch sequences as warnings", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "611",
      timelineItems: [{ id: "assistant-new", kind: "assistant", text: "new patch" }],
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "609",
      timelineItems: [{ id: "assistant-stale", kind: "assistant", text: "stale patch" }],
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "789",
      tokenUsage: { usedTokens: 10, contextWindowTokens: 100 },
    },
  });

  const state = useClientStore.getState();
  expect(state.warningEntries.map((entry) => entry.event)).not.toContain("thread.patch.stale");
  expect(state.warningEntries.map((entry) => entry.event)).not.toContain("thread.patch.gap");
  expect(state.timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "assistant-new", text: "new patch" })]);
  expect(state.tokenUsageByThread["thread-1"]).toEqual(
    expect.objectContaining({
      usedTokens: 10,
    }),
  );
});

it("applies restarted thread patch sequences when generation advances", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      generation: 1,
      sequence: "1",
      timelineItems: [{ id: "assistant-1", kind: "assistant", text: "first generation" }],
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      generation: 1,
      sequence: "2",
      timelineItems: [{ id: "assistant-2", kind: "assistant", text: "second patch" }],
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      generation: 2,
      sequence: "1",
      timelineItems: [{ id: "assistant-restarted", kind: "assistant", text: "restarted generation" }],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ id: "assistant-1", text: "first generation" }),
    expect.objectContaining({ id: "assistant-2", text: "second patch" }),
    expect.objectContaining({ id: "assistant-restarted", text: "restarted generation" }),
  ]);
});

it("rejects stale thread patch generation after restart advances", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      generation: "2",
      sequence: "1",
      timelineItems: [{ id: "assistant-current", kind: "assistant", text: "current generation" }],
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      generation: "1",
      sequence: "99",
      timelineItems: [{ id: "assistant-stale-generation", kind: "assistant", text: "stale generation" }],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "assistant-current", text: "current generation" })]);
});

it("coalesces repeated RPC warning entries while preserving occurrence count", () => {
  useClientStore.getState().addLog("error", "api.rpc.failed", {
    method: "thread/config/get",
    threadId: "thread-1",
    req_id: 1,
    error: { message: "backend unavailable" },
  });
  useClientStore.getState().addLog("error", "api.rpc.failed", {
    method: "thread/config/get",
    threadId: "thread-1",
    req_id: 2,
    error: { message: "backend unavailable" },
  });

  const warnings = useClientStore.getState().warningEntries;
  expect(warnings).toHaveLength(1);
  expect(warnings[0]).toEqual(
    expect.objectContaining({
      event: "api.rpc.failed",
      occurrenceCount: 2,
      fields: expect.objectContaining({
        method: "thread/config/get",
        req_id: 2,
      }),
    }),
  );
});

it("emits failed warning entries to frontend observability traces", () => {
  useClientStore.getState().addWarning("warn", "memory.badge.refresh.failed", {
    error: "记忆中心加载超时，请检查记忆数据或后端状态。",
    traceId: "trace-memory-1",
    spanId: "span-memory-1",
    threadId: "thread-1",
    req_id: 17,
  });

  expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
    expect.objectContaining({
      phase: "frontend.warning",
      method: "memory.badge.refresh.failed",
      trace_id: "trace-memory-1",
      span_id: "span-memory-1",
      thread_id: "thread-1",
      status: "error",
      error: "[redacted]",
      metadata: { component: "memory", req_id: 17 },
    }),
  );
});

it("coalesces repeated backend RPC return entries while preserving occurrence count", () => {
  const resultPreview = JSON.stringify({
    messages: [
      {
        id: 1,
        content: "private prompt body",
        path: "/home/l4place/private-project/secret.txt",
        api_key: "sk-live-secret",
        count: 2,
      },
    ],
    total: 1,
  });
  useClientStore.getState().addLog("debug", "api.rpc.done", {
    method: "thread/messages",
    threadId: "thread-1",
    req_id: 1,
    result: resultPreview,
  });
  useClientStore.getState().addLog("debug", "api.rpc.done", {
    method: "thread/messages",
    threadId: "thread-1",
    req_id: 2,
    result: resultPreview,
  });

  const results = useClientStore.getState().runtimeResultEntries;
  expect(results).toHaveLength(1);
  expect(results[0]).toEqual(
    expect.objectContaining({
      event: "api.rpc.done",
      occurrenceCount: 2,
      fields: expect.objectContaining({
        req_id: 2,
      }),
    }),
  );
  const serializedFields = JSON.stringify(results[0].fields);
  expect(serializedFields).not.toContain("private prompt body");
  expect(serializedFields).not.toContain("/home/l4place");
  expect(serializedFields).not.toContain("sk-live-secret");
  expect(serializedFields).not.toContain("secret.txt");
});

it("integrates large result from bridge producer to client store without crashing and without leaking sensitive values", async () => {
  const hadTraceDebugFlag = Object.prototype.hasOwnProperty.call(window, "__AO_FRONTEND_TRACE_DEBUG__");
  const previousTraceDebugFlag = window.__AO_FRONTEND_TRACE_DEBUG__;

  try {
    window.__AO_FRONTEND_TRACE_DEBUG__ = true;
    const largeResult = {
      api_key: "super-secret-password-123",
      values: Array.from({ length: 900 }, (_, index) => index),
    };

    vi.doMock("/wails/runtime.js", () => ({
      Call: {
        ByID: vi.fn().mockResolvedValue({
          ok: true,
          tool: "mcp__large__tool",
          result: largeResult,
        }),
      },
      Events: { On: vi.fn() },
    }));

    const { callAPI } = await import("../../../shared/api/wailsBridge.js");
    await callAPI("tools/call", { name: "mcp__large__tool" });

    const entries = useClientStore.getState().runtimeResultEntries;
    const entry = entries.find((e) => e.fields?.method === "tools/call");
    expect(entry).toBeDefined();
    expect(entry.detail).toHaveLength(500);
    expect(entry.detail.endsWith("...")).toBe(true);
    expect(entry.detail).not.toContain("super-secret-password-123");
    expect(JSON.stringify(entry.fields)).not.toContain("super-secret-password-123");
  } finally {
    vi.doUnmock("/wails/runtime.js");
    if (hadTraceDebugFlag) {
      window.__AO_FRONTEND_TRACE_DEBUG__ = previousTraceDebugFlag;
    } else {
      delete window.__AO_FRONTEND_TRACE_DEBUG__;
    }
  }
});

it("connects conversation card actions to backend RPCs with explicit cwd", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    activeTurnByThread: {
      "thread-1": { id: "turn-1", threadId: "thread-1", status: "running" },
    },
  });

  await useClientStore.getState().interruptActiveThread();
  await useClientStore.getState().forceCompleteActiveThread();
  await useClientStore.getState().compactActiveThread();
  await useClientStore.getState().recoverActiveThread();
  await useClientStore.getState().renameThread("thread-1", "Renamed");
  await useClientStore.getState().archiveThread("thread-1", true);

  expect(backendApi.interruptTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-1",
    expectedTurnId: "turn-1",
    requestId: expect.any(String),
    source: "ui_stop",
  });
  expect(backendApi.forceCompleteTurn).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  expect(backendApi.compactThread).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  expect(backendApi.recoverThread).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  expect(backendApi.renameThread).toHaveBeenCalledWith({ threadId: "thread-1", name: "Renamed" });
  expect(backendApi.archiveThread).toHaveBeenCalledWith({ threadId: "thread-1" });
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "archivedThreadAtById.thread-1",
    value: expect.any(Number),
  });
});
