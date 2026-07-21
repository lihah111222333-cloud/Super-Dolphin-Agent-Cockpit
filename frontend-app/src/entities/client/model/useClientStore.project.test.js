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
  flushAssistantDeltaBatch,
  flushPromises,
  frontendHealthSnapshot,
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  threadMessagesPage,
  useClientStore,
  waitFor,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("reloads the sidebar threads for the selected project after switching directories", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-old": [{ id: "old-user", role: "user", text: "old cwd message" }] },
    tokenUsageByThread: { "thread-old": { usedTokens: 8, contextWindowTokens: 100, usedPercent: 8 } },
    activityStatsByThread: { "thread-old": { lspCalls: 1, commands: 0, fileEdits: 0, toolCalls: {} } },
    diffTextByThread: { "thread-old": "old cwd diff" },
  });
  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-new",
    threads: [{ id: "thread-new", name: "Other project thread", provider: "claude", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-new",
    timelinesByThread: { "thread-new": [] },
    diffTextByThread: { "thread-new": "" },
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).resolves.toBe(true);

  expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/other" });
  expect(useClientStore.getState().activeThreadId).toBe("");
  expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-new", name: "Other project thread", provider: "claude" })]);
  expect(backendApi.getThreadState).not.toHaveBeenCalledWith({
    cwd: "/repo/other",
    threadId: "thread-new",
    includeDiff: true,
  });
  expect(useClientStore.getState().threads.some((thread) => thread.id === "thread-old")).toBe(false);
  expect(useClientStore.getState().timelinesByThread).not.toHaveProperty("thread-old");
  expect(useClientStore.getState().tokenUsageByThread).not.toHaveProperty("thread-old");
  expect(useClientStore.getState().activityStatsByThread).not.toHaveProperty("thread-old");
  expect(useClientStore.getState().diffTextByThread).not.toHaveProperty("thread-old");

  await useClientStore.getState().setActiveThread("thread-new");
  expect(backendApi.getThreadState).toHaveBeenCalledWith({
    cwd: "/repo/other",
    threadId: "thread-new",
    includeDiff: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-new");
});

it("keeps thread state tokenUsageByThread ahead of stale global token_usage", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Active", provider: "codex", status: "running" }],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Active", provider: "codex", status: "running" }],
    token_usage: { usedTokens: 999, contextWindowTokens: 2000, usedPercent: 50 },
    tokenUsageByThread: {
      "thread-1": { usedTokens: 42, contextWindowTokens: 100, usedPercent: 42 },
    },
    timelinesByThread: { "thread-1": [] },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  await expect(useClientStore.getState().syncThreadState("thread-1")).resolves.toBe(true);

  expect(useClientStore.getState().tokenUsageByThread["thread-1"]).toEqual({
    usedTokens: 42,
    contextWindowTokens: 100,
    usedPercent: 42,
  });
});

it("normalizes Go UI state status maps into the realtime status entry shape", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-wire",
    threads: [{ id: "thread-wire", agentId: "agent-wire", name: "Wire thread", provider: "codex", status: "idle" }],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-wire",
    threads: [{ id: "thread-wire", agent_id: "agent-wire", name: "Wire thread", state: "running" }],
    agents: [{ id: "agent-wire", thread_id: "thread-wire", provider: "codex", state: "running" }],
    statuses: { "thread-wire": "running" },
    statusHeadersByThread: { "thread-wire": "Thinking" },
    statusDetailsByThread: { "thread-wire": "Inspecting snapshot state" },
    interruptibleByThread: { "thread-wire": true },
    activityStatsByThread: {
      "thread-wire": { lspCalls: 2, commands: 3, fileEdits: 1, toolCalls: { read: 4 } },
    },
    agentRuntimeById: {
      "thread-wire": {
        agentId: "agent-wire",
        state: "running",
        provider: "codex",
        providerThreadId: "provider-thread-wire",
      },
    },
    timelinesByThread: { "thread-wire": [] },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  await expect(useClientStore.getState().syncThreadState("thread-wire")).resolves.toBe(true);

  expect(useClientStore.getState().statuses["thread-wire"]).toEqual({
    status: "running",
    statusHeader: "Thinking",
    statusDetails: "Inspecting snapshot state",
    interruptible: true,
    activityStats: { lspCalls: 2, commands: 3, fileEdits: 1, toolCalls: { read: 4 } },
    agentRuntime: {
      agentId: "agent-wire",
      state: "running",
      provider: "codex",
      providerThreadId: "provider-thread-wire",
    },
  });
});

it("preserves rich status fields when a same-status snapshot omits the parallel maps", async () => {
  const richStatus = {
    status: "running",
    statusHeader: "Thinking",
    statusDetails: "Inspecting live state",
    interruptible: true,
    activityStats: { lspCalls: 2, commands: 3, fileEdits: 1, toolCalls: { read: 4 } },
    agentRuntime: {
      agentId: "agent-wire",
      state: "running",
      provider: "codex",
      providerThreadId: "provider-thread-wire",
    },
  };
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-wire",
    threads: [{ id: "thread-wire", agentId: "agent-wire", name: "Wire thread", provider: "codex", status: "running" }],
    statuses: { "thread-wire": richStatus },
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-wire",
    threads: [{ id: "thread-wire", agent_id: "agent-wire", name: "Wire thread", state: "running" }],
    agents: [{ id: "agent-wire", thread_id: "thread-wire", provider: "codex", state: "running" }],
    statuses: { "thread-wire": "running" },
    timelinesByThread: { "thread-wire": [] },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  await expect(useClientStore.getState().syncThreadState("thread-wire")).resolves.toBe(true);

  expect(useClientStore.getState().statuses["thread-wire"]).toEqual(richStatus);
});

it("uses the active provider for deferred workflow designer threads without runtime metadata", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-design",
    provider: "codex",
    threads: [],
  });
  backendApi.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-design",
    threads: [{ id: "thread-design", name: "AI 设计流程", status: "created", agentKey: "dag_designer" }],
    timelinesByThread: { "thread-design": [] },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  await expect(useClientStore.getState().syncThreadState("thread-design")).resolves.toBe(true);

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-design",
      name: "AI 设计流程",
      provider: "codex",
      agentKey: "dag_designer",
    }),
  );
});

it("switches project after backend confirmation while the sidebar refresh continues in the background", async () => {
  const projectChange = deferred();
  const sidebarRefresh = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-old": [{ id: "old-user", role: "user", text: "old cwd message" }] },
  });
  backendApi.setActiveProject.mockReturnValue(projectChange.promise);
  backendApi.getSidebarState.mockReturnValue(sidebarRefresh.promise);

  const switchPromise = useClientStore.getState().setActiveProjectPath("/repo/other");

  expect(useClientStore.getState()).toEqual(expect.objectContaining({
    activeProject: "/repo/app",
    activeThreadId: "thread-old",
    chatSurfaceLoadingCwd: "",
  }));
  expect(useClientStore.getState().threads).toEqual([
    expect.objectContaining({ id: "thread-old", name: "Old project thread" }),
  ]);
  expect(backendApi.getSidebarState).not.toHaveBeenCalledWith({ cwd: "/repo/other" });

  projectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/other" });
  });

  expect(useClientStore.getState()).toEqual(
    expect.objectContaining({
      activeProject: "/repo/other",
      activeThreadId: "",
      chatSurfaceLoadingCwd: "/repo/other",
    }),
  );
  expect(useClientStore.getState().threads).toEqual([]);

  sidebarRefresh.resolve({
    activeThreadId: "thread-other",
    threads: [{ id: "thread-other", name: "Other project thread", provider: "claude", status: "idle" }],
  });

  await waitFor(() => expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-other", name: "Other project thread" })]));
  expect(useClientStore.getState().activeThreadId).toBe("");
  expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe("");

  await expect(switchPromise).resolves.toBe(true);
});

it("invalidates assistant buffers on a CWD switch before timer or stale patch flushes can repopulate the chat", async () => {
  vi.useFakeTimers();
  try {
    const projectChange = deferred();
    const sidebarRefresh = deferred();
    resetClientStoreForTests({
      cwd: "/repo/app",
      projectScopeCwd: "/repo/app",
      activeProject: "/repo/app",
      projects: ["/repo/app", "/repo/other"],
      activeThreadId: "thread-old",
      threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
      timelinesByThread: { "thread-old": [] },
    });
    backendApi.setActiveProject.mockReturnValue(projectChange.promise);
    backendApi.getSidebarState.mockReturnValue(sidebarRefresh.promise);
    registerBridgeEventHandlersForTest();

    runtime.bridgeCallback({
      type: "turn/output/delta",
      payload: {
        cwd: "/repo/app",
        threadId: "thread-old",
        turnId: "turn-old",
        itemId: "old-partial",
        delta: "old project content",
      },
    });

    const switchPromise = useClientStore.getState().setActiveProjectPath("/repo/other");
    projectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
    await vi.waitFor(() => {
      expect(useClientStore.getState().activeProject).toBe("/repo/other");
    });
    runtime.bridgeCallback({
      type: "ui/thread/patch",
      payload: {
        cwd: "/repo/app",
        threadId: "thread-old",
        sequence: "1",
        activeTurn: { id: "turn-old", status: "running" },
      },
    });
    await flushAssistantDeltaBatch();

    expect(useClientStore.getState()).toEqual(
      expect.objectContaining({
        activeProject: "/repo/other",
        activeThreadId: "",
        timelinesByThread: {},
      }),
    );
    expect(JSON.stringify(useClientStore.getState())).not.toContain("old project content");

    sidebarRefresh.resolve({ activeThreadId: "", threads: [] });
    await expect(switchPromise).resolves.toBe(true);
  } finally {
    vi.useRealTimers();
  }
});

it("publishes one safe Health diagnostic, warning and notice when a project sidebar refresh throws synchronously", async () => {
  const bearerSecret = "Bearer sk-frontend-session-refresh-secret-123456";
  const projectChange = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
  });
  backendApi.setActiveProject.mockReturnValue(projectChange.promise);
  backendApi.getSidebarState.mockImplementation(() => {
    throw new Error(`sidebar refresh failed ${bearerSecret}`);
  });

  const switchPromise = useClientStore.getState().setActiveProjectPath("/repo/other");
  projectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });

  await vi.waitFor(() => {
    expect(useClientStore.getState().actionNotice).toEqual(
      expect.objectContaining({
        message: "刷新会话列表失败，请稍后重试。",
        tone: "error",
      }),
    );
  });
  expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain(bearerSecret);
  expect(JSON.stringify(useClientStore.getState().warningEntries)).not.toContain(bearerSecret);
  expect(frontendHealthSnapshot().filter(({ actionId }) => actionId === "sidebar.project-threads.load")).toEqual([expect.objectContaining({ diagnosticId: expect.any(String) })]);
  expect(JSON.stringify(frontendHealthSnapshot())).not.toContain(bearerSecret);
  await expect(switchPromise).resolves.toBe(true);
});

it("publishes a safe notice and Health diagnostic when project session refresh fails", async () => {
  const bearerSecret = "Bearer sk-frontend-session-refresh-secret-123456";
  const projectChange = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
  });
  backendApi.setActiveProject.mockReturnValue(projectChange.promise);
  backendApi.getSidebarState.mockRejectedValue(new Error(`sidebar refresh failed ${bearerSecret}`));

  const switchPromise = useClientStore.getState().setActiveProjectPath("/repo/other");
  projectChange.resolve({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });

  await vi.waitFor(() => {
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: "刷新会话列表失败，请稍后重试。",
      tone: "error",
    }));
  });
  expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain(bearerSecret);
  expect(JSON.stringify(useClientStore.getState().warningEntries)).not.toContain(bearerSecret);
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({
      actionId: "sidebar.project-threads.load",
      diagnosticId: expect.any(String),
    }),
  ]));
  expect(JSON.stringify(frontendHealthSnapshot())).not.toContain(bearerSecret);
  await expect(switchPromise).resolves.toBe(true);
});

it("preserves a thread selected while project switch sidebar refresh is still in flight", async () => {
  const sidebarRefresh = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "idle", cwd: "/repo/app" }],
  });
  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  backendApi.getSidebarState.mockReturnValue(sidebarRefresh.promise);
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-other",
    threads: [{ id: "thread-other", name: "Other project thread", provider: "codex", status: "idle", cwd: "/repo/other" }],
    timelinesByThread: {
      "thread-other": [{ id: "message-thread-other", role: "assistant", text: "other message", time: "2026-06-18T00:00:00Z" }],
    },
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  await expect(
    useClientStore.getState().setActiveProjectPath("/repo/other", {
      preserveActiveThreadId: true,
    }),
  ).resolves.toBe(true);
  await expect(useClientStore.getState().setActiveThread("thread-other")).resolves.toBe(true);
  expect(useClientStore.getState().activeThreadId).toBe("thread-other");

  sidebarRefresh.resolve({
    activeThreadId: "",
    threads: [{ id: "thread-other", name: "Other project thread", provider: "codex", status: "idle", cwd: "/repo/other" }],
  });
  await flushPromises();

  expect(useClientStore.getState().activeThreadId).toBe("thread-other");
});

it("does not shrink the sidebar project cache from a thread-scoped state sync", async () => {
  const threads = [
    { id: "thread-a", name: "Thread A", provider: "codex", status: "idle", cwd: "/repo/app" },
    { id: "thread-b", name: "Thread B", provider: "codex", status: "idle", cwd: "/repo/app" },
  ];
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app"],
    activeThreadId: "thread-a",
    threads,
    sidebarThreadsByProject: {
      "/repo/app": threads,
    },
  });
  backendApi.getThreadState.mockResolvedValue({
    activeThreadId: "thread-b",
    threads: [threads[1]],
    timelinesByThread: {
      "thread-b": [{ id: "message-thread-b", role: "assistant", text: "thread b message", time: "2026-06-18T00:00:00Z" }],
    },
  });
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  await expect(useClientStore.getState().setActiveThread("thread-b")).resolves.toBe(true);

  expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-b", name: "Thread B" })]);
  expect(useClientStore.getState().sidebarThreadsByProject["/repo/app"]).toEqual([
    expect.objectContaining({ id: "thread-a", name: "Thread A" }),
    expect.objectContaining({ id: "thread-b", name: "Thread B" }),
  ]);
});

it("starts a clear-surface sidebar refresh without waiting for a background refresh", async () => {
  const backgroundRefresh = deferred();
  const clearSurfaceRefresh = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/other",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-other",
    threads: [{ id: "thread-other", name: "Other project thread", provider: "codex", status: "running" }],
  });
  backendApi.getSidebarState.mockReturnValueOnce(backgroundRefresh.promise).mockReturnValueOnce(clearSurfaceRefresh.promise);
  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({ type: "ui/sidebar/changed", payload: { revision: 2 } });
  expect(backendApi.getSidebarState).toHaveBeenCalledTimes(1);

  const switchPromise = useClientStore.getState().setActiveProjectPath("/repo/other");
  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledTimes(2);
  });
  expect(useClientStore.getState()).toEqual(
    expect.objectContaining({
      activeProject: "/repo/other",
      activeThreadId: "",
      chatSurfaceLoadingCwd: "/repo/other",
    }),
  );
  expect(useClientStore.getState().threads).toEqual([]);

  clearSurfaceRefresh.resolve({
    activeThreadId: "thread-clear",
    threads: [{ id: "thread-clear", name: "Clear refresh thread", provider: "claude", status: "idle" }],
  });

  await vi.waitFor(() => {
    expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-clear", name: "Clear refresh thread" })]);
  });
  expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe("");

  backgroundRefresh.resolve({
    activeThreadId: "thread-stale",
    threads: [{ id: "thread-stale", name: "Stale background thread", provider: "codex", status: "running" }],
  });
  await Promise.resolve();
  await Promise.resolve();

  expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-clear", name: "Clear refresh thread" })]);
  await expect(switchPromise).resolves.toBe(true);
});

it("filters mixed sidebar snapshots to the selected project cwd", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
  });
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-other",
    threads: [
      { id: "thread-app", cwd: "/repo/app", name: "App thread", provider: "codex", status: "idle" },
      { id: "thread-other", cwd: "/repo/other", name: "Other thread", provider: "claude", status: "running" },
    ],
  });

  await expect(useClientStore.getState().setActiveProjectPath("/repo/app")).resolves.toBe(true);

  expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-app", name: "App thread", cwd: "/repo/app" })]);
  expect(useClientStore.getState().activeThreadId).toBe("");
});

it("keeps runtime cwd threads when Windows separators differ from the selected project path", async () => {
  resetClientStoreForTests({
    cwd: "C:/Users/ai03/Desktop/Super-Dolphin",
    projectScopeCwd: "C:/Users/ai03/Desktop/Super-Dolphin",
    activeProject: "C:/Users/ai03/Desktop/Super-Dolphin",
    projects: ["C:/Users/ai03/Desktop/Super-Dolphin"],
  });
  backendApi.setActiveProject.mockResolvedValue({
    projects: ["C:/Users/ai03/Desktop/Super-Dolphin"],
    active: "C:/Users/ai03/Desktop/Super-Dolphin",
  });
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "agent-win",
    threads: [{ id: "agent-win", agent_id: "agent-win", name: "Windows cwd thread", provider: "codex", status: "idle" }],
    agentRuntimeById: {
      "agent-win": {
        cwd: "C:\\Users\\ai03\\Desktop\\Super-Dolphin",
        provider: "codex",
        providerThreadId: "session-win",
      },
    },
  });

  await expect(useClientStore.getState().setActiveProjectPath("C:/Users/ai03/Desktop/Super-Dolphin")).resolves.toBe(true);

  expect(useClientStore.getState().threads).toEqual([
    expect.objectContaining({
      id: "agent-win",
      cwd: "C:\\Users\\ai03\\Desktop\\Super-Dolphin",
      name: "Windows cwd thread",
    }),
  ]);
});
