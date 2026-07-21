import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, vi } from "vitest";
import { optionalDateFromValue } from "../pages/shared/pageShared.js";
import { requiredAppStoragePort } from "../shared/api/browser/browserStorage.js";

const DEFAULT_SIDEBAR_STATE = {
  activeThreadId: "thread-1",
  threads: [
    {
      id: "thread-1",
      name: "后端线程",
      provider: "codex",
      status: "工作中",
    },
  ],
  active_turn: { id: "turn-1", thread_id: "thread-1", status: "running" },
  tokenUsageByThread: {
    "thread-1": {
      usedTokens: 128,
      contextWindowTokens: 1024,
      usedPercent: 12.5,
    },
  },
  activityStatsByThread: {
    "thread-1": {
      lspCalls: 3,
      commands: 4,
      fileEdits: 2,
      toolCalls: { mcp__lsp__patch_edit: 3, json_render: 1, shell: 2 },
    },
  },
};

function dispatchPointer(target, type, clientX = 0, options = {}) {
  const defaultButtons = type === "pointerup" ? 0 : 1;
  act(() => {
    target.dispatchEvent(
      new MouseEvent(type, {
        bubbles: true,
        clientX,
        buttons: options.buttons ?? defaultButtons,
      }),
    );
  });
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

function formatParsedTimestampForTest(value) {
  const parsed = optionalDateFromValue(value, "test timestamp");
  if (!parsed) throw new Error("test timestamp is required");
  const year = String(parsed.getFullYear()).padStart(4, "0");
  const month = String(parsed.getMonth() + 1).padStart(2, "0");
  const day = String(parsed.getDate()).padStart(2, "0");
  const hour = String(parsed.getHours()).padStart(2, "0");
  const minute = String(parsed.getMinutes()).padStart(2, "0");
  const second = String(parsed.getSeconds()).padStart(2, "0");
  return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
}

function promptPreferenceValue(key, activePromptKey = "") {
  return (
    {
      "settings.provider.active": "codex",
      "settings.provider.codex.model": "gpt-5.5",
      "settings.provider.codex.effort": "xhigh",
      "settings.provider.codex.codexHome": "~/.codex",
      "settings.provider.codex.codexInstanceKey": "default",
      "settings.provider.codex.codexModelProvider": "openai",
      "settings.provider.claude.model": "sonnet",
      "settings.provider.claude.effort": "high",
      "settings.activePromptKey": activePromptKey,
    }[key] ?? null
  );
}

function decodedSvgDataUrl(image) {
  const src = image.getAttribute("src");
  if (!src) throw new Error("SVG image src is required");
  const prefix = "data:image/svg+xml;charset=utf-8,";
  expect(src.startsWith(prefix)).toBe(true);
  return decodeURIComponent(src.slice(prefix.length));
}

async function waitForBackendThreadHeading() {
  const chatPage = await screen.findByTestId("chat-page");
  return within(chatPage).findByRole("heading", { name: "后端线程" });
}

function createAppPreferenceDefaults() {
  return Object.freeze({
    "settings.provider.active": "codex",
    "settings.provider.codex.model": "gpt-5.5",
    "settings.provider.codex.effort": "xhigh",
    "settings.provider.codex.codexHome": "~/.codex",
    "settings.provider.codex.codexInstanceKey": "default",
    "settings.provider.codex.codexModelProvider": "openai",
    "settings.provider.claude.model": "sonnet",
    "settings.provider.claude.effort": "high",
  });
}

function mockPromptPreferences(backend, activePromptKey = "") {
  backend.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(promptPreferenceValue(key, activePromptKey)),
  );
}

function mockShortcutPreferenceLoad(
  backend,
  appPreferenceDefaults,
  loadShortcutPreference,
) {
  backend.getPreference.mockImplementation(({ key }) => {
    if (key === "settings.shortcuts.bindings") return loadShortcutPreference();
    return Promise.resolve(appPreferenceDefaults[key] ?? null);
  });
}

function openPluginsAndSkillsPage() {
  fireEvent.click(screen.getByLabelText("插件与技能"));
}

function getSidebarNavButton(name) {
  return within(screen.getByTestId("sidebar-nav")).getByRole("button", {
    name,
  });
}

function getBackendThreadText() {
  return screen.getAllByText("后端线程")[0];
}

function getThreadCardByName(name) {
  const card = screen
    .getAllByText(name)
    .map((node) => node.closest(".thread-card"))
    .find(Boolean);
  if (!card) throw new Error(`Thread card not found: ${name}`);
  return card;
}

function clickThreadCardByName(name) {
  const button = getThreadCardByName(name).querySelector(".thread-main");
  if (!button) throw new Error(`Thread card button not found: ${name}`);
  fireEvent.click(button);
}

function queryThreadCardByName(name) {
  return (
    screen
      .queryAllByText(name)
      .map((node) => node.closest(".thread-card"))
      .find(Boolean) ?? null
  );
}

function findThreadCardByName(name) {
  return waitFor(() => getThreadCardByName(name));
}

function defaultSkillFixtures() {
  return [
    {
      name: "backend",
      display_name: "后端",
      dir: "/repo/app/.agents/skills/backend",
      description: "当你需要 Go 后端开发时使用。",
      summary: "Go 后端开发指南",
      trigger_words: ["Go", "backend", "service"],
      force_words: ["sqlc"],
      scope: "project",
    },
    {
      name: "personal-review",
      dir: "/Users/test/.super-dolphin/skills/personal/user/personal-review",
      description: "当你需要私人代码审查偏好时使用。",
      trigger_words: ["review"],
      scope: "personal",
      personal_type: "user",
    },
  ];
}

function resetConnectedShellTestState(ctx, backend, resetClientStoreForTests) {
  Object.values(backend).forEach((mock) => {
    if (vi.isMockFunction(mock)) mock.mockReset();
  });
  ctx.bridgeCallback = null;
  backend.__bridgeCallback = null;
  backend.onFilesDropped.mockImplementation(() => () => {});
  backend.onRuntimeReconnect.mockImplementation(() => () => {});
  backend.onBridgeEvent.mockImplementation((callback) => {
    ctx.bridgeCallback = callback;
    backend.__bridgeCallback = callback;
    return () => {
      if (ctx.bridgeCallback === callback) ctx.bridgeCallback = null;
      if (backend.__bridgeCallback === callback)
        backend.__bridgeCallback = null;
    };
  });
  resetClientStoreForTests();
  const storage = requiredAppStoragePort("app shell test storage");
  storage.clear();
  window.history.replaceState({}, "", "/");
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1024,
  });
  Object.defineProperty(window, "innerHeight", {
    configurable: true,
    value: 768,
  });
}

function installAppOverlayHost(ctx) {
  document.querySelectorAll("#overlay-root").forEach((node) => node.remove());
  ctx.appOverlayHost = document.createElement("div");
  ctx.appOverlayHost.id = "overlay-root";
  document.body.append(ctx.appOverlayHost);
}

function createShellLayoutStorage(initialValue = null) {
  let storedValue = initialValue;
  return {
    get: vi.fn(() => storedValue),
    set: vi.fn((_key, value) => {
      storedValue = value;
    }),
    remove: vi.fn(() => {
      storedValue = null;
    }),
    value: () => storedValue,
  };
}

function mockBootstrapBackendDefaults(backend) {
  backend.readConfig.mockResolvedValue({ cwd: "/repo/app" });
  backend.getWindowBootstrap.mockResolvedValue({ snapshot: null });
  backend.openNewWindow.mockResolvedValue({ ok: true });
  backend.getProjects.mockResolvedValue({
    projects: ["/repo/app"],
    active: "/repo/app",
  });
  backend.setActiveProject.mockResolvedValue({
    projects: ["/repo/app"],
    active: "/repo/app",
  });
  backend.addProject.mockResolvedValue({
    projects: ["/repo/app"],
    active: "/repo/app",
  });
  backend.removeProject.mockResolvedValue({
    projects: ["/repo/app"],
    active: "/repo/app",
  });
  backend.getSidebarState.mockResolvedValue(DEFAULT_SIDEBAR_STATE);
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": "diff --git a/file b/file",
    },
  });
  backend.getThreadMessages.mockResolvedValue({
    messages: [],
    total: 0,
    hasMore: false,
    nextBefore: "",
  });
  backend.callBackend.mockResolvedValue({});
  backend.checkAppUpdate.mockResolvedValue({
    enabled: true,
    available: false,
  });
  backend.installLatestAppUpdate.mockResolvedValue({
    started: true,
    helper: "/tmp/updater",
  });
  backend.getVideoApiKey.mockResolvedValue({ configured: false, masked: "" });
  backend.setVideoApiKey.mockResolvedValue({ ok: true });
}

function dashboardMemoryPage() {
  return {
    memory: [],
    finalOutputRefs: [],
    sharedFileRetention: {
      items: [],
      protectedCount: 0,
      cleanupCandidateCount: 0,
    },
  };
}

function mockDashboardPageDefaults(backend) {
  const defaultSkills = defaultSkillFixtures();
  backend.getDashboardPage.mockImplementation(({ page }) => {
    if (page === "memory") return Promise.resolve(dashboardMemoryPage());
    if (page === "dags") return Promise.resolve({ dags: [] });
    if (page === "skills") return Promise.resolve({ skills: defaultSkills });
    return Promise.resolve({});
  });
}

export function createBaseShellFactory(ctx) {
  const { backend, resetClientStoreForTests } = ctx;
  const appPreferenceDefaults = createAppPreferenceDefaults();
  return {
    dispatchPointer,
    deferred,
    formatParsedTimestampForTest,
    promptPreferenceValue,
    mockPromptPreferences: mockPromptPreferences.bind(null, backend),
    decodedSvgDataUrl,
    waitForBackendThreadHeading,
    appPreferenceDefaults,
    mockShortcutPreferenceLoad: mockShortcutPreferenceLoad.bind(
      null,
      backend,
      appPreferenceDefaults,
    ),
    openPluginsAndSkillsPage,
    getSidebarNavButton,
    getBackendThreadText,
    getThreadCardByName,
    clickThreadCardByName,
    queryThreadCardByName,
    findThreadCardByName,
    defaultSkillFixtures,
    resetConnectedShellTestState: resetConnectedShellTestState.bind(
      null,
      ctx,
      backend,
      resetClientStoreForTests,
    ),
    installAppOverlayHost: installAppOverlayHost.bind(null, ctx),
    createShellLayoutStorage,
    mockBootstrapBackendDefaults: mockBootstrapBackendDefaults.bind(
      null,
      backend,
    ),
    mockDashboardPageDefaults: mockDashboardPageDefaults.bind(null, backend),
  };
}
