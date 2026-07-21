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
import { ForkDraftCard } from "../../../pages/chat/composer/ForkDraftCard.jsx";
import {
  React,
  deferred,
  expect,
  flushPromises,
  frontendHealthSnapshot,
  it,
  optionalUiArray,
  registerClientStoreTestHooks,
  render,
  resetClientStoreForTests,
  screen,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("does not let a stale send failure overwrite the active composer after a thread switch", async () => {
  const turnResult = deferred();
  const nextAttachments = [{ path: "/tmp/next.txt", name: "next.txt" }];

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Original pending send",
    attachments: [{ path: "/tmp/original.txt", name: "original.txt" }],
    threads: [{ id: "thread-other", name: "Other thread", provider: "codex", status: "running" }],
    sidebarThreadsByProject: {
      "/repo/app": [{ id: "thread-other", name: "Other thread", provider: "codex", status: "running" }],
    },
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
  await flushPromises();

  useClientStore.setState({
    activeThreadId: "thread-other",
    draft: "New active draft",
    attachments: nextAttachments,
  });
  turnResult.reject(new Error("turn/start failed"));

  await expect(sendPromise).rejects.toThrow("turn/start failed");

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-other");
  expect(state.draft).toBe("New active draft");
  expect(state.attachments).toEqual(nextAttachments);
  expect(state.threads.some((thread) => thread.id === "thread-provisional")).toBe(false);
  expect((state.sidebarThreadsByProject["/repo/app"] || optionalUiArray()).some((thread) => thread.id === "thread-provisional")).toBe(false);
  expect(state.timelinesByThread["thread-provisional"]).toBeUndefined();
  expect(backendApi.deleteThread).toHaveBeenCalledWith({ threadId: "thread-provisional" });
});

it("restores a failed new-chat draft when returning after a thread switch", async () => {
  const turnResult = deferred();
  const originalAttachments = [{ path: "/tmp/original.txt", name: "original.txt" }];
  const nextAttachments = [{ path: "/tmp/next.txt", name: "next.txt" }];

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Original pending send",
    attachments: originalAttachments,
    threads: [{ id: "thread-other", name: "Other thread", provider: "codex", status: "running" }],
    sidebarThreadsByProject: {
      "/repo/app": [{ id: "thread-other", name: "Other thread", provider: "codex", status: "running" }],
    },
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
  await flushPromises();

  useClientStore.setState({
    activeThreadId: "thread-other",
    draft: "New active draft",
    attachments: nextAttachments,
  });
  turnResult.reject(new Error("turn/start failed"));

  await expect(sendPromise).rejects.toThrow("turn/start failed");

  expect(useClientStore.getState().draft).toBe("New active draft");
  expect(useClientStore.getState().attachments).toEqual(nextAttachments);

  useClientStore.getState().newThread();

  expect(useClientStore.getState().draft).toBe("Original pending send");
  expect(useClientStore.getState().attachments).toEqual([expect.objectContaining({ path: "/tmp/original.txt", name: "original.txt" })]);
});

it("keeps sending fail-fast when cwd is missing", async () => {
  resetClientStoreForTests({
    cwd: "",
    activeProject: "",
    activeThreadId: "",
    draft: "Do not send without cwd",
    attachments: [],
  });

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("frontend-app: cwd is required for send message");

  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).not.toHaveBeenCalled();
});

it("opens an inherited fork draft from a shared file continuation action when a source thread exists", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    activePage: "files",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
    draft: "old draft",
    attachments: [{ path: "reports/final.md", name: "final.md" }],
  });

  expect(useClientStore.getState().continueWithSharedFile("reports/final.md")).toBe(true);

  const state = useClientStore.getState();
  expect(state.activePage).toBe("chat");
  expect(state.activeThreadId).toBe("thread-1");
  expect(state.forkDraft.open).toBe(true);
  expect(state.forkDraft.sourceThreadId).toBe("thread-1");
  expect(state.forkDraft.sourceTitle).toBe("继承自会话：Existing thread");
  expect(state.forkDraft.sharedFilePaths).toEqual(["reports/final.md"]);
  expect(state.draft).toBe("old draft");
});

it("falls back to a new composer draft from a shared file when no source thread exists", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    activePage: "files",
    draft: "old draft",
    attachments: [],
  });

  expect(useClientStore.getState().continueWithSharedFile("reports/final.md")).toBe(true);

  const state = useClientStore.getState();
  expect(state.activePage).toBe("chat");
  expect(state.activeThreadId).toBe("");
  expect(state.draft).toContain("reports/final.md");
  expect(state.attachments).toEqual([{ path: "reports/final.md", name: "final.md" }]);
});

it("uses canonical thread/fork and sends exactly one created-only kickoff", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [
        { id: "user-1", kind: "user", text: "first message" },
        { id: "assistant-1", kind: "assistant", text: "reply with next steps" },
      ],
    },
  });
  backendApi.forkThread.mockResolvedValue({
    thread: { id: "thread-fork", forkedFrom: "thread-1" },
    kickoffState: "created_only",
  });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await expect(useClientStore.getState().openForkDraft()).resolves.toBe(true);
  await expect(useClientStore.getState().submitForkThread()).resolves.toBe("thread-fork");

  expect(backendApi.forkThread).toHaveBeenCalledWith({ threadId: "thread-1" });
  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).toHaveBeenCalledTimes(1);
  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-fork",
    input: [{ type: "text", text: "请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-fork");
  expect(useClientStore.getState().forkDraft.open).toBe(false);
});

it("marks inherited fork kickoff failure as partial instead of a full working success", async () => {
  const bearerSecret = "Bearer sk-frontend-fork-kickoff-secret-123456";
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [
        { id: "user-1", kind: "user", text: "fork this work" },
        { id: "assistant-1", kind: "assistant", text: "forkable context" },
      ],
    },
  });
  backendApi.forkThread.mockResolvedValue({
    thread: { id: "thread-fork", forkedFrom: "thread-1" },
    kickoffState: "created_only",
  });
  backendApi.startTurn.mockRejectedValue(new Error(`turn/start failed ${bearerSecret}`));

  await expect(useClientStore.getState().openForkDraft()).resolves.toBe(true);
  await expect(useClientStore.getState().submitForkThread()).resolves.toBe("thread-fork");

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(
    expect.objectContaining({
      message: "已创建继承对话，但开场消息暂时无法发送。",
      tone: "warning",
    }),
  );
  expect(state.threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-fork",
      status: "需要操作",
      forkKickoffStatus: "failed",
      forkKickoffError: "已创建继承对话，但开场消息暂时无法发送。",
    }),
  );
  expect(state.timelinesByThread["thread-fork"] || optionalUiArray()).not.toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        id: expect.stringMatching(/^fork-kickoff-/),
        optimistic: true,
      }),
    ]),
  );
  expect(JSON.stringify(state.actionNotice)).not.toContain(bearerSecret);
  expect(JSON.stringify(state.warningEntries)).not.toContain(bearerSecret);
  expect(frontendHealthSnapshot()).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        actionId: "thread.fork.submit",
        diagnosticId: expect.any(String),
      }),
    ]),
  );
  expect(JSON.stringify(frontendHealthSnapshot())).not.toContain(bearerSecret);
});

it("keeps shared-file loading failures out of fork draft state, rendered feedback, and Health", async () => {
  const bearerSecret = "Bearer sk-frontend-fork-shared-files-secret-123456";
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
  });
  backendApi.listSharedFiles.mockRejectedValue(new Error(`shared files unavailable ${bearerSecret}`));

  await expect(useClientStore.getState().openForkDraft()).resolves.toBe(true);

  const state = useClientStore.getState();
  expect(state.forkDraft.error).toBe("共享文件列表暂时不可用，请稍后重试。");
  expect(JSON.stringify(state.forkDraft)).not.toContain(bearerSecret);
  expect(JSON.stringify(state.warningEntries)).not.toContain(bearerSecret);
  expect(frontendHealthSnapshot()).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        actionId: "thread.fork.open",
        diagnosticId: expect.any(String),
      }),
    ]),
  );
  expect(JSON.stringify(frontendHealthSnapshot())).not.toContain(bearerSecret);
  render(React.createElement(ForkDraftCard, { store: state }));
  expect(screen.getByRole("alert")).toHaveTextContent("共享文件列表暂时不可用，请稍后重试。");
  expect(document.body.textContent).not.toContain(bearerSecret);
});

it("sends selected shared files as canonical filecontent kickoff input", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [{ id: "user-1", kind: "user", text: "continue with shared files" }],
    },
  });
  backendApi.listSharedFiles.mockResolvedValue({
    files: [{ path: "notes/a.md" }, { path: "notes/b.md" }],
  });
  backendApi.forkThread.mockResolvedValue({
    thread: { id: "thread-fork", forkedFrom: "thread-1" },
    kickoffState: "created_only",
  });
  backendApi.readSharedFile.mockResolvedValue({
    path: "notes/a.md",
    content: "  indented\n",
  });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().openForkDraft();
  expect(useClientStore.getState().forkDraft.availableSharedFiles).toEqual([{ path: "notes/a.md" }, { path: "notes/b.md" }]);

  expect(useClientStore.getState().toggleForkDraftSharedFile("notes/a.md")).toBe(true);
  await useClientStore.getState().submitForkThread();

  expect(backendApi.readSharedFile).toHaveBeenCalledWith({ path: "notes/a.md" });
  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-fork",
    input: [
      { type: "text", text: "请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。" },
      {
        type: "filecontent",
        path: "notes/a.md",
        name: "notes/a.md",
        content: "  indented\n",
      },
    ],
    manualSkillSelection: false,
  });
});

it("validates selected filecontent before creating a backend fork", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
    timelinesByThread: { "thread-1": [] },
  });
  backendApi.listSharedFiles.mockResolvedValue({ files: [{ path: "notes/blank.md" }] });
  backendApi.readSharedFile.mockResolvedValue({ path: "notes/blank.md", content: "   \n" });

  await useClientStore.getState().openForkDraft();
  expect(useClientStore.getState().toggleForkDraftSharedFile("notes/blank.md")).toBe(true);
  await expect(useClientStore.getState().submitForkThread()).rejects.toThrow("fork shared file path and content are required");

  expect(backendApi.forkThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().activeThreadId).toBe("thread-1");
  expect(useClientStore.getState().forkDraft.open).toBe(true);
});

it("does not fall back to thread/start when canonical fork fails", async () => {
  const bearerSecret = "Bearer sk-frontend-fork-submit-secret-123456";
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing thread", provider: "codex", status: "idle" }],
    timelinesByThread: { "thread-1": [] },
  });
  backendApi.forkThread.mockRejectedValue(new Error(`thread/fork unsupported ${bearerSecret}`));

  await useClientStore.getState().openForkDraft();
  await expect(useClientStore.getState().submitForkThread()).rejects.toThrow("thread/fork unsupported");

  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().activeThreadId).toBe("thread-1");
  expect(useClientStore.getState().forkDraft.open).toBe(true);
  expect(useClientStore.getState().forkDraft.error).toBe("创建继承对话失败，请稍后重试。");
  expect(JSON.stringify(useClientStore.getState().forkDraft)).not.toContain(bearerSecret);
  expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain(bearerSecret);
  render(React.createElement(ForkDraftCard, { store: useClientStore.getState() }));
  expect(screen.getByRole("alert")).toHaveTextContent("创建继承对话失败，请稍后重试。");
  expect(document.body.textContent).not.toContain(bearerSecret);
});

it("keeps a newer active thread when an older sync response returns late", async () => {
  let resolveSnapshot;
  backendApi.getThreadState.mockReturnValue(
    new Promise((resolve) => {
      resolveSnapshot = resolve;
    }),
  );
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-old",
    threads: [
      { id: "thread-old", name: "Old", provider: "codex", status: "running" },
      { id: "thread-new", name: "New", provider: "codex", status: "running" },
    ],
    timelinesByThread: {
      "thread-new": [{ id: "user-new", role: "user", text: "new message", time: "2026-05-30T00:00:00Z" }],
    },
  });

  const sync = useClientStore.getState().syncThreadState("thread-old");
  await vi.waitFor(() => expect(backendApi.getThreadState).toHaveBeenCalled());
  useClientStore.setState({ activeThreadId: "thread-new" });
  resolveSnapshot({
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-old": [{ id: "old-assistant", kind: "assistant", text: "old reply" }],
    },
  });

  await sync;

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-new");
  expect(state.threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: "thread-new", name: "New" }), expect.objectContaining({ id: "thread-old", name: "Old" })]));
  expect(state.timelinesByThread["thread-new"]).toEqual([expect.objectContaining({ role: "user", text: "new message" })]);
});

it("keeps a newer active archived thread when an older sync response returns late", async () => {
  let resolveSnapshot;
  backendApi.getThreadState.mockReturnValue(
    new Promise((resolve) => {
      resolveSnapshot = resolve;
    }),
  );
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-old",
    threads: [
      { id: "thread-old", name: "Old", provider: "codex", status: "running" },
      { id: "thread-new-archived", name: "New Archived", provider: "codex", status: "archived" },
    ],
    timelinesByThread: {
      "thread-new-archived": [{ id: "user-new", role: "user", text: "new message", time: "2026-05-30T00:00:00Z" }],
    },
  });

  const sync = useClientStore.getState().syncThreadState("thread-old");
  await vi.waitFor(() => expect(backendApi.getThreadState).toHaveBeenCalled());
  useClientStore.setState({ activeThreadId: "thread-new-archived" });
  resolveSnapshot({
    activeThreadId: "thread-old",
    threads: [{ id: "thread-old", name: "Old", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-old": [{ id: "old-assistant", kind: "assistant", text: "old reply" }],
    },
  });

  await sync;

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe("thread-new-archived");
});

it("applies thread snapshot before a concurrent message history load finishes", async () => {
  const snapshot = deferred();
  const messages = deferred();
  backendApi.getThreadState.mockReturnValue(snapshot.promise);
  backendApi.getThreadMessages.mockReturnValue(messages.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
  });

  const sync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(backendApi.getThreadMessages).toHaveBeenCalledWith({ threadId: "thread-1", limit: 300 }));
  snapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Synced name", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [{ id: "snapshot-assistant", kind: "assistant", text: "snapshot reply" }],
    },
  });
  await vi.waitFor(() => expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ name: "Synced name" })));

  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "snapshot reply" })]);

  messages.resolve(
    threadMessagesPage({
      messages: [{ id: "message-user", role: "user", content: "loaded prompt", createdAt: "2026-05-30T00:00:00Z" }],
    }),
  );
  await expect(sync).resolves.toBe(true);
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([
    expect.objectContaining({ text: "loaded prompt" }),
    expect.objectContaining({ text: "snapshot reply" }),
  ]);
});

it("applies the first message page before a slower thread snapshot returns", async () => {
  const snapshot = deferred();
  backendApi.getThreadState.mockReturnValue(snapshot.promise);
  backendApi.getThreadMessages.mockResolvedValue(
    threadMessagesPage({
      messages: [{ id: "message-user", role: "user", content: "loaded prompt", createdAt: "2026-05-30T00:00:00Z" }],
      hasMore: false,
      nextBefore: "",
    }),
  );
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
  });

  const sync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "loaded prompt" })]));

  snapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Synced name", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });
  await expect(sync).resolves.toBe(true);
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "loaded prompt" })]);
});

it("keeps trusted cached messages visible while a refresh message page is loading", async () => {
  const snapshot = deferred();
  const messages = deferred();
  backendApi.getThreadState.mockReturnValue(snapshot.promise);
  backendApi.getThreadMessages.mockReturnValue(messages.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "running" }],
    timelinesByThread: {
      "thread-1": [{ id: "cached-user", role: "user", text: "cached prompt", time: "2026-05-30T00:00:00Z" }],
    },
    threadTimelineReadyByThread: { "thread-1": true },
  });

  const sync = useClientStore.getState().syncThreadState("thread-1");
  await vi.waitFor(() => expect(backendApi.getThreadMessages).toHaveBeenCalled());
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "cached prompt" })]);

  messages.resolve(threadMessagesPage());
  snapshot.resolve({
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Existing", provider: "codex", status: "idle" }],
    timelinesByThread: {},
  });

  await expect(sync).resolves.toBe(true);
  expect(useClientStore.getState().timelinesByThread["thread-1"]).toEqual([expect.objectContaining({ text: "cached prompt" })]);
});
