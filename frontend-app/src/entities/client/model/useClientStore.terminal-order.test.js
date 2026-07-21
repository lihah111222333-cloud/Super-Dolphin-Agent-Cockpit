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
import { EVENT_TYPED_WIRE_METHODS } from "../../../shared/api/eventWireMethods.js";
import {
  deferred,
  expect,
  flushAssistantDeltaBatch,
  flushPromises,
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("retires a pending T1 terminal for an authoritative T2 patch while keeping T1 late events tombstoned", async () => {
  vi.useFakeTimers();
  try {
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
        eventId: "terminal-pending-t1",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "failed",
        publicError: {
          code: "PROVIDER_FAILED",
          title: "Provider failed",
          message: "T1 failed before its partial output arrived",
          diagnosticId: "diag-pending-t1",
          retryable: false,
          recoveryActions: [],
        },
        partialItemIds: ["t1-partial"],
        occurredAt: "2026-07-21T01:00:00Z",
      },
    });
    runtime.bridgeCallback({
      type: "ui/thread/patch",
      payload: {
        threadId: "thread-1",
        sequence: "1",
        status: "running",
        activeTurn: { id: "turn-2", status: "running" },
      },
    });
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-2", itemId: "t2-partial", delta: "T2 partial" },
    });
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-t2",
        threadId: "thread-1",
        turnId: "turn-2",
        outcome: "success",
        occurredAt: "2026-07-21T01:00:01Z",
      },
    });
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-1", itemId: "t1-late", delta: "late T1 content" },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "t2-partial", text: "T2 partial", done: true, turnId: "turn-2" }),
        expect.objectContaining({ kind: "turn_terminal", turnId: "turn-2", terminalOutcome: "success" }),
      ]),
    );
    expect(state.timelinesByThread["thread-1"]).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "turn_terminal", turnId: "turn-1" }), expect.objectContaining({ text: "late T1 content" })]),
    );
    expect(state.warningEntries).toEqual(expect.arrayContaining([expect.objectContaining({ event: "turn.event.late", fields: expect.objectContaining({ turn_id: "turn-1" }) })]));
  } finally {
    vi.useRealTimers();
  }
});

it("rejects a late canonical terminal from a retired scope subscription", async () => {
  const callbacks = [];
  backendApi.onBridgeEvent.mockImplementation((callback, options = {}) => {
    callbacks.push(callback);
    runtime.bridgeCallback = callback;
    runtime.bridgeOptions = options;
    return () => {};
  });
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "A thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  backendApi.getSidebarState.mockResolvedValue({ activeThreadId: "", threads: [] });
  await registerBridgeEventHandlersForTest();
  const aSubscription = runtime.bridgeCallback;

  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).resolves.toBe(true);
  await flushPromises();
  const bSubscription = runtime.bridgeCallback;
  expect(callbacks).toHaveLength(2);
  useClientStore.setState({
    cwd: "/repo/other",
    activeProject: "/repo/other",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "B thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });

  aSubscription({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-from-a",
      threadId: "thread-1",
      turnId: "turn-a",
      outcome: "success",
      occurredAt: "2026-07-21T01:00:00Z",
    },
  });
  bSubscription({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-from-b",
      threadId: "thread-1",
      turnId: "turn-b",
      outcome: "success",
      occurredAt: "2026-07-21T01:00:01Z",
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ kind: "turn_terminal", turnId: "turn-b" }),
  ]);
  expect(useClientStore.getState().warningEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({ event: "bridge.event.scope_stale" }),
  ]));
});

it("replays an A pending terminal after A-B-A without adding scope to the canonical payload", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "A thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  backendApi.setActiveProject
    .mockResolvedValueOnce({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" })
    .mockResolvedValueOnce({ projects: ["/repo/app", "/repo/other"], active: "/repo/app" });
  backendApi.getSidebarState.mockResolvedValue({ activeThreadId: "", threads: [] });
  await registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-a-pending",
      threadId: "thread-1",
      turnId: "turn-a",
      outcome: "failed",
      publicError: {
        code: "PROVIDER_FAILED",
        title: "Provider failed",
        message: "A terminal arrived before its completed item",
        diagnosticId: "diag-a-pending",
        retryable: false,
        recoveryActions: [],
      },
      partialItemIds: ["a-partial"],
      occurredAt: "2026-07-21T01:00:00Z",
    },
  });
  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).resolves.toBe(true);
  await expect(useClientStore.getState().setActiveProjectPath("/repo/app")).resolves.toBe(true);
  await flushPromises();
  useClientStore.setState({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "A thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-a",
      item: { id: "a-partial", type: "assistant", content: "A replayed answer" },
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual(expect.arrayContaining([
    expect.objectContaining({ id: "a-partial", text: "A replayed answer", done: true }),
    expect.objectContaining({ kind: "turn_terminal", turnId: "turn-a", terminalOutcome: "failed" }),
  ]));
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    scopeCapacity: 8,
    scopeCount: 2,
    terminalStates: 1,
    totalTerminalStates: 1,
  });
});

it("fails closed without switching scope when the bounded scope ledger is exhausted", async () => {
  const scopes = ["/repo/app", ...Array.from({ length: 8 }, (_, index) => `/repo/scope-${index + 1}`)];
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: scopes,
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  backendApi.setActiveProject.mockImplementation(({ path }) => Promise.resolve({ projects: scopes, active: path }));
  backendApi.getSidebarState.mockResolvedValue({ activeThreadId: "", threads: [] });
  await registerBridgeEventHandlersForTest();

  for (const scope of scopes.slice(1, 8)) {
    await expect(useClientStore.getState().setActiveProjectPath(scope)).resolves.toBe(true);
  }
  await flushPromises();
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({ scopeCapacity: 8, scopeCount: 8 });

  const retainedScope = scopes[7];
  const retainedSubscription = runtime.bridgeCallback;
  await expect(useClientStore.getState().setActiveProjectPath(scopes[8])).rejects.toThrow(
    "frontend-app: turn terminal scope ledger capacity exhausted",
  );
  expect(useClientStore.getState().activeProject).toBe(retainedScope);
  expect(backendApi.setActiveProject).toHaveBeenCalledTimes(7);
  expect(backendApi.setActiveProject).not.toHaveBeenCalledWith(expect.objectContaining({ path: scopes[8] }));

  useClientStore.setState({
    activeProject: retainedScope,
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  retainedSubscription({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-after-scope-exhaustion",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      occurredAt: "2026-07-21T01:00:00Z",
    },
  });

  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({ scopeCapacity: 8, scopeCount: 8 });
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual(expect.arrayContaining([
    expect.objectContaining({ kind: "turn_terminal", turnId: "turn-1" }),
  ]));
  expect(useClientStore.getState().warningEntries).not.toEqual(expect.arrayContaining([
    expect.objectContaining({ event: "bridge.event.scope_stale" }),
  ]));
});

it("keeps a retired terminal sealed across CWD return and retired-cache eviction without poisoning another scope", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  const otherProjectChange = deferred();
  const otherSidebarRefresh = deferred();
  const appProjectChange = deferred();
  const appSidebarRefresh = deferred();
  backendApi.setActiveProject.mockReturnValueOnce(otherProjectChange.promise).mockReturnValueOnce(appProjectChange.promise);
  backendApi.getSidebarState.mockReturnValueOnce(otherSidebarRefresh.promise).mockReturnValueOnce(appSidebarRefresh.promise);
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-pending-t1",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "failed",
      publicError: {
        code: "PROVIDER_FAILED",
        title: "Provider failed",
        message: "T1 failed before its partial output arrived",
        diagnosticId: "diag-pending-t1",
        retryable: false,
        recoveryActions: [],
      },
      partialItemIds: ["t1-partial"],
      occurredAt: "2026-07-21T01:00:00Z",
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      status: "running",
      activeTurn: { id: "turn-2", status: "running" },
    },
  });
  runtime.bridgeCallback({
    type: "turn/output/delta",
    payload: { threadId: "thread-1", turnId: "turn-2", itemId: "t2-partial", delta: "T2 partial" },
  });
  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-t2",
      threadId: "thread-1",
      turnId: "turn-2",
      outcome: "success",
      occurredAt: "2026-07-21T01:00:01Z",
    },
  });
  useClientStore.setState({ activeTurnByThread: {} });

  for (let index = 3; index < 68; index++) {
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: {
        threadId: "thread-1",
        turnId: `turn-${index}`,
        itemId: `partial-${index}`,
        delta: `turn ${index}`,
      },
    });
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: `terminal-${index}`,
        threadId: "thread-1",
        turnId: `turn-${index}`,
        outcome: "success",
        occurredAt: "2026-07-21T01:00:01Z",
      },
    });
  }

  const switchToOther = useClientStore.getState().setActiveProjectPath("/repo/other");
  await vi.waitFor(() => {
    expect(backendApi.setActiveProject).toHaveBeenCalledTimes(1);
  });
  useClientStore.setState({
    cwd: "/repo/other",
    activeProject: "/repo/other",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Other project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  await useClientStore.getState().deleteStaleThreads(["thread-1"]);
  useClientStore.setState({
    cwd: "/repo/other",
    activeProject: "/repo/other",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Other project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-other-scope-t1",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      occurredAt: "2026-07-21T01:00:02Z",
    },
  });
  expect(useClientStore.getState().warningEntries).not.toEqual(
    expect.arrayContaining([expect.objectContaining({ event: "turn.terminal.stale", fields: expect.objectContaining({ turn_id: "turn-1" }) })]),
  );
  await useClientStore.getState().deleteStaleThreads(["thread-1"]);
  otherSidebarRefresh.resolve({ activeThreadId: "", threads: [] });
  otherProjectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  await expect(switchToOther).resolves.toBe(true);

  const switchToApp = useClientStore.getState().setActiveProjectPath("/repo/app");
  await vi.waitFor(() => {
    expect(backendApi.setActiveProject).toHaveBeenCalledTimes(2);
  });
  useClientStore.setState({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "App project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  runtime.bridgeCallback({
    type: "turn/output/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      itemId: "late-t1",
      delta: "late T1 content",
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).not.toEqual(expect.arrayContaining([expect.objectContaining({ id: "late-t1" })]));
  expect(useClientStore.getState().warningEntries).toEqual(
    expect.arrayContaining([expect.objectContaining({ event: "turn.event.late", fields: expect.objectContaining({ turn_id: "turn-1" }) })]),
  );
  appSidebarRefresh.resolve({ activeThreadId: "", threads: [] });
  appProjectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/app" });
  await expect(switchToApp).resolves.toBe(true);
});

it("does not flush or finalize a newer buffered turn when an older terminal arrives before the active-turn patch", async () => {
  vi.useFakeTimers();
  try {
    const actionNotice = { message: "旧轮仍显示运行中", tone: "info" };
    const timeline = [{ id: "turn-1-open", role: "assistant", kind: "assistant", text: "old", done: false, turnId: "turn-1" }];
    const activityEntries = [];
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      actionNotice,
      activityEntries,
      timelinesByThread: { "thread-1": timeline },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-2", itemId: "turn-2-open", delta: "new turn" },
    });
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-late-turn-1",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "success",
        occurredAt: "2026-07-16T01:00:00Z",
      },
    });

    const beforeFlush = useClientStore.getState();
    expect(beforeFlush.timelinesByThread["thread-1"]).toBe(timeline);
    expect(beforeFlush.actionNotice).toBe(actionNotice);
    expect(beforeFlush.activityEntries).toBe(activityEntries);
    expect(beforeFlush.warningEntries).toEqual([
      expect.objectContaining({
        event: "turn.terminal.stale",
        fields: expect.objectContaining({ eventName: "turn/terminal", turn_id: "turn-1" }),
        occurrenceCount: 1,
      }),
    ]);
    expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
      expect.objectContaining({
        phase: "frontend.turn_event.rejected",
        method: "turn.terminal.stale",
        turn_id: "turn-1",
      }),
    );

    await flushAssistantDeltaBatch();
    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ id: "turn-1-open", done: false, turnId: "turn-1" }),
      expect.objectContaining({ id: "turn-2-open", done: false, turnId: "turn-2" }),
    ]);
  } finally {
    vi.useRealTimers();
  }
});

it("cleans a pending terminal when a thread lifecycle ends", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
      timelinesByThread: { "thread-1": [] },
    });
    registerBridgeEventHandlersForTest();
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-before-delete-without-delta",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "failed",
        publicError: {
          code: "PROVIDER_FAILED",
          title: "运行失败",
          message: "删除前缺失部分响应",
          diagnosticId: "diag-before-delete",
          retryable: false,
          recoveryActions: [],
        },
        partialItemIds: ["partial-1"],
        occurredAt: "2026-07-19T01:00:00Z",
      },
    });

    await useClientStore.getState().deleteStaleThreads(["thread-1"]);
    useClientStore.setState({
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Recreated", provider: "codex", status: "running" }],
      timelinesByThread: { "thread-1": [] },
      actionNotice: null,
      activityEntries: [],
      warningEntries: [],
    });
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-1", itemId: "partial-1", delta: "new lifecycle partial" },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "partial-1", text: "new lifecycle partial", done: false })]);
    expect(state.timelinesByThread["thread-1"]).not.toEqual(expect.arrayContaining([expect.objectContaining({ kind: "turn_terminal" })]));
    expect(state.actionNotice).toBeNull();
  } finally {
    vi.useRealTimers();
  }
});

it("evicts sealed terminal state when a thread lifecycle ends", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();
  const terminal = {
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-before-delete",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      occurredAt: "2026-07-16T01:00:00Z",
    },
  };
  runtime.bridgeCallback(terminal);

  await useClientStore.getState().deleteStaleThreads(["thread-1"]);
  useClientStore.setState({
    activeThreadId: "thread-1",
    activeTurnByThread: { "thread-1": { id: "turn-2", status: "running" } },
    threads: [{ id: "thread-1", name: "Recreated", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
    actionNotice: null,
    activityEntries: [],
    warningEntries: [],
  });
  backendApi.emitFrontendTraceEvent.mockClear();

  runtime.bridgeCallback(terminal);

  expect(useClientStore.getState()).toMatchObject({
    actionNotice: null,
    activityEntries: [],
    warningEntries: [
      expect.objectContaining({
        event: "turn.terminal.stale",
        fields: expect.objectContaining({ eventName: "turn/terminal", turn_id: "turn-1" }),
        occurrenceCount: 1,
      }),
    ],
    timelinesByThread: { "thread-1": [] },
  });
  expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
    expect.objectContaining({
      phase: "frontend.turn_event.rejected",
      method: "turn.terminal.stale",
      thread_id: "thread-1",
      turn_id: "turn-1",
    }),
  );
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
