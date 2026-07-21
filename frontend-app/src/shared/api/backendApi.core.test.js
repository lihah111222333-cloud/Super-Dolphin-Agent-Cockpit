import { expect, it, vi } from "vitest";
import {
  RPC_METHODS,
  checkAppUpdate,
  createBackendApi,
  createDatasourceDocument,
  deleteDatasourceDocument,
  downloadAppUpdate,
  emitFrontendTraceEvent,
  getDatasourceDocument,
  importDatasourceLocalFile,
  installAppUpdate,
  installLatestAppUpdate,
  listDatasourceChunks,
  listDatasourceDocuments,
  listMCPServers,
  startPlaywrightMCPServer,
  startSQLiteMCPServer,
  stopPlaywrightMCPServer,
  stopSQLiteMCPServer,
  updateDatasourceDocument,
} from "./backendApi.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("exposes the dedicated frontend observability ingest RPC method name", () => {
  expect(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST).toBe("observability/frontend/ingest");
  expect(typeof emitFrontendTraceEvent).toBe("function");
});

it("maps observability query helpers to dedicated RPC methods", async () => {
  const response = { source: "memory", events: [{ traceId: "trace-1" }] };
  const callAPI = vi.fn().mockResolvedValue(response);
  const api = createBackendApi({ callAPI });

  await expect(api.getObservabilityTrace({ trace_id: "trace-1", limit: 5 })).resolves.toMatchObject({
    source: "memory",
    events: [expect.objectContaining({ traceId: "trace-1" })],
  });
  await api.getObservabilityThreadRecent({ thread_id: "thread-1", limit: 7 });
  await api.listObservabilityRecent({
    limit: 20,
    status: "error",
    component: "frontend",
    keyword: "thread/start",
    includeTail: false,
  });
  await api.listObservabilitySlow({ component: "rpc" });
  await api.listObservabilityErrors({ limit: 3 });
  await api.getObservabilityStatus();

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_TRACE_GET, {
    traceId: "trace-1",
    limit: 5,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, { threadId: "thread-1", limit: 7 });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_RECENT_LIST, {
    limit: 20,
    status: "error",
    component: "frontend",
    keyword: "thread/start",
    includeTail: false,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_SLOW_LIST, {
    component: "rpc",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_ERROR_LIST, {
    limit: 3,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_STATUS, {});
});

it("rejects malformed registered dashboard response boundaries", async () => {
  const callAPI = vi.fn((method) => {
    if (method === RPC_METHODS.UI_MEMORY_GET) return Promise.resolve({ private: null, team: { entries: [] } });
    if (method === RPC_METHODS.DASHBOARD_SHARED_FILES) return Promise.resolve({ files: null });
    if (method === RPC_METHODS.MODEL_PROVIDERS_LIST) return Promise.resolve(null);
    if (method === RPC_METHODS.OBSERVABILITY_TRACE_GET) return Promise.resolve(null);
    return Promise.resolve({ ok: true });
  });
  const api = createBackendApi({ callAPI });

  await expect(api.getMemorySnapshot({ cwd: "/repo/app" })).rejects.toThrow(/memory private entries must be an array/);
  await expect(api.listSharedFiles()).rejects.toThrow(/shared files dashboard response files must be an array/);
  await expect(api.listModelProviders({ cwd: "/repo/app" })).rejects.toThrow(/model provider registry/);
  await expect(api.getObservabilityTrace({ traceId: "trace-1" })).rejects.toThrow(
    /observability response must be an object/,
  );
});

it("rejects memory snapshot responses whose section entries are null at the facade boundary", async () => {
  // 生产端（internal/module/memory/ui_rpc.go loadUIMemoryScope）始终输出数组；
  // null entries 属于非法 wire 形状，facade 必须 fail-fast，不得归一为空列表。
  const callAPI = vi.fn().mockResolvedValue({
    overview: {},
    private: { entries: null },
    team: { entries: [] },
  });
  const api = createBackendApi({ callAPI });

  await expect(api.getMemorySnapshot({ cwd: "/repo/app" })).rejects.toThrow(/memory private entries must be an array/);
});

it("fails fast without extra backend calls for representative invalid facade inputs", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(callAPI, () => api.getObservabilityTrace({ trace_id: "" }), "traceId is required");
  expectInvalidInputDoesNotCall(callAPI, () => api.startThread({ cwd: "", modelProvider: "codex" }), "cwd is required");
  expectInvalidInputDoesNotCall(callAPI, () => api.startThread({ cwd: "/repo/app" }), "provider is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.startTurn({ cwd: "/repo/app", threadId: "", input: "build it" }),
    "threadId is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.createAndStartDag({ dagKey: "dag-1", title: "Dag", nodes: [] }),
    "nodes must be a non-empty array",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.dispatchDagNode({
        dagKey: "dag-1",
        runId: 88,
        nodeKey: "draft",
        assignedTo: "",
      }),
    "assignedTo is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.applyDagOps({ dagKey: "dag-1", ops: [] }),
    "baseVersion is required",
  );
  expectInvalidInputDoesNotCall(callAPI, () => api.setVideoApiKey({ apiKey: "" }), "apiKey is required");
  expectInvalidInputDoesNotCall(callAPI, () => api.listModelProviders({ cwd: "" }), "cwd is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.saveModelProviders({ registry: { vendors: [] } }),
    "cwd is required",
  );
  expectInvalidInputDoesNotCall(callAPI, () => api.applyModelProvider({ vendorId: "openrouter" }), "cwd is required");
});

it("wraps datasource_v2 CRUD RPC methods with strict payloads", async () => {
  const document = {
    documentId: 101,
    sourcePath: "C:\\data\\alpha.txt",
    fileName: "alpha.txt",
    extension: ".txt",
    sizeBytes: 42,
    contentHash: "hash",
    chunkCount: 1,
    totalChars: 5,
    status: "ready",
    errorMessage: "",
    createdAt: "2026-07-13T00:00:00Z",
    updatedAt: "2026-07-13T00:00:00Z",
  };
  const chunk = {
    id: 1,
    documentId: 101,
    chunkIndex: 0,
    content: "alpha",
    charCount: 5,
    byteCount: 5,
    embeddingModel: "",
    embeddingDim: 0,
    tokenCount: 1,
    createdAt: "2026-07-13T00:00:00Z",
  };
  const callAPI = vi.fn((method) =>
    Promise.resolve(
      {
        [RPC_METHODS.DATASOURCE_V2_LIST]: { documents: [document] },
        [RPC_METHODS.DATASOURCE_V2_GET]: {
          document,
          chunks: [chunk],
          hasMore: false,
          nextCursor: 1,
        },
        [RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS]: {
          chunks: [chunk],
          hasMore: false,
          nextCursor: 1,
        },
        [RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE]: {
          documentId: 102,
          sourcePath: "D:\\\\new\\\\fj.txt",
          fileName: "fj.txt",
          extension: ".txt",
          sizeBytes: 3,
          contentHash: "import-hash",
          chunkCount: 1,
          totalChars: 3,
          status: "ready",
        },
        [RPC_METHODS.DATASOURCE_V2_UPDATE]: document,
        [RPC_METHODS.DATASOURCE_V2_DELETE]: { documentId: 101, deleted: true },
      }[method] ?? { ok: true },
    ),
  );
  const api = createBackendApi({ callAPI });

  await api.createDatasourceDocument({ source_path: " C:\\data\\alpha.txt " });
  await api.listDatasourceDocuments({ keyword: "alpha", limit: "25" });
  await api.getDatasourceDocument({ document_id: "101" });
  await api.listDatasourceChunks({ document_id: "101", limit: "2", cursor: 0 });
  await api.updateDatasourceDocument({
    documentId: 101,
    sourcePath: " C:\\data\\alpha-renamed.txt ",
    fileName: " alpha-renamed.txt ",
    extension: " .txt ",
    sizeBytes: "42",
  });
  await api.deleteDatasourceDocument({ id: 101 });

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.DATASOURCE_V2_CREATE, {
    sourcePath: "C:\\data\\alpha.txt",
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.DATASOURCE_V2_LIST, {
    keyword: "alpha",
    limit: 25,
  });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.DATASOURCE_V2_GET, {
    documentId: 101,
  });
  expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS, {
    documentId: 101,
    limit: 2,
    cursor: 0,
  });
  expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.DATASOURCE_V2_UPDATE, {
    documentId: 101,
    sourcePath: "C:\\data\\alpha-renamed.txt",
    fileName: "alpha-renamed.txt",
    extension: ".txt",
    sizeBytes: 42,
  });
  expect(callAPI).toHaveBeenNthCalledWith(6, RPC_METHODS.DATASOURCE_V2_DELETE, {
    documentId: 101,
  });
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.createDatasourceDocument({ sourcePath: "" }),
    "sourcePath is required",
  );
  expectInvalidInputDoesNotCall(callAPI, () => api.listDatasourceDocuments({}), "limit must be a positive integer");
  expectInvalidInputDoesNotCall(callAPI, () => api.getDatasourceDocument({ documentId: 0 }), "documentId is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.listDatasourceChunks({ documentId: 101, limit: 2 }),
    "cursor is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.updateDatasourceDocument({
        documentId: 101,
        sourcePath: "C:\\data\\a.txt",
        sizeBytes: 1,
      }),
    "fileName is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.deleteDatasourceDocument({ documentId: "" }),
    "documentId is required",
  );
  expect(typeof createDatasourceDocument).toBe("function");
  expect(typeof listDatasourceDocuments).toBe("function");
  expect(typeof getDatasourceDocument).toBe("function");
  expect(typeof listDatasourceChunks).toBe("function");
  expect(typeof updateDatasourceDocument).toBe("function");
  expect(typeof deleteDatasourceDocument).toBe("function");
});

it("maps user-selected datasource imports to the local file RPC", async () => {
  const callAPI = vi.fn().mockResolvedValue({
    documentId: 102,
    sourcePath: "D:\\\\new\\\\fj.txt",
    fileName: "fj.txt",
    extension: ".txt",
    sizeBytes: 3,
    contentHash: "import-hash",
    chunkCount: 1,
    totalChars: 3,
    status: "ready",
  });
  const api = createBackendApi({ callAPI });

  await api.importDatasourceLocalFile({
    source_path: " D:\\new\\fj.txt ",
    picker_token: " picker-token ",
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE, {
    sourcePath: "D:\\new\\fj.txt",
    pickerToken: "picker-token",
  });
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.importDatasourceLocalFile({ sourcePath: "" }),
    "sourcePath is required",
  );
  expect(typeof importDatasourceLocalFile).toBe("function");
});

it("wraps app update RPC methods", async () => {
  const callAPI = vi
    .fn()
    .mockResolvedValueOnce({ ok: true })
    .mockResolvedValueOnce({ ok: true })
    .mockResolvedValueOnce({ started: true, helper: "updater" })
    .mockResolvedValueOnce({ started: true, helper: "updater" });
  const api = createBackendApi({ callAPI });

  await api.checkAppUpdate();
  await api.downloadAppUpdate();
  await api.installAppUpdate();
  await api.installLatestAppUpdate();

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.APP_UPDATE_CHECK, {});
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.APP_UPDATE_DOWNLOAD, {});
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.APP_UPDATE_INSTALL, {});
  expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.APP_UPDATE_INSTALL_LATEST, {});
  expect(typeof checkAppUpdate).toBe("function");
  expect(typeof downloadAppUpdate).toBe("function");
  expect(typeof installAppUpdate).toBe("function");
  expect(typeof installLatestAppUpdate).toBe("function");
});

it("rejects malformed app update recovery data with a fixed generic error", async () => {
  const secret = "secret helper output at /Users/alice/update.dmg";
  const failure = new Error(secret);
  failure.data = {
    code: "UPDATE_SIGNATURE_INVALID",
    retryable: false,
    action: "preserve_state_export_diagnostics",
    transaction_id: "",
    raw_output: secret,
  };
  const api = createBackendApi({ callAPI: vi.fn().mockRejectedValue(failure) });

  await expect(api.checkAppUpdate()).rejects.toThrow("请求失败，恢复信息无效。");
});

it("preserves valid app update recovery data for fixed UI mapping", async () => {
  const failure = new Error("secret verifier output");
  failure.data = {
    code: "UPDATE_SIGNATURE_INVALID",
    retryable: false,
    action: "preserve_state_export_diagnostics",
    transaction_id: "",
  };
  const api = createBackendApi({ callAPI: vi.fn().mockRejectedValue(failure) });

  await expect(api.installLatestAppUpdate()).rejects.toBe(failure);
});

it("rejects malformed app update install responses", async () => {
  const invalidResponses = [
    {},
    null,
    { ok: true },
    { started: false, helper: "updater" },
    { started: true, helper: "" },
  ];
  for (const response of invalidResponses) {
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.installAppUpdate()).rejects.toThrow("app/update/install");
    await expect(api.installLatestAppUpdate()).rejects.toThrow("app/update/installLatest");
  }
});

it("wraps MCP server list and default controls with strict empty payloads", async () => {
  const listResponse = {
    configPath: "/repo/.agent/mcp_server/config.json",
    mcpServers: { sqlite: { enabled: false } },
  };
  const startResponse = {
    configPath: "/repo/.agent/mcp_server/config.json",
    serverName: "sqlite",
    enabled: true,
  };
  const stopResponse = {
    configPath: "/repo/.agent/mcp_server/config.json",
    serverName: "sqlite",
    enabled: false,
  };
  const playwrightStartResponse = {
    configPath: "/repo/.agent/mcp_server/config.json",
    serverName: "playwright",
    enabled: true,
  };
  const playwrightStopResponse = {
    configPath: "/repo/.agent/mcp_server/config.json",
    serverName: "playwright",
    enabled: false,
  };
  const callAPI = vi
    .fn()
    .mockResolvedValueOnce(listResponse)
    .mockResolvedValueOnce(startResponse)
    .mockResolvedValueOnce(stopResponse)
    .mockResolvedValueOnce(playwrightStartResponse)
    .mockResolvedValueOnce(playwrightStopResponse);
  const api = createBackendApi({ callAPI });

  await expect(api.listMCPServers()).resolves.toEqual(listResponse);
  await expect(api.startSQLiteMCPServer()).resolves.toEqual(startResponse);
  await expect(api.stopSQLiteMCPServer()).resolves.toEqual(stopResponse);
  await expect(api.startPlaywrightMCPServer()).resolves.toEqual(playwrightStartResponse);
  await expect(api.stopPlaywrightMCPServer()).resolves.toEqual(playwrightStopResponse);

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MCP_SERVER_LIST, {});
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MCP_SERVER_SQLITE_START, {});
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MCP_SERVER_SQLITE_STOP, {});
  expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START, {});
  expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP, {});
  expect(typeof listMCPServers).toBe("function");
  expect(typeof startSQLiteMCPServer).toBe("function");
  expect(typeof stopSQLiteMCPServer).toBe("function");
  expect(typeof startPlaywrightMCPServer).toBe("function");
  expect(typeof stopPlaywrightMCPServer).toBe("function");
});
