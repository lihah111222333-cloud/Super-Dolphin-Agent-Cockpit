import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import {
  dashboardPageResponse,
  runtimeConfigResponse,
  sidebarStateResponse,
} from "./test-support/backendApi.contractResponse.testSupport.js";

it("reuses the same SkillTool DTO contract for skills/tools/list responses", async () => {
  const tool = {
    id: 3,
    cwd: "/repo/app",
    methodName: "backend",
    description: "后端技能",
    enabled: true,
    createdAt: "2026-07-17T10:00:00Z",
    updatedAt: "2026-07-17T10:00:00Z",
  };
  const api = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({ tools: [tool] }),
  });
  await expect(api.listSkillTools({ cwd: "/repo/app", keyword: "", limit: 10 })).resolves.toEqual({ tools: [tool] });

  const malformed = [
    { ...tool, id: 0 },
    { ...tool, cwd: "" },
    { ...tool, methodName: "bad name" },
    { ...tool, description: "" },
    { ...tool, createdAt: "not-a-time" },
    { ...tool, extra: "field" },
  ];
  for (const badTool of malformed) {
    const failingApi = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({ tools: [badTool] }),
    });
    await expect(failingApi.listSkillTools({ cwd: "/repo/app", keyword: "", limit: 10 })).rejects.toThrow(
      /skills\/tools\/list response/,
    );
  }
});

it("fails fast on malformed guarded backend responses before consumers normalize them", async () => {
  const cases = [
    {
      call: (api) => api.readConfig(),
      response: runtimeConfigResponse({
        toolRouting: {
          ...runtimeConfigResponse().toolRouting,
          timeoutSec: "8",
        },
      }),
      message: "config/read response toolRouting.timeoutSec must be an integer",
    },
    {
      call: (api) => api.readBuiltinTools({ cwd: "/repo/app" }),
      response: { tools: [{ id: "Shell", label: "Shell", enabled: "true" }] },
      message: "config/builtinTools/read response tools[0].enabled must be a boolean",
    },
    {
      call: (api) => api.getWindowBootstrap(),
      response: {},
      message: "ui/windowBootstrap/get response snapshot must be an object",
    },
    {
      call: (api) => api.getWindowBootstrap(),
      response: { snapshot: [] },
      message: "ui/windowBootstrap/get response snapshot must be an object",
    },
    {
      call: (api) => api.getSidebarState({ cwd: "/repo/app" }),
      response: sidebarStateResponse({
        token_usage: {
          inputTokens: 0,
          outputTokens: 0,
          totalTokens: 0,
          usedTokens: "0",
        },
      }),
      message: "ui/sidebar/get response token_usage.usedTokens must be an integer",
    },
    {
      call: (api) =>
        api.callBackend(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST, {
          events: [],
        }),
      response: { enabled: true, recorded: "1", dropped: 0 },
      message: "observability/frontend/ingest response recorded must be an integer",
    },
    {
      call: (api) => api.openNewWindow({ cwd: "/repo/app" }),
      response: { ok: "true", windowId: "window-2", cwd: "/repo/app" },
      message: "ui/openNewWindow response ok must be a boolean",
    },
    {
      call: (api) =>
        api.saveCodeFile({
          filePath: "src/App.jsx",
          content: "export default App;",
          project: "/repo/app",
        }),
      response: {
        ok: true,
        filePath: "/repo/app/src/App.jsx",
        relative: "src/App.jsx",
        totalLines: "1",
      },
      message: "ui/code/save response totalLines must be an integer",
    },
    ...[
      (api) => api.getProjects({ cwd: "/repo/app" }),
      (api) => api.setActiveProject({ cwd: "/repo/app", path: "/repo/next" }),
      (api) => api.addProject({ cwd: "/repo/app", path: "/repo/new" }),
      (api) => api.removeProject({ cwd: "/repo/app", path: "/repo/old" }),
    ].map((call) => ({
      call,
      response: { projects: [], active: 7 },
      message: "response active must be a string",
    })),
    {
      call: (api) => api.setPreference({ key: "settings.provider.active", value: "codex" }),
      response: { ok: false },
      message: "ui/preferences/set response ok must be true",
    },
    {
      call: (api) => api.setVideoApiKey({ apiKey: "sk-test-key" }),
      response: { ok: false },
      message: "ui/video/setApiKey response ok must be true",
    },
    {
      call: (api) => api.saveModelProviders({ cwd: "/repo/app", registry: { vendors: [] } }),
      response: { vendors: null },
      message: "modelProviders/save response model provider registry",
    },
    {
      call: (api) => api.getDashboardPage({ cwd: "/repo/app", page: "settings" }),
      response: { ...dashboardPageResponse(), commandCards: null },
      message: "ui/dashboard/get response commandCards must be an array",
    },
    {
      call: (api) => api.getVideoApiKey(),
      response: { configured: "false", masked: "" },
      message: "ui/video/getApiKey response configured must be a boolean",
    },
    {
      call: (api) => api.listDashboardLogs({ limit: 10 }),
      response: { logs: null },
      message: "dashboard/logs response logs must be an array",
    },
    {
      call: (api) => api.getThreadState({ cwd: "/repo/app", threadId: "thread-1" }),
      response: {},
      message: "ui/state/get response missing UI state snapshot fields",
    },
    {
      call: (api) => api.readLspPromptHint({ cwd: "/repo/app" }),
      response: { hint: "effective", overrideHint: "", usingDefault: true },
      message: "config/lspPromptHint/read response defaultHint must be a string",
    },
    {
      call: (api) => api.writeLspPromptHint({ cwd: "/repo/app", hint: "" }),
      response: {
        hint: "effective",
        defaultHint: "default",
        overrideHint: "",
        usingDefault: "true",
      },
      message: "config/lspPromptHint/write response usingDefault must be a boolean",
    },
    {
      call: (api) => api.startThread({ cwd: "/repo/app", modelProvider: "codex" }),
      response: { status: "running" },
      message: "thread/start response missing threadId or thread_id",
    },
    {
      call: (api) => api.getThreadMessages({ threadId: "thread-1" }),
      response: { messages: null },
      message: "thread/messages response messages must be an array",
    },
    {
      call: (api) => api.getThreadMessages({ threadId: "thread-1" }),
      response: { messages: [], total: "1" },
      message: "thread/messages response total must be a non-negative integer",
    },
    {
      call: (api) => api.resolveThreadIdentity({ cwd: "/repo/app", threadId: "thread-1" }),
      response: {},
      message: "thread/resolve response missing id or threadId or thread_id",
    },
    {
      call: (api) =>
        api.startTurn({
          cwd: "/repo/app",
          threadId: "thread-1",
          input: "build it",
        }),
      response: { ok: true },
      message: "turn/start response missing turn_id or turnId",
    },
    {
      call: (api) => api.forceCompleteTurn({ cwd: "/repo/app", threadId: "thread-1" }),
      response: { ok: true },
      message: "turn/forceComplete response forceCompleted must be a boolean",
    },
    {
      call: (api) => api.forceCompleteTurn({ cwd: "/repo/app", threadId: "thread-1" }),
      response: { ok: false, forceCompleted: false },
      message: "turn/forceComplete response failure must include errorCode, error, or message",
    },
    {
      call: (api) => api.forceCompleteTurn({ cwd: "/repo/app", threadId: "thread-1" }),
      response: {
        ok: true,
        forceCompleted: false,
        errorCode: "force_complete_target_not_found",
      },
      message: "turn/forceComplete response ok true cannot have forceCompleted false",
    },
    {
      call: (api) => api.forceCompleteTurn({ cwd: "/repo/app", threadId: "thread-1" }),
      response: {
        ok: false,
        forceCompleted: "false",
        errorCode: "force_complete_target_not_found",
      },
      message: "turn/forceComplete response forceCompleted must be a boolean",
    },
    {
      call: (api) => api.startDag({ dagKey: "dag-1", triggerSource: "manual" }),
      response: { ok: true },
      message: "dashboard/dagStart response missing runKey or run_key",
    },
    {
      call: (api) =>
        api.createAndStartDag({
          dagKey: "dag-created",
          title: "Created DAG",
          nodes: [
            {
              nodeKey: "draft",
              title: "Draft",
              nodeType: "agent",
              dependsOn: [],
            },
          ],
        }),
      response: { dagKey: "dag-created" },
      message: "dashboard/dagCreateAndStart response missing runKey or run_key",
    },
    {
      call: (api) =>
        api.createAndStartDag({
          dagKey: "dag-created",
          title: "Created DAG",
          nodes: [
            {
              nodeKey: "draft",
              title: "Draft",
              nodeType: "agent",
              dependsOn: [],
            },
          ],
        }),
      response: { runKey: "run-created" },
      message: "dashboard/dagCreateAndStart response missing dagKey or dag_key",
    },
    {
      call: (api) =>
        api.readSkill({
          cwd: "/repo/app",
          path: ".agents/skills/demo/SKILL.md",
        }),
      response: {},
      message: "skills/local/read response skill must be an object",
    },
    {
      call: (api) =>
        api.readSkill({
          cwd: "/repo/app",
          path: ".agents/skills/demo/SKILL.md",
        }),
      response: { skill: [] },
      message: "skills/local/read response skill must be an object",
    },
    {
      call: (api) =>
        api.readSkill({
          cwd: "/repo/app",
          path: ".agents/skills/demo/SKILL.md",
        }),
      response: { skill: { content: "# Demo" } },
      message: "skills/local/read response missing path",
    },
    {
      call: (api) =>
        api.readSkill({
          cwd: "/repo/app",
          path: ".agents/skills/demo/SKILL.md",
        }),
      response: { skill: { path: ".agents/skills/demo/SKILL.md" } },
      message: "skills/local/read response skill.content must be a string",
    },
    {
      call: (api) =>
        api.readSkill({
          cwd: "/repo/app",
          path: ".agents/skills/demo/SKILL.md",
        }),
      response: {
        skill: { path: ".agents/skills/demo/SKILL.md", content: null },
      },
      message: "skills/local/read response skill.content must be a string",
    },
  ];

  for (const item of cases) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    const api = createBackendApi({ callAPI });
    await expect(item.call(api)).rejects.toThrow(item.message);
    expect(callAPI).toHaveBeenCalledTimes(1);
  }
});
