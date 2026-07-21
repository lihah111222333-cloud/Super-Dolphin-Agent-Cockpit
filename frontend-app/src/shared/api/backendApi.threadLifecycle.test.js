import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("calls canonical thread/fork with only the source thread id", async () => {
  const callAPI = vi.fn().mockResolvedValue({
    thread: { id: "thread-fork", forkedFrom: "thread-parent" },
    kickoff_state: "created_only",
    kickoffState: "created_only",
  });
  const api = createBackendApi({ callAPI });

  await expect(api.forkThread({ threadId: "thread-parent" })).resolves.toEqual(
    expect.objectContaining({
      thread: { id: "thread-fork", forkedFrom: "thread-parent" },
      kickoffState: "created_only",
    }),
  );
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_FORK, {
    threadId: "thread-parent",
  });
});

it("rejects non-canonical thread/fork request fields before calling the backend", () => {
  const callAPI = vi.fn();
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.forkThread({
        threadId: "thread-parent",
        cwd: "/repo/app",
        provider: "codex",
        baseInstructions: "summary fallback",
      }),
    "thread/fork: unsupported payload field",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.forkThread({
        threadId: "thread-parent",
        thread_id: "different-parent",
      }),
    "thread/fork: conflicting threadId values",
  );
});

it("rejects thread/fork responses whose source does not match the request", async () => {
  const callAPI = vi.fn().mockResolvedValue({
    thread: { id: "thread-fork", forkedFrom: "different-parent" },
    kickoffState: "created_only",
  });
  const api = createBackendApi({ callAPI });

  await expect(api.forkThread({ threadId: "thread-parent" })).rejects.toThrow(
    "thread/fork response thread.forkedFrom must equal thread-parent",
  );
});

it("rejects thread/fork responses that reuse the source thread id", async () => {
  const callAPI = vi.fn().mockResolvedValue({
    thread: { id: "thread-parent", forkedFrom: "thread-parent" },
    kickoffState: "created_only",
  });
  const api = createBackendApi({ callAPI });

  await expect(api.forkThread({ threadId: "thread-parent" })).rejects.toThrow(
    "thread/fork response thread.id must differ from thread-parent",
  );
});

it("starts a pending backend thread with the canonical thread/start payload shape", async () => {
  const response = { threadId: "thread-123", state: "pending" };
  const callAPI = vi.fn().mockResolvedValue(response);
  const api = createBackendApi({ callAPI });

  await expect(
    api.startThread({
      cwd: "/repo/app",
      name: "Hello",
      provider: "codex",
      promptKey: "main/dag_designer_zh",
      agentKey: "assistant",
      toolSurfaceMode: "chat",
      deferSpawn: true,
      codexModelProvider: "openai",
      config: {
        codexHome: "C:\\Users\\ai01\\.codex",
        codexInstanceKey: "default",
        codexModelProvider: "openai",
      },
      launchIntentId: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
      optimisticUserMessage: "Hello",
      skipInitialRuntimeSync: true,
    }),
  ).resolves.toEqual(response);

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
    cwd: "/repo/app",
    name: "Hello",
    provider: "codex",
    prompt_key: "main/dag_designer_zh",
    agent_key: "assistant",
    toolSurfaceMode: "chat",
    defer_spawn: true,
    config: {
      codexHome: "C:\\Users\\ai01\\.codex",
      codexInstanceKey: "default",
      codexModelProvider: "openai",
    },
    launchIntentId: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
  });
});

it("rejects invalid thread/start tool surface mode", () => {
  const callAPI = vi.fn().mockResolvedValue({ threadId: "thread-123" });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.startThread({
        cwd: "/repo/app",
        modelProvider: "codex",
        toolSurfaceMode: "full",
      }),
    "toolSurfaceMode must be chat, auto, or agent",
  );
});

it("rejects unknown thread/start payload fields before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ threadId: "thread-123" });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.startThread({
        cwd: "/repo/app",
        modelProvider: "codex",
        unexpectedUiField: true,
      }),
    "thread/start: unsupported payload field unexpectedUiField",
  );
});

it("does not opt into pending launch unless deferSpawn is explicitly requested", async () => {
  const callAPI = vi.fn().mockResolvedValue({ threadId: "thread-123" });
  const api = createBackendApi({ callAPI });

  await api.startThread({
    cwd: "/repo/app",
    modelProvider: "claude",
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
    cwd: "/repo/app",
    provider: "claude",
  });
});

it("allows launch skill facade keys on thread/start", async () => {
  const callAPI = vi.fn().mockResolvedValue({ threadId: "thread-123" });
  const api = createBackendApi({ callAPI });

  await api.startThread({
    cwd: "/repo/app",
    modelProvider: "claude",
    selectedSkills: ["review"],
    selectedSkillRefs: [{ name: "review", scope: "project" }],
    manualSkillSelection: true,
  });

  expect(callAPI).toHaveBeenCalledWith(
    RPC_METHODS.THREAD_START,
    expect.objectContaining({
      selectedSkills: ["review"],
      selectedSkillRefs: [{ name: "review", scope: "project" }],
      manualSkillSelection: true,
    }),
  );
});

it("rejects unknown turn/start facade fields before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.startTurn({
        cwd: "/repo/app",
        threadId: "thread-123",
        input: "build it",
        surprise: true,
      }),
    "turn/start: unsupported payload field surprise",
  );
});

it("sends turn/start input-only text and expanded arrays with explicit cwd", async () => {
  const firstResponse = { turnId: "turn-1", status: "queued" };
  const secondResponse = { turnId: "turn-2", status: "queued" };
  const callAPI = vi.fn().mockResolvedValueOnce(firstResponse).mockResolvedValueOnce(secondResponse);
  const api = createBackendApi({ callAPI });

  await expect(
    api.startTurn({
      cwd: "/repo/app",
      threadId: "thread-123",
      input: "build it",
      manualSkillSelection: false,
    }),
  ).resolves.toEqual(firstResponse);
  await expect(
    api.startTurn({
      cwd: "/repo/app",
      threadId: "thread-456",
      input: [
        { type: "text", text: "inspect this" },
        { type: "mention", name: "a.txt", path: "/tmp/a.txt" },
      ],
    }),
  ).resolves.toEqual(secondResponse);

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_START, {
    cwd: "/repo/app",
    threadId: "thread-123",
    prompt: "build it",
    manualSkillSelection: false,
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.TURN_START, {
    cwd: "/repo/app",
    threadId: "thread-456",
    input: [
      { type: "text", text: "inspect this" },
      { type: "mention", name: "a.txt", path: "/tmp/a.txt" },
    ],
  });
});

it("sends turn/start legacy attachments when input is absent or empty", async () => {
  const callAPI = vi
    .fn()
    .mockResolvedValueOnce({ turn_id: "turn-legacy-1" })
    .mockResolvedValueOnce({ turn_id: "turn-legacy-2" });
  const api = createBackendApi({ callAPI });

  await api.startTurn({
    cwd: "/repo/app",
    threadId: "thread-123",
    attachments: ["/tmp/a.txt"],
    manualSkillSelection: false,
  });
  await api.startTurn({
    cwd: "/repo/app",
    threadId: "thread-456",
    input: "  ",
    attachments: [
      {
        path: "/tmp/b.png",
        kind: "image",
        previewUrl: "data:image/png;base64,abc",
      },
    ],
  });

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_START, {
    cwd: "/repo/app",
    threadId: "thread-123",
    input: [{ type: "mention", name: "a.txt", path: "/tmp/a.txt" }],
    manualSkillSelection: false,
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.TURN_START, {
    cwd: "/repo/app",
    threadId: "thread-456",
    input: [
      {
        type: "localImage",
        path: "/tmp/b.png",
        url: "data:image/png;base64,abc",
      },
    ],
  });
});
