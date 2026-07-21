import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import {
  builtinToolsResponse,
  codeSaveResponse,
  dashboardLogsResponse,
  dashboardPageResponse,
  frontendIngestResponse,
  modelProviderRegistryResponse,
  okResponse,
  openWindowResponse,
  projectsStateResponse,
  runtimeConfigResponse,
  sidebarStateResponse,
  videoApiKeyStatusResponse,
  windowBootstrapResponse,
} from "./test-support/backendApi.contractResponse.testSupport.js";

it("rejects unknown fields across all newly guarded public response facades", async () => {
  const codeSaveCall = (api) =>
    api.saveCodeFile({
      filePath: "src/App.jsx",
      content: "export default App;",
      project: "/repo/app",
    });
  const cases = [
    {
      call: (api) => api.readConfig(),
      response: runtimeConfigResponse({ surprise: true }),
    },
    {
      call: (api) => api.readBuiltinTools({ cwd: "/repo/app" }),
      response: { ...builtinToolsResponse(), surprise: true },
    },
    {
      call: (api) => api.writeBuiltinTool({ cwd: "/repo/app", id: "Shell", enabled: false }),
      response: { ...builtinToolsResponse(), surprise: true },
    },
    {
      call: (api) => api.getWindowBootstrap(),
      response: { ...windowBootstrapResponse(), surprise: true },
    },
    {
      call: (api) => api.getSidebarState({ cwd: "/repo/app" }),
      response: sidebarStateResponse({ surprise: true }),
    },
    {
      call: (api) =>
        api.callBackend(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST, {
          events: [],
        }),
      response: { ...frontendIngestResponse(), surprise: true },
    },
    {
      call: (api) => api.openNewWindow({ cwd: "/repo/app" }),
      response: { ...openWindowResponse(), surprise: true },
    },
    { call: codeSaveCall, response: { ...codeSaveResponse(), surprise: true } },
    {
      call: (api) => api.getProjects({ cwd: "/repo/app" }),
      response: { ...projectsStateResponse(), surprise: true },
    },
    {
      call: (api) => api.setActiveProject({ cwd: "/repo/app", path: "/repo/next" }),
      response: { ...projectsStateResponse(), surprise: true },
    },
    {
      call: (api) => api.addProject({ cwd: "/repo/app", path: "/repo/new" }),
      response: { ...projectsStateResponse(), surprise: true },
    },
    {
      call: (api) => api.removeProject({ cwd: "/repo/app", path: "/repo/old" }),
      response: { ...projectsStateResponse(), surprise: true },
    },
    {
      call: (api) => api.setPreference({ key: "settings.provider.active", value: "codex" }),
      response: { ...okResponse(), surprise: true },
    },
    {
      call: (api) => api.setVideoApiKey({ apiKey: "sk-test-key" }),
      response: { ...okResponse(), surprise: true },
    },
    {
      call: (api) => api.saveModelProviders({ cwd: "/repo/app", registry: { vendors: [] } }),
      response: { ...modelProviderRegistryResponse(), surprise: true },
    },
    {
      call: (api) => api.getDashboardPage({ cwd: "/repo/app", page: "settings" }),
      response: { ...dashboardPageResponse(), surprise: true },
    },
    {
      call: (api) => api.getVideoApiKey(),
      response: { ...videoApiKeyStatusResponse(), surprise: true },
    },
    {
      call: (api) => api.listDashboardLogs({ limit: 10 }),
      response: { ...dashboardLogsResponse(), surprise: true },
    },
  ];

  for (const item of cases) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    const api = createBackendApi({ callAPI });
    await expect(item.call(api)).rejects.toThrow("must not include surprise");
    expect(callAPI).toHaveBeenCalledTimes(1);
  }
});

it("rejects unknown fields in nested guarded response DTOs", async () => {
  const vendor = {
    id: "openrouter",
    label: "OpenRouter",
    enabled: true,
    baseURL: "https://openrouter.ai/api/v1",
    envKey: "OPENROUTER_API_KEY",
    codexModelProvider: "openrouter",
    defaultModel: "openai/gpt-5.5",
  };
  const cases = [
    {
      call: (api) => api.writeBuiltinTool({ cwd: "/repo/app", id: "Shell", enabled: false }),
      response: {
        tools: [{ ...builtinToolsResponse().tools[0], surprise: true }],
      },
    },
    {
      call: (api) => api.saveModelProviders({ cwd: "/repo/app", registry: { vendors: [] } }),
      response: { vendors: [{ ...vendor, surprise: true }] },
    },
    {
      call: (api) => api.saveModelProviders({ cwd: "/repo/app", registry: { vendors: [] } }),
      response: {
        vendors: [{ ...vendor, budget: { dailyUsd: 1, surprise: true } }],
      },
    },
    {
      call: (api) => api.saveModelProviders({ cwd: "/repo/app", registry: { vendors: [] } }),
      response: {
        vendors: [{ ...vendor, tokenPool: { priority: 1, surprise: true } }],
      },
    },
    {
      call: (api) => api.getDashboardPage({ cwd: "/repo/app", page: "settings" }),
      response: {
        ...dashboardPageResponse(),
        sharedFileRetention: {
          ...dashboardPageResponse().sharedFileRetention,
          surprise: true,
        },
      },
    },
    {
      call: (api) => api.listDashboardLogs({ limit: 10 }),
      response: {
        logs: [
          {
            source: "app",
            id: 1,
            timestamp: "2026-07-13T00:00:00Z",
            surprise: true,
          },
        ],
      },
    },
  ];

  for (const item of cases) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(item.response),
    });
    await expect(item.call(api)).rejects.toThrow("must not include surprise");
  }
});

it("rejects malformed runtime config fields and nested tool routing", async () => {
  const { sandbox: _sandbox, ...missingSandbox } = runtimeConfigResponse();
  const cases = [
    {
      response: missingSandbox,
      message: "config/read response sandbox is required",
    },
    {
      response: runtimeConfigResponse({ toolRouting: [] }),
      message: "config/read response toolRouting must be an object",
    },
    {
      response: runtimeConfigResponse({
        toolRouting: {
          ...runtimeConfigResponse().toolRouting,
          routerHasAPIKey: "false",
        },
      }),
      message: "config/read response toolRouting.routerHasAPIKey must be a boolean",
    },
    {
      response: runtimeConfigResponse({
        toolRouting: {
          ...runtimeConfigResponse().toolRouting,
          confidenceThreshold: "0.65",
        },
      }),
      message: "config/read response toolRouting.confidenceThreshold must be a finite number",
    },
    {
      response: runtimeConfigResponse({
        toolRouting: { ...runtimeConfigResponse().toolRouting, surprise: true },
      }),
      message: "config/read response toolRouting must not include surprise",
    },
  ];

  for (const item of cases) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(item.response),
    });
    await expect(api.readConfig()).rejects.toThrow(item.message);
  }
});

it("rejects malformed sidebar required, optional, and nested DTO fields", async () => {
  const call = (api) => api.getSidebarState({ cwd: "/repo/app" });
  const base = sidebarStateResponse();
  const cases = [
    {
      response: sidebarStateResponse({ threads: {} }),
      message: "threads must be an array",
    },
    {
      response: sidebarStateResponse({ workspace: [] }),
      message: "workspace must be an object",
    },
    {
      response: sidebarStateResponse({
        token_usage: { ...base.token_usage, totalTokens: "3" },
      }),
      message: "token_usage.totalTokens must be an integer",
    },
    {
      response: sidebarStateResponse({
        token_usage: { ...base.token_usage, contextWindowTokens: "128000" },
      }),
      message: "token_usage.contextWindowTokens must be an integer",
    },
    {
      response: sidebarStateResponse({
        token_usage: { ...base.token_usage, usedPercent: "0.01" },
      }),
      message: "token_usage.usedPercent must be a finite number",
    },
    {
      response: sidebarStateResponse({ statuses: { "thread-1": false } }),
      message: "statuses.thread-1 must be a string",
    },
    {
      response: sidebarStateResponse({
        interruptibleByThread: { "thread-1": "true" },
      }),
      message: "interruptibleByThread.thread-1 must be a boolean",
    },
    {
      response: sidebarStateResponse({ statusHeadersByThread: [] }),
      message: "statusHeadersByThread must be an object",
    },
    {
      response: sidebarStateResponse({
        statusDetailsByThread: { "thread-1": 7 },
      }),
      message: "statusDetailsByThread.thread-1 must be a string",
    },
    {
      response: sidebarStateResponse({ agentRuntimeById: { "agent-1": [] } }),
      message: "agentRuntimeById.agent-1 must be an object",
    },
    {
      response: sidebarStateResponse({
        activityStatsByThread: {
          "thread-1": { lspCalls: "1", commands: 0, fileEdits: 0 },
        },
      }),
      message: "activityStatsByThread.thread-1.lspCalls must be an integer",
    },
    {
      response: sidebarStateResponse({
        activityStatsByThread: {
          "thread-1": {
            lspCalls: 0,
            commands: 0,
            fileEdits: 0,
            toolCalls: { read: "1" },
          },
        },
      }),
      message: "activityStatsByThread.thread-1.toolCalls.read must be an integer",
    },
    {
      response: sidebarStateResponse({ activeThreadId: 1 }),
      message: "activeThreadId must be a string",
    },
    {
      response: sidebarStateResponse({ "viewPrefs.chat": [] }),
      message: "viewPrefs.chat must be an object",
    },
    {
      response: sidebarStateResponse({
        "threadPins.chat": { "thread-1": 1.5 },
      }),
      message: "threadPins.chat values must be integers",
    },
    {
      response: sidebarStateResponse({
        groups: [{ key: "active", title: "Active", threads: [], surprise: true }],
      }),
      message: "groups[0] must not include surprise",
    },
    {
      response: sidebarStateResponse({
        threads: [{ id: "thread-1", surprise: true }],
      }),
      message: "threads[0] must not include surprise",
    },
    {
      response: sidebarStateResponse({ agents: [{ id: 1 }] }),
      message: "agents[0].id must be a string",
    },
    {
      response: sidebarStateResponse({
        active_turn: { ...base.active_turn, success: "true" },
      }),
      message: "active_turn.success must be a boolean",
    },
    {
      response: sidebarStateResponse({
        recent_turns: [{ ...base.active_turn, surprise: true }],
      }),
      message: "recent_turns[0] must not include surprise",
    },
    {
      response: sidebarStateResponse({
        workspace: { runs: [{ run_key: "run-1", merged_file_count: "1" }] },
      }),
      message: "workspace.runs[0].merged_file_count must be an integer",
    },
  ];

  for (const item of cases) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    const api = createBackendApi({ callAPI });
    await expect(call(api)).rejects.toThrow(item.message);
  }
});

it("fails fast when the sidebar facade receives empty or malformed success bodies", async () => {
  const missingWorkspaceWithMalformedThread = sidebarStateResponse({
    threads: [{ id: 7 }],
  });
  delete missingWorkspaceWithMalformedThread.workspace;
  const missingRecentTurns = sidebarStateResponse();
  delete missingRecentTurns.recent_turns;
  const cases = [
    { response: {}, message: "ui/sidebar/get response threads is required" },
    {
      response: { statuses: { "thread-1": "running" } },
      message: "ui/sidebar/get response threads is required",
    },
    {
      response: missingWorkspaceWithMalformedThread,
      message: "ui/sidebar/get response workspace is required",
    },
    {
      response: missingRecentTurns,
      message: "ui/sidebar/get response recent_turns is required",
    },
  ];

  for (const item of cases) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(item.response),
    });
    await expect(api.getSidebarState({ cwd: "/repo/app" })).rejects.toThrow(item.message);
  }
});

it("rejects null sidebar list fields instead of accepting Go nil slice drift", async () => {
  // nil slice 漂移必须在 Go producer/clone 层修复（输出 []），前端契约保持严格：
  // 字段存在时必须是数组，null 一律拒绝。
  const call = (api) => api.getSidebarState({ cwd: "/repo/app" });
  const cases = [
    sidebarStateResponse({ agents: null }),
    sidebarStateResponse({ threads: null }),
    sidebarStateResponse({ recent_turns: null }),
    sidebarStateResponse({ agents: {} }),
    sidebarStateResponse({ agents: "agents" }),
    sidebarStateResponse({ threads: 3 }),
  ];

  for (const response of cases) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(response),
    });
    await expect(call(api)).rejects.toThrow(/must be an array/);
  }
});

it("accepts empty arrays for sidebar list fields", async () => {
  const response = sidebarStateResponse({
    agents: [],
    threads: [],
    recent_turns: [],
  });
  const api = createBackendApi({
    callAPI: vi.fn().mockResolvedValue(response),
  });
  await expect(api.getSidebarState({ cwd: "/repo/app" })).resolves.toEqual(response);
});

it("rejects unsuccessful or malformed code save response fields", async () => {
  const call = (api) =>
    api.saveCodeFile({
      filePath: "src/App.jsx",
      content: "export default App;",
      project: "/repo/app",
    });
  const cases = [
    {
      response: { ...codeSaveResponse(), ok: false },
      message: "ui/code/save response ok must be true",
    },
    {
      response: { ...codeSaveResponse(), ok: "true" },
      message: "ui/code/save response ok must be a boolean",
    },
    {
      response: { ...codeSaveResponse(), filePath: "" },
      message: "ui/code/save response filePath must be a non-empty string",
    },
    {
      response: { ...codeSaveResponse(), filePath: 7 },
      message: "ui/code/save response filePath must be a non-empty string",
    },
    {
      response: { ...codeSaveResponse(), relative: "  " },
      message: "ui/code/save response relative must be a non-empty string",
    },
  ];

  for (const item of cases) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(item.response),
    });
    await expect(call(api)).rejects.toThrow(item.message);
  }
});

it("accepts a null window bootstrap snapshot after the desktop host consumed it", async () => {
  // 一次性快照被桌面宿主消费后返回 { snapshot: null }，必须放行给 normalize 层回退，
  // 否则浏览器直开/刷新会让 bootstrap 永久失败、控件全部禁用。
  const api = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({ snapshot: null }),
  });
  await expect(api.getWindowBootstrap()).resolves.toEqual({ snapshot: null });
});

it("validates skills/tools/create responses at the facade boundary", async () => {
  const created = {
    id: 9,
    cwd: "/repo/app",
    methodName: "deploy_frontend",
    description: "部署前端到本地预览",
    enabled: true,
    createdAt: "2026-07-17T10:00:00+08:00",
    updatedAt: "2026-07-17T10:00:00+08:00",
  };
  const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(created) });
  await expect(
    api.createSkillTool({
      cwd: "/repo/app",
      methodName: "deploy_frontend",
      description: "部署前端到本地预览",
      enabled: true,
    }),
  ).resolves.toEqual(created);

  const malformed = [
    { ...created, id: 0 },
    { ...created, id: -3 },
    { ...created, id: "9" },
    { ...created, cwd: "" },
    { ...created, cwd: "   " },
    { ...created, methodName: "" },
    { ...created, methodName: "bad name" },
    { ...created, methodName: "bad/name" },
    { ...created, description: "" },
    { ...created, enabled: "true" },
    { ...created, createdAt: "not-a-time" },
    { ...created, createdAt: "" },
    { ...created, updatedAt: "2026-13-99" },
    { ...created, surprise: true },
  ];
  for (const response of malformed) {
    const failingApi = createBackendApi({
      callAPI: vi.fn().mockResolvedValue(response),
    });
    await expect(
      failingApi.createSkillTool({
        cwd: "/repo/app",
        methodName: "deploy_frontend",
        description: "部署前端到本地预览",
        enabled: true,
      }),
    ).rejects.toThrow(/skills\/tools\/create response/);
  }
});
