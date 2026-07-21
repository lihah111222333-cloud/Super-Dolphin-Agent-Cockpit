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

it("keeps composer drafts isolated by selected thread and project cwd", async () => {
  const reviewCapability = {
    kind: "skill",
    key: "skill:project::review:/repo/app/.agents/skills/review",
    name: "review",
    label: "Code Review",
    availability: "ready",
    ref: {
      name: "review",
      scope: "project",
      personalType: "",
      path: "/repo/app/.agents/skills/review",
    },
  };
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-b", cwd: "/repo/app", name: "Thread B", provider: "codex", status: "idle" },
    ],
    draft: "draft for A",
    attachments: [{ path: "/tmp/a.txt", name: "a.txt" }],
    composerCapabilities: [reviewCapability],
  });
  backendApi.getThreadState.mockImplementation(({ threadId }) =>
    Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
        { id: "thread-b", cwd: "/repo/app", name: "Thread B", provider: "codex", status: "idle" },
      ],
      timelinesByThread: { [threadId]: [] },
    }),
  );

  await useClientStore.getState().setActiveThread("thread-b");
  expect(useClientStore.getState().draft).toBe("");
  expect(useClientStore.getState().attachments).toEqual([]);
  expect(useClientStore.getState().composerCapabilities).toEqual([]);

  useClientStore.getState().setDraft("draft for B");

  await useClientStore.getState().setActiveThread("thread-a");
  expect(useClientStore.getState().draft).toBe("draft for A");
  expect(useClientStore.getState().attachments).toEqual([expect.objectContaining({ path: "/tmp/a.txt", name: "a.txt" })]);
  expect(useClientStore.getState().composerCapabilities).toEqual([
    expect.objectContaining({
      key: reviewCapability.key,
      availability: "unverified",
    }),
  ]);

  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "",
    threads: [{ id: "thread-other", cwd: "/repo/other", name: "Other project thread", provider: "claude", status: "idle" }],
  });
  await useClientStore.getState().setActiveProjectPath("/repo/other");

  expect(useClientStore.getState().draft).toBe("");
  expect(useClientStore.getState().attachments).toEqual([]);
  expect(useClientStore.getState().composerCapabilities).toEqual([]);

  backendApi.setActiveProject.mockResolvedValueOnce({
    projects: ["/repo/app", "/repo/other"],
    active: "/repo/app",
  });
  backendApi.getSidebarState.mockResolvedValueOnce({
    activeThreadId: "",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-b", cwd: "/repo/app", name: "Thread B", provider: "codex", status: "idle" },
    ],
  });
  await useClientStore.getState().setActiveProjectPath("/repo/app");
  await useClientStore.getState().setActiveThread("thread-a");
  expect(useClientStore.getState().composerCapabilities).toEqual([
    expect.objectContaining({
      key: reviewCapability.key,
      availability: "unverified",
    }),
  ]);
});

it("does not keep the old active thread when the selected project sidebar has no active thread", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
  });
  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "",
    threads: [{ id: "thread-new", name: "Other project thread", provider: "claude", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-new",
    timelinesByThread: { "thread-new": [] },
    diffTextByThread: { "thread-new": "" },
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).resolves.toBe(true);

  expect(backendApi.getThreadState).not.toHaveBeenCalledWith({
    cwd: "/repo/other",
    threadId: "thread-new",
    includeDiff: true,
  });
  expect(useClientStore.getState().activeThreadId).toBe("");
  expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-new", name: "Other project thread" })]);
});

it("loads and saves global composer model preferences when no thread is selected", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    provider: "codex",
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.codex.model": "gpt-5.4",
        "settings.provider.codex.effort": "medium",
        "settings.provider.codex.codexModelProvider": "openai",
      }[key] ?? null,
    ),
  );

  await expect(useClientStore.getState().refreshProviderConfig()).resolves.toEqual(
    expect.objectContaining({
      provider: "codex",
      model: "gpt-5.4",
      effort: "medium",
      codexModelProvider: "openai",
    }),
  );

  await expect(useClientStore.getState().saveComposerModelConfig({ model: "gpt-5.5", effort: "xhigh" })).resolves.toBe(true);
  await expect(useClientStore.getState().saveComposerModelProvider("openrouter")).resolves.toBe(true);

  expect(backendApi.setPreference).toHaveBeenCalledWith({ cwd: "/repo/app", key: "settings.provider.codex.model", value: "gpt-5.5" });
  expect(backendApi.setPreference).toHaveBeenCalledWith({ cwd: "/repo/app", key: "settings.provider.codex.effort", value: "xhigh" });
  expect(backendApi.setPreference).toHaveBeenCalledWith({ cwd: "/repo/app", key: "settings.provider.codex.codexModelProvider", value: "openrouter" });
  expect(useClientStore.getState().providerConfig).toEqual(
    expect.objectContaining({
      model: "gpt-5.5",
      effort: "xhigh",
      codexModelProvider: "openrouter",
    }),
  );
});

it("saves active thread model overrides through thread config RPCs", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    provider: "codex",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadConfig.mockResolvedValue({
    threadId: "thread-1",
    provider: "codex",
    supportsThreadOverride: true,
    override: {},
    effective: { model: "gpt-5.4", effort: "medium" },
  });
  backendApi.setThreadConfig.mockResolvedValue({
    threadId: "thread-1",
    provider: "codex",
    supportsThreadOverride: true,
    override: { model: "gpt-5.5", effort: "" },
    effective: { model: "gpt-5.5", effort: "medium" },
  });

  await expect(useClientStore.getState().loadThreadConfig("thread-1")).resolves.toEqual(
    expect.objectContaining({
      supportsThreadOverride: true,
    }),
  );
  await expect(useClientStore.getState().saveComposerModelConfig({ model: "gpt-5.5", effort: "" })).resolves.toBe(true);

  expect(backendApi.setThreadConfig).toHaveBeenCalledWith({
    threadId: "thread-1",
    model: "gpt-5.5",
    effort: "",
  });
  expect(backendApi.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({ key: "settings.provider.codex.model" }));
  expect(useClientStore.getState().threadConfigByThread["thread-1"]).toEqual(
    expect.objectContaining({
      override: { model: "gpt-5.5", effort: "" },
    }),
  );
});

it("uses global model preferences when the selector has no thread config target", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent-failed",
    provider: "codex",
    providerConfig: { provider: "codex", model: "gpt-5.5", effort: "xhigh" },
    threads: [{ id: "agent-failed", name: "Failed runtime", provider: "codex", status: "error" }],
  });

  await expect(
    useClientStore.getState().saveComposerModelConfig({
      threadId: "",
      model: "gpt-5.4",
      effort: "medium",
    }),
  ).resolves.toBe(true);

  expect(backendApi.getThreadConfig).not.toHaveBeenCalled();
  expect(backendApi.setThreadConfig).not.toHaveBeenCalled();
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "settings.provider.codex.model",
    value: "gpt-5.4",
  });
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "settings.provider.codex.effort",
    value: "medium",
  });
});

it("canonicalizes backend thread ids before scoped thread RPCs", async () => {
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "agent_123",
    threads: [
      {
        id: "agent_123",
        thread_id: "thread-canonical",
        agent_id: "agent_123",
        name: "Runtime thread",
        provider: "codex",
        status: "running",
      },
    ],
  });
  backendApi.getThreadState.mockResolvedValue({ activeThreadId: "thread-canonical", timelinesByThread: {} });

  await useClientStore.getState().bootstrap();
  await useClientStore.getState().compactActiveThread();

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-canonical");
  expect(state.threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-canonical",
      agentId: "agent_123",
    }),
  );
  expect(backendApi.getThreadState).not.toHaveBeenCalled();
  expect(backendApi.compactThread).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-canonical" });
});

it("does not query thread config for codex runtime agent ids during chat switches", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_1780223999392305000",
    provider: "codex",
    threads: [
      {
        id: "agent_1780223999392305000",
        agentId: "agent_1780223999392305000",
        name: "Runtime codex thread",
        provider: "codex",
        status: "running",
      },
    ],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "agent_1780223999392305000",
    threads: [
      {
        id: "agent_1780223999392305000",
        agent_id: "agent_1780223999392305000",
        name: "Runtime codex thread",
        provider: "codex",
        status: "running",
      },
    ],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  await useClientStore.getState().syncThreadState("agent_1780223999392305000");

  expect(backendApi.getThreadState).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "agent_1780223999392305000",
    includeDiff: true,
  });
  expect(backendApi.getThreadConfig).not.toHaveBeenCalled();
  expect(useClientStore.getState().warningEntries).toEqual([]);
});

it("does not query thread config for agent-only runtime threads", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_1780223389861443000",
    provider: "codex",
    threads: [
      {
        id: "agent_1780223389861443000",
        agentId: "agent_1780223389861443000",
        name: "Runtime codex thread",
        provider: "codex",
        status: "running",
      },
    ],
  });

  await expect(useClientStore.getState().loadThreadConfig("agent_1780223389861443000")).resolves.toBeNull();
  await expect(
    useClientStore.getState().saveComposerModelConfig({
      threadId: "agent_1780223389861443000",
      model: "gpt-5.5",
      effort: "xhigh",
    }),
  ).resolves.toBe(true);

  expect(backendApi.getThreadConfig).not.toHaveBeenCalled();
  expect(backendApi.setThreadConfig).not.toHaveBeenCalled();
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "settings.provider.codex.model",
    value: "gpt-5.5",
  });
  expect(useClientStore.getState().warningEntries).toEqual([]);
});

it("does not auto-retry thread config after a failed auto-load for the same thread", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    provider: "codex",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });
  backendApi.getThreadConfig.mockRejectedValue(new Error("thread session is not available"));

  await useClientStore.getState().syncThreadState("thread-1");
  await useClientStore.getState().syncThreadState("thread-1");

  expect(backendApi.getThreadConfig).toHaveBeenCalledTimes(1);
  expect(useClientStore.getState().warningEntries.filter((entry) => entry.event === "thread.config.get.failed")).toHaveLength(1);
});

it("releases the timeline loading state and exposes a stalled history sync failure", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
  });
  const timeout = new Error("ui/state/get timed out after 10000ms");
  timeout.name = "BridgeRPCTimeoutError";
  backendApi.getThreadState.mockRejectedValueOnce(timeout);

  await expect(useClientStore.getState().syncThreadState("thread-1")).resolves.toBe(false);

  const state = useClientStore.getState();
  expect(state.threadStateLoadingByThread["thread-1"]).toBe(false);
  expect(state.actionNotice).toEqual(expect.objectContaining({
    message: "同步会话失败，请重试。",
    tone: "error",
  }));
  expect(JSON.stringify(state.actionNotice)).not.toContain(timeout.message);
  expect(state.warningEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({
      event: "thread.sync.failed",
      threadId: "thread-1",
      fields: expect.objectContaining({
        error: "[redacted]",
      }),
    }),
  ]));
  expect(JSON.stringify(state.warningEntries)).not.toContain(timeout.message);
});

it("never sends unknown runtime agent ids to thread-scoped RPCs", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [],
  });

  await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);
  await expect(useClientStore.getState().compactActiveThread()).resolves.toBe(false);
  await expect(useClientStore.getState().recoverActiveThread()).resolves.toBe(false);
  await expect(useClientStore.getState().archiveThread("agent_123", true)).resolves.toBe(false);

  expect(backendApi.interruptTurn).not.toHaveBeenCalled();
  expect(backendApi.compactThread).not.toHaveBeenCalled();
  expect(backendApi.recoverThread).not.toHaveBeenCalled();
  expect(backendApi.setPreference).not.toHaveBeenCalled();
});

it("opens backend-resolved DAG child threads even when the id looks like an agent runtime id", async () => {
  resetClientStoreForTests({
    cwd: "/repo/main",
    activeProject: "/repo/main",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running" }],
  });
  backendApi.resolveThreadIdentity.mockResolvedValue({
    id: "agent_child_1",
    agent_id: "agent_child_1",
    name: "Review child",
    provider: "codex",
    cwd: "/repo/main/.worktrees/review-child",
    status: "running",
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValue(
    threadMessagesPage({
      messages: [{ id: "m-child", role: "assistant", content: "子代理评审完成" }],
      hasMore: false,
      nextBefore: "",
    }),
  );

  await expect(useClientStore.getState().openThreadById("agent_child_1", { source: "dag-node" })).resolves.toBe(true);

  expect(backendApi.resolveThreadIdentity).toHaveBeenCalledWith({ cwd: "/repo/main", threadId: "agent_child_1" });
  expect(backendApi.getThreadState).toHaveBeenCalledWith({ cwd: "/repo/main", threadId: "agent_child_1", includeDiff: false });
  expect(backendApi.getThreadMessages).toHaveBeenCalledWith({ threadId: "agent_child_1", limit: 300 });
  expect(useClientStore.getState().activeThreadId).toBe("agent_child_1");
  expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "agent_child_1", agentId: "agent_child_1", name: "Review child" })]));
  expect(useClientStore.getState().timelinesByThread.agent_child_1).toEqual([expect.objectContaining({ text: "子代理评审完成" })]);
});

it("continues backend-resolved DAG child threads with the child thread cwd", async () => {
  resetClientStoreForTests({
    cwd: "/repo/main",
    activeProject: "/repo/main",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running", cwd: "/repo/main" }],
  });
  backendApi.resolveThreadIdentity.mockResolvedValue({
    id: "agent_child_1",
    agent_id: "agent_child_1",
    name: "Review child",
    provider: "codex",
    cwd: "/repo/main/.worktrees/review-child",
    status: "done",
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running", cwd: "/repo/main" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValue(
    threadMessagesPage({
      messages: [{ id: "m-child", role: "assistant", content: "子代理评审完成" }],
      hasMore: false,
      nextBefore: "",
    }),
  );
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await expect(useClientStore.getState().openThreadById("agent_child_1", { source: "dag-node" })).resolves.toBe(true);
  expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "agent_child_1", cwd: "/repo/main/.worktrees/review-child" })]));
  useClientStore.getState().setDraft("继续处理这个 DAG 结果");
  await useClientStore.getState().sendDraft();

  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/main/.worktrees/review-child",
    threadId: "agent_child_1",
    input: [{ type: "text", text: "继续处理这个 DAG 结果" }],
    manualSkillSelection: false,
  });
});

it("shows DAG node prompt and result when a child thread has no provider history", async () => {
  resetClientStoreForTests({
    cwd: "/repo/main",
    activeProject: "/repo/main",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running" }],
  });
  backendApi.resolveThreadIdentity.mockResolvedValue({
    id: "agent_child_1",
    agent_id: "agent_child_1",
    name: "Review child",
    provider: "codex",
    cwd: "/repo/main/.worktrees/review-child",
    status: "done",
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValue(
    threadMessagesPage({
      messages: [],
      hasMore: false,
      nextBefore: "",
    }),
  );

  await expect(
    useClientStore.getState().openThreadById("agent_child_1", {
      source: "dag-node",
      dagNode: {
        nodeKey: "review",
        title: "Review",
        config: { prompt: "请评审这个方案" },
        result: "评审完成：可以继续。",
      },
    }),
  ).resolves.toBe(true);

  expect(useClientStore.getState().timelinesByThread.agent_child_1).toEqual([
    expect.objectContaining({ role: "user", text: "请评审这个方案" }),
    expect.objectContaining({ role: "assistant", text: "评审完成：可以继续。" }),
  ]);
  expect(useClientStore.getState().threadTimelineReadyByThread.agent_child_1).toBe(true);
  expect(useClientStore.getState().threadStateLoadingByThread.agent_child_1).toBe(false);
});

it("prefers provider history over DAG node fallback content", async () => {
  resetClientStoreForTests({
    cwd: "/repo/main",
    activeProject: "/repo/main",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running" }],
  });
  backendApi.resolveThreadIdentity.mockResolvedValue({
    id: "agent_child_1",
    agent_id: "agent_child_1",
    name: "Review child",
    provider: "codex",
    cwd: "/repo/main/.worktrees/review-child",
    status: "done",
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "",
    threads: [{ id: "thread-main", name: "Main", provider: "codex", status: "running" }],
    timelinesByThread: {},
  });
  backendApi.getThreadMessages.mockResolvedValue(
    threadMessagesPage({
      messages: [{ id: "m-real", role: "assistant", content: "真实 provider 历史" }],
      hasMore: false,
      nextBefore: "",
    }),
  );

  await expect(
    useClientStore.getState().openThreadById("agent_child_1", {
      source: "dag-node",
      dagNode: {
        nodeKey: "review",
        title: "Review",
        config: { prompt: "请评审这个方案" },
        result: "DAG 兜底结果",
      },
    }),
  ).resolves.toBe(true);

  expect(useClientStore.getState().timelinesByThread.agent_child_1).toEqual([expect.objectContaining({ role: "assistant", text: "真实 provider 历史" })]);
});
