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
  frontendBreadcrumbs,
  diagnosticBreadcrumbs,
  expect,
  flushPromises,
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("shows a warning when force complete returns a diagnosed no-target envelope", async () => {
  backendApi.forceCompleteTurn.mockResolvedValueOnce({
    ok: false,
    forceCompleted: false,
    errorCode: "force_complete_target_not_found",
  });
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    activeTurnByThread: {
      "thread-1": { id: "turn-1", threadId: "thread-1", status: "running" },
    },
  });

  await expect(useClientStore.getState().forceCompleteActiveThread()).resolves.toBe(false);

  expect(backendApi.forceCompleteTurn).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "强制完成当前执行失败，请重试。",
      tone: "warning",
    }),
  );
  expect(useClientStore.getState().warningEntries).toContainEqual(
    expect.objectContaining({
      level: "warn",
      event: "thread.force_complete.failed",
      fields: expect.objectContaining({
        error: "[redacted]",
      }),
    }),
  );
});

const approvalItem = (requestId, overrides = {}) => ({
  sessionScope: "session-scope-a",
  callId: `call-${requestId}`,
  requestId,
  command: "deploy",
  ...overrides,
});

it("responds to timeline approval requests through the approval RPC", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
  });

  await expect(useClientStore.getState().respondApproval(approvalItem(11), true)).resolves.toBe(true);

  expect(backendApi.respondApproval).toHaveBeenCalledWith({
    sessionScope: "session-scope-a",
    callId: "call-11",
    requestId: 11,
    approved: true,
  });
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "审批结果已提交",
      tone: "success",
    }),
  );
  expect(diagnosticBreadcrumbs()).toEqual([
    { actionCode: "approval.submit", routeId: "chat", phase: "start" },
    { actionCode: "approval.submit", routeId: "chat", phase: "success" },
  ]);
});

it("rejects malformed approval responses without publishing success", async () => {
  for (const response of [{ ok: false }, { ok: true }, undefined]) {
    backendApi.respondApproval.mockResolvedValueOnce(response);
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
    });

    await expect(useClientStore.getState().respondApproval(approvalItem(11), true)).rejects.toThrow("approval/respond response must be null");

    expect(useClientStore.getState().actionNotice).not.toEqual(
      expect.objectContaining({
        message: "审批结果已提交",
        tone: "success",
      }),
    );
    expect(useClientStore.getState().warningEntries).toContainEqual(
      expect.objectContaining({
        level: "error",
        event: "timeline.approval.respond.failed",
      }),
    );
    expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
  }
});

it("rejects malformed timeline approval request ids before calling the approval RPC", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
  });

  for (const item of [
    approvalItem("11.9"),
    { sessionScope: "session-scope-a", callId: "call-11", request_id: "11", command: "deploy" },
    { sessionScope: "session-scope-a", requestId: 11, command: "deploy" },
    { callId: "call-11", requestId: 11, command: "deploy" },
  ]) {
    await expect(useClientStore.getState().respondApproval(item, true)).resolves.toBe(false);
  }

  expect(backendApi.respondApproval).not.toHaveBeenCalled();
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "当前审批缺少完整身份，无法提交",
      tone: "error",
    }),
  );
  expect(diagnosticBreadcrumbs()).toEqual([]);
});

it.each([
  { label: "false string", approved: "false" },
  { label: "true string", approved: "true" },
  { label: "number", approved: 1 },
  { label: "null", approved: null },
  { label: "undefined", approved: undefined },
  { label: "object", approved: { value: true } },
])("rejects a non-boolean approval decision: $label", async ({ approved }) => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
  });

  await expect(useClientStore.getState().respondApproval(approvalItem(11), approved)).resolves.toBe(false);

  expect(backendApi.respondApproval).not.toHaveBeenCalled();
  expect(diagnosticBreadcrumbs()).toEqual([]);
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "审批提交失败，请重试。",
      tone: "error",
    }),
  );
  expect(useClientStore.getState().warningEntries).toContainEqual(
    expect.objectContaining({
      level: "error",
      event: "timeline.approval.respond.failed",
      fields: expect.objectContaining({
        requestId: 11,
        error: "[redacted]",
      }),
    }),
  );
  expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
});

it("keeps approval RPC submission idempotent per exact identity while in flight", async () => {
  const pendingApproval = deferred();
  backendApi.respondApproval.mockReturnValueOnce(pendingApproval.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
  });

  const identity = approvalItem(11);
  const first = useClientStore.getState().respondApproval(identity, true);
  await flushPromises();
  await expect(useClientStore.getState().respondApproval(identity, false)).resolves.toBe(false);

  expect(backendApi.respondApproval).toHaveBeenCalledTimes(1);
  expect(Object.values(useClientStore.getState().approvalSubmitByIdentity)).toEqual([
    expect.objectContaining({
      sessionScope: "session-scope-a",
      callId: "call-11",
      requestId: 11,
      approved: true,
      inFlight: true,
    }),
  ]);
  expect(diagnosticBreadcrumbs()).toEqual([{ actionCode: "approval.submit", routeId: "chat", phase: "start" }]);

  pendingApproval.resolve(null);
  await expect(first).resolves.toBe(true);
  expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
  expect(diagnosticBreadcrumbs()).toEqual([
    { actionCode: "approval.submit", routeId: "chat", phase: "start" },
    { actionCode: "approval.submit", routeId: "chat", phase: "success" },
  ]);
});

it("dedupes only the exact approval identity while allowing the same request id in another session", async () => {
  const firstPending = deferred();
  const secondPending = deferred();
  backendApi.respondApproval.mockReturnValueOnce(firstPending.promise).mockReturnValueOnce(secondPending.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
  });

  const firstIdentity = {
    sessionScope: "session-scope-a",
    callId: "call-a",
    requestId: 11,
    command: "deploy",
  };
  const secondIdentity = {
    sessionScope: "session-scope-b",
    callId: "call-b",
    requestId: 11,
    command: "deploy",
  };
  const first = useClientStore.getState().respondApproval(firstIdentity, true);
  await flushPromises();
  await expect(useClientStore.getState().respondApproval(firstIdentity, false)).resolves.toBe(false);
  const second = useClientStore.getState().respondApproval(secondIdentity, false);
  await flushPromises();

  expect(backendApi.respondApproval).toHaveBeenCalledTimes(2);
  expect(backendApi.respondApproval).toHaveBeenNthCalledWith(1, {
    sessionScope: "session-scope-a",
    callId: "call-a",
    requestId: 11,
    approved: true,
  });
  expect(backendApi.respondApproval).toHaveBeenNthCalledWith(2, {
    sessionScope: "session-scope-b",
    callId: "call-b",
    requestId: 11,
    approved: false,
  });

  firstPending.resolve(null);
  secondPending.resolve(null);
  await expect(first).resolves.toBe(true);
  await expect(second).resolves.toBe(true);
});

it("records malformed and ordinary approval failures as one failed terminal without private fields", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
  });
  backendApi.respondApproval.mockResolvedValueOnce({ ok: false });

  await expect(useClientStore.getState().respondApproval(approvalItem(11), true)).rejects.toThrow("approval/respond response must be null");
  expect(diagnosticBreadcrumbs()).toEqual([
    { actionCode: "approval.submit", routeId: "chat", phase: "start" },
    { actionCode: "approval.submit", routeId: "chat", phase: "failure" },
  ]);

  frontendBreadcrumbs.resetFrontendBreadcrumbsForTests();
  backendApi.respondApproval.mockRejectedValueOnce(new Error("private failure /Users/alice"));
  await expect(useClientStore.getState().respondApproval(approvalItem(12), false)).rejects.toThrow("private failure");
  expect(diagnosticBreadcrumbs()).toEqual([
    { actionCode: "approval.submit", routeId: "chat", phase: "start" },
    { actionCode: "approval.submit", routeId: "chat", phase: "failure" },
  ]);
});

it("times out the owned approval attempt and keeps a retried request isolated from the late transport", async () => {
  vi.useFakeTimers();
  try {
    const firstApproval = deferred();
    const secondApproval = deferred();
    backendApi.respondApproval.mockReturnValueOnce(firstApproval.promise).mockReturnValueOnce(secondApproval.promise);
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "waiting" }],
    });

    let firstOutcome;
    const identity = approvalItem(11);
    const first = useClientStore.getState().respondApproval(identity, true);
    const firstHandled = first.then(
      (value) => {
        firstOutcome = { status: "fulfilled", value };
      },
      (error) => {
        firstOutcome = { status: "rejected", error };
      },
    );
    await flushPromises();

    await vi.advanceTimersByTimeAsync(15_000);
    await flushPromises();

    expect(firstOutcome).toMatchObject({
      status: "rejected",
      error: { code: "APPROVAL_SUBMIT_TIMEOUT", message: "审批提交超时" },
    });
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: "approval.submit", routeId: "chat", phase: "start" },
      { actionCode: "approval.submit", routeId: "chat", phase: "timeout" },
    ]);
    expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});

    const second = useClientStore.getState().respondApproval(identity, true);
    await flushPromises();
    expect(backendApi.respondApproval).toHaveBeenCalledTimes(2);
    expect(Object.values(useClientStore.getState().approvalSubmitByIdentity)).toEqual([expect.objectContaining({ approved: true, inFlight: true })]);

    firstApproval.resolve(null);
    await flushPromises();
    expect(Object.values(useClientStore.getState().approvalSubmitByIdentity)).toEqual([expect.objectContaining({ approved: true, inFlight: true })]);

    secondApproval.resolve(null);
    await expect(second).resolves.toBe(true);
    await firstHandled;
    expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: "approval.submit", routeId: "chat", phase: "start" },
      { actionCode: "approval.submit", routeId: "chat", phase: "timeout" },
      { actionCode: "approval.submit", routeId: "chat", phase: "start" },
      { actionCode: "approval.submit", routeId: "chat", phase: "success" },
    ]);
  } finally {
    vi.useRealTimers();
  }
});

it("does not call interrupt when the selected running thread has no active turn id", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "running" }],
  });

  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

  expect(backendApi.interruptTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "当前没有可中断任务",
      tone: "warning",
    }),
  );
});

it("does not interrupt a runtime agent when backend status marks it interruptible without an active turn id", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", name: "Runtime Agent", provider: "codex", status: "running" }],
    statuses: {
      agent_123: { status: "running", interruptible: true },
    },
  });

  expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);

  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

  expect(backendApi.interruptTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "当前没有可中断任务",
      tone: "warning",
    }),
  );
});

it("does not treat a stale active turn as interruptible after the thread becomes idle", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", name: "Runtime Agent", provider: "codex", status: "idle" }],
    activeTurnByThread: {
      agent_123: { id: "turn-123", threadId: "agent_123", status: "running" },
    },
    statuses: {
      agent_123: { status: "idle", interruptible: false },
    },
  });

  expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);

  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

  expect(backendApi.interruptTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "当前没有可中断任务",
      tone: "warning",
    }),
  );
});

it.each(["completed", "failed", "interrupted", "stalled", "done", "ended", "closed"])("does not treat a terminal active turn status as interruptible: %s", async (status) => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", name: "Runtime Agent", provider: "codex", status: "idle" }],
    activeTurnByThread: {
      agent_123: { id: "turn-123", threadId: "agent_123", status },
    },
    statuses: {
      agent_123: { status: "idle", interruptible: false },
    },
  });

  expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);

  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

  expect(backendApi.interruptTurn).not.toHaveBeenCalled();
});

it("clears active turn state when a bridge patch reports a completed active turn", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", name: "Runtime Agent", provider: "codex", status: "running" }],
    activeTurnByThread: {
      agent_123: { id: "turn-123", threadId: "agent_123", status: "running" },
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "agent_123",
      status: "idle",
      activeTurn: { id: "turn-123", threadId: "agent_123", status: "completed" },
      thread: { id: "agent_123", name: "Runtime Agent", status: "idle" },
    },
  });

  expect(useClientStore.getState().activeTurnByThread.agent_123).toBeUndefined();
  expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);
});

it("interrupts a runtime agent when an active turn id is present", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", name: "Runtime Agent", provider: "codex", status: "running" }],
    activeTurnByThread: {
      agent_123: { id: "turn-123", threadId: "agent_123", status: "running" },
    },
    statuses: {
      agent_123: { status: "running", interruptible: true },
    },
  });

  expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(true);

  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(true);

  expect(backendApi.interruptTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "agent_123",
    expectedTurnId: "turn-123",
    requestId: expect.any(String),
    source: "ui_stop",
  });
});

it("surfaces recover RPC failures to the unified action boundary", async () => {
  backendApi.recoverThread.mockRejectedValueOnce(new Error("orchestration: service not configured"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "running" }],
  });

  await expect(useClientStore.getState().recoverActiveThread()).rejects.toThrow("orchestration: service not configured");

  expect(backendApi.recoverThread).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "恢复连接失败，请重试。",
      tone: "error",
    }),
  );
  expect(useClientStore.getState().warningEntries.at(-1)).toEqual(
    expect.objectContaining({
      event: "thread.recover.failed",
      level: "error",
    }),
  );
});

it("submits one recover RPC while the same thread request is pending", async () => {
  const recovery = deferred();
  backendApi.recoverThread.mockReturnValueOnce(recovery.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "running" }],
  });

  const first = useClientStore.getState().recoverActiveThread();
  const repeated = useClientStore.getState().recoverActiveThread();

  await expect(repeated).resolves.toBe(false);
  expect(backendApi.recoverThread).toHaveBeenCalledTimes(1);
  expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({ "thread-1": true });

  recovery.resolve({
    thread: { id: "thread-1", status: "recovering" },
    recovered: true,
    mode: "relaunch_resume",
  });
  await expect(first).resolves.toBe(true);

  expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({});
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "恢复请求已接受，正在恢复",
      tone: "success",
    }),
  );
  expect(useClientStore.getState().actionNotice.message).not.toContain("已恢复完成");
});

it("treats recovered false as a failed request and never as accepted", async () => {
  backendApi.recoverThread.mockResolvedValueOnce({
    thread: { id: "thread-1", status: "recovering" },
    recovered: false,
    mode: "relaunch_resume",
  });
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "运行线程", provider: "codex", status: "running" }],
  });

  await expect(useClientStore.getState().recoverActiveThread()).resolves.toBe(false);

  expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({});
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "恢复请求失败",
      tone: "warning",
    }),
  );
  expect(useClientStore.getState().actionNotice.message).not.toContain("已接受");
  expect(useClientStore.getState().warningEntries.at(-1)).toEqual(
    expect.objectContaining({
      event: "thread.recover.failed",
      level: "warn",
    }),
  );
});
