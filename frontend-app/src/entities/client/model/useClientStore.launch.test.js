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
import { expect, it, registerClientStoreTestHooks, resetClientStoreForTests, threadMessagesPage, useClientStore } from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("filters injected AGENTS instructions from restored thread history", async () => {
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
        {
          id: "injected-agents",
          role: "user",
          content: [
            "# AGENTS.md instructions for /home/ai01@f666.com/桌面/project/Super-Dolphin",
            "",
            "<INSTRUCTIONS>",
            "# Super Dolphin Agent Agent Context Policy",
            "</INSTRUCTIONS>",
          ].join("\n"),
          createdAt: "2026-05-30T00:00:00Z",
        },
        { id: "real-user", role: "user", content: "真实用户问题", createdAt: "2026-05-30T00:01:00Z" },
        { id: "assistant-reply", role: "assistant", content: "真实 AI 回复", createdAt: "2026-05-30T00:02:00Z" },
      ],
      total: 3,
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  expect(useClientStore.getState().timelinesByThread["thread-1"].map((message) => message.text)).toEqual(["真实用户问题", "真实 AI 回复"]);
});

it("[regression] strips <image> XML placeholders and extracts image attachments from history metadata", async () => {
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
        {
          id: "user-with-image",
          role: "user",
          content: "能先识别这张截图内容。<image name=[Image #1]></image>",
          metadata: {
            input: [
              { type: "text", text: "能先识别这张截图内容。" },
              { type: "localImage", path: "/var/folders/abc/T/clipboard-123456.png" },
            ],
          },
          createdAt: "2026-05-30T00:00:00Z",
        },
        {
          id: "assistant-reply",
          role: "assistant",
          content: "图片内容是一段代码。",
          createdAt: "2026-05-30T00:01:00Z",
        },
      ],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  const userMsg = timeline.find((m) => m.role === "user");

  // XML 占位符应被剥离
  expect(userMsg.text).toBe("能先识别这张截图内容。");
  expect(userMsg.text).not.toContain("<image");
  // 图片附件应被提取
  expect(Array.isArray(userMsg.attachments)).toBe(true);
  expect(userMsg.attachments).toHaveLength(1);
  expect(userMsg.attachments[0].kind).toBe("image");
  expect(userMsg.attachments[0].path).toBe("/var/folders/abc/T/clipboard-123456.png");
  // clipboard 路径应转为 /clipboard/ HTTP 路由
  expect(userMsg.attachments[0].previewUrl).toBe("/clipboard/clipboard-123456.png");
});

it("applies the selected thread first message page without waiting for older history", async () => {
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
  const messages = Array.from({ length: 301 }, (_, index) => {
    const id = index + 1;
    return {
      id,
      role: id % 2 === 0 ? "assistant" : "user",
      content: `message ${id}`,
      createdAt: new Date(Date.UTC(2026, 4, 30, 0, id, 0)).toISOString(),
    };
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: messages.slice(1).reverse(),
      total: 301,
      hasMore: true,
      nextBefore: "2",
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  expect(backendApi.getThreadMessages).toHaveBeenNthCalledWith(1, { threadId: "thread-1", limit: 300 });
  expect(backendApi.getThreadMessages).toHaveBeenCalledTimes(1);
  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  expect(timeline).toHaveLength(300);
  expect(timeline[0]).toEqual(expect.objectContaining({ id: "2", text: "message 2" }));
  expect(timeline[299]).toEqual(expect.objectContaining({ id: "301", text: "message 301" }));
  expect(useClientStore.getState().threadMessagePaginationByThread["thread-1"]).toEqual(
    expect.objectContaining({
      hasMore: true,
      nextBefore: "2",
      loading: false,
    }),
  );
  expect(backendApi.emitFrontendTraceEvent).toHaveBeenCalledWith(
    expect.objectContaining({
      phase: "frontend.thread_history.initial_page.load",
      thread_id: "thread-1",
      page_size: 300,
      message_count: 300,
      has_more: true,
      next_before: "present",
      status: "ok",
    }),
  );
  expect(JSON.stringify(backendApi.emitFrontendTraceEvent.mock.calls.at(-1)[0])).not.toContain("message 301");
});

it("loads older thread messages on demand with backend pagination cursors", async () => {
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
  backendApi.getThreadMessages
    .mockResolvedValueOnce(
      threadMessagesPage({
        messages: [
          { id: "3", role: "assistant", content: "new reply", createdAt: "2026-05-30T00:03:00Z" },
          { id: "2", role: "user", content: "new prompt", createdAt: "2026-05-30T00:02:00Z" },
        ],
        hasMore: true,
        nextBefore: "2",
      }),
    )
    .mockResolvedValueOnce(
      threadMessagesPage({
        messages: [{ id: "1", role: "user", content: "old prompt", createdAt: "2026-05-30T00:01:00Z" }],
        hasMore: false,
        nextBefore: "",
      }),
    );

  await useClientStore.getState().syncThreadState("thread-1");
  await expect(useClientStore.getState().loadOlderThreadMessages("thread-1")).resolves.toBe(true);

  expect(backendApi.getThreadMessages).toHaveBeenNthCalledWith(2, {
    threadId: "thread-1",
    limit: 300,
    before: "2",
  });
  expect(useClientStore.getState().timelinesByThread["thread-1"].map((message) => message.text)).toEqual(["old prompt", "new prompt", "new reply"]);
  expect(useClientStore.getState().threadMessagePaginationByThread["thread-1"]).toEqual(
    expect.objectContaining({
      hasMore: false,
      nextBefore: "",
      loading: false,
    }),
  );
});

it("rejects malformed string hasMore thread message pages with a visible failure", async () => {
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
  backendApi.getThreadMessages.mockResolvedValueOnce({
    messages: [
      {
        id: 2,
        agentId: "",
        role: "assistant",
        eventType: "",
        method: "",
        content: "reply",
        createdAt: "2026-05-30T00:02:00Z",
      },
    ],
    total: 1,
    hasMore: "0",
    nextBefore: "2",
  });

  await expect(useClientStore.getState().syncThreadState("thread-1")).resolves.toBe(true);

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toBeUndefined();
  expect(useClientStore.getState().warningEntries).toEqual([
    expect.objectContaining({
      event: "thread.messages.failed",
      level: "error",
      threadId: "thread-1",
    }),
  ]);
});

it("does not invent an older-message cursor when backend hasMore is true without nextBefore", async () => {
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
      messages: [{ id: "2", role: "assistant", content: "reply", createdAt: "2026-05-30T00:02:00Z" }],
      hasMore: true,
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");
  await expect(useClientStore.getState().loadOlderThreadMessages("thread-1")).resolves.toBe(false);

  expect(backendApi.getThreadMessages).toHaveBeenCalledTimes(1);
  expect(useClientStore.getState().warningEntries).toEqual([
    expect.objectContaining({
      event: "thread.messages.pagination.missing_cursor",
      threadId: "thread-1",
    }),
  ]);
});

it("keeps thread/state assistant text when thread/messages later returns empty assistant rows", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        agentId: "agent_1780323743107010000",
        providerThreadId: "019e8390-77dc-7951-960f-246fac8780bd",
        name: "Existing",
        provider: "codex",
        status: "idle",
      },
    ],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        agentId: "agent_1780323743107010000",
        providerThreadId: "019e8390-77dc-7951-960f-246fac8780bd",
        name: "Existing",
        provider: "codex",
        status: "idle",
      },
    ],
    timelinesByThread: {
      "thread-1": [{ id: "assistant-1", role: "assistant", text: "1", createdAt: "2026-06-01T14:22:00Z" }],
    },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(
    threadMessagesPage({
      messages: [
        { id: "assistant-1", role: "assistant", content: "", createdAt: "2026-06-01T14:26:00Z" },
        { id: "assistant-2", role: "assistant", content: "", createdAt: "2026-06-01T14:27:00Z" },
      ],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "assistant-1", role: "assistant", text: "1" })]);
});

it("does not let later thread/state empty assistant rows replace visible replies", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());
  backendApi.getThreadState
    .mockResolvedValueOnce({
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
      timelinesByThread: {
        "thread-1": [{ id: "assistant-1", role: "assistant", text: "1", createdAt: "2026-06-01T14:22:00Z" }],
      },
    })
    .mockResolvedValueOnce({
      activeThreadId: "thread-1",
      threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
      timelinesByThread: {
        "thread-1": [
          { id: "assistant-1", role: "assistant", text: "", createdAt: "2026-06-01T14:34:00Z" },
          { id: "assistant-empty-new", role: "assistant", text: "", createdAt: "2026-06-01T14:34:01Z" },
        ],
      },
    });

  await useClientStore.getState().syncThreadState("thread-1");
  await useClientStore.getState().syncThreadState("thread-1");

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ id: "assistant-1", role: "assistant", text: "1" })]);
});

it("reads thread/messages content fields instead of rendering blank assistant bubbles", async () => {
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
      messages: [{ id: "assistant-content", role: "assistant", content: "loaded from content field", createdAt: "2026-06-01T14:26:00Z" }],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ role: "assistant", text: "loaded from content field" })]);
});

it("does not render turn_aborted control blocks from thread/messages history", async () => {
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
        { id: "message-user", role: "user", content: "visible prompt", createdAt: "2026-06-01T14:26:00Z" },
        {
          id: "message-aborted-control",
          role: "user",
          content:
            [
              "<turn_aborted>",
              "The user interrupted the previous turn on purpose.",
              "Any running unified exec processes may still be running in the background.",
              "If any tools/commands were aborted, they may have partially executed.",
              "</turn_aborted>",
            ].join("\n"),
          createdAt: "2026-06-01T14:27:00Z",
        },
        { id: "assistant-text", role: "assistant", content: "visible reply", createdAt: "2026-06-01T14:28:00Z" },
      ],
    }),
  );

  await useClientStore.getState().syncThreadState("thread-1");

  expect(useClientStore.getState().timelinesByThread["thread-1"].map((message) => message.text)).toEqual(["visible prompt", "visible reply"]);
});

it("builds thread/start launch payload from provider preferences", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use configured launch",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "/Users/test/.codex-alt",
        "settings.provider.codex.codexInstanceKey": "desktop-main",
        "settings.provider.codex.codexModelProvider": "openrouter",
        "settings.activePromptKey": "main/dag_designer_zh",
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-configured" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.getPreference).toHaveBeenCalledWith({ cwd: "/repo/app", key: "settings.provider.active" });
  expect(backendApi.getPreference).toHaveBeenCalledWith({ cwd: "/repo/app", key: "settings.activePromptKey" });
  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      modelProvider: "codex",
      model: "gpt-5.5",
      effort: "xhigh",
      prompt_key: "main/dag_designer_zh",
      config: {
        codexHome: "/Users/test/.codex-alt",
        codexInstanceKey: "desktop-main",
        codexModelProvider: "openrouter",
      },
    }),
  );
});

it("rejects a Claude active provider preference before thread/start", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Do not silently remap provider",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "claude",
        "settings.provider.claude.model": "sonnet",
        "settings.provider.claude.effort": "high",
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-should-not-start" });

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("invalid UI preference response for settings.provider.active");

  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().draft).toBe("Do not silently remap provider");
});

it("includes default Codex identity preferences in thread/start launch payload", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use default Codex identity",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "~/.codex",
        "settings.provider.codex.codexInstanceKey": "default",
        "settings.provider.codex.codexModelProvider": "openai",
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-default-codex" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  const payload = backendApi.startThread.mock.calls[0][0];
  expect(payload).toEqual(
    expect.objectContaining({
      cwd: "/repo/app",
      modelProvider: "codex",
      model: "gpt-5.5",
      effort: "xhigh",
      config: {
        codexHome: "~/.codex",
        codexInstanceKey: "default",
        codexModelProvider: "openai",
      },
    }),
  );
});

it("falls back to global Codex identity preferences for thread/start launch payload", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use global Codex identity",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ cwd, key }) => {
    if (!cwd) {
      return Promise.resolve(
        {
          "settings.provider.codex.codexHome": "C:\\Users\\ai01\\.codex",
          "settings.provider.codex.codexInstanceKey": "default",
          "settings.provider.codex.codexModelProvider": "openai",
        }[key] ?? null,
      );
    }
    return Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "low",
      }[key] ?? null,
    );
  });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-global-codex" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.getPreference).toHaveBeenCalledWith({ key: "settings.provider.codex.codexHome" });
  expect(backendApi.getPreference).toHaveBeenCalledWith({ key: "settings.provider.codex.codexInstanceKey" });
  expect(backendApi.getPreference).toHaveBeenCalledWith({ key: "settings.provider.codex.codexModelProvider" });
  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      modelProvider: "codex",
      model: "gpt-5.5",
      effort: "low",
      codexModelProvider: "openai",
      config: {
        codexHome: "C:\\Users\\ai01\\.codex",
        codexInstanceKey: "default",
        codexModelProvider: "openai",
      },
    }),
  );
});
