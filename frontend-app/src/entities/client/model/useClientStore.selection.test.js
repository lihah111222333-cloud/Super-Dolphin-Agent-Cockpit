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
  expect,
  it,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("includes expanded local Codex home defaults in thread/start launch payload", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use expanded default Codex home",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "C:\\Users\\ai01\\.codex",
        "settings.provider.codex.codexInstanceKey": "default",
        "settings.provider.codex.codexModelProvider": "openai",
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-expanded-default-codex" });
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
        codexHome: "C:\\Users\\ai01\\.codex",
        codexInstanceKey: "default",
        codexModelProvider: "openai",
      },
    }),
  );
});

it("starts thread without model preference when it is missing", async () => {
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
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "/Users/test/.codex-alt",
        "settings.provider.codex.codexInstanceKey": "desktop-main",
        "settings.provider.codex.codexModelProvider": "openrouter",
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-default-model" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      effort: "xhigh",
    }),
  );
  expect(backendApi.startThread).toHaveBeenCalledWith(expect.not.objectContaining({ model: expect.any(String) }));
});

it("exposes the same launch preferences for non-chat thread launches", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
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
        "settings.activePromptKey": "main/reviewer",
      }[key] ?? null,
    ),
  );

  await expect(useClientStore.getState().resolveLaunchPreferences("/repo/app")).resolves.toEqual({
    modelProvider: "codex",
    model: "gpt-5.5",
    effort: "xhigh",
    codexModelProvider: "openrouter",
    prompt_key: "main/reviewer",
    config: {
      codexHome: "/Users/test/.codex-alt",
      codexInstanceKey: "desktop-main",
      codexModelProvider: "openrouter",
    },
  });
});

it("includes Codex runtime permission preferences in launch preferences", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
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
        "settings.provider.codex.sandbox": {
          type: "workspaceWrite",
          writableRoots: ["/repo/app"],
          networkAccess: true,
        },
        "settings.provider.codex.approvalPolicy": "on-request",
        "settings.provider.codex.personality": "pragmatic",
        "settings.provider.codex.summary": "concise",
      }[key] ?? null,
    ),
  );

  await expect(useClientStore.getState().resolveLaunchPreferences("/repo/app")).resolves.toEqual({
    modelProvider: "codex",
    model: "gpt-5.5",
    effort: "xhigh",
    codexModelProvider: "openrouter",
    sandbox: {
      type: "workspaceWrite",
      writableRoots: ["/repo/app"],
      networkAccess: true,
    },
    approvalPolicy: "on-request",
    personality: "pragmatic",
    summary: "concise",
    config: {
      codexHome: "/Users/test/.codex-alt",
      codexInstanceKey: "desktop-main",
      codexModelProvider: "openrouter",
    },
  });
});

it("rejects object-shaped provider preferences before thread/start", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use object prefs",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": { value: "codex", label: "Codex" },
        "settings.provider.codex.model": { value: "gpt-5.5", label: "GPT" },
        "settings.provider.codex.effort": { id: "medium", label: "Medium" },
        "settings.provider.codex.codexHome": "/Users/test/.codex-alt",
        "settings.provider.codex.codexInstanceKey": "desktop-main",
        "settings.provider.codex.codexModelProvider": "openrouter",
        "settings.provider.codex.sandbox": {
          type: "workspaceWrite",
          writableRoots: ["/repo/app"],
          networkAccess: false,
        },
        "settings.provider.codex.approvalPolicy": "never",
        "settings.provider.codex.personality": "pragmatic",
        "settings.provider.codex.summary": "concise",
      }[key] ?? null,
    ),
  );
  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("invalid UI preference response for settings.provider.active");

  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().draft).toBe("Use object prefs");
});

it("rejects object-shaped provider preferences before thread/start without partial launch", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Reject object prefs",
    attachments: [],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": { value: "gpt-5.5", label: "GPT" },
        "settings.provider.codex.effort": "medium",
      }[key] ?? null,
    ),
  );

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow("invalid UI preference response for settings.provider.codex.model");

  expect(backendApi.startThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).not.toHaveBeenCalled();
  expect(useClientStore.getState().draft).toBe("Reject object prefs");
});

it("starts a selected Codex provider thread instead of sending into a failed active session", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-failed",
    provider: "codex",
    draft: "Retry through selected Codex provider",
    attachments: [],
    threads: [{ id: "thread-failed", name: "Broken", provider: "codex", status: "failed" }],
  });
  backendApi.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
      }[key] ?? null,
    ),
  );
  backendApi.startThread.mockResolvedValue({ threadId: "thread-codex" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      modelProvider: "codex",
      model: "gpt-5.5",
      effort: "xhigh",
      deferSpawn: true,
    }),
  );
  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-codex",
    input: [{ type: "text", text: "Retry through selected Codex provider" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-codex");
});

it("does not auto-recover or retry turn/start when the backend session is missing", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    draft: "Retry on missing session",
    attachments: [],
  });
  backendApi.startTurn.mockRejectedValueOnce(new Error('session not found for agent "agent_123"'));
  backendApi.recoverThread.mockResolvedValue({ recovered: true });

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow('session not found for agent "agent_123"');

  expect(backendApi.recoverThread).not.toHaveBeenCalled();
  expect(backendApi.startTurn).toHaveBeenCalledTimes(1);
  expect(useClientStore.getState().draft).toBe("Retry on missing session");
});

it("recovers a stopped thread and retries turn/start once", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    draft: "Continue stopped DAG agent",
    attachments: [],
    threads: [{ id: "thread-1", name: "DAG agent", provider: "codex", status: "stopped" }],
  });
  backendApi.startTurn
    .mockRejectedValueOnce(new Error('{"message":"[-32098] resolve session: thread \\"thread-1\\": resolve session: thread \\"thread-1\\" is stopped"}'))
    .mockResolvedValueOnce({ ok: true });
  backendApi.recoverThread.mockResolvedValue({ recovered: true, mode: "relaunch_resume" });

  await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);

  expect(backendApi.recoverThread).toHaveBeenCalledWith({ cwd: "/repo/app", threadId: "thread-1" });
  expect(backendApi.startTurn).toHaveBeenCalledTimes(2);
  expect(backendApi.startTurn).toHaveBeenNthCalledWith(2, {
    cwd: "/repo/app",
    threadId: "thread-1",
    input: [{ type: "text", text: "Continue stopped DAG agent" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().draft).toBe("");
});

it("starts a fresh Codex thread when auto-resume fails because identity is missing", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-legacy",
    draft: "Continue legacy thread",
    attachments: [],
    composerCapabilities: boundCapabilities,
    threads: [{ id: "thread-legacy", name: "Legacy", provider: "codex", status: "running" }],
  });
  backendApi.startTurn
    .mockRejectedValueOnce(new Error('resolve session: thread "thread-legacy": resolve session: auto-resume failed: codex identity required for resume'))
    .mockResolvedValueOnce({ ok: true });
  backendApi.startThread.mockResolvedValue({ threadId: "thread-recovered", agentId: "agent-recovered" });

  await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);

  expect(backendApi.startThread).toHaveBeenCalledWith(
    expect.objectContaining({
      cwd: "/repo/app",
      modelProvider: "codex",
      config: {
        codexHome: "~/.codex",
        codexInstanceKey: "default",
        codexModelProvider: "openai",
      },
    }),
  );
  expect(backendApi.startTurn).toHaveBeenCalledTimes(2);
  expect(backendApi.startTurn).toHaveBeenNthCalledWith(1, {
    cwd: "/repo/app",
    threadId: "thread-legacy",
    input: [{ type: "text", text: "Continue legacy thread" }],
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
  expect(backendApi.startTurn).toHaveBeenNthCalledWith(2, {
    cwd: "/repo/app",
    threadId: "thread-recovered",
    input: [{ type: "text", text: "Continue legacy thread" }],
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
  expect(useClientStore.getState().activeThreadId).toBe("thread-recovered");
  expect(useClientStore.getState().draft).toBe("");
});

it("applies window bootstrap snapshot before scoped RPCs", async () => {
  backendApi.getWindowBootstrap.mockResolvedValue({
    snapshot: { cwd: "/repo/other", page: "skills" },
  });
  backendApi.getProjects.mockResolvedValue({ projects: ["/repo/app", "/repo/other"], active: "/repo/other" });
  backendApi.getSidebarState.mockResolvedValue({ threads: [] });

  await useClientStore.getState().bootstrap();

  expect(backendApi.getProjects).toHaveBeenCalledWith({ cwd: "/repo/other" });
  expect(backendApi.getSidebarState).toHaveBeenCalledWith({ cwd: "/repo/other" });
  expect(useClientStore.getState()).toEqual(
    expect.objectContaining({
      cwd: "/repo/app",
      activeProject: "/repo/other",
      activePage: "skills",
    }),
  );
});

it("accepts the real backend nested thread/start response shape", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Hello nested backend",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ thread: { id: "thread-nested" }, pending_launch: true });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-nested",
    input: [{ type: "text", text: "Hello nested backend" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-nested");
});

it("prefers nested thread_id over agent-like ids in thread/start responses", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Hello canonical nested backend",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ thread: { id: "agent_123", thread_id: "thread-nested", agent_id: "agent_123" } });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-nested",
    input: [{ type: "text", text: "Hello canonical nested backend" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("thread-nested");
  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-nested",
      agentId: "agent_123",
    }),
  );
});

it("accepts non-placeholder agent_id as a thread/start fallback id", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use agent id fallback",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({ agent_id: "essay_agent_16" });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "essay_agent_16",
    input: [{ type: "text", text: "Use agent id fallback" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("essay_agent_16");
});

it("accepts backend pending-launch thread ids that look like runtime agent ids", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    draft: "Use pending launch id",
    attachments: [],
  });
  backendApi.startThread.mockResolvedValue({
    thread: { id: "agent_1780163711518420000", status: "created" },
    threadId: "agent_1780163711518420000",
    thread_id: "agent_1780163711518420000",
    sessionId: "agent_1780163711518420000",
    session_id: "agent_1780163711518420000",
    status: "created",
    agentId: "agent_1780163711518420000",
    agent_id: "agent_1780163711518420000",
    pending_launch: true,
    pendingLaunch: true,
  });
  backendApi.startTurn.mockResolvedValue({ ok: true });

  await useClientStore.getState().sendDraft();

  expect(backendApi.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "agent_1780163711518420000",
    input: [{ type: "text", text: "Use pending launch id" }],
    manualSkillSelection: false,
  });
  expect(useClientStore.getState().activeThreadId).toBe("agent_1780163711518420000");
  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "agent_1780163711518420000",
      agentId: "agent_1780163711518420000",
    }),
  );
});

it("keeps opened sidebar threads with zero archivedAt visible", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    threads: [
      {
        id: "thread-existing",
        name: "Existing chat",
        provider: "codex",
        status: "idle",
        cwd: "/repo/app",
        archived: false,
        archivedAt: 0,
      },
    ],
  });

  useClientStore.getState().beginOpeningThread({
    id: "thread-existing",
    agentId: "thread-existing",
    providerThreadId: "",
    sessionId: "",
    cwd: "/repo/app",
    name: "Existing chat",
    provider: "codex",
    status: "idle",
    archived: false,
    archivedAt: 0,
  });

  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      id: "thread-existing",
      archived: false,
      archivedAt: 0,
    }),
  );
});

it("keeps opened sidebar threads in place when selecting an existing thread", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", name: "Thread A", provider: "codex", status: "idle", cwd: "/repo/app" },
      { id: "thread-b", name: "Thread B", provider: "codex", status: "idle", cwd: "/repo/app" },
      { id: "thread-c", name: "Thread C", provider: "codex", status: "idle", cwd: "/repo/app" },
    ],
  });

  useClientStore.getState().beginOpeningThread({
    id: "thread-b",
    name: "Thread B updated",
    provider: "codex",
    status: "running",
    cwd: "/repo/app",
  });

  const state = useClientStore.getState();
  expect(state.threads.map((thread) => thread.id)).toEqual(["thread-a", "thread-b", "thread-c"]);
  expect(state.threads[1]).toEqual(
    expect.objectContaining({
      id: "thread-b",
      name: "Thread B updated",
      status: "running",
    }),
  );
  expect(state.activeThreadId).toBe("thread-b");
  expect(state.pendingActiveThreadId).toBe("thread-b");
});

it("issues distinct monotonic selection intents when the same thread is selected again", () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-b", cwd: "/repo/app", name: "Thread B", provider: "codex", status: "idle" },
    ],
  });

  const firstA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });
  const middleB = useClientStore.getState().beginOpeningThread({ id: "thread-b" });
  const latestA = useClientStore.getState().beginOpeningThread({ id: "thread-a" });

  expect(firstA).toEqual(expect.objectContaining({ targetThreadId: "thread-a" }));
  expect(middleB).toEqual(expect.objectContaining({ targetThreadId: "thread-b" }));
  expect(latestA).toEqual(expect.objectContaining({ targetThreadId: "thread-a" }));
  expect(latestA.selectionIntentId).toBeGreaterThan(middleB.selectionIntentId);
  expect(middleB.selectionIntentId).toBeGreaterThan(firstA.selectionIntentId);
});

it("rejects a conditional selection after a newer user selection invalidates its snapshot", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", cwd: "/repo/app", name: "Thread A", provider: "codex", status: "idle" },
      { id: "thread-b", cwd: "/repo/app", name: "Thread B", provider: "codex", status: "idle" },
    ],
  });
  backendApi.getThreadState.mockImplementation(({ threadId }) => ({
    activeThreadId: threadId,
    threads: [{ id: threadId, cwd: "/repo/app", provider: "codex", status: "idle" }],
  }));
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());
  const snapshot = useClientStore.getState().captureThreadSelection?.();

  await expect(useClientStore.getState().setActiveThread("thread-b")).resolves.toBe(true);
  await expect(useClientStore.getState().setActiveThread("thread-a", { selectionSnapshot: snapshot })).resolves.toBe(false);

  expect(useClientStore.getState().activeThreadId).toBe("thread-b");
});
