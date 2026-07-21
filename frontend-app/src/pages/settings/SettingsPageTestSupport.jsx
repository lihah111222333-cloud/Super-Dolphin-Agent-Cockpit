import React from "react";
import { cleanup, render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, vi } from "vitest";
import { SettingsPage } from "./SettingsPage.jsx";

const backend = vi.hoisted(() => ({
  applyModelProvider: vi.fn(),
  callBackend: vi.fn(),
  checkAppUpdate: vi.fn(),
  copyTextToClipboard: vi.fn(),
  downloadAppUpdate: vi.fn(),
  getBuildInfo: vi.fn(),
  getPreference: vi.fn(),
  getVideoApiKey: vi.fn(),
  installAppUpdate: vi.fn(),
  installLatestAppUpdate: vi.fn(),
  listDashboardLogs: vi.fn(),
  listModelProviders: vi.fn(),
  readBuiltinTools: vi.fn(),
  readConfig: vi.fn(),
  readLspPromptHint: vi.fn(),
  setPreference: vi.fn(),
  setVideoApiKey: vi.fn(),
  saveModelProviders: vi.fn(),
  writeBuiltinTool: vi.fn(),
  writeLspPromptHint: vi.fn(),
}));

const clientStore = vi.hoisted(() => ({
  value: {
    activeProject: "/repo/app",
    cwd: "/repo/app",
    logEntries: [],
    logLevel: "info",
    setLogLevel: vi.fn(),
  },
  hook: vi.fn(),
  listeners: new Set(),
  setValue(nextValue) {
    this.value = { ...this.value, ...nextValue };
    this.listeners.forEach((listener) => listener());
  },
  subscribe(listener) {
    clientStore.listeners.add(listener);
    return () => clientStore.listeners.delete(listener);
  },
}));

vi.mock("../../shared/api/backendApi.js", () => backend);

vi.mock("../../entities/client/model/useClientStore.js", async () => {
  const ReactModule = await import("react");
  clientStore.hook.mockImplementation((selector) =>
    ReactModule.useSyncExternalStore(
      (notify) => {
        let selected = selector(clientStore.value);
        return clientStore.subscribe(() => {
          const nextSelected = selector(clientStore.value);
          if (nextSelected === selected) return;
          selected = nextSelected;
          notify();
        });
      },
      () => selector(clientStore.value),
    ),
  );
  return { useClientStore: clientStore.hook };
});

function preferenceFixture(overrides = {}) {
  return {
    "settings.provider.active": "codex",
    "settings.provider.codex.codexHome": "/Users/test/.codex",
    "settings.provider.codex.codexInstanceKey": "desktop-main",
    "settings.provider.codex.codexModelProvider": "openai",
    "settings.provider.codex.model": null,
    "settings.provider.codex.effort": null,
    "settings.provider.codex.personality": "pragmatic",
    "settings.provider.codex.sandbox": { type: "readOnly" },
    "settings.provider.codex.summary": "detailed",
    "settings.provider.codex.approvalPolicy": "on-request",
    stallThresholdSec: 60,
    "contextUsageAlerts.thresholds": [70, 85, 95],
    ...overrides,
  };
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function settingsPageView(queryClient, projectPath, pageProps = {}) {
  return (
    <QueryClientProvider client={queryClient}>
      <SettingsPage projectPath={projectPath} {...pageProps} />
    </QueryClientProvider>
  );
}

function renderSettingsPage(projectPath = "/repo/app", pageProps = {}) {
  const queryClient = createTestQueryClient();
  const result = render(settingsPageView(queryClient, projectPath, pageProps));
  return {
    queryClient,
    ...result,
    rerenderSettingsPage: (nextProjectPath) =>
      result.rerender(
        settingsPageView(queryClient, nextProjectPath, pageProps),
      ),
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function resetSettingsPageTestState() {
  Object.values(backend).forEach((mock) => mock.mockReset());
  clientStore.listeners.clear();
  vi.clearAllMocks();
  clientStore.value = {
    activeProject: "/repo/app",
    cwd: "/repo/app",
    logEntries: [],
    logLevel: "info",
    setLogLevel: vi.fn(),
  };
  const preferences = preferenceFixture();
  backend.getBuildInfo.mockResolvedValue({
    version: "v1.2.3",
    runtime: "darwin/arm64",
    buildTime: "2026-06-03T08:00:00Z",
    commit: "abc123",
  });
  backend.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(preferences[key] ?? null),
  );
  backend.callBackend.mockResolvedValue({});
  backend.getVideoApiKey.mockResolvedValue({ configured: false, masked: "" });
  backend.setVideoApiKey.mockResolvedValue({ ok: true });
  backend.checkAppUpdate.mockResolvedValue({ available: false });
  backend.downloadAppUpdate.mockResolvedValue({ ok: true });
  backend.installAppUpdate.mockResolvedValue({ ok: true });
  backend.installLatestAppUpdate.mockResolvedValue({ ok: true });
  backend.listModelProviders.mockResolvedValue({
    activeVendorId: "",
    vendors: [
      {
        id: "openrouter",
        label: "OpenRouter",
        enabled: true,
        baseURL: "https://openrouter.ai/api/v1",
        envKey: "OPENROUTER_API_KEY",
        codexModelProvider: "openrouter",
        defaultModel: "openai/gpt-4.1",
        configured: true,
        maskedEnv: "********",
        envStatus: "configured",
        budget: { dailyUsd: 5, monthlyUsd: 100 },
        tokenPool: { priority: 10, fallbackVendorId: "deepseek" },
      },
      {
        id: "deepseek",
        label: "DeepSeek",
        enabled: false,
        baseURL: "https://api.deepseek.com/v1",
        envKey: "DEEPSEEK_API_KEY",
        codexModelProvider: "deepseek",
        defaultModel: "deepseek-chat",
        configured: false,
        maskedEnv: "",
        envStatus: "missing",
        budget: {},
        tokenPool: { priority: 20, fallbackVendorId: "qwen" },
      },
      {
        id: "qwen",
        label: "Qwen",
        enabled: false,
        baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
        envKey: "QWEN_API_KEY",
        codexModelProvider: "qwen",
        defaultModel: "qwen-plus",
        configured: false,
        maskedEnv: "",
        envStatus: "missing",
        budget: {},
        tokenPool: { priority: 30 },
      },
    ],
  });
  backend.saveModelProviders.mockResolvedValue({ ok: true });
  backend.applyModelProvider.mockResolvedValue({
    activeVendorId: "openrouter",
    vendors: [
      {
        id: "openrouter",
        label: "OpenRouter",
        enabled: true,
        baseURL: "https://openrouter.ai/api/v1",
        envKey: "OPENROUTER_API_KEY",
        codexModelProvider: "openrouter",
        defaultModel: "openai/gpt-4.1",
        configured: true,
        maskedEnv: "********",
        envStatus: "configured",
        budget: { dailyUsd: 5, monthlyUsd: 100 },
        tokenPool: { priority: 10, fallbackVendorId: "deepseek" },
      },
    ],
  });
  backend.setPreference.mockResolvedValue({ ok: true });
  backend.readConfig.mockResolvedValue({ cwd: "/repo/app" });
  backend.readLspPromptHint.mockResolvedValue({
    hint: "effective prompt",
    defaultHint: "default prompt",
    overrideHint: "",
    usingDefault: true,
  });
  backend.writeLspPromptHint.mockResolvedValue({
    hint: "saved prompt",
    defaultHint: "default prompt",
    overrideHint: "saved prompt",
    usingDefault: false,
  });
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.readBuiltinTools.mockResolvedValue({ tools: [] });
  backend.writeBuiltinTool.mockImplementation(({ id, enabled }) =>
    Promise.resolve({
      tools: [
        {
          id,
          label: "读文件",
          description: "读取文件",
          enabled,
          provider: "claude",
          filterMode: "hard",
          enforcement: enabled ? "" : "native-hard",
        },
      ],
    }),
  );
  backend.listDashboardLogs.mockResolvedValue({ logs: [] });
}

function installSettingsPageTestHooks() {
  beforeEach(resetSettingsPageTestState);

  afterEach(() => {
    cleanup();
  });
}

export {
  backend,
  clientStore,
  deferred,
  installSettingsPageTestHooks,
  preferenceFixture,
  renderSettingsPage,
};
