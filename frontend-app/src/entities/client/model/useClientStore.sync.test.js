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
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("removes a compact runtime duplicate when a later patch carries the formatted assistant reply", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        { id: "user-1", role: "user", text: "怎么没有内容了", time: "2026-05-30T00:00:00Z" },
        {
          id: "assistant-from-completion",
          role: "assistant",
          text: "你是指：1.页面/应用里没有内容了？2.某个文件被清空了？",
          time: "2026-05-30T00:01:00Z",
          done: true,
          runtime: true,
        },
      ],
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
          createdAt: "2026-05-30T00:01:00Z",
        },
      ],
    },
  });

  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  expect(timeline).toEqual([
    expect.objectContaining({ id: "user-1", role: "user" }),
    expect.objectContaining({
      id: "assistant-from-patch",
      role: "assistant",
      text: "你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？",
    }),
  ]);
});

it("removes a loosely matching runtime assistant duplicate when the formatted patch has small content differences", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "总结这个 Markdown 文件", time: "2026-06-03T13:15:36Z" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    method: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      stream: "message",
      delta: [
        "我会用“核心信息提取与总结”技能来提炼这个 Markdown 文件。摘要这个文件是一个 JSON 内容库，",
        "包含 5 条抖音爆款短视频脚本，主题覆盖省钱生活、选择困难、亲情愧疚、健身变化和职场面试。",
        "内容结构每条视频都包含 title：标题 hook：开场钩子 script：完整短视频脚本 thumbnail_idea：封面设计思路 cta：评论/转发引导语。",
        "爆款套路总结：开头都使用强钩子：哭了、懂了、活下去、笑死、实拍变化。",
      ].join(""),
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [
        {
          id: "assistant-from-patch",
          kind: "assistant",
          text: [
            "## 摘要",
            "",
            [
              "这个文件是一个 JSON 内容库，包含 5 条抖音爆款短视频脚本，",
              "主题覆盖省钱生活、选择困难、亲情愧疚、健身变化和职场面试。",
            ].join(""),
            "",
            "## 内容结构",
            "",
            "每条视频都包含：",
            "",
            "- `title`：标题",
            "- `hook`：开场钩子",
            "- `script`：完整短视频脚本",
            "- `thumbnail_idea`：封面设计思路",
            "- `cta`：评论/转发引导语",
            "",
            "爆款套路总结：开头都使用强钩子：哭了、懂了、活下去、笑死、实拍变化。",
          ].join("\n"),
          createdAt: "2026-06-03T13:15:40Z",
        },
      ],
    },
  });

  const assistantMessages = useClientStore.getState().timelinesByThread["thread-1"].filter((message) => message.role === "assistant");
  expect(assistantMessages).toEqual([
    expect.objectContaining({
      id: "assistant-from-patch",
      text: expect.stringContaining("## 摘要"),
    }),
  ]);
});

it("deduplicates compact assistant replies that arrive in the same backend timeline patch", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
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
          id: "assistant-compact",
          kind: "assistant",
          text: "你是指：1.页面/应用里没有内容了？2.某个文件被清空了？",
          createdAt: "2026-05-30T00:01:00Z",
        },
        {
          id: "assistant-formatted",
          kind: "assistant",
          text: "你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？",
          createdAt: "2026-05-30T00:01:01Z",
        },
      ],
    },
  });

  const assistantMessages = useClientStore.getState().timelinesByThread["thread-1"].filter((message) => message.role === "assistant");
  expect(assistantMessages).toEqual([
    expect.objectContaining({
      id: "assistant-formatted",
      text: "你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？",
    }),
  ]);
});

it("replaces a shorter runtime assistant completion when item completion carries the full answer", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "检查抖音脚本", time: "2026-06-03T13:15:36Z" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    method: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      timestamp: "2026-06-03T13:15:38Z",
      item: {
        id: "msg-short-prefix",
        type: "agentMessage",
        text: "我先读取共享资源里是否有 `reports/douyin_viral_scripts.md`。",
      },
    },
  });
  runtime.bridgeCallback({
    method: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      timestamp: "2026-06-03T13:15:43Z",
      result: "我先读取共享资源里是否有 `reports/douyin_viral_scripts.md`。\n\n已找到脚本文件，接下来会根据模板整理今日任务。",
    },
  });

  const assistantMessages = useClientStore.getState().timelinesByThread["thread-1"].filter((message) => message.role === "assistant");
  expect(assistantMessages).toEqual([
    expect.objectContaining({
      id: "assistant-final-turn-1",
      text: "我先读取共享资源里是否有 `reports/douyin_viral_scripts.md`。\n\n已找到脚本文件，接下来会根据模板整理今日任务。",
    }),
  ]);
});

it("applies fallback turn output deltas with empty stream as assistant message text", async () => {
  vi.useFakeTimers();
  try {
    resetClientStoreForTests({
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Thread 1", provider: "claude", status: "running" }],
      timelinesByThread: {
        "thread-1": [{ id: "user-1", role: "user", text: "say ok", time: "2026-05-30T00:00:00Z" }],
      },
    });
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      method: "turn/output/delta",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        delta: "o",
        stream: "",
      },
    });
    runtime.bridgeCallback({
      method: "turn/output/delta",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        delta: "k",
        stream: "",
      },
    });
    runtime.bridgeCallback({
      method: "turn/output/delta",
      payload: {
        threadId: "thread-1",
        turnId: "turn-1",
        delta: " hidden reasoning",
        stream: "reasoning",
      },
    });

    await flushAssistantDeltaBatch();

    expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
      expect.objectContaining({ role: "user", text: "say ok" }),
      expect.objectContaining({ id: "assistant-stream-turn-1", role: "assistant", text: "ok", done: false }),
    ]);
  } finally {
    vi.useRealTimers();
  }
});

it("deduplicates overlapping assistant deltas before merging the formatted patch reply", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "say math", time: "2026-05-30T00:00:00Z" }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    method: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      delta: "正常",
      stream: "message",
    },
  });
  runtime.bridgeCallback({
    method: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      delta: "常数学",
      stream: "message",
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [
        {
          id: "assistant-from-patch",
          kind: "assistant",
          text: "正常数学",
          createdAt: "2026-05-30T00:01:00Z",
        },
      ],
    },
  });

  const assistantMessages = useClientStore.getState().timelinesByThread["thread-1"].filter((message) => message.role === "assistant");
  expect(assistantMessages).toEqual([
    expect.objectContaining({
      id: "assistant-from-patch",
      text: "正常数学",
    }),
  ]);
});

it("keeps runtime assistant replies when later partial bridge patches omit them", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "say ok", done: true }],
    },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      delta: "ok",
    },
  });
  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "msg-final", type: "agentMessage", text: "ok" },
    },
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [{ id: "turn-end:turn-1", kind: "turn_end", status: "completed" }],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ role: "user", text: "say ok" }),
    expect.objectContaining({ id: "msg-final", role: "assistant", text: "ok", done: true }),
  ]);
});

it("retains runtime: true protection on completed assistant message when double-channel completed event arrives and later patch omits it", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [{ id: "user-1", role: "user", text: "say ok", done: true }],
    },
  });
  registerBridgeEventHandlersForTest();

  // 1. Stream delta
  runtime.bridgeCallback({
    type: "item/agentMessage/delta",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      delta: "ok",
    },
  });

  // 2. Item completion and backend snapshot arrive on independent channels.
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "2",
      timelineItems: [
        { id: "user-1", role: "user", text: "say ok", done: true },
        { id: "msg-final", role: "assistant", text: "ok", done: true, turnId: "turn-1" },
      ],
    },
  });
  runtime.bridgeCallback({
    type: "item/completed",
    payload: {
      threadId: "thread-1",
      turnId: "turn-1",
      item: { id: "msg-final", type: "agentMessage", text: "ok" },
    },
  });

  // Verify it is merged and retains runtime: true
  const timelineAfterCompleted = useClientStore.getState().timelinesByThread["thread-1"];
  const assistantMsg = timelineAfterCompleted.find((m) => m.id === "msg-final");
  expect(assistantMsg).toBeDefined();
  expect(assistantMsg.runtime).toBe(true);

  // 3. Subsequent ui/thread/patch omitting the message
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "3",
      timelineItems: [{ id: "turn-end:turn-1", kind: "turn_end", status: "completed" }],
    },
  });

  // Verify it is still preserved and not discarded
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ role: "user", text: "say ok" }),
    expect.objectContaining({ id: "msg-final", role: "assistant", text: "ok", done: true }),
  ]);
});

it("marks visible live timeline bridge patches ready so tool cards survive empty history hydration", async () => {
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage({ messages: [], hasMore: false, nextBefore: "" }));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
    threadTimelineReadyByThread: {},
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [
        {
          id: "tool-file-read",
          kind: "tool",
          title: "file",
          status: "completed",
          text: '{"success":true}',
          callId: "call-file",
        },
      ],
    },
  });

  expect(useClientStore.getState().threadTimelineReadyByThread["thread-1"]).toBe(true);

  await expect(useClientStore.getState().syncThreadState("thread-1")).resolves.toBe(true);

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "tool-file-read", kind: "tool", title: "file" })]);
});

it("replaces an unready cached timeline with the authoritative sync snapshot", async () => {
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [{ id: "fresh", kind: "assistant", text: "fresh content" }],
    },
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage({ messages: [], hasMore: false, nextBefore: "" }));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [{ id: "stale", role: "assistant", text: "stale cached content" }],
    },
    threadTimelineReadyByThread: {},
  });
  registerBridgeEventHandlersForTest();

  await expect(useClientStore.getState().syncThreadState("thread-1")).resolves.toBe(true);

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ id: "fresh", text: "fresh content" }),
  ]);
  expect(useClientStore.getState().timelinesByThread["thread-1"]).not.toEqual(
    expect.arrayContaining([expect.objectContaining({ id: "stale" })]),
  );
});

it("does not mark structural-only bridge patches ready", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
    threadTimelineReadyByThread: {},
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [{ id: "turn-end:turn-1", kind: "turn_end", status: "completed" }],
    },
  });

  expect(useClientStore.getState().threadTimelineReadyByThread["thread-1"]).toBeUndefined();
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([]);
});

it("drops live ghost command bridge patches emitted immediately after send", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-1": [] },
    threadTimelineReadyByThread: {},
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "1",
      timelineItems: [
        {
          id: "ghost-command",
          kind: "command",
          title: "执行命令",
          status: "completed",
          done: true,
        },
        {
          id: "tool-shadow-command",
          kind: "command",
          title: "file",
          toolName: "file",
          status: "completed",
          done: true,
        },
      ],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([]);
  expect(useClientStore.getState().threadTimelineReadyByThread["thread-1"]).toBeUndefined();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "2",
      timelineItems: [
        {
          id: "real-command",
          kind: "command",
          command: "npm test",
          status: "running",
        },
      ],
    },
  });

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "real-command", kind: "command", command: "npm test" })]);
  expect(useClientStore.getState().threadTimelineReadyByThread["thread-1"]).toBe(true);
});

it("applies bridge patches for timeline, token usage, diff and warnings", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "9007199254740993123",
      timelineItems: [{ id: "assistant-1", kind: "assistant", text: "pong" }],
      tokenUsage: { usedTokens: 12, contextWindowTokens: 100, usedPercent: 12 },
      activityStats: { lspCalls: 2, commands: 1, fileEdits: 1, toolCalls: { edit: 2, shell: 1 } },
      diffText: "diff --git a/file b/file",
    },
  });
  runtime.bridgeCallback({
    type: "rpc.failed",
    payload: { method: "turn/start", threadId: "thread-1", traceId: "trace-123" },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread["thread-1"][0]).toEqual(
    expect.objectContaining({
      role: "assistant",
      text: "pong",
    }),
  );
  expect(state.tokenUsageByThread["thread-1"]).toEqual({
    usedTokens: 12,
    contextWindowTokens: 100,
    usedPercent: 12,
  });
  expect(state.activityStatsByThread["thread-1"]).toEqual({
    lspCalls: 2,
    commands: 1,
    fileEdits: 1,
    toolCalls: { edit: 2, shell: 1 },
  });
  expect(state.diffTextByThread["thread-1"]).toContain("diff --git");
  expect(state.warningEntries).toEqual([expect.objectContaining({ level: "error", event: "rpc.failed" })]);
});
