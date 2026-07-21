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
  flushAssistantDeltaBatch,
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("seals the first terminal and routes conflicting or late turn events to diagnostics", async () => {
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
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-1", itemId: "partial-1", delta: "partial answer" },
    });
    const firstTerminal = {
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-first",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "failed",
        publicError: {
          code: "FAILED",
          title: "运行失败",
          message: "本轮执行失败",
          diagnosticId: "diag-first",
          retryable: false,
          recoveryActions: ["copy_diagnostics"],
        },
        partialItemIds: ["partial-1"],
        occurredAt: "2026-07-16T01:00:00Z",
      },
    };
    runtime.bridgeCallback(firstTerminal);
    backendApi.emitFrontendTraceEvent.mockClear();
    runtime.bridgeCallback(firstTerminal);
    expect(backendApi.emitFrontendTraceEvent).not.toHaveBeenCalled();
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-first",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "success",
        occurredAt: "2026-07-16T01:00:01Z",
      },
    });
    runtime.bridgeCallback({
      ...firstTerminal,
      payload: {
        ...firstTerminal.payload,
        eventId: "terminal-replayed-content",
      },
    });
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-conflict",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "success",
        occurredAt: "2026-07-16T01:00:01Z",
      },
    });
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-2", itemId: "partial-2", delta: "next turn" },
    });
    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        itemId: "partial-late",
        delta: "late mutation token=super-secret-value",
      },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.actionNotice.tone).toBe("error");
    expect(state.timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ id: "partial-1", text: "partial answer", done: true }),
      expect.objectContaining({ terminalOutcome: "failed" }),
      expect.objectContaining({ id: "partial-2", text: "next turn", done: false }),
    ]);
    expect(state.timelinesByThread["thread-1"]).not.toEqual(expect.arrayContaining([expect.objectContaining({ text: expect.stringContaining("late mutation") })]));
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        event: "turn.event.late",
        threadId: "thread-1",
        fields: expect.objectContaining({ eventName: "turn/output/delta", turn_id: "turn-1" }),
        occurrenceCount: 1,
      }),
      expect.objectContaining({
        event: "turn.terminal.conflict",
        threadId: "thread-1",
        fields: expect.objectContaining({ eventName: "turn/terminal", turn_id: "turn-1" }),
        occurrenceCount: 3,
      }),
      expect.objectContaining({ event: "turn.terminal.failed" }),
    ]);
    expect(JSON.stringify(state.warningEntries)).not.toContain("super-secret-value");
    expect(JSON.stringify(backendApi.emitFrontendTraceEvent.mock.calls)).not.toContain("super-secret-value");
    expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
      expect.objectContaining({
        phase: "frontend.turn_event.rejected",
        method: "turn.terminal.conflict",
        thread_id: "thread-1",
        turn_id: "turn-1",
      }),
    );
    expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
      expect.objectContaining({
        phase: "frontend.turn_event.rejected",
        method: "turn.event.late",
        thread_id: "thread-1",
        turn_id: "turn-1",
      }),
    );
  } finally {
    vi.useRealTimers();
  }
});

it("rejects a stale terminal when a newer turn is active without changing UI truth", () => {
  const actionNotice = { message: "新一轮仍在运行", tone: "info" };
  const timeline = [
    { id: "turn-1-answer", role: "assistant", text: "older answer", done: true, turnId: "turn-1" },
    { id: "turn-2-answer", role: "assistant", text: "new answer", done: false, turnId: "turn-2" },
  ];
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    activeTurnByThread: { "thread-1": { id: "turn-2", status: "running" } },
    actionNotice,
    timelinesByThread: { "thread-1": timeline },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-stale-turn-1",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      occurredAt: "2026-07-16T01:00:00Z",
    },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["thread-1"]).toBe(timeline);
  expect(state.actionNotice).toBe(actionNotice);
  expect(state.timelinesByThread["thread-1"][1]).toEqual(expect.objectContaining({ done: false, turnId: "turn-2" }));
  expect(state.warningEntries).toEqual([
    expect.objectContaining({
      event: "turn.terminal.stale",
      fields: expect.objectContaining({ eventName: "turn/terminal", turn_id: "turn-1" }),
      occurrenceCount: 1,
    }),
  ]);
  expect(state.activityEntries).toEqual([]);
  expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
    expect.objectContaining({
      phase: "frontend.turn_event.rejected",
      method: "turn.terminal.stale",
      thread_id: "thread-1",
      turn_id: "turn-1",
    }),
  );
});

it("rejects item completion after the same turn is sealed", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [{ id: "assistant-turn-1", role: "assistant", text: "sealed answer", done: false, turnId: "turn-1" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-turn-1",
      threadId: "thread-1",
      turnId: "turn-1",
      outcome: "success",
      occurredAt: "2026-07-16T01:00:00Z",
    },
  });
  const sealedTimeline = useClientStore.getState().timelinesByThread["thread-1"];
  const sealedNotice = useClientStore.getState().actionNotice;

  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "assistant-turn-1", type: "assistant", text: "late replacement" },
    },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["thread-1"]).toBe(sealedTimeline);
  expect(state.actionNotice).toBe(sealedNotice);
  expect(state.warningEntries).toEqual([
    expect.objectContaining({
      event: "turn.event.late",
      fields: expect.objectContaining({ eventName: "item/completed", turn_id: "turn-1" }),
      occurrenceCount: 1,
    }),
  ]);
  expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
    expect.objectContaining({
      phase: "frontend.turn_event.rejected",
      method: "turn.event.late",
      thread_id: "thread-1",
      turn_id: "turn-1",
    }),
  );
});

it.each([
  ["assistant delta", { type: "turn/output/delta", payload: { threadId: "thread-1", itemId: "assistant-open", delta: "late text" } }],
  ["reasoning delta", { type: "item/reasoning/textDelta", payload: { threadId: "thread-1", delta: "late thought" } }],
  ["command output", { type: "item/commandExecution/outputDelta", payload: { threadId: "thread-1", delta: "late output" } }],
  ["item completion", { type: "item/completed", payload: { threadId: "thread-1", item: { id: "assistant-open", type: "assistant", text: "late final" } } }],
])("rejects %s without a canonical TurnRef before mutating UI state", async (_label, event) => {
  vi.useFakeTimers();
  try {
    const actionNotice = { message: "保持原状态", tone: "info" };
    const timeline = [{ id: "assistant-open", role: "assistant", kind: "command", text: "existing", done: false, turnId: "turn-1" }];
    const activityEntries = [{ id: "existing-activity", method: "existing", threadId: "thread-1" }];
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      actionNotice,
      activityEntries,
      timelinesByThread: { "thread-1": timeline },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback(event);
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toBe(timeline);
    expect(state.actionNotice).toBe(actionNotice);
    expect(state.activityEntries).toBe(activityEntries);
    expect(state.warningEntries).toEqual([expect.objectContaining({ level: "error", event: "turn.event.contract_invalid" })]);
  } finally {
    vi.useRealTimers();
  }
});

it("rejects every sealed turn event through telemetry without mutating UI state", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      timelinesByThread: {
        "thread-1": [
          { id: "assistant-turn-1", role: "assistant", kind: "assistant", text: "answer", done: false, turnId: "turn-1" },
          { id: "command-turn-1", role: "assistant", kind: "command", text: "command", done: false, turnId: "turn-1" },
        ],
      },
    });
    registerBridgeEventHandlersForTest();
    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-sealed-turn-1",
        threadId: "thread-1",
        turnId: "turn-1",
        outcome: "success",
        occurredAt: "2026-07-16T01:00:00Z",
      },
    });
    const sealedState = useClientStore.getState();
    const timeline = sealedState.timelinesByThread["thread-1"];
    const actionNotice = sealedState.actionNotice;
    const activityEntries = sealedState.activityEntries;
    backendApi.emitFrontendTraceEvent.mockClear();

    runtime.bridgeCallback({ type: "turn/output/delta", payload: { threadId: "thread-1", turnId: "turn-1", itemId: "assistant-turn-1", delta: "late assistant" } });
    runtime.bridgeCallback({ type: "item/reasoning/textDelta", payload: { threadId: "thread-1", turnId: "turn-1", delta: "late thought" } });
    runtime.bridgeCallback({ type: "item/commandExecution/outputDelta", payload: { threadId: "thread-1", turnId: "turn-1", delta: "late output" } });
    runtime.bridgeCallback({
      type: "item/completed",
      payload: { threadId: "thread-1", turnId: "turn-1", item: { id: "assistant-turn-1", type: "assistant", text: "late final" } },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toBe(timeline);
    expect(state.actionNotice).toBe(actionNotice);
    expect(state.activityEntries).toBe(activityEntries);
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        event: "turn.event.late",
        fields: expect.objectContaining({ eventName: "item/completed", turn_id: "turn-1" }),
        occurrenceCount: 4,
      }),
    ]);
    expect(backendApi.emitFrontendTraceEvent.mock.calls.filter(([payload]) => payload.phase === "frontend.turn_event.rejected")).toHaveLength(4);
    expect(backendApi.emitFrontendTraceEvent.mock.calls.filter(([payload]) => payload.phase === "frontend.warning" && payload.method === "turn.event.late")).toHaveLength(4);
    expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
      expect.objectContaining({
        phase: "frontend.turn_event.rejected",
        method: "turn.event.late",
        thread_id: "thread-1",
        turn_id: "turn-1",
      }),
    );
  } finally {
    vi.useRealTimers();
  }
});

it.each([
  ["assistant delta", { type: "turn/output/delta", payload: { threadId: "thread-1", turnId: "turn-1", itemId: "late-turn-1", delta: "late answer" } }],
  ["item completion", { type: "item/completed", payload: { threadId: "thread-1", turnId: "turn-1", item: { id: "late-turn-1", type: "assistant", text: "late final" } } }],
])("rejects stale %s when the active turn is authoritative", async (_label, event) => {
  vi.useFakeTimers();
  try {
    const activeTurn = { id: "turn-2", status: "running" };
    const actionNotice = { message: "T2 正在运行", tone: "info" };
    const activityEntries = [{ id: "existing-activity", method: "turn/started", threadId: "thread-1" }];
    const warningEntries = [];
    const timeline = [{ id: "turn-2-open", role: "assistant", kind: "assistant", text: "current", done: false, turnId: "turn-2" }];
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      activeTurnByThread: { "thread-1": activeTurn },
      actionNotice,
      activityEntries,
      warningEntries,
      timelinesByThread: { "thread-1": timeline },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback(event);
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toBe(timeline);
    expect(state.actionNotice).toBe(actionNotice);
    expect(state.activityEntries).toBe(activityEntries);
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        event: "turn.event.stale",
        fields: expect.objectContaining({ eventName: event.type, turn_id: "turn-1" }),
        occurrenceCount: 1,
      }),
    ]);
    expect(state.activeTurnByThread["thread-1"]).toBe(activeTurn);
    expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
      expect.objectContaining({
        phase: "frontend.turn_event.rejected",
        method: "turn.event.stale",
        thread_id: "thread-1",
        turn_id: "turn-1",
      }),
    );

    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: `terminal-turn-2-after-${event.type}`,
        threadId: "thread-1",
        turnId: "turn-2",
        outcome: "success",
        occurredAt: "2026-07-16T01:00:00Z",
      },
    });
    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "turn_terminal", turnId: "turn-2", terminalOutcome: "success" })]),
    );
  } finally {
    vi.useRealTimers();
  }
});

it.each(["success", "failed"])("retires an observed turn when a patch selects T2 and rejects stale T1 %s terminals without mutation", async (outcome) => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      actionNotice: { message: "T1 is streaming", tone: "info" },
      timelinesByThread: { "thread-1": [] },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-1", itemId: "turn-1-open", delta: "T1 partial" },
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

    const beforeTerminal = useClientStore.getState();
    const timeline = beforeTerminal.timelinesByThread["thread-1"];
    const actionNotice = beforeTerminal.actionNotice;
    const activityEntries = beforeTerminal.activityEntries;

    runtime.bridgeCallback({
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: `terminal-stale-turn-1-${outcome}`,
        threadId: "thread-1",
        turnId: "turn-1",
        outcome,
        ...(outcome === "failed"
          ? {
              publicError: {
                code: "PROVIDER_FAILED",
                title: "Provider failed",
                message: "T1 failed",
                diagnosticId: "diag-stale-turn-1",
                retryable: false,
                recoveryActions: [],
              },
            }
          : {}),
        occurredAt: "2026-07-18T01:00:00Z",
      },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toBe(timeline);
    expect(state.actionNotice).toBe(actionNotice);
    expect(state.activityEntries).toBe(activityEntries);
    expect(state.timelinesByThread["thread-1"]).toEqual(
      expect.arrayContaining([expect.objectContaining({ id: "turn-1-open", text: "T1 partial", done: false, turnId: "turn-1" })]),
    );
    expect(state.timelinesByThread["thread-1"]).not.toEqual(expect.arrayContaining([expect.objectContaining({ kind: "turn_terminal", turnId: "turn-1" })]));
    expect(state.warningEntries).toEqual([
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
        thread_id: "thread-1",
        turn_id: "turn-1",
      }),
    );
  } finally {
    vi.useRealTimers();
  }
});

it("accepts T2 first terminal after a patch retires the observed T1 turn", async () => {
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
      type: "turn/output/delta",
      payload: { threadId: "thread-1", turnId: "turn-1", itemId: "turn-1-open", delta: "T1 partial" },
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
      type: "turn/terminal",
      payload: {
        schemaVersion: 2,
        eventId: "terminal-active-turn-2",
        threadId: "thread-1",
        turnId: "turn-2",
        outcome: "success",
        occurredAt: "2026-07-18T01:00:01Z",
      },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread["thread-1"]).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "turn-1-open", text: "T1 partial", done: false, turnId: "turn-1" }),
        expect.objectContaining({ kind: "turn_terminal", turnId: "turn-2", terminalOutcome: "success" }),
      ]),
    );
    expect(state.actionNotice).toEqual(expect.objectContaining({ message: "已收到回复", tone: "success" }));
    expect(state.warningEntries).toEqual([]);
  } finally {
    vi.useRealTimers();
  }
});
