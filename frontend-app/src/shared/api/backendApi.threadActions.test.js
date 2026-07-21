import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("passes through diagnosed turn force-complete failure envelopes from the backend facade", async () => {
  const responses = [
    {
      ok: false,
      forceCompleted: false,
      errorCode: "force_complete_target_not_found",
    },
    {
      ok: false,
      forceCompleted: false,
      error: "force complete target not found",
    },
    {
      ok: false,
      forceCompleted: false,
      message: "force complete target not found",
    },
  ];

  for (const response of responses) {
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.forceCompleteTurn({ cwd: "/repo/app", threadId: "thread-1" })).resolves.toEqual(response);
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_FORCE_COMPLETE, {
      threadId: "thread-1",
    });
  }
});

it("rejects unknown turn/forceComplete facade fields before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.forceCompleteTurn({
        cwd: "/repo/app",
        threadId: "thread-1",
        surprise: true,
      }),
    "turn/forceComplete: unsupported payload field surprise",
  );
});

it("wraps approval/respond with strict composite identity and decision payloads", async () => {
  const callAPI = vi.fn().mockResolvedValue(null);
  const api = createBackendApi({ callAPI });
  const identity = {
    sessionScope: "session-scope-a",
    callId: "call-a",
    requestId: 11,
  };

  await api.respondApproval({ ...identity, approved: false });

  expect(RPC_METHODS.APPROVAL_RESPOND).toBe("approval/respond");
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.APPROVAL_RESPOND, {
    sessionScope: "session-scope-a",
    callId: "call-a",
    requestId: 11,
    approved: false,
  });
  expect(() => api.respondApproval({ ...identity, requestId: 0, approved: true })).toThrow(
    "approval/respond: requestId is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.respondApproval({ ...identity, requestId: "11", approved: true }),
    "approval/respond: requestId must be a positive integer",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.respondApproval({ ...identity, requestId: "11.9", approved: true }),
    "approval/respond: requestId must be a positive integer",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.respondApproval({ ...identity, requestId: 11.9, approved: true }),
    "approval/respond: requestId must be a positive integer",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.respondApproval({
        ...identity,
        requestId: Number.MAX_SAFE_INTEGER + 1,
        approved: true,
      }),
    "approval/respond: requestId must be a positive integer",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.respondApproval({ callId: "call-a", requestId: 11, approved: true }),
    "approval/respond: sessionScope is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.respondApproval({
        sessionScope: "session-scope-a",
        requestId: 11,
        approved: true,
      }),
    "approval/respond: callId is required",
  );
  expect(() => api.respondApproval(identity)).toThrow("approval/respond: approved is required");

  await api.respondApproval({
    session_scope: "session-scope-b",
    call_id: "call-b",
    request_id: 12,
    approved: true,
  });
  expect(callAPI).toHaveBeenLastCalledWith(RPC_METHODS.APPROVAL_RESPOND, {
    sessionScope: "session-scope-b",
    callId: "call-b",
    requestId: 12,
    approved: true,
  });
});

it("rejects unknown approval/respond facade fields before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.respondApproval({
        sessionScope: "session-scope-a",
        callId: "call-a",
        requestId: 11,
        approved: true,
        surprise: true,
      }),
    "approval/respond: unsupported payload field surprise",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.respondApproval({
        sessionScope: "session-scope-a",
        session_scope: "session-scope-b",
        callId: "call-a",
        requestId: 11,
        approved: true,
      }),
    "approval/respond: conflicting sessionScope values",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.respondApproval({
        sessionScope: "",
        session_scope: "session-scope-a",
        callId: "call-a",
        requestId: 11,
        approved: true,
      }),
    "approval/respond: conflicting sessionScope values",
  );
});

it("maps turn/interrupt to the backend request and response contract", async () => {
  const response = {
    ok: true,
    turnId: "turn-1",
    status: "interrupted",
    confirmed: true,
    mode: "interrupt_confirmed",
    interruptSent: true,
    stateBefore: "running",
    stateAfter: "interrupted",
    waitedMs: 25,
    activeObserved: false,
  };
  const callAPI = vi.fn().mockResolvedValue(response);
  const api = createBackendApi({ callAPI });

  await expect(
    api.interruptTurn({
      cwd: "/repo/app",
      threadId: "thread-1",
      expectedTurnId: "turn-1",
      requestId: "stop-request-1",
      source: "ui_stop",
    }),
  ).resolves.toEqual(response);

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_INTERRUPT, {
    thread_id: "thread-1",
    expected_turn_id: "turn-1",
    request_id: "stop-request-1",
    source: "ui_stop",
  });
});

it.each([
  [
    {
      cwd: "/repo/app",
      threadId: "thread-1",
      requestId: "stop-request-1",
      source: "ui_stop",
    },
    "expectedTurnId is required",
  ],
  [
    {
      cwd: "/repo/app",
      threadId: "thread-1",
      expectedTurnId: "turn-1",
      source: "ui_stop",
    },
    "requestId is required",
  ],
  [
    {
      cwd: "/repo/app",
      threadId: "thread-1",
      expectedTurnId: "turn-1",
      requestId: "",
      source: "ui_stop",
    },
    "requestId is required",
  ],
])("rejects incomplete stop identity before turn/interrupt transport", (payload, message) => {
  const callAPI = vi.fn();
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(callAPI, () => api.interruptTurn(payload), `turn/interrupt: ${message}`);
});

it("fails fast before cwd-scoped RPCs when cwd is missing", () => {
  const callAPI = vi.fn();
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(callAPI, () => api.getProjects({ cwd: "" }), "cwd is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.startThread({ cwd: "/repo/app", name: "Hello" }),
    "provider is required",
  );
});
