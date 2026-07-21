import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import { guardedOpsConfigResponse } from "./test-support/backendApi.opsConfig.responses.js";

it("wraps settings config RPCs with the internal uistate method names", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsConfigResponse(method)));
  const api = createBackendApi({ callAPI });

  await api.readLspPromptHint({ cwd: "/repo/app" });
  await api.writeLspPromptHint({ cwd: "/repo/app", hint: "custom prompt" });
  await api.readBuiltinTools({ cwd: "/repo/app" });
  await api.writeBuiltinTool({ cwd: "/repo/app", id: "Shell", enabled: false });
  await api.listDashboardLogs({ limit: 14 });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ, { cwd: "/repo/app" });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE, {
    cwd: "/repo/app",
    hint: "custom prompt",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, {
    cwd: "/repo/app",
    id: "Shell",
    enabled: false,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_LOGS, {
    limit: 14,
  });
  expect(() => api.readLspPromptHint({ cwd: "" })).toThrow("cwd is required");
  expect(() => api.writeLspPromptHint({ cwd: "/repo/app" })).toThrow("hint is required");
  expect(() => api.writeBuiltinTool({ cwd: "/repo/app", id: "", enabled: true })).toThrow("id is required");
  expect(() => api.writeBuiltinTool({ cwd: "/repo/app", id: "Shell", enabled: "false" })).toThrow(
    "enabled must be boolean",
  );
  expect(() => api.listDashboardLogs({ limit: 0 })).toThrow("limit must be a positive integer");
});

it("wraps config, project, preference, and dashboard page RPCs with stable payloads", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsConfigResponse(method)));
  const api = createBackendApi({ callAPI });

  await api.readConfig();
  await api.getWindowBootstrap();
  await api.getSidebarState({ cwd: "/repo/app" });
  await api.getThreadState({ cwd: "/repo/app", threadId: "thread-1" });
  await api.getProjects({ cwd: "/repo/app" });
  await api.setActiveProject({ cwd: "/repo/app", path: "/repo/next" });
  await api.addProject({ cwd: "/repo/app", path: "/repo/new" });
  await api.removeProject({ cwd: "/repo/app", path: "/repo/old" });
  await api.getPreference({ key: "settings.provider.active" });
  await api.getAllPreferences({});
  await api.setPreference({ key: "settings.provider.active", value: "codex" });
  await api.getDashboardPage({ cwd: "/repo/app", page: "settings" });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_READ, {});
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET, {});
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SIDEBAR_GET, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_STATE_GET, {
    cwd: "/repo/app",
    threadId: "thread-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_GET, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, {
    cwd: "/repo/app",
    path: "/repo/next",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_ADD, {
    cwd: "/repo/app",
    path: "/repo/new",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_REMOVE, {
    cwd: "/repo/app",
    path: "/repo/old",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PREFERENCES_GET, {
    key: "settings.provider.active",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PREFERENCES_GET_ALL, {});
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PREFERENCES_SET, {
    key: "settings.provider.active",
    value: "codex",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_DASHBOARD_GET, {
    cwd: "/repo/app",
    page: "settings",
  });

  expect(() => api.getSidebarState({ cwd: "" })).toThrow("cwd is required");
  expect(() => api.getThreadState({ cwd: "/repo/app", threadId: "" })).toThrow("threadId is required");
  expect(() => api.setActiveProject({ cwd: "/repo/app", path: "" })).toThrow("path is required");
  expect(() => api.setPreference({ key: "", value: "codex" })).toThrow("key is required");
  expect(() => api.setPreference({ key: "settings.provider.active" })).toThrow("value is required");
  expect(() => api.getDashboardPage({ cwd: "/repo/app", page: "" })).toThrow("page is required");
});

it("rejects unknown full UI state fields through the backend facade", async () => {
  const api = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({
      threads: [],
      agents: [],
      token_usage: {},
      surprise: true,
    }),
  });

  await expect(api.getThreadState({ cwd: "/repo/app", threadId: "thread-1" })).rejects.toThrow(
    "ui/state/get response body must not include surprise",
  );
});

it("exposes model provider management RPC facade methods", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsConfigResponse(method)));
  const api = createBackendApi({ callAPI });
  const registry = {
    vendors: [
      {
        id: "openrouter",
        label: "OpenRouter",
        enabled: true,
        baseURL: "https://openrouter.ai/api/v1",
        envKey: "OPENROUTER_API_KEY",
        codexModelProvider: "openrouter",
        defaultModel: "openai/gpt-4.1",
      },
    ],
  };

  await api.listModelProviders({ cwd: "/repo/app" });
  await api.saveModelProviders({ cwd: "/repo/app", registry });
  await api.applyModelProvider({ cwd: "/repo/app", vendorId: "openrouter" });

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MODEL_PROVIDERS_LIST, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MODEL_PROVIDERS_SAVE, {
    cwd: "/repo/app",
    registry,
  });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MODEL_PROVIDERS_APPLY, {
    cwd: "/repo/app",
    vendorId: "openrouter",
  });
});
