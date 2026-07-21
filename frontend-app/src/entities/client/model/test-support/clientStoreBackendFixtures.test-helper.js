export function createDefaultBackendResponses(page) {
  return {
    resolved: {
      readConfig: { cwd: "/repo/app" },
      getWindowBootstrap: { snapshot: null },
      openNewWindow: { ok: true },
      getProjects: { projects: ["/repo/app"], active: "/repo/app" },
      setActiveProject: { projects: ["/repo/app"], active: "/repo/app" },
      addProject: { projects: ["/repo/app"], active: "/repo/app" },
      removeProject: { projects: ["/repo/app"], active: "/repo/app" },
      getSidebarState: {
        activeThreadId: "thread-1",
        threads: [
          {
            id: "thread-1",
            name: "Existing",
            provider: "codex",
            status: "running",
          },
        ],
        tokenUsageByThread: {
          "thread-1": {
            usedTokens: 42,
            contextWindowTokens: 100,
            usedPercent: 42,
          },
        },
      },
      getThreadState: { timelinesByThread: {} },
      getThreadMessages: page(),
      archiveThread: { ok: true },
      unarchiveThread: { ok: true },
      forceCompleteTurn: { confirmed: true },
      recoverThread: { recovered: true },
      respondApproval: null,
      deleteThread: { ok: true },
      getThreadConfig: {
        threadId: "thread-1",
        provider: "codex",
        supportsThreadOverride: true,
        override: {},
        effective: { model: "gpt-5.4", effort: "medium" },
      },
      setThreadConfig: {
        threadId: "thread-1",
        provider: "codex",
        supportsThreadOverride: true,
        override: { model: "gpt-5.4", effort: "medium" },
        effective: { model: "gpt-5.4", effort: "medium" },
      },
      setPreference: { ok: true },
      selectProjectDir: "/repo/new",
      beginTextClipboardWrite: null,
      copyTextToClipboard: true,
      listSharedFiles: { files: [] },
    },
    preference: ({ key }) =>
      Promise.resolve(
        {
          "settings.provider.active": "codex",
          "settings.provider.codex.model": "gpt-5.5",
          "settings.provider.codex.effort": "xhigh",
          "settings.provider.codex.codexHome": "~/.codex",
          "settings.provider.codex.codexInstanceKey": "default",
          "settings.provider.codex.codexModelProvider": "openai",
          "settings.provider.claude.model": "sonnet",
          "settings.provider.claude.effort": "high",
        }[key] ?? null,
      ),
    readSharedFile: ({ path }) =>
      Promise.resolve({ path, content: `content for ${path}` }),
  };
}
