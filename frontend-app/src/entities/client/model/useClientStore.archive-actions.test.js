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
  it,
  registerBridgeEventHandlersForTest,
  registerClientStoreTestHooks,
  resetClientStoreForTests,
  systemClockMillis,
  threadMessagesPage,
  useClientStore,
} from "./useClientStore.testHarness.js";

registerClientStoreTestHooks({ runtime, backend: runtime.backend });

it("clears stale recover pending without polluting the newly active thread", async () => {
  const recovery = deferred();
  backendApi.recoverThread.mockReturnValueOnce(recovery.promise);
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "旧线程", provider: "codex", status: "running" },
      { id: "thread-2", name: "新线程", provider: "codex", status: "idle" },
    ],
  });

  const pending = useClientStore.getState().recoverActiveThread();
  expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({ "thread-1": true });
  useClientStore.setState({ activeThreadId: "thread-2", actionNotice: null });

  recovery.resolve({
    thread: { id: "thread-1", status: "recovering" },
    recovered: true,
    mode: "relaunch_resume",
  });
  await expect(pending).resolves.toBe(true);

  expect(useClientStore.getState().activeThreadId).toBe("thread-2");
  expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({});
  expect(useClientStore.getState().actionNotice).toBeNull();
  expect(useClientStore.getState().warningEntries.filter((entry) => entry.event === "thread.recover.failed")).toEqual([]);
});

it("restores archived threads without enabling active thread actions", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-archived",
    threads: [{ id: "thread-archived", name: "归档线程", provider: "codex", status: "archived", archived: true }],
  });

  expect(useClientStore.getState().hasActiveThreadActions()).toBe(false);
  await expect(useClientStore.getState().archiveThread("thread-archived", false)).resolves.toBe(true);

  expect(backendApi.unarchiveThread).toHaveBeenCalledWith({ threadId: "thread-archived" });
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "archivedThreadAtById.thread-archived",
    value: null,
  });
  expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ archived: false }));
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "线程已恢复到列表",
      tone: "success",
    }),
  );
});

it("surfaces archive RPC failures without mutating local archive state", async () => {
  backendApi.archiveThread.mockRejectedValueOnce(new Error("orchestration: service not configured"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "后端线程", provider: "codex", status: "idle", archived: false }],
  });

  await expect(useClientStore.getState().archiveThread("thread-1", true)).rejects.toThrow("orchestration: service not configured");

  expect(backendApi.archiveThread).toHaveBeenCalledWith({ threadId: "thread-1" });
  expect(backendApi.setPreference).not.toHaveBeenCalledWith(
    expect.objectContaining({
      key: "archivedThreadAtById.thread-1",
    }),
  );
  expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ archived: false, status: "idle" }));
  expect(useClientStore.getState().threadArchiveLoadingByThread["thread-1"]).toBe(false);
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "归档会话失败，请重试。",
      tone: "error",
    }),
  );
  expect(useClientStore.getState().warningEntries.at(-1)).toEqual(
    expect.objectContaining({
      event: "thread.archive.failed",
      level: "error",
    }),
  );
});

it("surfaces archive preference failures after backend archive succeeds", async () => {
  backendApi.setPreference.mockRejectedValueOnce(new Error("preference backend offline"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "后端线程", provider: "codex", status: "idle", archived: false }],
  });

  await expect(useClientStore.getState().archiveThread("thread-1", true)).rejects.toThrow("preference backend offline");

  expect(backendApi.archiveThread).toHaveBeenCalledWith({ threadId: "thread-1" });
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "archivedThreadAtById.thread-1",
    value: expect.any(Number),
  });
  expect(useClientStore.getState().threads[0]).toEqual(
    expect.objectContaining({
      archived: true,
      status: "archived",
    }),
  );
  expect(useClientStore.getState().activeThreadId).toBe("");
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "归档偏好保存失败，请重试。",
      tone: "error",
    }),
  );
  expect(useClientStore.getState().warningEntries.at(-1)).toEqual(
    expect.objectContaining({
      event: "thread.archive.preference.failed",
      level: "error",
    }),
  );
});

it("surfaces rename RPC failures without closing over a rejected action", async () => {
  backendApi.renameThread.mockRejectedValueOnce(new Error("name backend offline"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "旧名称", provider: "codex", status: "idle" }],
  });

  await expect(useClientStore.getState().renameThread("thread-1", "新名称")).rejects.toThrow("name backend offline");

  expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ name: "旧名称" }));
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "重命名会话失败，请重试。",
      tone: "error",
    }),
  );
  expect(useClientStore.getState().warningEntries.at(-1)).toEqual(
    expect.objectContaining({
      event: "thread.rename.failed",
      level: "error",
    }),
  );
});

it("surfaces pin preference failures without mutating local pin state", async () => {
  backendApi.setPreference.mockRejectedValueOnce(new Error("preference backend offline"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "后端线程", provider: "codex", status: "idle", pinned: false, pinnedAt: 0 }],
  });

  await expect(useClientStore.getState().toggleThreadPin("thread-1")).rejects.toThrow("preference backend offline");

  expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ pinned: false, pinnedAt: 0 }));
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "置顶会话失败，请重试。",
      tone: "error",
    }),
  );
  expect(useClientStore.getState().warningEntries.at(-1)).toEqual(
    expect.objectContaining({
      event: "thread.pin.failed",
      level: "error",
    }),
  );
});

it("deletes stale archived threads through backend and clears archive preferences", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-stale",
    threads: [
      { id: "thread-stale", name: "旧归档线程", provider: "codex", status: "archived", archived: true, archivedAt: systemClockMillis() - 8 * 24 * 60 * 60 * 1000 },
      { id: "thread-fresh", name: "近期归档线程", provider: "codex", status: "archived", archived: true, archivedAt: systemClockMillis() },
    ],
  });

  await expect(useClientStore.getState().deleteStaleThreads(["thread-stale"])).resolves.toEqual({ deleted: 1, failed: 0 });

  expect(backendApi.deleteThread).toHaveBeenCalledWith({ threadId: "thread-stale" });
  expect(backendApi.setPreference).toHaveBeenCalledWith({
    cwd: "/repo/app",
    key: "archivedThreadAtById.thread-stale",
    value: null,
  });
  expect(useClientStore.getState().threads.map((thread) => thread.id)).toEqual(["thread-fresh"]);
  expect(useClientStore.getState().activeThreadId).toBe("");
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "已删除 1 个无用会话",
      tone: "success",
    }),
  );
});

it("commits successful thread deletions but rejects a partial failure for the action boundary", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-ok",
    threads: [
      { id: "thread-ok", name: "可删除", provider: "codex", status: "archived", archived: true },
      { id: "thread-failed", name: "删除失败", provider: "codex", status: "archived", archived: true },
    ],
  });
  const rawFailure = new Error("raw delete provider failure");
  backendApi.deleteThread.mockResolvedValueOnce({ ok: true }).mockRejectedValueOnce(rawFailure);

  await expect(useClientStore.getState().deleteStaleThreads(["thread-ok", "thread-failed"])).rejects.toThrow("1 thread delete action(s) failed");

  expect(useClientStore.getState().threads.map((thread) => thread.id)).toEqual(["thread-failed"]);
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({
      message: "已删除 1 个无用会话，1 个失败",
      tone: "warning",
    }),
  );
  expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain("raw delete");
});

it("preserves the reference of equivalent timeline items during bridge patch merges", async () => {
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle" }],
    timelinesByThread: {
      "thread-1": [
        {
          id: "msg-1",
          role: "assistant",
          kind: "assistant",
          text: "hello world",
          done: true,
          time: "2026-06-05T00:00:00Z",
        },
      ],
    },
  });
  registerBridgeEventHandlersForTest();

  const existingMessage = useClientStore.getState().timelinesByThread["thread-1"][0];
  // 模拟推送一个具有相同 ID 并且内容完全一致的 replacement timelineItem，但引用不同
  const patchItem = { ...existingMessage };
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      timelineItems: [patchItem],
    },
  });

  const timeline = useClientStore.getState().timelinesByThread["thread-1"];
  expect(timeline).toHaveLength(1);
  // 判定其引用必须保持为原来的 existingMessage，而不是被 patchItem 替换
  expect(timeline[0]).toBe(existingMessage);

  // 另外测试如果内容不一致（比如 done 变为了 false），则必须被 replacement 覆盖，且引用改变
  const changedPatchItem = { ...existingMessage, done: false };
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      timelineItems: [changedPatchItem],
    },
  });
  const updatedTimeline = useClientStore.getState().timelinesByThread["thread-1"];
  expect(updatedTimeline[0]).not.toBe(existingMessage);
  expect(updatedTimeline[0].done).toBe(false);
});

it("keeps the backend archive result but rejects when its preference write fails", async () => {
  backendApi.setPreference.mockRejectedValueOnce(new Error("preference write error"));
  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle", archived: false }],
  });

  await expect(useClientStore.getState().archiveThread("thread-1", true)).rejects.toThrow("preference write error");
  expect(useClientStore.getState().threads[0].archived).toBe(true);
});

it("preserves the optimistic archive state when a snapshot or patch is applied while loading or recently mutated", async () => {
  backendApi.getThreadState
    .mockResolvedValueOnce({
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle", archived: false }],
    })
    .mockResolvedValueOnce({
      threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle", archived: false }],
    });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage()).mockResolvedValueOnce(threadMessagesPage());

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [{ id: "thread-1", name: "Thread 1", provider: "codex", status: "idle", archived: false }],
  });
  registerBridgeEventHandlersForTest();

  // Start archiving (simulates in-flight archive)
  const archivePromise = useClientStore.getState().archiveThread("thread-1", true);
  expect(useClientStore.getState().threads[0].archived).toBe(true);
  expect(useClientStore.getState().threadArchiveLoadingByThread["thread-1"]).toBe(true);

  // 1. Simulate a bridge patch containing stale state
  runtime.bridgeCallback({
    type: "ui/thread/patch",
    payload: {
      threadId: "thread-1",
      patchedThread: {
        id: "thread-1",
        archived: false,
      },
    },
  });
  expect(useClientStore.getState().threads[0].archived).toBe(true);

  // 2. Simulate a syncThreadState database reload containing stale state
  await useClientStore.getState().syncThreadState("thread-1");
  expect(useClientStore.getState().threads[0].archived).toBe(true);

  // Resolve the archive RPC
  await archivePromise;
  expect(useClientStore.getState().threads[0].archived).toBe(true);
  expect(useClientStore.getState().threadArchiveLoadingByThread["thread-1"]).toBe(false);

  // 3. Simulate another syncThreadState database reload containing stale state within the 8s window
  await useClientStore.getState().syncThreadState("thread-1");
  expect(useClientStore.getState().threads[0].archived).toBe(true);
});

it("matches optimistic archive overrides by both agent runtime ID and database UUID", async () => {
  backendApi.getThreadState.mockResolvedValueOnce({
    threads: [{ id: "019e98df-2cd9-76b0-ad5b-9f1f252fa764", agent_id: "agent_123", name: "Draft", provider: "codex", status: "idle", archived: false }],
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", agentId: "agent_123", name: "Draft", provider: "codex", status: "idle", archived: false }],
  });

  // Start archiving. Because store.threads has id = 'agent_123', the id in archiveThread resolves to 'agent_123'
  const archivePromise = useClientStore.getState().archiveThread("agent_123", true);
  expect(useClientStore.getState().threads[0].archived).toBe(true);
  expect(useClientStore.getState().threadArchiveLoadingByThread["agent_123"]).toBe(true);

  // Now, run syncThreadState. The server responds with database UUID '019e98df-2cd9-76b0-ad5b-9f1f252fa764'
  // B's override (saved under agent_123) should be matched via identity.agentId and preserve its archived status!
  await useClientStore.getState().syncThreadState("agent_123");
  expect(useClientStore.getState().threads[0].archived).toBe(true);

  await archivePromise;
});

it("preserves other threads optimistic archive states when a concurrent archive action fails and rolls back", async () => {
  // A fails, B succeeds.
  backendApi.archiveThread
    .mockRejectedValueOnce(new Error("Archiving A failed")) // A fails
    .mockResolvedValueOnce({ ok: true }); // B succeeds

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-A",
    threads: [
      { id: "thread-A", name: "Thread A", provider: "codex", status: "idle", archived: false },
      { id: "thread-B", name: "Thread B", provider: "codex", status: "idle", archived: false },
    ],
  });

  const promiseA = useClientStore.getState().archiveThread("thread-A", true);
  const promiseB = useClientStore.getState().archiveThread("thread-B", true);

  expect(useClientStore.getState().threads.find((t) => t.id === "thread-A").archived).toBe(true);
  expect(useClientStore.getState().threads.find((t) => t.id === "thread-B").archived).toBe(true);

  // Resolve A (which fails)
  await expect(promiseA).rejects.toThrow("Archiving A failed");

  // A should be rolled back to active (archived = false)
  expect(useClientStore.getState().threads.find((t) => t.id === "thread-A").archived).toBe(false);
  // B's optimistic archive state should NOT be affected (remains true)!
  expect(useClientStore.getState().threads.find((t) => t.id === "thread-B").archived).toBe(true);

  // Resolve B (succeeds)
  await promiseB;
  expect(useClientStore.getState().threads.find((t) => t.id === "thread-B").archived).toBe(true);
});

it("resolves archivedAt and pinnedAt states using both agent runtime ID and database UUID from configuration maps", async () => {
  backendApi.getThreadState.mockReset();
  backendApi.getThreadMessages.mockReset();
  backendApi.getThreadMessages.mockResolvedValue(threadMessagesPage());

  // Case A: Map has agent_123, Thread has DB UUID
  backendApi.getThreadState.mockResolvedValueOnce({
    threads: [{ id: "019e98df-2cd9-76b0-ad5b-9f1f252fa764", agent_id: "agent_123", name: "Draft", provider: "codex", status: "idle" }],
    archivedThreadAtById: { agent_123: 1500000000000 },
    pinnedThreadAtById: { agent_123: 1600000000000 },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "agent_123",
    threads: [{ id: "agent_123", agentId: "agent_123", name: "Draft", provider: "codex", status: "idle", archived: false }],
  });

  await useClientStore.getState().syncThreadState("agent_123");
  let syncedThread = useClientStore.getState().threads[0];
  expect(syncedThread.archived).toBe(true);
  expect(syncedThread.archivedAt).toBe(1500000000000);
  expect(syncedThread.pinned).toBe(true);
  expect(syncedThread.pinnedAt).toBe(1600000000000);

  // Case B: Map has DB UUID, Thread has agent_123
  backendApi.getThreadState.mockResolvedValueOnce({
    threads: [{ id: "agent_123", agent_id: "agent_123", name: "Draft", provider: "codex", status: "idle" }],
    archivedThreadAtById: { "019e98df-2cd9-76b0-ad5b-9f1f252fa764": 1500000000000 },
    pinnedThreadAtById: { "019e98df-2cd9-76b0-ad5b-9f1f252fa764": 1600000000000 },
  });
  backendApi.getThreadMessages.mockResolvedValueOnce(threadMessagesPage());

  resetClientStoreForTests({
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "019e98df-2cd9-76b0-ad5b-9f1f252fa764",
    threads: [{ id: "019e98df-2cd9-76b0-ad5b-9f1f252fa764", agentId: "agent_123", name: "Draft", provider: "codex", status: "idle", archived: false }],
  });

  await useClientStore.getState().syncThreadState("019e98df-2cd9-76b0-ad5b-9f1f252fa764");
  syncedThread = useClientStore.getState().threads[0];
  expect(syncedThread.archived).toBe(true);
  expect(syncedThread.archivedAt).toBe(1500000000000);
  expect(syncedThread.pinned).toBe(true);
  expect(syncedThread.pinnedAt).toBe(1600000000000);
});
