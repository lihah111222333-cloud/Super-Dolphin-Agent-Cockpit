import { beforeEach, expect, it, vi } from "vitest";

const runtime = vi.hoisted(() => ({
  backend: {
    addProject: vi.fn(),
    deleteThread: vi.fn(),
    emitFrontendTraceEvent: vi.fn(),
    getProjects: vi.fn(),
    getSidebarState: vi.fn(),
    getThreadMessages: vi.fn(),
    getThreadState: vi.fn(),
    getWindowBootstrap: vi.fn(),
    onBridgeEvent: vi.fn((callback, options = {}) => {
      runtime.bridgeCallback = callback;
      runtime.bridgeOptions = options;
      return () => {
        if (runtime.bridgeCallback === callback) {
          runtime.bridgeCallback = null;
          runtime.bridgeOptions = null;
        }
      };
    }),
    onRuntimeReconnect: vi.fn((callback) => {
      runtime.runtimeReconnectCallback = callback;
      return () => {
        runtime.runtimeReconnectCallback = null;
      };
    }),
    readConfig: vi.fn(),
    setActiveProject: vi.fn(),
    setPreference: vi.fn(),
  },
  bridgeCallback: null,
  bridgeOptions: null,
  runtimeReconnectCallback: null,
}));

vi.mock("../../../shared/api/backendApi.js", async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    ...runtime.backend,
    registerBridgeLogStore: actual.registerBridgeLogStore,
    sendFrontendLogBatch: vi.fn(),
  };
});

import * as backendApi from "../../../shared/api/backendApi.js";
import { EVENT_TYPED_WIRE_METHODS } from "../../../shared/api/eventWireMethods.js";
import {
  publishVisibleActionFailure,
  resetVisibleActionFailureForTest,
  visibleActionFailureSnapshot,
} from "../../../shared/ui/actionFailureSink.js";
import { resetClientStoreForTests, useClientStore } from "./useClientStore.js";

async function flushPromises(count = 8) {
  for (let index = 0; index < count; index += 1) await Promise.resolve();
}

function registerBridgeEventHandlersForTest() {
  const initialization = useClientStore.getState().initializeEvents();
  void initialization.catch((error) => {
    if (error?.message !== "runtime event initialization superseded") throw error;
  });
  return initialization;
}

beforeEach(() => {
  vi.clearAllMocks();
  runtime.bridgeCallback = null;
  runtime.bridgeOptions = null;
  runtime.runtimeReconnectCallback = null;
  resetClientStoreForTests();
  resetVisibleActionFailureForTest();
  runtime.backend.readConfig.mockResolvedValue({ cwd: "/repo/app" });
  runtime.backend.getWindowBootstrap.mockResolvedValue({ snapshot: null });
  runtime.backend.getProjects.mockResolvedValue({ projects: ["/repo/app"], active: "/repo/app" });
  runtime.backend.setActiveProject.mockResolvedValue({ projects: ["/repo/app"], active: "/repo/app" });
  runtime.backend.addProject.mockResolvedValue({ projects: ["/repo/app"], active: "/repo/app" });
  runtime.backend.deleteThread.mockResolvedValue({ ok: true });
  runtime.backend.setPreference.mockResolvedValue({ ok: true });
  runtime.backend.getSidebarState.mockResolvedValue({ activeThreadId: "", threads: [] });
  runtime.backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
  runtime.backend.getThreadMessages.mockResolvedValue({ messages: [] });
});

it("rejects legacy or malformed terminal payloads into a visible contract error sink", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/completed",
    payload: { threadId: "thread-1", turnId: "turn-1", success: true },
  });

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(
    expect.objectContaining({
      tone: "error",
      message: "响应契约错误",
    }),
  );
  expect(state.warningEntries).toEqual([expect.objectContaining({ event: "turn.terminal.contract_invalid" })]);
  expect(state.timelinesByThread["thread-1"]).toEqual([]);
});

it("clears a scoped interrupt failure when the same thread reaches terminal state", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
    statuses: { "thread-1": { status: "running" } },
    activeTurnByThread: { "thread-1": { id: "turn-1", threadId: "thread-1", status: "running" } },
    timelinesByThread: { "thread-1": [] },
  });
  publishVisibleActionFailure({ actionId: "thread.interrupt", threadId: "thread-1", publicError: {} });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-clears-interrupt",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      publicSummary: "done",
      occurredAt: "2026-08-12T01:00:00Z",
    },
  });

  expect(visibleActionFailureSnapshot()).toBeNull();
});

it("seals the local turn and closes thinking after the bridged final reply completes", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
    statuses: { "thread-1": { status: "running", interruptible: true } },
    activeTurnByThread: {
      "thread-1": {
        id: "turn-local",
        threadId: "thread-1",
        status: "thinking",
      },
    },
    timelinesByThread: {
      "thread-1": [{
        id: "thinking:turn-local",
        role: "assistant",
        kind: "thinking",
        turnId: "turn-local",
        status: "running",
        done: false,
      }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-local",
      item: { id: "assistant-final-turn-local", type: "assistant", content: "done" },
    },
  });
  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-local-turn",
      threadId: "thread-1",
      turnId: "turn-local",
      outcome: "success",
      publicSummary: "done",
      partialItemIds: ["assistant-final-turn-local"],
      occurredAt: "2026-08-12T01:02:03Z",
    },
  });

  const state = useClientStore.getState();
  expect(state.activeTurnByThread).not.toHaveProperty("thread-1");
  expect(state.statuses["thread-1"]).toEqual(expect.objectContaining({ status: "completed", interruptible: false }));
  expect(state.timelinesByThread["thread-1"]).toEqual(expect.arrayContaining([
    expect.objectContaining({ id: "thinking:turn-local", turnId: "turn-local", done: true }),
    expect.objectContaining({ id: "assistant-final-turn-local", turnId: "turn-local", text: "done", done: true }),
    expect.objectContaining({ kind: "turn_terminal", turnId: "turn-local", terminalOutcome: "success" }),
  ]));
  expect(state.timelinesByThread["thread-1"].some(
    (item) => item.turnId === "turn-local" && item.done === false,
  )).toBe(false);
});

it("routes every typed Wails wire method without treating lifecycle events as legacy turn terminals", () => {
  for (const method of EVENT_TYPED_WIRE_METHODS) {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
      timelinesByThread: { "thread-1": [] },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      type: method,
      payload: method === "turn/terminal"
        ? {
          schemaVersion: 2,
          eventId: "typed-wire-terminal",
          threadId: "thread-1",
          turnId: "turn-1",
          outcome: "success",
          publicSummary: "Public success summary",
          occurredAt: "2026-07-21T01:00:00Z",
        }
        : method === "task/node/statusChanged"
          ? {
            dag_key: "typed-wire-dag",
            run_key: "typed-wire-run",
            node_key: "typed-wire-node",
            new_status: "running",
          }
          : {
            threadId: "thread-1",
            turnId: "turn-1",
            input_tokens: 1,
            output_tokens: 1,
            context_window: 100,
          },
    });

    expect(useClientStore.getState().warningEntries.some(
      (entry) => entry.event === "turn.terminal.contract_invalid",
    )).toBe(false);
  }
});

it.each([
  ["thread/stopped", "stopped", { status: "completed", reason: "Authorization: Bearer raw-thread-secret" }],
  ["agent/stopped", "stopped", { reason: "Authorization: Bearer raw-agent-secret" }],
  ["agent/failed", "error", { error: "Authorization: Bearer raw-failure-secret /private/agent.log" }],
])("applies %s lifecycle state, refreshes the sidebar, and keeps diagnostics safe", async (method, status, lifecyclePayload) => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", agentId: "agent-1", name: "Existing", provider: "codex", status: "running" }],
    statuses: { "thread-1": { status: "running" } },
    activeTurnByThread: { "thread-1": { id: "turn-1", threadId: "thread-1", status: "running" } },
  });
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", agentId: "agent-1", name: "Existing", provider: "codex", status }],
  });
  registerBridgeEventHandlersForTest();
  backendApi.getSidebarState.mockClear();

  const event = { type: method, payload: { threadId: "thread-1", agentId: "agent-1", ...lifecyclePayload } };
  runtime.bridgeCallback(event);
  runtime.bridgeCallback(event);
  await flushPromises(16);

  const state = useClientStore.getState();
  expect(state.threads).toEqual([expect.objectContaining({ id: "thread-1", status })]);
  expect(state.statuses["thread-1"]).toEqual(expect.objectContaining({ status }));
  expect(state.activeTurnByThread).not.toHaveProperty("thread-1");
  expect(backendApi.getSidebarState).toHaveBeenCalledTimes(1);
  expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  expect(state.warningEntries.some((entry) => entry.event === "turn.terminal.contract_invalid")).toBe(false);

  if (method === "agent/failed") {
    expect(state.actionNotice).toEqual(expect.objectContaining({ message: "代理运行失败", tone: "error" }));
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        event: "agent.lifecycle.failed",
        level: "error",
        occurrenceCount: 1,
        fields: expect.objectContaining({ agent_id: "agent-1", reason: "agent_failed" }),
      }),
    ]);
    expect(state.warningEntries[0].fields).not.toHaveProperty("error");
    expect(JSON.stringify({ warnings: state.warningEntries, traces: backendApi.emitFrontendTraceEvent.mock.calls }))
      .not.toMatch(/raw-failure-secret|\/private\/agent\.log/);
  } else {
    expect(state.warningEntries).toEqual([]);
  }
});

it("notifies once when an error patch arrives before duplicate agent failure events", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        agentId: "agent-1",
        name: "Existing",
        provider: "codex",
        status: "error",
      },
    ],
    statuses: { "thread-1": { status: "error" } },
    activeTurnByThread: {},
  });
  registerBridgeEventHandlersForTest();
  backendApi.getSidebarState.mockClear();
  const event = {
    type: "agent/failed",
    payload: {
      threadId: "thread-1",
      agentId: "agent-1",
      sessionId: "session-1",
      error: "Authorization: Bearer raw-failure-secret /private/agent.log",
    },
  };

  runtime.bridgeCallback(event);
  runtime.bridgeCallback(event);
  await flushPromises(8);

  const state = useClientStore.getState();
  expect(backendApi.getSidebarState).not.toHaveBeenCalled();
  expect(state.actionNotice).toEqual(
    expect.objectContaining({ message: "代理运行失败", tone: "error" }),
  );
  expect(state.warningEntries).toEqual([
    expect.objectContaining({
      event: "agent.lifecycle.failed",
      level: "error",
      occurrenceCount: 1,
      fields: expect.objectContaining({
        agent_id: "agent-1",
        reason: "agent_failed",
      }),
    }),
  ]);
  expect(
    JSON.stringify({
      notice: state.actionNotice,
      warnings: state.warningEntries,
      traces: backendApi.emitFrontendTraceEvent.mock.calls,
    }),
  ).not.toMatch(/raw-failure-secret|\/private\/agent\.log/);
});

it.each([
  ["cancelled", "本轮已取消"],
  ["interrupted", "本轮已中断"],
])("keeps a user-requested %s terminal visibly non-successful", (outcome, message) => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: `terminal-${outcome}-1`,
      threadId: "thread-1",
      turnId: "turn-1",
      outcome,
      terminationCause: "user_request",
      terminationRequestId: "stop-1",
      occurredAt: "2026-07-16T01:00:00Z",
    },
  });

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(expect.objectContaining({ tone: "info", message }));
  expect(state.actionNotice.tone).not.toBe("success");
  expect(state.timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ kind: "turn_terminal", terminalOutcome: outcome })]);
  expect(state.warningEntries).toEqual([]);
});

it("routes malformed bridge event parse failures into visible warnings", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "bridge.event.parse_failed",
    payload: {
      eventName: "bridge-event",
      error: "Unexpected end of JSON input",
      rawLen: 10,
      rawPreview: '{"method":',
    },
  });

  expect(useClientStore.getState().warningEntries).toEqual([
    expect.objectContaining({
      level: "error",
      event: "bridge.event.parse_failed",
      fields: expect.objectContaining({
        eventName: "bridge-event",
        error: "[redacted]",
        rawLen: 10,
      }),
    }),
  ]);
  expect(useClientStore.getState().warningEntries[0].fields).not.toHaveProperty("rawPreview");
});

it("routes bridge events without a method into visible warnings", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({ payload: { source: "runtime", rawPreview: "{}" } });

  expect(useClientStore.getState().warningEntries).toEqual([
    expect.objectContaining({
      level: "error",
      event: "bridge.event.method_missing",
      fields: expect.objectContaining({
        payloadKeys: ["source", "rawPreview"],
      }),
    }),
  ]);
  expect(useClientStore.getState().warningEntries[0].fields).not.toHaveProperty("payload");
});

it("normalizes legacy token usage pushes like the Vue frontend", () => {
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
      input_tokens: 40000,
      output_tokens: 2000,
      context_window: 258400,
    },
  });

  const usage = useClientStore.getState().tokenUsageByThread["thread-1"];
  expect(usage.usedTokens).toBe(42000);
  expect(usage.contextWindowTokens).toBe(258400);
  expect(usage.usedPercent).toBeCloseTo((42000 / 258400) * 100, 6);
});
