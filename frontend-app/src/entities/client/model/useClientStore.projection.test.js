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
  boundCapabilities,
  deferred,
  expect,
  flushPromises,
  frontendHealthSnapshot,
  it,
  optionalUiArray,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  useClientStore,
  waitFor,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("refreshes the chat list when the backend sidebar projection changes", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main agent", provider: "codex", status: "running" }],
  });
  backendApi.getSidebarState.mockResolvedValueOnce({
    activeThreadId: "thread-main",
    threads: [
      { id: "thread-main", name: "Main agent", provider: "codex", status: "running" },
      { id: "thread-child", name: "Child agent", provider: "codex", status: "running" },
    ],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/sidebar/changed",
    payload: { projection: "sidebar", revision: 2 },
  });

  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  });
  await vi.waitFor(() => {
    expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "thread-child", name: "Child agent" })]));
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-main");
});

it("keeps an active refresh independent from a later non-active project refresh", async () => {
  const projectA = deferred();
  const projectB = deferred();
  resetClientStoreForTests({
    cwd: "/repo/a",
    projectScopeCwd: "/repo/a",
    activeProject: "/repo/a",
    projects: ["/repo/a", "/repo/b"],
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
    sidebarThreadsByProject: {
      "/repo/a": [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
    },
  });
  backendApi.getSidebarState.mockReturnValueOnce(projectA.promise).mockReturnValueOnce(projectB.promise);

  useClientStore.getState().refreshActiveChatSidebarInBackground();
  useClientStore.getState().refreshSidebarSnapshotForCwdInBackground("/repo/b");

  projectA.resolve({
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/a", name: "Project A refreshed", provider: "codex", status: "idle" }],
  });
  await waitFor(() => expect(useClientStore.getState().sidebarThreadsByProject["/repo/a"][0].name).toBe("Project A refreshed"));

  projectB.resolve({
    activeThreadId: "thread-b",
    threads: [{ id: "thread-b", name: "Project B late", provider: "codex", status: "idle" }],
  });

  await waitFor(() => expect(useClientStore.getState().sidebarThreadsByProject["/repo/b"][0].name).toBe("Project B late"));
  expect(useClientStore.getState().threads).toEqual([expect.objectContaining({ id: "thread-a", cwd: "/repo/a", name: "Project A refreshed", provider: "codex", status: "idle" })]);
});

it("drops a superseded project sidebar response and applies only its trailing refresh", async () => {
  const firstProjectB = deferred();
  const secondProjectB = deferred();
  resetClientStoreForTests({
    cwd: "/repo/a",
    projectScopeCwd: "/repo/a",
    activeProject: "/repo/a",
    projects: ["/repo/a", "/repo/b"],
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
    sidebarThreadsByProject: {
      "/repo/a": [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
    },
  });
  backendApi.getSidebarState.mockReturnValueOnce(firstProjectB.promise).mockReturnValueOnce(secondProjectB.promise);

  useClientStore.getState().refreshSidebarSnapshotForCwdInBackground("/repo/b");
  useClientStore.getState().refreshSidebarSnapshotForCwdInBackground("/repo/b");
  firstProjectB.resolve({
    activeThreadId: "thread-b",
    threads: [{ id: "thread-b", name: "Project B stale", provider: "codex", status: "idle" }],
  });

  await waitFor(() => expect(backendApi.getSidebarState).toHaveBeenCalledTimes(2));
  expect(useClientStore.getState().sidebarThreadsByProject["/repo/b"]).toBeUndefined();
  secondProjectB.resolve({
    activeThreadId: "thread-b",
    threads: [{ id: "thread-b", name: "Project B fresh", provider: "codex", status: "idle" }],
  });

  await waitFor(() => expect(useClientStore.getState().sidebarThreadsByProject["/repo/b"][0].name).toBe("Project B fresh"));
});

it("keeps both project projections intact when a non-active sidebar refresh fails", async () => {
  resetClientStoreForTests({
    cwd: "/repo/a",
    projectScopeCwd: "/repo/a",
    activeProject: "/repo/a",
    projects: ["/repo/a", "/repo/b"],
    activeThreadId: "thread-a",
    threads: [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
    sidebarThreadsByProject: {
      "/repo/a": [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
      "/repo/b": [{ id: "thread-b", cwd: "/repo/b", name: "Project B", provider: "codex", status: "idle" }],
    },
  });
  backendApi.getSidebarState.mockRejectedValueOnce(new Error("project B refresh failed"));

  useClientStore.getState().refreshSidebarSnapshotForCwdInBackground("/repo/b");

  await waitFor(() => expect(useClientStore.getState().warningEntries).toEqual(expect.arrayContaining([expect.objectContaining({ event: "thread.sidebar.refresh.failed" })])));
  expect(frontendHealthSnapshot().filter(({ actionId }) => actionId === "sidebar.project-threads.load")).toHaveLength(1);
  expect(useClientStore.getState().threads).toEqual([{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }]);
  expect(useClientStore.getState().sidebarThreadsByProject).toEqual(
    expect.objectContaining({
      "/repo/a": [{ id: "thread-a", cwd: "/repo/a", name: "Project A", provider: "codex", status: "idle" }],
      "/repo/b": [{ id: "thread-b", cwd: "/repo/b", name: "Project B", provider: "codex", status: "idle" }],
    }),
  );
});

it("keeps live running status when a sidebar refresh returns a stale idle projection", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    projectScopeCwd: "/repo/app",
    activeProject: "/repo/app",
    projects: ["/repo/app"],
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main agent", provider: "codex", status: "running", cwd: "/repo/app" }],
    sidebarThreadsByProject: {
      "/repo/app": [{ id: "thread-main", name: "Main agent", provider: "codex", status: "running", cwd: "/repo/app" }],
    },
    activeTurnByThread: {
      "thread-main": { id: "turn-main", threadId: "thread-main", status: "running" },
    },
  });
  backendApi.getSidebarState.mockResolvedValueOnce({
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main agent", provider: "codex", status: "idle", cwd: "/repo/app" }],
  });
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({
    type: "ui/sidebar/changed",
    payload: { projection: "sidebar", revision: 2 },
  });

  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/app" });
  });
  await flushPromises();

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-main",
      status: "running",
    }),
  );
  expect(useClientStore.getState().sidebarThreadsByProject["/repo/app"][0]).toEqual(
    expect.objectContaining({
      id: "thread-main",
      status: "running",
    }),
  );

  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-main",
      sequence: "done",
      status: "completed",
      interruptible: false,
      thread: { name: "Main agent" },
    },
  });
  await flushPromises();

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-main",
      status: "completed",
    }),
  );
  expect(useClientStore.getState().sidebarThreadsByProject["/repo/app"][0]).toEqual(
    expect.objectContaining({
      id: "thread-main",
      status: "completed",
    }),
  );
});

it("coalesces burst sidebar projection events and runs one trailing refresh", async () => {
  const firstRefresh = deferred();
  const trailingRefresh = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main agent", provider: "codex", status: "running" }],
  });
  backendApi.getSidebarState.mockReturnValueOnce(firstRefresh.promise).mockReturnValueOnce(trailingRefresh.promise);
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({ type: "ui/sidebar/changed", payload: { revision: 2 } });
  runtime.bridgeCallback({ type: "ui/sidebar/changed", payload: { revision: 3 } });
  runtime.bridgeCallback({ type: "ui/sidebar/changed", payload: { revision: 4 } });

  expect(backendApi.getSidebarState).toHaveBeenCalledTimes(1);
  firstRefresh.resolve({
    activeThreadId: "thread-main",
    threads: [
      { id: "thread-main", name: "Main agent", provider: "codex", status: "running" },
      { id: "thread-stale", name: "Stale snapshot", provider: "codex", status: "running" },
    ],
  });

  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledTimes(2);
  });
  expect(backendApi.getSidebarState).toHaveBeenNthCalledWith(2, { cwd: "/repo/app" });

  trailingRefresh.resolve({
    activeThreadId: "thread-main",
    threads: [
      { id: "thread-main", name: "Main agent", provider: "codex", status: "running" },
      { id: "thread-fresh", name: "Fresh snapshot", provider: "codex", status: "running" },
    ],
  });

  await vi.waitFor(() => {
    expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "thread-fresh", name: "Fresh snapshot" })]));
  });
  expect(useClientStore.getState().threads).not.toEqual(expect.arrayContaining([expect.objectContaining({ id: "thread-stale" })]));
  expect(backendApi.getSidebarState).toHaveBeenCalledTimes(2);
});

it("runs a pending sidebar refresh after an in-flight refresh rejects", async () => {
  const bearerSecret = "Bearer sk-frontend-sidebar-secret-123456";
  const failedRefresh = deferred();
  const retryRefresh = deferred();
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-main",
    threads: [{ id: "thread-main", name: "Main agent", provider: "codex", status: "running" }],
  });
  backendApi.getSidebarState.mockReturnValueOnce(failedRefresh.promise).mockReturnValueOnce(retryRefresh.promise);
  registerBridgeEventHandlersForTest();

  runtime.bridgeCallback({ type: "ui/sidebar/changed", payload: { revision: 2 } });
  runtime.bridgeCallback({ type: "ui/sidebar/changed", payload: { revision: 3 } });

  expect(backendApi.getSidebarState).toHaveBeenCalledTimes(1);
  failedRefresh.reject(new Error(`sidebar refresh failed ${bearerSecret}`));

  await vi.waitFor(() => {
    expect(backendApi.getSidebarState).toHaveBeenCalledTimes(2);
  });
  retryRefresh.resolve({
    activeThreadId: "thread-main",
    threads: [
      { id: "thread-main", name: "Main agent", provider: "codex", status: "running" },
      { id: "thread-recovered", name: "Recovered snapshot", provider: "codex", status: "running" },
    ],
  });

  await vi.waitFor(() => {
    expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "thread-recovered", name: "Recovered snapshot" })]));
  });
  expect(useClientStore.getState().warningEntries[0]).toEqual(
    expect.objectContaining({
      level: "error",
      event: "thread.sidebar.refresh.failed",
    }),
  );
  expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain(bearerSecret);
  expect(JSON.stringify(useClientStore.getState().warningEntries)).not.toContain(bearerSecret);
  expect(frontendHealthSnapshot()).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        actionId: "sidebar.project-threads.load",
        diagnosticId: expect.any(String),
      }),
    ]),
  );
  expect(JSON.stringify(frontendHealthSnapshot())).not.toContain(bearerSecret);
  expect(backendApi.getSidebarState).toHaveBeenCalledTimes(2);
});

it("increments prompt revision from prompt and active-prompt preference bridge events", () => {
  registerBridgeEventHandlersForTest();

  expect(useClientStore.getState().promptRevision).toBe(0);
  runtime.bridgeCallback({
    type: "ui/preferences/changed",
    payload: { key: "settings.activePromptKey", value: "main/reviewer" },
  });
  expect(useClientStore.getState().promptRevision).toBe(1);

  runtime.bridgeCallback({
    type: "ui/preferences/changed",
    payload: { key: "settings.provider.active", value: "codex" },
  });
  expect(useClientStore.getState().promptRevision).toBe(1);

  runtime.bridgeCallback({
    type: "prompts/changed",
    payload: { cwd: "/repo/app" },
  });
  expect(useClientStore.getState().promptRevision).toBe(2);
});

it("restores draft and attachments when backend send fails", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Do not lose this",
    attachments: [{ path: "/tmp/a.txt", name: "a.txt" }],
  });
  backendApi.startThread.mockRejectedValue(new Error("thread/start failed"));

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("thread/start failed");

  const state = useClientStore.getState();
  expect(state.draft).toBe("Do not lose this");
  expect(state.attachments).toEqual([{ path: "/tmp/a.txt", name: "a.txt" }]);
  expect(state.warningEntries[0]).toEqual(
    expect.objectContaining({
      level: "error",
      event: "thread.send.failed",
    }),
  );
});

it("shows a fixed recovery action without retaining backend details", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Keep this draft",
  });
  const secret = "secret MCP stderr at /private/workspace";
  const recoveryError = new Error(secret);
  recoveryError.data = {
    code: "MCP_SCHEMA_REAP_FAILED",
    retryable: false,
    action: "restart_application",
    transaction_id: "",
  };
  backendApi.startThread.mockRejectedValue(recoveryError);

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow(secret);

  const state = useClientStore.getState();
  expect(state.error).toBe("工具恢复失败，请重启应用后重试。");
  expect(state.actionNotice).toEqual(expect.objectContaining({
    message: "工具恢复失败，请重启应用后重试。",
    tone: "error",
    category: "send",
  }));
  expect(JSON.stringify(state)).not.toContain(secret);
});

it("clears text, attachments, and capabilities after a successful send", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    draft: "Review this change",
    attachments: [{ path: "/tmp/change.patch", name: "change.patch" }],
    composerCapabilities: boundCapabilities,
  });
  backendApi.startTurn.mockResolvedValueOnce({ ok: true });

  await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);

  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-1",
    input: [
      { type: "text", text: "Review this change" },
      { type: "mention", name: "change.patch", path: "/tmp/change.patch" },
    ],
    selectedSkills: ["review"],
    selectedSkillRefs: [
      {
        name: "review",
        scope: "project",
        path: "/repo/app/.agents/skills/review",
      },
    ],
    manualSkillSelection: true,
    enabledTools: ["lsp_edit"],
  });
  expect(useClientStore.getState()).toEqual(
    expect.objectContaining({
      draft: "",
      attachments: [],
      composerCapabilities: [],
    }),
  );
});

it("restores text, attachments, and capabilities after a failed send", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    draft: "Review this change",
    attachments: [{ path: "/tmp/change.patch", name: "change.patch" }],
    composerCapabilities: boundCapabilities,
  });
  backendApi.startTurn.mockRejectedValueOnce(new Error("turn/start failed"));

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("turn/start failed");

  expect(useClientStore.getState()).toEqual(
    expect.objectContaining({
      draft: "Review this change",
      attachments: [expect.objectContaining({ path: "/tmp/change.patch" })],
      composerCapabilities: [
        expect.objectContaining({
          key: "skill:project::review:/repo/app/.agents/skills/review",
        }),
        expect.objectContaining({ key: "mcp_tool:lsp:lsp_edit" }),
      ],
    }),
  );
  expect(backendApi.startTurn).toHaveBeenCalledWith(
    expect.objectContaining({
      selectedSkills: ["review"],
      manualSkillSelection: true,
      enabledTools: ["lsp_edit"],
    }),
  );
});

it.each(["unverified", "stale"])("blocks %s capabilities before turn/start", async (availability) => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    draft: "Review this change",
    attachments: [],
    composerCapabilities: [
      {
        kind: "mcp_tool",
        key: "mcp_tool:lsp:grep",
        name: "grep",
        label: "grep",
        serverName: "lsp",
        availability,
      },
    ],
  });
  backendApi.startTurn.mockReset();
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow(`composer capability mcp_tool:lsp:grep is ${availability}`);
  expect(backendApi.startTurn).not.toHaveBeenCalled();
});

it("does not send capability-only composer state", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    draft: "",
    attachments: [],
    composerCapabilities: [boundCapabilities[1]],
  });

  await expect(useClientStore.getState().sendDraft()).resolves.toBe(false);
  expect(backendApi.startTurn).not.toHaveBeenCalled();
});

it("exposes capability mutations and clears the whole composer", () => {
  resetClientStoreForTests({
    draft: "Keep together",
    attachments: [{ path: "/tmp/change.patch", name: "change.patch" }],
    composerCapabilities: [],
  });

  useClientStore.getState().addComposerCapability(boundCapabilities[0]);
  expect(useClientStore.getState().composerCapabilities).toEqual([expect.objectContaining({ key: boundCapabilities[0].key })]);

  useClientStore.getState().reconcileComposerCapabilities({
    kind: "skill",
    status: "success",
    items: [],
  });
  expect(useClientStore.getState().composerCapabilities[0]).toEqual(expect.objectContaining({ availability: "stale" }));

  useClientStore.getState().removeComposerCapability(boundCapabilities[0].key);
  useClientStore.getState().addComposerCapability(boundCapabilities[1]);
  useClientStore.getState().clearComposer();

  expect(useClientStore.getState()).toEqual(
    expect.objectContaining({
      draft: "",
      attachments: [],
      composerCapabilities: [],
    }),
  );
});

it("deletes a provisional backend thread when the first turn fails", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Clean up provisional thread",
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
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-provisional" });
  backendApi.startTurn.mockRejectedValue(new Error("turn/start failed"));

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("turn/start failed");

  expect(backendApi.deleteThread).toHaveBeenCalledWith({ threadId: "thread-provisional" });
  const state = useClientStore.getState();
  expect(state.draft).toBe("Clean up provisional thread");
  expect(state.activeThreadId).not.toBe("thread-provisional");
  expect(state.threads.some((thread) => thread.id === "thread-provisional")).toBe(false);
  expect((state.sidebarThreadsByProject["/repo/app"] || optionalUiArray()).some((thread) => thread.id === "thread-provisional")).toBe(false);
  expect(state.timelinesByThread["thread-provisional"]).toBeUndefined();
  expect(state.threadTimelineReadyByThread["thread-provisional"]).toBeUndefined();
  expect(state.activityThreadAtById["thread-provisional"]).toBeUndefined();
});

it("does not delete an unrelated active thread when a provisional send fails", async () => {
  const turnResult = deferred();

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Keep draft",
    attachments: [],
    threads: [{ id: "thread-other", name: "Other thread", provider: "codex", status: "running" }],
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
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-provisional" });
  backendApi.startTurn.mockImplementation(() => turnResult.promise);

  const sendPromise = useClientStore.getState().sendDraft();
  useClientStore.setState({ activeThreadId: "thread-other" });
  turnResult.reject(new Error("turn/start failed"));

  await expect(sendPromise).rejects.toThrow("turn/start failed");

  expect(backendApi.deleteThread).toHaveBeenCalledWith({ threadId: "thread-provisional" });
  expect(backendApi.deleteThread).not.toHaveBeenCalledWith({ threadId: "thread-other" });
});
