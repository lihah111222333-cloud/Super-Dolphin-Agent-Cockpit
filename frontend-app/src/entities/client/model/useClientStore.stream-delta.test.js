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

import { expect, it, registerBridgeEventHandlersForTest, registerClientStoreTestHooks, resetClientStoreForTests, useClientStore } from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("never publishes success before a failed terminal after item completion", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [{ id: "assistant-open", role: "assistant", text: "partial", status: "running", turnId: "turn-1" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "assistant-open", role: "assistant", text: "partial" },
    },
  });
  expect(useClientStore.getState().actionNotice).toBeNull();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-failed-1",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "failed",
      publicError: {
        code: "PROVIDER_FAILED",
        title: "provider-token=secret-value",
        message: "TypeError: /private/agent/config.go\nstack: remote failure",
        diagnosticId: "diag-failed-1",
        retryable: false,
        recoveryActions: [],
      },
      occurredAt: "2026-07-16T01:00:00Z",
    },
  });

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(
    expect.objectContaining({
      tone: "error",
      message: "运行失败：提供方未能完成本轮请求，请稍后重试。",
    }),
  );
  expect(state.actionNotice.tone).not.toBe("success");
  expect(state.timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ id: "assistant-open", text: "partial", done: true }),
    expect.objectContaining({
      kind: "turn_terminal",
      terminalOutcome: "failed",
      publicError: expect.objectContaining({
        code: "PROVIDER_FAILED",
        title: "提供方暂不可用",
        diagnosticId: "diag-failed-1",
      }),
    }),
  ]);
  expect(JSON.stringify({ notice: state.actionNotice, timeline: state.timelinesByThread["thread-1"] })).not.toMatch(/secret-value|\/private\/|stack:/);
  expect(state.warningEntries).toEqual([
    expect.objectContaining({
      level: "error",
      event: "turn.terminal.failed",
      threadId: "thread-1",
    }),
  ]);
});

it("replays a canonical terminal after its accepted partial delta arrives out of order", () => {
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
      eventId: "terminal-out-of-order",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "failed",
      publicError: {
        code: "PROVIDER_FAILED",
        title: "运行失败",
        message: "提供方未能完成本轮响应",
        diagnosticId: "diag-out-of-order",
        retryable: false,
        recoveryActions: ["copy_diagnostics"],
      },
      partialItemIds: ["partial-1"],
      occurredAt: "2026-07-16T01:00:00Z",
    },
  });
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([]);

  runtime.bridgeCallback({
    type: "turn/output/delta",
    payload: { threadId: "thread-1", turnId: "turn-1", itemId: "partial-1", delta: "partial answer" },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ id: "partial-1", text: "partial answer", done: true }),
    expect.objectContaining({ kind: "turn_terminal", terminalOutcome: "failed" }),
  ]);
  expect(state.actionNotice).toEqual(
    expect.objectContaining({
      tone: "error",
      message: expect.stringContaining("提供方未能完成本轮请求，请稍后重试。"),
    }),
  );
  expect(state.warningEntries).not.toEqual(expect.arrayContaining([expect.objectContaining({ event: "turn.terminal.contract_invalid" })]));
});

it("replays a pending terminal after item completion replaces its missing partial item", () => {
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
      eventId: "terminal-before-completion",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "failed",
      publicError: {
        code: "PROVIDER_FAILED",
        title: "Provider failed",
        message: "The provider failed after producing a partial response",
        diagnosticId: "diag-before-completion",
        retryable: false,
        recoveryActions: [],
      },
      partialItemIds: ["partial-1"],
      occurredAt: "2026-07-21T01:00:00Z",
    },
  });
  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "partial-1", type: "agentMessage", text: "final partial response" },
    },
  });
  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "partial-1", type: "agentMessage", text: "final partial response" },
    },
  });

  const terminalItems = useClientStore.getState().timelinesByThread["thread-1"].filter((item) => item.kind === "turn_terminal");
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual(
    expect.arrayContaining([expect.objectContaining({ id: "partial-1", text: "final partial response", done: true, turnId: "turn-1" })]),
  );
  expect(terminalItems).toEqual([expect.objectContaining({ terminalOutcome: "failed", turnId: "turn-1" })]);
});

it("keeps the first pending terminal truth when a conflicting terminal arrives before its delta", () => {
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
      eventId: "terminal-first-pending",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "failed",
      publicError: {
        code: "PROVIDER_FAILED",
        title: "运行失败",
        message: "首个终态失败",
        diagnosticId: "diag-first-pending",
        retryable: false,
        recoveryActions: [],
      },
      partialItemIds: ["partial-1"],
      occurredAt: "2026-07-19T01:00:00Z",
    },
  });
  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-conflicting-pending",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      occurredAt: "2026-07-19T01:00:01Z",
    },
  });
  runtime.bridgeCallback({
    type: "turn/output/delta",
    payload: { threadId: "thread-1", turnId: "turn-1", itemId: "partial-1", delta: "partial answer" },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["thread-1"]).toEqual(expect.arrayContaining([expect.objectContaining({ kind: "turn_terminal", terminalOutcome: "failed" })]));
  expect(state.actionNotice).toEqual(expect.objectContaining({ tone: "error" }));
  expect(state.warningEntries).toEqual(expect.arrayContaining([expect.objectContaining({ event: "turn.terminal.conflict" })]));
});

it("rejects the oldest late event after sequential turns exceed tombstone capacity", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  for (let index = 0; index <= 65; index++) {
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: {
        threadId: "thread-1",
        turnId: `turn-${index}`,
        itemId: `item-${index}`,
        delta: `answer ${index}`,
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
        occurredAt: "2026-07-20T01:00:00Z",
      },
    });
  }

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual(
    expect.arrayContaining([expect.objectContaining({ kind: "turn_terminal", turnId: "turn-65", terminalOutcome: "success" })]),
  );
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 1,
    observedTurns: 1,
    retiredTurns: 64,
  });

  runtime.bridgeCallback({
    type: "turn/output/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-0",
      itemId: "late-item",
      delta: "late mutation",
    },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["thread-1"]).not.toEqual(expect.arrayContaining([expect.objectContaining({ id: "late-item" })]));
  expect(state.warningEntries).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        event: "turn.event.late",
        fields: expect.objectContaining({ turn_id: "turn-0" }),
      }),
    ]),
  );
});

it("keeps pending terminals replayable and fails closed when they fill capacity", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "pending-thread-0",
    threads: Array.from({ length: 65 }, (_, index) => ({
      id: `pending-thread-${index}`,
      name: `Pending ${index}`,
      provider: "codex",
      status: "running",
    })),
    timelinesByThread: { "pending-thread-0": [] },
  });
  registerBridgeEventHandlersForTest();
  const pendingTerminal = (index) => ({
    schemaVersion: 2,
    eventId: `pending-terminal-${index}`,
    threadId: `pending-thread-${index}`,
    turnId: `pending-turn-${index}`,
    outcome: "failed",
    publicError: {
      code: "PROVIDER_FAILED",
      title: "运行失败",
      message: `第 ${index} 个缺失部分响应`,
      diagnosticId: `diag-pending-${index}`,
      retryable: false,
      recoveryActions: [],
    },
    partialItemIds: [`partial-${index}`],
    occurredAt: "2026-07-20T01:00:00Z",
  });

  for (let index = 0; index < 64; index++) {
    runtime.bridgeCallback({ type: "turn/terminal", payload: pendingTerminal(index) });
  }

  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 64,
    observedTurns: 64,
    retiredTurns: 0,
  });
  runtime.bridgeCallback({ type: "turn/terminal", payload: pendingTerminal(64) });
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 64,
    observedTurns: 64,
    retiredTurns: 0,
  });
  expect(useClientStore.getState().warningEntries).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        event: "turn.terminal.cache_exhausted",
        fields: expect.objectContaining({ turn_id: "pending-turn-64", reason: "capacity" }),
      }),
    ]),
  );

  runtime.bridgeCallback({
    type: "turn/output/delta",
    payload: {
      threadId: "pending-thread-0",
      turnId: "pending-turn-0",
      itemId: "partial-0",
      delta: "replayed partial",
    },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["pending-thread-0"]).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ id: "partial-0", text: "replayed partial", done: true }),
      expect.objectContaining({ kind: "turn_terminal", turnId: "pending-turn-0", terminalOutcome: "failed" }),
    ]),
  );
  expect(state.getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 64,
    observedTurns: 64,
    retiredTurns: 0,
  });
});

it("does not evict active turn references when terminal capacity is full", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "active-thread-0",
    activeTurnByThread: Object.fromEntries(Array.from({ length: 65 }, (_, index) => [`active-thread-${index}`, { id: `active-turn-${index}`, status: "running" }])),
    threads: Array.from({ length: 65 }, (_, index) => ({
      id: `active-thread-${index}`,
      name: `Active ${index}`,
      provider: "codex",
      status: "running",
    })),
    timelinesByThread: { "active-thread-0": [] },
  });
  registerBridgeEventHandlersForTest();

  for (let index = 0; index < 64; index++) {
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: `active-terminal-${index}`,
        threadId: `active-thread-${index}`,
        turnId: `active-turn-${index}`,
        outcome: "success",
        occurredAt: "2026-07-20T01:00:00Z",
      },
    });
  }

  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 64,
    observedTurns: 64,
    retiredTurns: 0,
  });
  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "active-terminal-64",
      threadId: "active-thread-64",
      turnId: "active-turn-64",
      outcome: "success",
      occurredAt: "2026-07-20T01:00:00Z",
    },
  });

  expect(useClientStore.getState().timelinesByThread["active-thread-64"]).toBeUndefined();
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 64,
    observedTurns: 64,
    retiredTurns: 0,
  });
  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "active-terminal-conflict-0",
      threadId: "active-thread-0",
      turnId: "active-turn-0",
      outcome: "success",
      occurredAt: "2026-07-20T01:00:01Z",
    },
  });
  expect(useClientStore.getState().warningEntries).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        event: "turn.terminal.cache_exhausted",
        fields: expect.objectContaining({ turn_id: "active-turn-64", reason: "capacity" }),
      }),
      expect.objectContaining({
        event: "turn.terminal.conflict",
        fields: expect.objectContaining({ turn_id: "active-turn-0" }),
      }),
    ]),
  );
});
