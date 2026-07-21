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
  deferredThreadMessagesPage,
  expect,
  flushAssistantDeltaBatch,
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("drops stale empty userMessage command cards while preserving cached messages", async () => {
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage({ messages: [], hasMore: false, nextBefore: "" }));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [
        { id: "cached-user", role: "user", text: "cached prompt", time: "2026-05-30T00:00:00Z" },
        { id: "item:userMessage", kind: "command", status: "completed", itemType: "userMessage", done: true, success: true },
      ],
    },
    threadTimelineReadyByThread: { "thread-1": true },
  });

  await expect(useClientStore.getState().syncThreadState("thread-1")).resolves.toBe(true);

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "cached-user", text: "cached prompt" })]);
});

it("ignores stale message pages and loading cleanup from older same-thread requests", async () => {
  const firstSnapshot = deferred();
  const secondSnapshot = deferred();
  const firstMessages = deferredThreadMessagesPage();
  const secondMessages = deferredThreadMessagesPage();
  backendApi.getThreadState.mockReturnValueOnce(firstSnapshot.promise).mockReturnValueOnce(secondSnapshot.promise);
  backendApi.getThreadMessages.mockReturnValueOnce(firstMessages.promise).mockReturnValueOnce(secondMessages.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
  });

  const firstSync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(backendApi.getThreadMessages).toHaveBeenCalledTimes(1));
  const secondSync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(backendApi.getThreadMessages).toHaveBeenCalledTimes(2));

  secondMessages.resolvePage({
    messages: [{ id: "fresh", role: "user", content: "fresh prompt", createdAt: "2026-05-30T00:02:00Z" }],
    hasMore: false,
    nextBefore: "",
  });
  await vi.waitFor(() => expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "fresh prompt" })]));
  firstMessages.resolvePage({
    messages: [{ id: "stale", role: "user", content: "stale prompt", createdAt: "2026-05-30T00:01:00Z" }],
    hasMore: true,
    nextBefore: "stale",
  });
  firstSnapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Old snapshot", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });
  secondSnapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Fresh snapshot", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });

  await expect(firstSync).resolves.toBe(true);
  await expect(secondSync).resolves.toBe(true);
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "fresh prompt" })]);
  expect(useClientStore.getState().threadMessagePaginationByThread["thread-1"]).toEqual(
    expect.objectContaining({
      hasMore: false,
      loading: false,
    }),
  );
});

it("ignores stale same-thread snapshots that resolve after a newer sync applied", async () => {
  const firstSnapshot = deferred();
  const secondSnapshot = deferred();
  const firstMessages = deferredThreadMessagesPage();
  const secondMessages = deferredThreadMessagesPage();
  backendApi.getThreadState.mockReturnValueOnce(firstSnapshot.promise).mockReturnValueOnce(secondSnapshot.promise);
  backendApi.getThreadMessages.mockReturnValueOnce(firstMessages.promise).mockReturnValueOnce(secondMessages.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
  });

  const firstSync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(backendApi.getThreadState).toHaveBeenCalledTimes(1));
  const secondSync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(backendApi.getThreadState).toHaveBeenCalledTimes(2));

  secondMessages.resolvePage({
    messages: [{ id: "fresh-message", role: "user", content: "fresh prompt", createdAt: "2026-05-30T00:02:00Z" }],
    hasMore: false,
    nextBefore: "",
  });
  secondSnapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Fresh snapshot", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [{ id: "fresh-snapshot", kind: "assistant", text: "fresh snapshot reply" }],
    },
    diffText: "fresh diff",
  });
  await vi.waitFor(() =>
    expect(useClientStore.getState().threads[0]).toEqual(
      expect.objectContaining({
        name: "Fresh snapshot",
        status: "idle",
      }),
    ),
  );

  firstMessages.resolvePage({
    messages: [{ id: "stale-message", role: "user", content: "stale prompt", createdAt: "2026-05-30T00:01:00Z" }],
    hasMore: true,
    nextBefore: "stale",
  });
  firstSnapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Old snapshot", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "old-snapshot", kind: "assistant", text: "old snapshot reply" }],
    },
    diffText: "old diff",
  });

  await expect(firstSync).resolves.toBe(true);
  await expect(secondSync).resolves.toBe(true);
  const state = useClientStore.getState();
  expect(state.threads[0]).toEqual(
    expect.objectContaining({
      name: "Fresh snapshot",
      status: "idle",
    }),
  );
  expect(state.timelinesByThread["thread-1"].map((message) => message.text)).toEqual(["fresh prompt", "fresh snapshot reply"]);
  expect(state.diffTextByThread["thread-1"]).toBe("fresh diff");
  expect(state.threadStateLoadingByThread["thread-1"]).toBe(false);
});

it("batches burst runtime assistant deltas before applying them to the timeline", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
      timelinesByThread: {
        "thread-1": [{ id: "user-1", role: "user", text: "count", time: "2026-05-30T00:00:00Z" }],
      },
    });
    registerBridgeEventHandlersForTest();

    const chunks = Array.from({ length: 100 }, (_, index) => `${index},`);
    for (const delta of chunks) {
      runtime.bridgeCallback({
        method: "item/agentMessage/delta",
        payload: {
          threadId: "thread-1",
          turnId: "turn-1",
          delta,
          stream: "message",
        },
      });
    }

    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "user-1", role: "user", text: "count" })]);

    await flushAssistantDeltaBatch();

    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ id: "user-1", role: "user", text: "count" }),
      expect.objectContaining({
        id: "assistant-stream-turn-1",
        role: "assistant",
        text: chunks.join(""),
        done: false,
      }),
    ]);
  } finally {
    vi.useRealTimers();
  }
});

it("flushes pending assistant deltas before applying completion events", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
      timelinesByThread: {
        "thread-1": [{ id: "user-1", role: "user", text: "say ok", time: "2026-05-30T00:00:00Z" }],
      },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      method: "item/agentMessage/delta",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        delta: "o",
        stream: "message",
      },
    });
    runtime.bridgeCallback({
      method: "item/completed",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        item: { id: "msg-final", type: "agentMessage", text: "ok" },
      },
    });

    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ role: "user", text: "say ok" }),
      expect.objectContaining({ id: "msg-final", role: "assistant", text: "ok", done: true }),
    ]);
    await flushAssistantDeltaBatch();
    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ role: "user", text: "say ok" }),
      expect.objectContaining({ id: "msg-final", role: "assistant", text: "ok", done: true }),
    ]);
  } finally {
    vi.useRealTimers();
  }
});

it("preserves markdown block whitespace across assistant delta chunks", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
      timelinesByThread: {
        "thread-1": [{ id: "user-1", role: "user", text: "inspect repo", time: "2026-05-30T00:00:00Z" }],
      },
    });
    registerBridgeEventHandlersForTest();

    for (const delta of ["已完成代码库速览。", "\n\n## 代码库画像\n", "- 这是一个多 agent 编排平台"]) {
      runtime.bridgeCallback({
        method: "item/agentMessage/delta",
        payload: {
          threadId: "thread-1",
          turnId: "turn-1",
          delta,
          stream: "message",
        },
      });
    }

    await flushAssistantDeltaBatch();

    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ id: "user-1", role: "user", text: "inspect repo" }),
      expect.objectContaining({
        id: "assistant-stream-turn-1",
        role: "assistant",
        text: "已完成代码库速览。\n\n## 代码库画像\n- 这是一个多 agent 编排平台",
        done: false,
      }),
    ]);
  } finally {
    vi.useRealTimers();
  }
});

it("merges completion into stream messages stored under provider thread aliases", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        providerThreadId: "provider-thread-1",
        agentId: "agent_123",
        name: "Thread 1",
        provider: "codex",
        status: "running",
      },
    ],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "say ok", time: "2026-05-30T00:00:00Z" }],
      "provider-thread-1": [
        {
          id: "assistant-stream-turn-1",
          role: "assistant",
          kind: "assistant",
          text: "ok",
          done: false,
          runtime: true,
          turnId: "turn-1",
          time: "2026-05-30T00:01:00Z",
        },
      ],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    method: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "assistant-final-turn-1", type: "agentMessage", text: "ok" },
    },
  });

  expect(useClientStore.getState().timelinesByThread["provider-thread-1"]).toEqual([
    expect.objectContaining({
      id: "assistant-final-turn-1",
      role: "assistant",
      text: "ok",
      done: true,
    }),
  ]);
});

it("clears pending assistant delta timers and buffers on store reset", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
      timelinesByThread: {
        "thread-1": [{ id: "user-1", role: "user", text: "say ok", time: "2026-05-30T00:00:00Z" }],
      },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      method: "item/agentMessage/delta",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        delta: "ok",
        stream: "message",
      },
    });
    resetClientStoreForTests();
    await flushAssistantDeltaBatch();

    expect(useClientStore.getState().timelinesByThread).toEqual({});
  } finally {
    vi.useRealTimers();
  }
});

it("applies runtime agent message delta and completion events to the timeline", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "say ok", time: "2026-05-30T00:00:00Z" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    method: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      delta: "o",
      stream: "message",
    },
  });
  runtime.bridgeCallback({
    method: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      delta: "k",
      stream: "message",
    },
  });
  runtime.bridgeCallback({
    method: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "msg-final", type: "agentMessage", text: "ok" },
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ role: "user", text: "say ok" }),
    expect.objectContaining({ id: "msg-final", role: "assistant", text: "ok", done: true }),
  ]);
});

it("does not duplicate an assistant reply when patch and completion carry the same answer with different ids", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "怎么没有内容了", time: "2026-05-30T00:00:00Z" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [
        {
          id: "assistant-from-patch",
          kind: "assistant",
          text: "你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？",
          turnId: "turn-1",
          createdAt: "2026-05-30T00:01:00Z",
        },
      ],
    },
  });
  runtime.bridgeCallback({
    method: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: {
        id: "assistant-from-completion",
        type: "agentMessage",
        text: "你是指：1.页面/应用里没有内容了？2.某个文件被清空了？",
      },
    },
  });

  const assistantMessages = useClientStore.getState().timelinesByThread["thread-1"].filter((message) => message.role === "assistant");
  expect(assistantMessages).toHaveLength(1);
  expect(assistantMessages[0]).toEqual(
    expect.objectContaining({
      id: "assistant-from-patch",
      text: "你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？",
    }),
  );
});

it("does not duplicate assistant messages split by tool calls during turn completion", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "say hi", time: "2026-05-30T00:00:00Z" }],
    },
  });
  registerBridgeEventHandlersForTest();

  // 1. Assistant outputs part 1
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [
        {
          id: "assistant-part-1",
          kind: "assistant",
          text: "hello",
          turnId: "turn-1",
          createdAt: "2026-05-30T00:01:00Z",
        },
      ],
    },
  });

  // 2. A tool call is made
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "2",
      timelineItems: [
        {
          id: "tool-call-1",
          kind: "toolCall",
          toolName: "my_tool",
          createdAt: "2026-05-30T00:01:01Z",
        },
      ],
    },
  });

  // 3. Assistant outputs part 2
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "3",
      timelineItems: [
        {
          id: "assistant-part-2",
          kind: "assistant",
          text: "world",
          turnId: "turn-1",
          createdAt: "2026-05-30T00:01:02Z",
        },
      ],
    },
  });

  // 4. item/completed is called with the concatenated turn result
  runtime.bridgeCallback({
    method: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: {
        id: "assistant-concatenated",
        type: "agentMessage",
        text: "helloworld",
      },
    },
  });

  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  const assistantMessages = timeline.filter((message) => message.role === "assistant" && (message.kind === "assistant" || !message.kind));
  expect(assistantMessages).toHaveLength(2);
  expect(assistantMessages[0]).toEqual(
    expect.objectContaining({
      id: "assistant-part-1",
      text: "hello",
      done: true,
    }),
  );
  expect(assistantMessages[1]).toEqual(
    expect.objectContaining({
      id: "assistant-part-2",
      text: "world",
      done: true,
    }),
  );
});
