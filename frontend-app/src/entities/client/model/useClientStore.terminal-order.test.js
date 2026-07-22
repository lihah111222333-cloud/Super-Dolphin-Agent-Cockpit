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
import { resetClientStoreForTests, useClientStore } from "./useClientStore.js";

function deferred() {
  let reject;
  let resolve;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}

async function flushPromises(count = 8) {
  for (let index = 0; index < count; index += 1) await Promise.resolve();
}

async function flushAssistantDeltaBatch() {
  vi.advanceTimersByTime(50);
  await flushPromises();
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
  appSidebarRefresh.resolve({ activeThreadId: "", threads: [] });
  appProjectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/app" });
  await expect(switchToApp).resolves.toBe(true);
  useClientStore.setState({
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
  expect(useClientStore.getState().warningEntries).toEqual([]);
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
