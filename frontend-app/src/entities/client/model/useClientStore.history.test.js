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
  it,
  parseRequiredJsonObject,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("copies thread info as backend-resolved JSON and treats dot project as current cwd", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: ".",
    activeThreadId: "thread-1",
    provider: "codex",
    providerConfig: { provider: "codex", model: "gpt-5.5", effort: "xhigh" },
    threads: [
      {
        id: "thread-1",
        agentId: "agent-1",
        name: "Thread 1",
        provider: "codex",
        status: "running",
      },
    ],
  });
  backendApi.resolveThreadIdentity.mockResolvedValue({
    id: "thread-1",
    agent_id: "agent-1",
    providerThreadId: "provider-thread-1",
    sessionId: "session-uuid-1",
    provider: "codex",
    port: 4512,
    cwd: "/repo/app",
  });
  backendApi.getThreadConfig.mockResolvedValue({
    threadId: "thread-1",
    provider: "codex",
    supportsThreadOverride: true,
    effective: { model: "gpt-5.4", effort: "medium" },
  });
  await expect(useClientStore.getState().copyActiveThreadInfo()).resolves.toBe(true);

  expect(backendApi.resolveThreadIdentity).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  const payload = parseRequiredJsonObject(backendApi.copyTextToClipboard.mock.calls[0][0]);
  expect(payload).toEqual(
    expect.objectContaining({
      agentId: "agent-1",
      providerThreadId: "provider-thread-1",
      uuid: "session-uuid-1",
      name: "Thread 1",
      status: "running",
      provider: "codex",
      model: "gpt-5.4",
      effort: "medium",
      port: 4512,
      cwd: "/repo/app",
      "log-path": "~/.multi-agent/log/app/",
    }),
  );
  expect(payload.copiedAt).toContain("UTC+8");
});

it("commits a prepared clipboard write when browser clipboard would lose activation after async calls", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: ".",
    activeThreadId: "thread-1",
    provider: "codex",
    providerConfig: { provider: "codex", model: "gpt-5.5", effort: "xhigh" },
    threads: [{ id: "thread-1", agentId: "agent-1", name: "Thread 1", provider: "codex", status: "running" }],
  });
  backendApi.resolveThreadIdentity.mockResolvedValue({
    id: "thread-1",
    agent_id: "agent-1",
    providerThreadId: "provider-thread-1",
    provider: "codex",
    cwd: "/repo/app",
  });
  Object.assign(globalThis.navigator, {
    clipboard: { writeText: vi.fn().mockRejectedValue(new Error("The request is not allowed")) },
  });
  const preparedClipboardWrite = {
    commit: vi.fn().mockResolvedValue(true),
    cancel: vi.fn(),
  };
  backendApi.beginTextClipboardWrite.mockReturnValue(preparedClipboardWrite);
  backendApi.copyTextToClipboard.mockResolvedValue(false);

  await expect(useClientStore.getState().copyActiveThreadInfo()).resolves.toBe(true);

  expect(globalThis.navigator.clipboard.writeText).not.toHaveBeenCalled();
  expect(backendApi.beginTextClipboardWrite).toHaveBeenCalledTimes(1);
  expect(preparedClipboardWrite.commit).toHaveBeenCalledTimes(1);
  expect(backendApi.copyTextToClipboard).not.toHaveBeenCalled();
  expect(parseRequiredJsonObject(preparedClipboardWrite.commit.mock.calls[0][0])).toEqual(
    expect.objectContaining({
      agentId: "agent-1",
      providerThreadId: "provider-thread-1",
    }),
  );
});

it("treats status archived threads as inactive for scoped chat actions", async () => {
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "essay_agent_15",
    threads: [
      {
        id: "essay_agent_15",
        name: "作文Agent-15",
        provider: "codex",
        status: "archived",
      },
    ],
  });

  await useClientStore.getState().bootstrap();
  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("");
  expect(state.threads[0]).toEqual(expect.objectContaining({ id: "essay_agent_15", archived: true }));
  expect(state.hasActiveThreadActions()).toBe(false);
  expect(backendApi.getThreadState).not.toHaveBeenCalledWith(expect.objectContaining({ threadId: "essay_agent_15" }));
  expect(backendApi.interruptTurn).not.toHaveBeenCalled();
});

it("sends an empty-thread message through thread/start before turn/start and keeps the user message visible", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Hello backend",
    attachments: [{ path: "/tmp/a.txt", name: "a.txt" }],
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-new" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      name: "Hello backend",
      modelProvider: "codex",
      deferSpawn: true,
    }),
  );
  const startPayload = backendApi.startThread.mock.calls[0][0];
  expect(startPayload).not.toHaveProperty("toolSurfaceMode");
  expect(startPayload).not.toHaveProperty("prompt");
  expect(startPayload).not.toHaveProperty("optimisticUserMessage");
  expect(startPayload).not.toHaveProperty("skipInitialRuntimeSync");
  expect(backendApi.startThread).toHaveBeenCalledBefore(backendApi.startTurn);
  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-new",
    input: [
      { type: "text", text: "Hello backend" },
      { type: "mention", name: "a.txt", path: "/tmp/a.txt" },
    ],
    manualSkillSelection: false,
  });
  const turnPayload = backendApi.startTurn.mock.calls[0][0];
  expect(turnPayload).not.toHaveProperty("attachments");

  const timeline = useClientStore.getState().timelinesByThread["thread-new"];
  expect(timeline).toEqual([expect.objectContaining({ role: "user", text: "Hello backend" })]);
  expect(useClientStore.getState().draft).toBe("");
  expect(useClientStore.getState().threadTimelineReadyByThread["thread-new"]).toBe(true);
});

it("stores a new dot-project thread under the real cwd sidebar cache", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: ".",
    projects: [],
    activeThreadId: "",
    draft: "Hello from dot project",
    attachments: [],
    sidebarThreadsByProject: {
      "/repo/app": [],
    },
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-dot" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      name: "Hello from dot project",
    }),
  );
  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-dot",
      cwd: "/repo/app",
      name: "Hello from dot project",
    }),
  );
  expect(useClientStore.getState().sidebarThreadsByProject["/repo/app"]).toEqual([
    expect.objectContaining({
      id: "thread-dot",
      cwd: "/repo/app",
      name: "Hello from dot project",
    }),
  ]);
});

it("does not classify engineering intents into a frontend tool mode", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "请帮我看这个文件并跑一下测试",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-agent" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      name: "请帮我看这个文件并跑一下测试",
      deferSpawn: true,
    }),
  );
  expect(backendApi.startThread.mock.calls[0][0]).not.toHaveProperty("toolSurfaceMode");
});

it("does not classify trace diagnosis intents into a frontend tool mode", async () => {
  const drafts = [
    "这个慢请求 trace_id=abc123 帮我定位一下",
    "traceparent 是 00-abc123-def456-01，查链路追踪",
    "span_id=def456 看下观测日志",
    "请用 observability_trace_get 查本地落盘日志",
  ];

  for (const [index, draft] of drafts.entries()) {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "",
      draft,
      attachments: [],
    });
    backendApi.startThread.mockClear();
    backendApi.startTurn.mockClear();
    backendApi.startThread.mockResolvedValue({ threadId: `thread-trace-${index}` });
    backendApi.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backendApi.startThread).toHaveBeenCalledWith(
      expect.objectContaining({
        cwd: "/repo/app",
        name: draft,
        deferSpawn: true,
      }),
    );
    expect(backendApi.startThread.mock.calls[0][0]).not.toHaveProperty("toolSurfaceMode");
  }
});

it("preserves the optimistic first user message when a fresh thread sync has an empty timeline", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Hello backend",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-new" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-new",
    threads: [{ id: "thread-new", name: "Hello backend", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-new": [] },
  });

  await useClientStore.getState().syncThreadState("thread-new");

  expect(useClientStore.getState().timelinesByThread["thread-new"]).toEqual([expect.objectContaining({ role: "user", text: "Hello backend" })]);
});

it("loads selected thread messages in chronological order when the backend returns latest first", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: [
        { id: "assistant-new", role: "assistant", content: "latest reply", createdAt: "2026-05-30T00:03:00Z" },
        { id: "user-old", role: "user", content: "first prompt", createdAt: "2026-05-30T00:01:00Z" },
        { id: "assistant-old", role: "assistant", content: "first reply", createdAt: "2026-05-30T00:02:00Z" },
      ],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  expect(useClientStore.getState().timelinesByThread["thread-1"].map((message) => message.text)).toEqual(["first prompt", "first reply", "latest reply"]);
});

it("deduplicates intermediate turns that are concatenated in final assistant message", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [
        { id: "assistant-stream-turn1", role: "assistant", text: "我会先加载 使用超能力 技能，确认本轮技能选择规则。", done: true },
        { id: "assistant-stream-turn2", role: "assistant", text: "Hi，我在。需要我帮你看代码、排查问题，还是继续当前仓库里的改动？", done: true },
      ],
    },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: [
        {
          id: "assistant-final-msg",
          role: "assistant",
          content: [
            "我会先加载 使用超能力 技能，确认本轮技能选择规则。",
            "Hi，我在。需要我帮你看代码、排查问题，还是继续当前仓库里的改动？",
          ].join(""),
          createdAt: "2026-06-01T14:26:00Z",
        },
      ],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  const texts = useClientStore.getState().timelinesByThread["thread-1"].map(
    (message) => message.text,
  );
  expect(texts).toEqual([
    "我会先加载 使用超能力 技能，确认本轮技能选择规则。Hi，我在。需要我帮你看代码、排查问题，还是继续当前仓库里的改动？",
  ]);
});

it("deduplicates and merges in-progress assistant messages with completed backend messages", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [{ id: "assistant-stream", role: "assistant", text: "你好！我是 Super Dolphin。", done: false }],
    },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: [
        {
          id: "assistant-final-msg",
          role: "assistant",
          content: "你好！我是 Super Dolphin。",
          createdAt: "2026-06-01T14:26:00Z",
        },
      ],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  expect(timeline.length).toBe(1);
  expect(timeline[0].text).toBe("你好！我是 Super Dolphin。");
  expect(timeline[0].done).toBe(true);
});

it("does not override activeThreadId back to the previous thread when an in-flight sync resolves after clicking newThread", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
  });

  let resolveSync;
  const syncPromise = new Promise((resolve) => {
    resolveSync = resolve;
  });

  backendApi.getThreadState.mockImplementationOnce(() => syncPromise);
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  // Start syncThreadState (simulates in-flight sync)
  const syncCall = useClientStore.getState().syncThreadState("thread-1");

  // User clicks newThread in the meantime
  useClientStore.getState().newThread();
  expect(useClientStore.getState().activeThreadId).toBe("");

  // Resolve the in-flight sync
  resolveSync({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [],
    },
  });

  await syncCall;

  // Verify that activeThreadId remains empty and was not overridden back to thread-1
  expect(useClientStore.getState().activeThreadId).toBe("");
});

it("supports creating consecutive new threads, sending messages, and switching between them", async () => {
  // 1. Initialize empty store
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "hi 1",
    attachments: [],
    threads: [],
  });

  // Mock first send
  backendApi.startThread.mockResolvedValueOnce({ threadId: "thread-1" });
  backendApi.startTurn.mockResolvedValueOnce({ ok: true });

  // Send first draft
  await useClientStore.getState().sendDraft();
  expect(useClientStore.getState().activeThreadId).toBe("thread-1");

  // 2. Click New Chat again
  useClientStore.getState().newThread();
  expect(useClientStore.getState().activeThreadId).toBe("");
  useClientStore.setState({ draft: "hi 2" }); // type "hi 2"

  // Mock second send
  backendApi.startThread.mockResolvedValueOnce({ threadId: "thread-2" });
  backendApi.startTurn.mockResolvedValueOnce({ ok: true });

  // Send second draft
  await useClientStore.getState().sendDraft();
  expect(useClientStore.getState().activeThreadId).toBe("thread-2");

  // 3. Switch back to thread-1
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "hi 1", provider: "codex", status: "idle" },
      { id: "thread-2", name: "hi 2", provider: "codex", status: "idle" },
    ],
    timelinesByThread: {
      "thread-1": [],
    },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: [
        { id: "msg-1", role: "user", content: "hi 1", createdAt: "2026-06-01T12:00:00Z" },
        { id: "msg-2", role: "assistant", content: "reply 1", createdAt: "2026-06-01T12:01:00Z" },
      ],
    }),
  );

  await useClientStore.getState().setActiveThread("thread-1");

  expect(useClientStore.getState().activeThreadId).toBe("thread-1");
  const texts = useClientStore.getState().timelinesByThread["thread-1"].map((message) => message.text);
  expect(texts).toEqual(["hi 1", "reply 1"]);
});

it("supports concurrent thread creation and preserves streaming response when switching back and loading empty backend messages", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "hi 1",
    attachments: [],
    threads: [],
  });

  // We will control startThread resolutions manually using deferred promises
  let resolveStartThread1;
  const startThreadPromise1 = new Promise((resolve) => {
    resolveStartThread1 = resolve;
  });
  backendApi.startThread.mockReturnValueOnce(startThreadPromise1);
  backendApi.startTurn.mockResolvedValueOnce({ ok: true });

  // Send first draft (async, does not await finish yet)
  const sendPromise1 = useClientStore.getState().sendDraft();
  const provisionalId1 = useClientStore.getState().activeThreadId;
  expect(provisionalId1).toMatch(/^launch_/);

  // Simulate assistant streaming replies on provisionalId1
  useClientStore.setState((state) => ({
    timelinesByThread: {
      ...state.timelinesByThread,
      [provisionalId1]: [
        { id: "user-msg", role: "user", text: "hi 1" },
        { id: "assistant-msg", role: "assistant", text: "streaming reply...", optimistic: false, done: false },
      ],
    },
  }));

  // User clicks New Chat while sendDraft1 is in-flight
  useClientStore.getState().newThread();
  expect(useClientStore.getState().activeThreadId).toBe("");
  useClientStore.setState({ draft: "hi 2" });

  // Mock second send
  backendApi.startThread.mockResolvedValueOnce({ threadId: "thread-2" });
  backendApi.startTurn.mockResolvedValueOnce({ ok: true });

  // Send second draft
  await useClientStore.getState().sendDraft();
  expect(useClientStore.getState().activeThreadId).toBe("thread-2");

  // Now, resolve the first thread creation
  resolveStartThread1({ threadId: "thread-1" });
  await sendPromise1;

  // Verify activeThreadId is NOT hijacked (it must remain thread-2)
  expect(useClientStore.getState().activeThreadId).toBe("thread-2");

  // Check that timeline of provisionalId1 was promoted to thread-1
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toBeDefined();
  expect(useClientStore.getState().timelinesByThread["thread-1"].map((m) => m.text)).toEqual(["hi 1", "streaming reply..."]);

  // Now switch back to thread-1
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "hi 1", provider: "codex", status: "idle" },
      { id: "thread-2", name: "hi 2", provider: "codex", status: "idle" },
    ],
    timelinesByThread: {
      "thread-1": [],
    },
  });

  // Mock backend returning empty message list for the new thread (common case)
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: [],
    }),
  );

  await useClientStore.getState().setActiveThread("thread-1");

  expect(useClientStore.getState().activeThreadId).toBe("thread-1");

  // Confirm that the streaming assistant message is preserved and not cleared
  const finalTexts = useClientStore.getState().timelinesByThread["thread-1"].map((message) => message.text);
  expect(finalTexts).toEqual(["hi 1", "streaming reply..."]);
});
