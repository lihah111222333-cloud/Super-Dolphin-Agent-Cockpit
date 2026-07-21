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
  deferred,
  expect,
  it,
  optionalUiArray,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  setClientStoreClockMillisForTests,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("keeps C active when A B C thread syncs finish out of order", async () => {
  const syncA = deferred();
  const syncB = deferred();
  const syncC = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: ["a", "b", "c"].map((suffix) => ({
      id: `thread-${suffix}`,
      cwd: "/repo/app",
      name: `Thread ${suffix.toUpperCase()}`,
      provider: "codex",
      status: "idle",
    })),
  });
  backendApi.getThreadState.mockImplementation(
    ({ threadId }) =>
      ({
        "thread-a": syncA.promise,
        "thread-b": syncB.promise,
        "thread-c": syncC.promise,
      })[threadId],
  );
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const intentA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  const openA = useClientStore.getState().setActiveThread("thread-a", { selectionIntent: intentA });
  const intentB = useClientStore.getState().beginOpeningThread({ id: "thread-b" });
  const openB = useClientStore.getState().setActiveThread("thread-b", { selectionIntent: intentB });
  const intentC = useClientStore.getState().beginOpeningThread({ id: "thread-c" });
  const openC = useClientStore.getState().setActiveThread("thread-c", { selectionIntent: intentC });

  syncC.resolve({ activeThreadId: "thread-c", threads: [{ id: "thread-c", cwd: "/repo/app" }] });
  await expect(openC).resolves.toBe(true);
  syncA.resolve({ activeThreadId: "thread-a", threads: [{ id: "thread-a", cwd: "/repo/app" }] });
  await expect(openA).resolves.toBe(false);
  syncB.resolve({ activeThreadId: "thread-b", threads: [{ id: "thread-b", cwd: "/repo/app" }] });
  await expect(openB).resolves.toBe(false);

  expect(useClientStore.getState().activeThreadId).toBe("thread-c");
});

it("invalidates an opening intent when newThread creates a newer user intent", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const staleIntent = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  useClientStore.getState().newThread();
  await useClientStore.getState().setActiveThread("thread-a", { selectionIntent: staleIntent });

  expect(useClientStore.getState().activeThreadId).toBe("");
  expect(useClientStore.getState().pendingActiveThreadId).toBe("");
});

it("invalidates an opening intent when a shared-file fork draft takes ownership", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    activePage: "chat",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
    draft: "keep this draft",
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const staleIntent = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  expect(useClientStore.getState().continueWithSharedFile("reports/final.md")).toBe(true);
  await expect(useClientStore.getState().setActiveThread("thread-a", { selectionIntent: staleIntent })).resolves.toBe(false);

  expect(useClientStore.getState().forkDraft).toEqual(
    expect.objectContaining({
      open: true,
      sourceThreadId: "thread-a",
      sharedFilePaths: ["reports/final.md"],
    }),
  );
  expect(useClientStore.getState().draft).toBe("keep this draft");
});

it("suppresses stale sync failure notices while clearing the target keyed loading flag", async () => {
  const staleSync = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-c", cwd: "/repo/app", name: "Thread C", provider: "codex", status: "idle" },
    ],
    actionNotice: null,
    warningEntries: [],
  });
  backendApi.getThreadState.mockReturnValue(staleSync.promise);
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const intentA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  const openA = useClientStore.getState().setActiveThread("thread-a", { selectionIntent: intentA });
  useClientStore.getState().beginOpeningThread({ id: "thread-c" });
  staleSync.reject(new Error("stale thread A failed"));
  await expect(openA).resolves.toBe(false);

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-c");
  expect(state.threadStateLoadingByThread["thread-a"]).toBe(false);
  expect(state.actionNotice).toBeNull();
  expect(state.warningEntries).not.toEqual(expect.arrayContaining([expect.objectContaining({ event: "thread.sync.failed" })]));
});

it("lets a stale successful sync update keyed cache without changing the active intent", async () => {
  const staleSync = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-c", cwd: "/repo/app", name: "Thread C", provider: "codex", status: "idle" },
    ],
  });
  backendApi.getThreadState.mockReturnValue(staleSync.promise);
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const intentA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  const openA = useClientStore.getState().setActiveThread("thread-a", { selectionIntent: intentA });
  useClientStore.getState().beginOpeningThread({ id: "thread-c" });
  staleSync.resolve({
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A refreshed", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-a": [{ id: "a-message", role: "assistant", text: "stale cache is still useful" }],
    },
  });
  await openA;

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-c");
  expect(state.threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "thread-a", name: "Thread A refreshed" })]));
  expect(state.timelinesByThread["thread-a"]).toEqual(expect.arrayContaining([expect.objectContaining({ id: "a-message", text: "stale cache is still useful" })]));
  expect(state.threadStateLoadingByThread["thread-a"]).toBe(false);
});

it("clears stale resolve failure loading without changing the active intent or publishing a notice", async () => {
  const staleResolve = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-c", cwd: "/repo/app", name: "Thread C", provider: "codex", status: "idle" },
    ],
    actionNotice: null,
    warningEntries: [],
  });
  backendApi.resolveThreadIdentity.mockReturnValue(staleResolve.promise);

  const intentA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  const openA = useClientStore.getState().openThreadById("thread-a", {
    source: "sidebar",
    selectionIntent: intentA,
  });
  useClientStore.getState().beginOpeningThread({ id: "thread-c" });
  staleResolve.reject(new Error("stale resolve failed"));
  await expect(openA).resolves.toBe(false);

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-c");
  expect(state.threadStateLoadingByThread["thread-a"]).toBe(false);
  expect(state.actionNotice).toBeNull();
  expect(state.warningEntries).not.toEqual(expect.arrayContaining([expect.objectContaining({ event: "thread.open.resolve.failed" })]));
});

it("clears keyed loading when the current resolve fails", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
  });
  backendApi.resolveThreadIdentity.mockRejectedValue(new Error("current resolve failed"));

  const intentA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  await expect(
    useClientStore.getState().openThreadById("thread-a", {
      source: "sidebar",
      selectionIntent: intentA,
    }),
  ).rejects.toThrow("current resolve failed");

  expect(useClientStore.getState().threadStateLoadingByThread["thread-a"]).toBe(false);
});

it("does not let a stale same-target resolve failure clear the newer intent loading", async () => {
  const staleResolve = deferred();
  const currentSync = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
    actionNotice: null,
    warningEntries: [],
  });
  backendApi.resolveThreadIdentity.mockReturnValue(staleResolve.promise);
  backendApi.getThreadState.mockReturnValue(currentSync.promise);
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const intentA1 = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  const openA1 = useClientStore.getState().openThreadById("thread-a", {
    source: "sidebar",
    selectionIntent: intentA1,
  });
  const intentA2 = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  expect(intentA2).not.toBe(intentA1);
  const openA2 = useClientStore.getState().setActiveThread("thread-a", { selectionIntent: intentA2 });
  expect(useClientStore.getState().pendingActiveThreadId).toBe("");
  expect(useClientStore.getState().threadStateLoadingByThread["thread-a"]).toBe(true);
  staleResolve.reject(new Error("stale same-target resolve failed"));
  await expect(openA1).resolves.toBe(false);

  const stateAfterStaleFailure = useClientStore.getState();
  expect(stateAfterStaleFailure.activeThreadId).toBe("thread-a");
  expect(stateAfterStaleFailure.pendingActiveThreadId).toBe("");
  expect(stateAfterStaleFailure.threadStateLoadingByThread["thread-a"]).toBe(true);
  expect(stateAfterStaleFailure.actionNotice).toBeNull();
  expect(stateAfterStaleFailure.warningEntries).not.toEqual(expect.arrayContaining([expect.objectContaining({ event: "thread.open.resolve.failed" })]));

  currentSync.resolve({
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" }],
  });
  await expect(openA2).resolves.toBe(true);
  expect(useClientStore.getState().threadStateLoadingByThread["thread-a"]).toBe(false);
});

it("does not commit a stale resolved canonical id after a newer selection", async () => {
  const resolvedIdentity = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "alias-a",
    threads: [
      { id: "alias-a", cwd: "/repo/app", name: "Alias A", provider: "codex", status: "idle" },
      { id: "thread-c", cwd: "/repo/app", name: "Thread C", provider: "codex", status: "idle" },
    ],
  });
  backendApi.resolveThreadIdentity.mockReturnValue(resolvedIdentity.promise);
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "canonical-a",
    threads: [{ id: "canonical-a", agentId: "alias-a", cwd: "/repo/app", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  const intentA = useClientStore.getState().beginOpeningThread({ id: "alias-a" });
  const openA = useClientStore.getState().openThreadById("alias-a", {
    source: "sidebar",
    selectionIntent: intentA,
  });
  useClientStore.getState().beginOpeningThread({ id: "thread-c" });
  resolvedIdentity.resolve({
    id: "canonical-a",
    agentId: "alias-a",
    cwd: "/repo/app",
    provider: "codex",
    status: "idle",
  });
  await openA;

  expect(useClientStore.getState().activeThreadId).toBe("thread-c");
});

it("starts a new thread instead of sending turns to an unknown active agent id", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [],
    draft: "Recover from bad active id",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-safe" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startThread).toHaveBeenCalled();
  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-safe",
    input: [{ type: "text", text: "Recover from bad active id" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-safe");
});

it("preserves the optimistic user message when a backend patch only contains assistant output", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Keep my message visible",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-new" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();
  registerBridgeEventHandlersForTest();
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-new",
      sequence: "1",
      timelineItems: [{ id: "assistant-1", kind: "assistant", text: "AI reply" }],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-new"]).toEqual([
    expect.objectContaining({ role: "user", text: "Keep my message visible" }),
    expect.objectContaining({ role: "assistant", text: "AI reply" }),
  ]);
});

it("emits a sanitized slow patch trace after thresholded bridge patch application", () => {
  resetClientStoreForTests({
    threads: [{ id: "thread-new", name: "Trace me", provider: "codex", status: "running" }],
  });
  let clockCalls = 0;
  setClientStoreClockMillisForTests(() => {
    clockCalls += 1;
    return clockCalls === 1 ? 0 : 75;
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-new",
      sequence: "1",
      prompt: "forbidden prompt text",
      timelineItems: [{ id: "assistant-1", kind: "assistant", text: "AI reply" }],
      agentRuntime: { agentId: "agent-1" },
      activeTurn: { id: "turn-1" },
    },
  });

  expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
    expect.objectContaining({
      phase: "frontend.patch.apply.slow",
      method: "ui/thread/patch",
      thread_id: "thread-new",
      agent_id: "agent-1",
      turn_id: "turn-1",
      duration_ms: 75,
      status: "ok",
    }),
  );
  expect(JSON.stringify(backendApi.emitFrontendTraceEvent.mock.calls[0][0])).not.toContain("prompt");
  setClientStoreClockMillisForTests(null);
});

it("maps explicit activeTurn patch payload without inventing one when omitted", () => {
  resetClientStoreForTests({
    threads: [
      { id: "thread-active", name: "Active", provider: "codex", status: "running" },
      { id: "thread-empty", name: "Empty", provider: "codex", status: "running" },
    ],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-active",
      sequence: "1",
      activeTurn: { id: "turn-active", threadId: "thread-active", status: "thinking" },
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-empty",
      sequence: "1",
      interruptible: true,
    },
  });

  expect(useClientStore.getState().activeTurnByThread).toEqual({
    "thread-active": expect.objectContaining({
      id: "turn-active",
      threadId: "thread-active",
      status: "thinking",
    }),
  });
});

it("preserves the selected Claude provider when runtime patches omit provider metadata", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    provider: "claude",
    activeThreadId: "thread-claude",
    threads: [{ id: "thread-claude", name: "Claude chat", provider: "claude", status: "running" }],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-claude",
      sequence: "1",
      status: "error",
      statusDetails: "API Error: Unable to connect to API (ConnectionRefused)",
    },
  });

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-claude",
      provider: "claude",
      status: "error",
    }),
  );
});

it("ignores late bridge patches from a different cwd after project switching", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/other",
    activeThreadId: "thread-other",
    threads: [{ id: "thread-other", name: "Other project chat", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-other": [{ id: "other-user", role: "user", text: "other cwd message" }] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-old",
      source: "turn/completed",
      sequence: "1",
      status: "running",
      thread: { id: "thread-old", name: "Old cwd chat" },
      agentRuntime: { cwd: "/repo/app", provider: "codex" },
      timelineItems: [{ id: "old-assistant", kind: "assistant", text: "old cwd reply" }],
    },
  });

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-other");
  expect(state.threads).toEqual([expect.objectContaining({ id: "thread-other", name: "Other project chat" })]);
  expect(state.timelinesByThread).not.toHaveProperty("thread-old");
  expect(state.warningEntries[0]).toEqual(
    expect.objectContaining({
      level: "warn",
      event: "thread.patch.cwd_mismatch",
    }),
  );
});

it("increments workflow revision from task and cron bridge events", () => {
  registerBridgeEventHandlersForTest();

  expect(useClientStore.getState().workflowRevision).toBe(0);
  runtime.bridgeCallback({
    type: "task/node/statusChanged",
    payload: { dag_key: "flow-a", run_key: "run-a", node_key: "step", new_status: "running" },
  });
  expect(useClientStore.getState().workflowRevision).toBe(1);

  runtime.bridgeCallback({
    method: "cron/job/runStateChanged",
    payload: { job_id: "job-1", run_id: "run-1", status: "running" },
  });
  expect(useClientStore.getState().workflowRevision).toBe(2);
});

it("fails fast instead of refreshing workflow data for malformed task node status events", () => {
  registerBridgeEventHandlersForTest();

  expect(() =>
    runtime.bridgeCallback({
      type: "task/node/statusChanged",
      payload: { dag_key: "flow-a", node_key: "step", new_status: "running" },
    }),
  ).toThrow("dag status event run identity is required");

  expect(useClientStore.getState().workflowRevision).toBe(0);
});

it("[regression] completes agent timeline streaming when the canonical terminal arrives without cwd", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent-douyin",
    threads: [{ id: "agent-douyin", name: "Douyin agent", provider: "codex", status: "running" }],
    timelinesByThread: {
      "agent-douyin": [{ id: "assistant-stream-turn1", role: "assistant", text: "正在流式输出...", done: false, turnId: "turn1" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "turn/terminal",
    payload: {
      schemaVersion: 2,
      eventId: "terminal-turn1",
      threadId: "agent-douyin",
      turnId: "turn1",
      outcome: "success",
      occurredAt: "2026-07-16T01:00:00Z",
    },
  });

  await vi.waitFor(() => {
    const timeline = useClientStore.getState().timelinesByThread["agent-douyin"] || optionalUiArray();
    const msg = timeline.find((m) => m.id === "assistant-stream-turn1");
    expect(msg).toBeDefined();
    expect(msg.done).toBe(true);
  });
});

it("subscribes bridge events with callback error escalation for malformed DAG payloads", () => {
  registerBridgeEventHandlersForTest();

  expect(backendApi.onBridgeEvent).toHaveBeenCalledWith(
    expect.any(Function),
    expect.objectContaining({
      escalateCallbackError: expect.any(Function),
    }),
  );
  expect(runtime.bridgeOptions).toEqual(
    expect.objectContaining({
      escalateCallbackError: expect.any(Function),
    }),
  );
  expect(
    runtime.bridgeOptions.escalateCallbackError(new Error("bad payload"), {
      type: "task/node/statusChanged",
    }),
  ).toBe(true);
  expect(
    runtime.bridgeOptions.escalateCallbackError(new Error("non-critical"), {
      type: "ui/sidebar/changed",
    }),
  ).toBe(false);
});
