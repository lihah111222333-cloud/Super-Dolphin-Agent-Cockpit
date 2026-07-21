import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi, getPromptHistory, listToolbridgeTools } from "./backendApi.js";
import {
  guardedOpsFilesResponse,
} from "./test-support/backendApi.opsFiles.testSupport.js";
import { codeSaveResponse } from "./test-support/backendApi.contractResponse.testSupport.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("wraps the independent new-window RPC with cwd validation", async () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true, windowId: "window-2", cwd: "/repo/window" });
  const api = createBackendApi({ callAPI });

  await api.openNewWindow({ cwd: "/repo/window" });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_OPEN_NEW_WINDOW, {
    cwd: "/repo/window",
  });
  expect(() => api.openNewWindow({ cwd: "" })).toThrow("cwd is required");
});

it("wraps shared file list, read, delete, open and preview helpers with the expected payload shapes", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsFilesResponse(method)));
  const openSharedFile = vi.fn().mockResolvedValue({ opened: true });
  const previewSharedFile = vi.fn().mockResolvedValue({ url: "/shared-file-preview?id=sf_1" });
  const api = createBackendApi({ callAPI, openSharedFile, previewSharedFile });

  await api.listSharedFiles();
  await api.readSharedFile({ path: "reports/final.md" });
  await api.deleteSharedFile({ path: "scratch/work.json" });
  await api.openSharedFile({ path: "dag/video/final.mp4" });
  await api.previewSharedFile({ path: "dag/video/final.mp4" });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SHARED_FILE_GET, {
    path: "reports/final.md",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SHARED_FILE_DELETE, {
    path: "scratch/work.json",
  });
  expect(openSharedFile).toHaveBeenCalledWith({ path: "dag/video/final.mp4" });
  expect(previewSharedFile).toHaveBeenCalledWith({
    path: "dag/video/final.mp4",
  });
  expect(() => api.listSharedFiles([])).toThrow("params must be an object");
  expect(() => api.readSharedFile({ path: "" })).toThrow("path is required");
  expect(() => api.deleteSharedFile({ path: "" })).toThrow("path is required");
  expect(() => api.previewSharedFile({ path: "" })).toThrow("path is required");
});

it("rejects malformed shared file detail responses at the RPC boundary", async () => {
  const callAPI = vi.fn().mockResolvedValue({ content: "missing path" });
  const api = createBackendApi({ callAPI });

  await expect(api.readSharedFile({ path: "reports/final.md" })).rejects.toThrow(/shared file detail path is required/);
});

it("wraps runtime code locate, open and save RPCs with scoped payloads", async () => {
  const callAPI = vi.fn((method) =>
    Promise.resolve(method === RPC_METHODS.UI_CODE_SAVE ? codeSaveResponse() : { ok: true }),
  );
  const api = createBackendApi({ callAPI });

  await api.locateCodeFile({
    filePath: "src/App.jsx",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  await api.openCodeFile({
    filePath: "src/App.jsx",
    project: "/repo/app",
    projects: ["/repo/app"],
    line: 10,
    column: 2,
  });
  await api.openPath({
    filePath: "src",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  await api.saveCodeFile({
    filePath: "src/App.jsx",
    content: "export default App;",
    project: "/repo/app",
    projects: ["/repo/app"],
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_LOCATE, {
    filePath: "src/App.jsx",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_OPEN, {
    filePath: "src/App.jsx",
    project: "/repo/app",
    projects: ["/repo/app"],
    line: 10,
    column: 2,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PATH_OPEN, {
    filePath: "src",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_SAVE, {
    filePath: "src/App.jsx",
    content: "export default App;",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  expect(() => api.locateCodeFile({ filePath: "" })).toThrow("filePath is required");
  expect(() => api.openCodeFile({ filePath: "" })).toThrow("filePath is required");
  expect(() => api.openPath({ filePath: "" })).toThrow("filePath is required");
  expect(() => api.saveCodeFile({ filePath: "src/App.jsx" })).toThrow("content is required");
  expect(() => api.saveCodeFile({ filePath: "src/App.jsx", content: null })).toThrow("content must be a string");
});

it("lists the canonical toolbridge catalog for one cwd", async () => {
  const response = { tools: [] };
  const callAPI = vi.fn().mockResolvedValue(response);
  const api = createBackendApi({ callAPI });

  await expect(api.listToolbridgeTools({ cwd: "/repo/app" })).resolves.toEqual(response);

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
    cwd: "/repo/app",
  });
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.listToolbridgeTools({ cwd: "/repo/app", serverName: "lsp" }),
    "toolbridge/tools/list: unsupported payload field serverName",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.listToolbridgeTools({ cwd: " " }),
    "toolbridge/tools/list: cwd is required",
  );
  expect(typeof listToolbridgeTools).toBe("function");
});

it("rejects an invalid recover response before a runtime consumer can observe it", async () => {
  const callAPI = vi.fn().mockResolvedValue({
    thread: { id: "thread-1", status: "recovering" },
    recovered: true,
    mode: "relaunch_resume",
    unexpected: true,
  });
  const runtimeConsumer = vi.fn();
  const api = createBackendApi({ callAPI });

  await expect(api.recoverThread({ cwd: "/repo/app", threadId: "thread-1" }).then(runtimeConsumer)).rejects.toThrow(
    "thread/recover response body must not include unexpected",
  );

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_RECOVER, {
    threadId: "thread-1",
  });
  expect(runtimeConsumer).not.toHaveBeenCalled();
});

it("wraps prompt history with the exact bounded request contract", async () => {
  const response = {
    entries: [],
    nextCursor: "",
    hasMore: false,
    nonce: "nonce-1",
  };
  const callAPI = vi.fn().mockResolvedValue(response);
  const api = createBackendApi({ callAPI });

  await expect(
    api.getPromptHistory({
      cwd: " /repo/app ",
      activeThreadId: "thread-1",
      cursor: " cursor-1 ",
      nonce: " nonce-1 ",
      limit: 50,
    }),
  ).resolves.toBe(response);
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_PROMPT_HISTORY, {
    cwd: "/repo/app",
    activeThreadId: "thread-1",
    cursor: " cursor-1 ",
    nonce: " nonce-1 ",
    limit: 50,
  });

  for (const params of [
    { cwd: "", limit: 50 },
    { cwd: "/repo/app", limit: 0 },
    { cwd: "/repo/app", limit: 51 },
    { cwd: "/repo/app", limit: 1.5 },
    { cwd: "/repo/app", limit: "10" },
    { cwd: "/repo/app", cursor: "x".repeat(2049), limit: 10 },
    { cwd: "/repo/app", nonce: "x".repeat(2049), limit: 10 },
    { cwd: "/repo/app", cursor: "界".repeat(683), limit: 10 },
    { cwd: "/repo/app", limit: 10, surprise: true },
  ]) {
    expect(() => api.getPromptHistory(params)).toThrow();
  }
  expect(callAPI).toHaveBeenCalledTimes(1);
  expect(getPromptHistory).toBeTypeOf("function");
});
