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
  diagnosticBreadcrumbs,
  expect,
  flushPromises,
  it,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  threadMessage,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("fails fast when thread message fixtures drift from the wire contract", () => {
  expect(() => threadMessage({ id: "legacy", text: "not a wire field" })).toThrow("unsupported thread/messages fixture key: text");
  expect(() => threadMessagesPage({ hasMore: "false" })).toThrow("thread/messages fixture hasMore must be a boolean");
  expect(threadMessagesPage({ messages: [{ id: "stable-label", content: "ok" }] }).messages[0]).toEqual(
    expect.objectContaining({
      id: 1,
      content: "ok",
      agentId: "",
      eventType: "",
      method: "",
    }),
  );
});

it("reports log level preference save failures without changing the selected level", () => {
  const setItemSpy = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
    throw new Error("storage denied");
  });
  try {
    expect(() => useClientStore.getState().setLogLevel("error")).toThrow("storage denied");

    const state = useClientStore.getState();
    expect(state.logLevel).toBe("info");
    expect(state.warningEntries).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          level: "error",
          event: "log_level.preference_save.failed",
          fields: expect.objectContaining({
            status: "storage_write_failed",
          }),
        }),
      ]),
    );
  } finally {
    setItemSpy.mockRestore();
  }
});

it("keeps composer file selection on plain path arrays without picker tokens", async () => {
  backendApi.selectFiles.mockResolvedValue(["/tmp/plain.txt"]);

  const attachments = await useClientStore.getState().selectFilesForComposer();

  expect(backendApi.selectFiles).toHaveBeenCalledWith();
  expect(attachments).toEqual([
    expect.objectContaining({
      path: "/tmp/plain.txt",
      name: "plain.txt",
    }),
  ]);
  expect(useClientStore.getState().attachments).toEqual([
    expect.objectContaining({
      path: "/tmp/plain.txt",
      name: "plain.txt",
    }),
  ]);
});

it("classifies composer file selection failures as attachment errors", async () => {
  backendApi.selectFiles.mockRejectedValue(new Error("picker unavailable"));

  await expect(useClientStore.getState().selectFilesForComposer()).rejects.toThrow("picker unavailable");

  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      category: "attachment",
      message: "选择附件失败，请重试。",
      tone: "error",
    }),
  );
  expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain("picker unavailable");
});

it("bootstraps through config, window, projects, and sidebar without blocking on thread snapshot", async () => {
  await useClientStore.getState().bootstrap();

  expect(backendApi.getPreference).toHaveBeenCalledWith({ cwd: "/repo/app", key: "settings.provider.active" });
  expect(backendApi.getProjects).toHaveBeenCalledWith({ cwd: "/repo/app" });
  expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  expect(backendApi.getThreadState).not.toHaveBeenCalled();

  const state = useClientStore.getState();
  expect(state.cwd).toBe("/repo/app");
  expect(state.activeProject).toBe("/repo/app");
  expect(state.provider).toBe("codex");
  expect(state.threads).toHaveLength(1);
  expect(state.tokenUsageByThread["thread-1"].usedTokens).toBe(42);
});

it("keeps sidebar tokenUsageByThread ahead of stale global token_usage", async () => {
  backendApi.getSidebarState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "Active", provider: "codex", status: "running" },
      { id: "thread-2", name: "Other", provider: "codex", status: "idle" },
    ],
    token_usage: { usedTokens: 999, contextWindowTokens: 2000, usedPercent: 50 },
    tokenUsageByThread: {
      "thread-1": { usedTokens: 42, contextWindowTokens: 100, usedPercent: 42 },
      "thread-2": { usedTokens: 70, contextWindowTokens: 400, usedPercent: 17.5 },
    },
  });

  await useClientStore.getState().bootstrap();

  const state = useClientStore.getState();
  expect(state.tokenUsageByThread["thread-1"]).toEqual({
    usedTokens: 42,
    contextWindowTokens: 100,
    usedPercent: 42,
  });
  expect(state.tokenUsageByThread["thread-2"]).toEqual({
    usedTokens: 70,
    contextWindowTokens: 400,
    usedPercent: 17.5,
  });
});

it("records each central active-page transition once and ignores same-page updates", () => {
  resetClientStoreForTests({ activePage: "chat" });

  useClientStore.getState().setActivePage("settings");
  useClientStore.getState().setActivePage("settings");
  useClientStore.getState().setActivePage("memory");

  expect(useClientStore.getState().activePage).toBe("memory");
  expect(diagnosticBreadcrumbs()).toEqual([
    { actionCode: "app.navigation", routeId: "settings", phase: "complete" },
    { actionCode: "app.navigation", routeId: "memory", phase: "complete" },
  ]);
});

it("fails bootstrap when the active provider preference is missing", async () => {
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.codex.codexHome": "~/.codex",
        "settings.provider.codex.codexInstanceKey": "default",
        "settings.provider.codex.codexModelProvider": "openai",
      }[key] ?? null,
    ),
  );

  await expect(useClientStore.getState().bootstrap()).rejects.toThrow("frontend-app bootstrap: settings.provider.active preference is required");

  expect(useClientStore.getState().bootstrapStatus).toBe("failed");
});

it("bootstraps when the selected provider model preference is missing", async () => {
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexModelProvider": "openai",
      }[key] ?? null,
    ),
  );

  await useClientStore.getState().bootstrap();

  expect(useClientStore.getState().bootstrapStatus).toBe("ready");
  expect(useClientStore.getState().providerConfig).toEqual(
    expect.objectContaining({
      provider: "codex",
      model: "",
      effort: "xhigh",
    }),
  );
});

it("bootstraps when optional Codex model provider preference is absent", async () => {
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "~/.codex",
        "settings.provider.codex.codexInstanceKey": "default",
      }[key] ?? null,
    ),
  );

  await useClientStore.getState().bootstrap();

  expect(useClientStore.getState().bootstrapStatus).toBe("ready");
  expect(backendApi.getProjects).toHaveBeenCalledWith({ cwd: "/repo/app" });
  expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  expect(backendApi.getThreadState).not.toHaveBeenCalled();
  expect(useClientStore.getState().providerConfig).toEqual(
    expect.objectContaining({
      provider: "codex",
      model: "gpt-5.5",
      effort: "xhigh",
      codexModelProvider: "",
    }),
  );
});

it("retries bootstrap after a transient cold-start runtime reconnect", async () => {
  backendApi.readConfig.mockRejectedValueOnce(new Error("Wails runtime bridge not ready")).mockResolvedValue({ cwd: "/repo/app" });

  await expect(useClientStore.getState().bootstrap()).rejects.toThrow("Wails runtime bridge not ready");
  expect(useClientStore.getState().bootstrapStatus).toBe("failed");
  expect(runtime.runtimeReconnectCallback).toEqual(expect.any(Function));

  runtime.runtimeReconnectCallback();
  await vi.waitFor(() => {
    expect(backendApi.readConfig).toHaveBeenCalledTimes(2);
    expect(useClientStore.getState().bootstrapStatus).toBe("ready");
  });
  expect(useClientStore.getState().activeProject).toBe("/repo/app");
});

it("keeps the previous bootstrap error visible while an explicit retry is loading", async () => {
  const retryConfig = deferred();
  backendApi.readConfig.mockRejectedValueOnce(new Error("event bridge unavailable")).mockReturnValueOnce(retryConfig.promise);

  await expect(useClientStore.getState().bootstrap()).rejects.toThrow("event bridge unavailable");

  const retryPromise = useClientStore.getState().bootstrap();
  expect(useClientStore.getState().bootstrapStatus).toBe("loading");
  expect(useClientStore.getState().error).toBe("连接后端失败，请重试。");

  retryConfig.resolve({ cwd: "/repo/app" });
  await retryPromise;
  expect(useClientStore.getState().bootstrapStatus).toBe("ready");
  expect(useClientStore.getState().error).toBe("");
});

it("retries bootstrap when runtime reconnect arrives before the first cold-start RPC fails", async () => {
  const firstConfig = deferred();
  backendApi.readConfig.mockReturnValueOnce(firstConfig.promise).mockResolvedValue({ cwd: "/repo/app" });

  const bootstrapPromise = useClientStore.getState().bootstrap();
  await flushPromises(2);
  expect(useClientStore.getState().bootstrapStatus).toBe("loading");
  expect(runtime.runtimeReconnectCallback).toEqual(expect.any(Function));

  runtime.runtimeReconnectCallback();
  firstConfig.reject(new Error("runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws"));
  await expect(bootstrapPromise).rejects.toThrow("runtime shim: failed to connect");
  await flushPromises();

  expect(backendApi.readConfig).toHaveBeenCalledTimes(2);
  expect(useClientStore.getState().bootstrapStatus).toBe("ready");
  expect(useClientStore.getState().activeProject).toBe("/repo/app");
});

it("waits for both runtime subscriptions before the first bootstrap RPC", async () => {
  const bridgeReady = deferred();
  const reconnectReady = deferred();
  backendApi.onBridgeEvent.mockImplementationOnce((callback, options = {}) => {
    runtime.bridgeCallback = callback;
    runtime.bridgeOptions = options;
    return { ready: bridgeReady.promise, unsubscribe: vi.fn() };
  });
  backendApi.onRuntimeReconnect.mockImplementationOnce((callback) => {
    runtime.runtimeReconnectCallback = callback;
    return { ready: reconnectReady.promise, unsubscribe: vi.fn() };
  });

  const bootstrapPromise = useClientStore.getState().bootstrap();
  await flushPromises();
  expect(backendApi.readConfig).not.toHaveBeenCalled();
  expect(backendApi.getWindowBootstrap).not.toHaveBeenCalled();

  bridgeReady.resolve(true);
  await flushPromises();
  expect(backendApi.readConfig).not.toHaveBeenCalled();

  reconnectReady.resolve(true);
  await bootstrapPromise;
  expect(backendApi.readConfig).toHaveBeenCalledTimes(1);
  expect(backendApi.getWindowBootstrap).toHaveBeenCalledTimes(1);
});

it("fails bootstrap before RPCs when runtime subscription readiness is unavailable", async () => {
  backendApi.onBridgeEvent.mockImplementationOnce(() => ({
    ready: Promise.resolve(true),
    unsubscribe: vi.fn(),
  }));
  backendApi.onRuntimeReconnect.mockImplementationOnce(() => ({
    ready: Promise.resolve(false),
    unsubscribe: vi.fn(),
  }));

  await expect(useClientStore.getState().bootstrap()).rejects.toThrow("runtime.reconnect.subscribe unavailable");

  expect(backendApi.readConfig).not.toHaveBeenCalled();
  expect(backendApi.getWindowBootstrap).not.toHaveBeenCalled();
  expect(useClientStore.getState().bootstrapStatus).toBe("failed");
  expect(useClientStore.getState().error).toBe("连接后端失败，请重试。");
});

it("preserves a live bridge status over a stale bootstrap sidebar snapshot", async () => {
  const sidebar = deferred();
  resetClientStoreForTests({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
  });
  backendApi.getSidebarState.mockReturnValueOnce(sidebar.promise);

  const bootstrapPromise = useClientStore.getState().bootstrap();
  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  });
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      sequence: "bootstrap-live",
      status: "running",
      interruptible: true,
      activeTurn: { id: "turn-live", threadId: "thread-1", status: "running" },
      thread: { name: "Existing" },
    },
  });
  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-1",
      status: "running",
    }),
  );

  sidebar.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
  });
  await bootstrapPromise;

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-1",
      status: "running",
    }),
  );
});

it("hydrates thread providers from sidebar runtime metadata", async () => {
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-claude",
    threads: [{ id: "thread-claude", name: "Claude runtime thread", status: "running" }],
    agentRuntimeById: {
      "thread-claude": { provider: "claude", providerThreadId: "provider-1" },
    },
  });

  await useClientStore.getState().bootstrap();

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-claude",
      provider: "claude",
    }),
  );
});

it("hydrates pinned chat threads from the backend threadPins preference", async () => {
  backendApi.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "Existing", provider: "codex", status: "running" },
      { id: "thread-2", name: "Pinned", provider: "codex", status: "idle" },
    ],
    "threadPins.chat": { "thread-2": 1735689600000 },
  });

  await useClientStore.getState().bootstrap();

  const state = useClientStore.getState();
  expect(state.pinnedThreadAtById).toEqual({ "thread-2": 1735689600000 });
  expect(state.threads.find((thread) => thread.id === "thread-2")).toEqual(
    expect.objectContaining({
      pinned: true,
      pinnedAt: 1735689600000,
    }),
  );
});

it("toggles thread pins through the backend threadPins chat preference map", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
    pinnedThreadAtById: {},
  });

  await expect(useClientStore.getState().toggleThreadPin("thread-1")).resolves.toBe(true);

  const pinnedAt = useClientStore.getState().pinnedThreadAtById["thread-1"];
  expect(pinnedAt).toBeGreaterThan(0);
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "threadPins.chat",
    value: { "thread-1": pinnedAt },
  });
});

it("keeps the desktop active provider locked to Codex", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    provider: "codex",
  });

  await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(false);

  expect(backendApi.setPreference).not.toHaveBeenCalledWith(
    expect.objectContaining({
      key: "settings.provider.active",
    }),
  );
  expect(useClientStore.getState().provider).toBe("codex");
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "当前桌面仅支持 Codex provider",
      tone: "warning",
    }),
  );
  expect(useClientStore.getState().warningEntries).toEqual([
    expect.objectContaining({
      level: "warn",
      event: "provider.toggle.unsupported",
      fields: { requestedProvider: "claude" },
    }),
  ]);
});

it("does not change the active provider while an opened chat is selected", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    provider: "codex",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
  });

  await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(false);

  expect(backendApi.setPreference).not.toHaveBeenCalledWith(
    expect.objectContaining({
      key: "settings.provider.active",
    }),
  );
  expect(useClientStore.getState().provider).toBe("codex");
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "已开启的聊天不能更改 provider，请新建对话后切换",
      tone: "warning",
    }),
  );
});

it("keeps provider toggle disabled without requiring cwd", async () => {
  resetClientStoreForTests({
    cwd: "",
    activeProject: "",
    provider: "codex",
  });

  await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(false);

  expect(backendApi.setPreference).not.toHaveBeenCalledWith(
    expect.objectContaining({
      key: "settings.provider.active",
    }),
  );
  expect(useClientStore.getState().provider).toBe("codex");
});

it("routes project selector actions through the project RPC contract", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
  });
  backendApi.setActiveProject.mockImplementation(({ path }) =>
    Promise.resolve({
      projects: path === "/repo/new" ? ["/repo/app", "/repo/other", "/repo/new"] : ["/repo/app", "/repo/other"],
      active: path,
    }),
  );
  backendApi.addProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other", "/repo/new"], active: "/repo/other" });
  backendApi.removeProject.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });

  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).resolves.toBe(true);
  expect(backendApi.setActiveProject).toHaveBeenCalledWith({ cwd: "/repo/app", path: "/repo/other" });
  expect(useClientStore.getState().activeProject).toBe("/repo/other");

  await expect(useClientStore.getState().addProjectFromPicker()).resolves.toBe(true);
  expect(backendApi.selectProjectDir).toHaveBeenCalledWith("/repo/other");
  expect(backendApi.addProject).toHaveBeenCalledWith({ cwd: "/repo/app", path: "/repo/new" });
  expect(backendApi.setActiveProject).toHaveBeenLastCalledWith({ cwd: "/repo/app", path: "/repo/new" });
  expect(useClientStore.getState().activeProject).toBe("/repo/new");

  await expect(useClientStore.getState().removeProjectPath("/repo/new")).resolves.toBe(true);
  expect(backendApi.removeProject).toHaveBeenCalledWith({ cwd: "/repo/app", path: "/repo/new" });
  expect(useClientStore.getState().projects).toEqual(["/repo/app", "/repo/other"]);
});

it("restores the project selector state when setActiveProject RPC fails", async () => {
  backendApi.setActiveProject.mockRejectedValueOnce(new Error("project backend offline"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
  });

  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).rejects.toThrow("project backend offline");

  expect(useClientStore.getState().activeProject).toBe("/repo/app");
  expect(useClientStore.getState().projects).toEqual(["/repo/app", "/repo/other"]);
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "切换项目失败，请重试。",
      tone: "error",
    }),
  );
});

it("keeps the complete previous chat scope when setActiveProject RPC fails", async () => {
  backendApi.setActiveProject.mockRejectedValueOnce(new Error("project backend offline"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-old": [{ id: "old-user", role: "user", text: "old cwd message" }] },
    draft: "keep this unsent draft",
    sidebarThreadsByProject: {
      "/repo/app": [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
    },
  });

  await expect(useClientStore.getState().setActiveProjectPath("/repo/other")).rejects.toThrow("project backend offline");

  expect(useClientStore.getState()).toEqual(expect.objectContaining({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app", "/repo/other"],
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
    timelinesByThread: { "thread-old": [{ id: "old-user", role: "user", text: "old cwd message" }] },
    draft: "keep this unsent draft",
    sidebarThreadsByProject: {
      "/repo/app": [{ id: "thread-old", name: "Old project thread", provider: "codex", status: "running" }],
    },
    chatSurfaceLoadingCwd: "",
  }));
  expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
    message: "切换项目失败，请重试。",
    tone: "error",
  }));
});

it("opens an independent app window from the selected directory", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/other",
    projects: ["/repo/app", "/repo/other"],
  });
  backendApi.selectProjectDir.mockResolvedValue("/repo/window");

  await expect(useClientStore.getState().openNewWindow()).resolves.toBe(true);

  expect(backendApi.selectProjectDir).toHaveBeenCalledWith("/repo/other");
  expect(backendApi.openNewWindow).toHaveBeenCalledWith({ cwd: "/repo/window" });
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "已打开新窗口：repo/window",
      tone: "success",
    }),
  );
});

it("registers a visible fallback project before switching to it", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: ".",
    projects: [],
  });
  backendApi.addProject.mockResolvedValue({ projects: ["/repo/app"], active: "." });
  backendApi.setActiveProject.mockResolvedValue({ projects: ["/repo/app"], active: "/repo/app" });

  await expect(useClientStore.getState().setActiveProjectPath("/repo/app")).resolves.toBe(true);

  expect(backendApi.addProject).toHaveBeenCalledWith({ cwd: "/repo/app", path: "/repo/app" });
  expect(backendApi.setActiveProject).toHaveBeenCalledWith({ cwd: "/repo/app", path: "/repo/app" });
  expect(useClientStore.getState().activeProject).toBe("/repo/app");
});
