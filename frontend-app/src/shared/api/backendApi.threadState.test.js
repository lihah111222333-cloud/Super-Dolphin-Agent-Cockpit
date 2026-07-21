import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import {
  threadCompactResponse,
  threadConfigResponse,
  threadRecoverResponse,
  guardedThreadStateResponse,
} from "./test-support/backendApi.threadState.testSupport.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("maps archive, unarchive, and delete thread actions to legacy thread RPCs", async () => {
  const callAPI = vi.fn().mockResolvedValue(null);
  const api = createBackendApi({ callAPI });

  await api.archiveThread({ cwd: "/repo/app", threadId: "thread-1" });
  await api.unarchiveThread({ cwd: "/repo/app", thread_id: "thread-2" });
  await api.deleteThread({ cwd: "/repo/app", threadId: "thread-3" });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_ARCHIVE, {
    threadId: "thread-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_UNARCHIVE, {
    threadId: "thread-2",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_DELETE, {
    threadId: "thread-3",
  });
});

it("rejects malformed null command responses", async () => {
  const commands = [
    { call: (api) => api.archiveThread({ threadId: "thread-1" }) },
    { call: (api) => api.unarchiveThread({ threadId: "thread-1" }) },
    { call: (api) => api.deleteThread({ threadId: "thread-1" }) },
    {
      call: (api) => api.renameThread({ threadId: "thread-1", name: "Renamed" }),
    },
    {
      call: (api) =>
        api.respondApproval({
          sessionScope: "session-scope-a",
          callId: "call-a",
          requestId: 11,
          approved: true,
        }),
    },
  ];

  for (const command of commands) {
    for (const response of [{}, { ok: true }, false, undefined]) {
      const api = createBackendApi({
        callAPI: vi.fn().mockResolvedValue(response),
      });
      await expect(command.call(api)).rejects.toThrow("response must be null");
    }

    const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(null) });
    await expect(command.call(api)).resolves.toBeNull();
  }
});

it("rejects unknown thread-scoped facade fields before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.resolveThreadIdentity({
        cwd: "/repo/app",
        threadId: "thread-1",
        surprise: true,
      }),
    "thread/resolve: unsupported payload field surprise",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.archiveThread({
        cwd: "/repo/app",
        threadId: "thread-1",
        surprise: true,
      }),
    "thread/archive: unsupported payload field surprise",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.renameThread({
        cwd: "/repo/app",
        threadId: "thread-1",
        name: "Renamed",
        surprise: true,
      }),
    "thread/name/set: unsupported payload field surprise",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.setThreadConfig({
        threadId: "thread-1",
        model: "gpt-5.4",
        surprise: true,
      }),
    "thread/config/set: unsupported payload field surprise",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.interruptTurn({
        cwd: "/repo/app",
        threadId: "thread-1",
        expectedTurnId: "turn-1",
        requestId: "stop-request-1",
        source: "ui_stop",
        surprise: true,
      }),
    "turn/interrupt: unsupported payload field surprise",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.compactThread({
        cwd: "/repo/app",
        threadId: "thread-1",
        surprise: true,
      }),
    "thread/compact/start: unsupported payload field surprise",
  );
});

it("rejects conflicting thread id aliases before calling thread-scoped backend RPCs", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.archiveThread({
        threadId: "thread-A",
        thread_id: "thread-B",
      }),
    "thread/archive: conflicting threadId values for threadId and thread_id",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.resolveThreadIdentity({
        cwd: "/repo/app",
        threadId: "thread-A",
        thread_id: "thread-B",
      }),
    "thread/resolve: conflicting threadId values for threadId and thread_id",
  );
});

it("exposes text copy through the native bridge helper without adding a backend RPC payload", async () => {
  const callAPI = vi.fn();
  const beginTextClipboardWrite = vi.fn().mockReturnValue(null);
  const copyTextToClipboard = vi.fn().mockResolvedValue(true);
  const api = createBackendApi({
    callAPI,
    beginTextClipboardWrite,
    copyTextToClipboard,
  });

  expect(api.beginTextClipboardWrite()).toBeNull();
  await expect(api.copyTextToClipboard("thread info")).resolves.toBe(true);

  expect(beginTextClipboardWrite).toHaveBeenCalledTimes(1);
  expect(copyTextToClipboard).toHaveBeenCalledWith("thread info");
  expect(callAPI).not.toHaveBeenCalled();
});

it("maps thread rename to the legacy name RPC without cwd", async () => {
  const callAPI = vi.fn().mockResolvedValue(null);
  const api = createBackendApi({ callAPI });

  await api.renameThread({
    cwd: "/repo/app",
    threadId: "thread-1",
    name: "Renamed",
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_NAME_SET, {
    threadId: "thread-1",
    name: "Renamed",
  });
});

it("maps thread config get and set to legacy thread config RPCs", async () => {
  const callAPI = vi.fn().mockResolvedValue(threadConfigResponse());
  const api = createBackendApi({ callAPI });

  await api.getThreadConfig({ thread_id: "thread-1" });
  await api.setThreadConfig({
    threadId: "thread-1",
    model: { value: "gpt-5.4" },
    effort: { id: "medium" },
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_CONFIG_GET, {
    threadId: "thread-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_CONFIG_SET, {
    threadId: "thread-1",
    model: "gpt-5.4",
    effort: "medium",
  });
});

it("rejects malformed thread lifecycle responses", async () => {
  const configCalls = [
    (api) => api.getThreadConfig({ threadId: "thread-1" }),
    (api) => api.setThreadConfig({ threadId: "thread-1", model: "gpt-5.5" }),
  ];
  const { threadId: _threadId, ...missingThreadId } = threadConfigResponse();
  const { override: _override, ...missingOverride } = threadConfigResponse();
  const { effective: _effective, ...missingEffective } = threadConfigResponse();
  const configResponses = [
    missingThreadId,
    missingOverride,
    missingEffective,
    threadConfigResponse({ supportsThreadOverride: "true" }),
    threadConfigResponse({ override: { model: 7 } }),
    threadConfigResponse({ effective: { surprise: true } }),
    threadConfigResponse({ surprise: true }),
  ];

  for (const call of configCalls) {
    for (const response of configResponses) {
      const api = createBackendApi({
        callAPI: vi.fn().mockResolvedValue(response),
      });
      await expect(call(api)).rejects.toThrow();
    }
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(threadConfigResponse()),
    });
    await expect(call(api)).resolves.toEqual(threadConfigResponse());
  }

  const compactCall = (api) => api.compactThread({ cwd: "/repo/app", threadId: "thread-1" });
  const compactResponses = [
    { ...threadCompactResponse(), threadId: undefined },
    { ...threadCompactResponse(), command: undefined },
    { ...threadCompactResponse(), beforeTokens: "1200" },
    { ...threadCompactResponse(), afterTokens: 640.5 },
    { ...threadCompactResponse(), compacted: "true" },
    { ...threadCompactResponse(), estimated: "false" },
    { ...threadCompactResponse(), surprise: true },
  ];
  for (const response of compactResponses) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(response),
    });
    await expect(compactCall(api)).rejects.toThrow();
  }
  await expect(
    compactCall(
      createBackendApi({
        callAPI: vi.fn().mockResolvedValue(threadCompactResponse()),
      }),
    ),
  ).resolves.toEqual(threadCompactResponse());

  const recoverCall = (api) => api.recoverThread({ cwd: "/repo/app", threadId: "thread-1" });
  const recoverResponses = [
    threadRecoverResponse({ thread: { status: "recovering" } }),
    threadRecoverResponse({ thread: { id: "thread-1", status: false } }),
    threadRecoverResponse({ thread: { id: "thread-1", surprise: true } }),
    threadRecoverResponse({ recovered: "true" }),
    threadRecoverResponse({ mode: undefined }),
    threadRecoverResponse({ surprise: true }),
  ];
  for (const response of recoverResponses) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(response),
    });
    await expect(recoverCall(api)).rejects.toThrow();
  }
  await expect(
    recoverCall(
      createBackendApi({
        callAPI: vi.fn().mockResolvedValue(threadRecoverResponse()),
      }),
    ),
  ).resolves.toEqual(threadRecoverResponse());
});

it("strips cwd from strict thread-scoped runtime RPC payloads", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedThreadStateResponse(method)));
  const api = createBackendApi({ callAPI });

  await api.interruptTurn({
    cwd: "/repo/app",
    threadId: "thread-1",
    expectedTurnId: "turn-1",
    requestId: "stop-request-1",
    source: "ui_stop",
  });
  await api.forceCompleteTurn({ cwd: "/repo/app", threadId: "thread-1" });
  await api.compactThread({ cwd: "/repo/app", threadId: "thread-1" });
  await api.recoverThread({ cwd: "/repo/app", threadId: "thread-1" });

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_INTERRUPT, {
    thread_id: "thread-1",
    expected_turn_id: "turn-1",
    request_id: "stop-request-1",
    source: "ui_stop",
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.TURN_FORCE_COMPLETE, {
    threadId: "thread-1",
  });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.THREAD_COMPACT_START, {
    threadId: "thread-1",
  });
  expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.THREAD_RECOVER, {
    threadId: "thread-1",
  });
});
