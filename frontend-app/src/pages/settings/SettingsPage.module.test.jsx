import { describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { APP_COPY } from "../../shared/i18n/appI18n.js";
import settingsPageSource from "./SettingsPage.jsx?raw";
import {
  backend,
  clientStore,
  deferred,
  renderSettingsPage,
} from "./SettingsPageTestSupport.jsx";

import { installSettingsPageTestHooks } from "./SettingsPageTestSupport.jsx";

installSettingsPageTestHooks();

describe("SettingsPage module", () => {
  it("renders the settings page component", () => {
    renderSettingsPage();
    expect(screen.getByTestId("settings-page")).toBeInTheDocument();
  });

  it("keeps project and log store subscriptions isolated at runtime", async () => {
    renderSettingsPage();
    await screen.findByTestId("settings-log-card");
    await act(async () => {
      await Promise.resolve();
    });

    const state = {
      activeProject: "/repo/project",
      cwd: "/repo/cwd",
      logEntries: [{ id: "ignored" }],
      logLevel: "debug",
      setLogLevel: vi.fn(),
    };
    const selectors = clientStore.hook.mock.calls.map(([selector]) => selector);
    const selectProjectScope = selectors.find(
      (selector) => "activeProject" in selector(state),
    );
    const selectLogs = selectors.find(
      (selector) => "logEntries" in selector(state),
    );
    expect(selectProjectScope).toBeTypeOf("function");
    expect(selectLogs).toBeTypeOf("function");
    expect(selectProjectScope(state)).toEqual({
      activeProject: "/repo/project",
      cwd: "/repo/cwd",
    });
    expect(selectLogs(state)).toMatchObject({
      logEntries: state.logEntries,
      logLevel: "debug",
      setLogLevel: state.setLogLevel,
    });

    await act(async () => {
      clientStore.setValue({
        logEntries: [
          {
            id: "log-1",
            ts: "2026-07-21T12:00:00Z",
            level: "warn",
            scope: "runtime",
            event: "changed",
          },
        ],
        logLevel: "warn",
      });
    });

    expect(screen.getByTestId("settings-log-card")).toHaveTextContent("warn");
    expect(screen.getByTestId("settings-log-list")).toHaveTextContent(
      "runtime.changed",
    );
  });

  it("renders mobile account cards without enabling unsupported logout", async () => {
    renderSettingsPage("/repo/app");

    const panel = screen.getByTestId("settings-mobile-account");
    await screen.findByTestId("settings-update-card");
    expect(panel).toHaveTextContent("燧元");
    expect(panel).toHaveTextContent("app");
    expect(panel).toHaveTextContent("/repo/app");
    expect(panel).toHaveTextContent("Codex");
    expect(panel).toHaveTextContent("待鉴权接入");
    expect(within(panel).getByRole("button", { name: "菜单" })).toBeDisabled();
    expect(
      within(panel).getByTestId("settings-mobile-logout-button"),
    ).toBeDisabled();
    expect(within(panel).getByRole("button", { name: "账号" })).toBeDisabled();
    expect(within(panel).getByRole("button", { name: "设置" })).toBeDisabled();
    const logOutButtons = within(panel).getAllByRole("button", {
      name: "退出登录",
    });
    expect(logOutButtons).toHaveLength(2);
    logOutButtons.forEach((button) => expect(button).toBeDisabled());
  });
});

describe("SettingsPage shortcut controller boundary", () => {
  it.each([
    ["zh", "键盘快捷键", "新建对话"],
    ["en", "Keyboard shortcuts", "New chat"],
  ])(
    "renders only the injected controller with %s copy",
    async (locale, title, commandLabel) => {
      const localeCopy = APP_COPY[locale];
      const shortcutController = {
        status: "ready",
        error: "",
        commands: [
          {
            id: "chat.new",
            label: commandLabel,
            help:
              locale === "zh"
                ? "开始一个空对话"
                : "Start an empty conversation",
            defaultDisplay: "Ctrl+N",
            currentDisplay: "Ctrl+N",
            isCustomized: false,
          },
        ],
        setDraftBinding: vi.fn(),
        save: vi.fn(),
        reset: vi.fn(),
      };

      renderSettingsPage("/repo/app", {
        copy: localeCopy.settings,
        shortcutController,
      });

      const card = screen.getByTestId("shortcut-settings-card");
      expect(
        within(card).getByRole("heading", { name: title }),
      ).toBeInTheDocument();
      expect(within(card).getByText(commandLabel)).toBeInTheDocument();
      expect(localeCopy.settings.shortcuts).toBeDefined();
      expect(backend.getPreference).not.toHaveBeenCalledWith(
        expect.objectContaining({ key: "settings.shortcuts.bindings" }),
      );
      expect(backend.setPreference).not.toHaveBeenCalledWith(
        expect.objectContaining({ key: "settings.shortcuts.bindings" }),
      );
    },
  );

  it("does not import or create a second shortcut settings hook", () => {
    expect(settingsPageSource).not.toContain("useShortcutSettings");
    expect(settingsPageSource).not.toContain("settings.shortcuts.bindings");
  });
});

describe("SettingsPage app update entry", () => {
  it("renders the app update area as a prominent about card", async () => {
    renderSettingsPage();

    const updateCard = await screen.findByTestId("settings-update-card");
    expect(updateCard).toHaveTextContent("应用更新");
    await waitFor(() => {
      expect(updateCard).toHaveTextContent("当前版本 v1.2.3");
    });
    expect(
      within(updateCard).getByTestId("settings-update-check-button"),
    ).toHaveTextContent("检查更新");
  });

  it("prefers the packaged app version in the app update card", async () => {
    backend.getBuildInfo.mockResolvedValueOnce({
      version: "v0.0.0-20260608130413-c9d1688c7e99+dirty",
      appVersion: "1.0.2",
      runtime: "darwin/arm64",
      buildTime: "2026-06-08T13:04:13Z",
      commit: "c9d1688c7e99",
    });

    renderSettingsPage();

    const updateCard = await screen.findByTestId("settings-update-card");
    expect(updateCard).toHaveTextContent("当前版本 v1.0.2");
  });

  it("shows an available update and installs it", async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({
      enabled: true,
      available: true,
      version: "v1.2.4",
      artifact: { platform: "darwin-arm64" },
    });

    renderSettingsPage();

    await act(async () => {
      fireEvent.click(await screen.findByTestId("settings-update-check-button"));
    });

    await waitFor(() => {
      expect(backend.checkAppUpdate).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "发现新版本 v1.2.4 (darwin-arm64)",
      );
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId("settings-update-install-button"));
    });

    await waitFor(() => {
      expect(backend.installLatestAppUpdate).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "正在安装更新 v1.2.4 (darwin-arm64)",
      );
    });
    expect(
      screen.queryByTestId("settings-update-install-button"),
    ).not.toBeInTheDocument();
  });

  it("hides the install action while install is pending", async () => {
    const pendingInstall = deferred();
    backend.checkAppUpdate.mockResolvedValueOnce({
      enabled: true,
      available: true,
      version: "v1.2.4",
      artifact: { platform: "darwin-arm64" },
    });
    backend.installLatestAppUpdate.mockReturnValueOnce(pendingInstall.promise);

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId("settings-update-check-button"));
    await screen.findByTestId("settings-update-install-button");
    fireEvent.click(screen.getByTestId("settings-update-install-button"));

    await waitFor(() => {
      expect(
        screen.queryByTestId("settings-update-install-button"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("settings-update-check-button")).toBeDisabled();
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "正在安装更新 v1.2.4 (darwin-arm64)",
      );
    });

  });

  it("clears stale update details when no update is available", async () => {
    backend.checkAppUpdate
      .mockResolvedValueOnce({
        enabled: true,
        available: true,
        version: "v1.2.4",
      })
      .mockResolvedValueOnce({ enabled: true, available: false });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId("settings-update-check-button"));
    await screen.findByTestId("settings-update-install-button");

    fireEvent.click(screen.getByTestId("settings-update-check-button"));

    await waitFor(() => {
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "已是最新版本",
      );
      expect(
        screen.queryByTestId("settings-update-install-button"),
      ).not.toBeInTheDocument();
    });
  });

  it("shows a disabled update notice instead of saying the dev build is current", async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({
      enabled: false,
      available: false,
    });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId("settings-update-check-button"));

    await waitFor(() => {
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "当前构建未启用应用更新",
      );
      expect(screen.getByTestId("settings-update-notice")).toHaveAttribute(
        "role",
        "status",
      );
    });
    expect(
      screen.queryByTestId("settings-update-install-button"),
    ).not.toBeInTheDocument();
  });

  it("clears stale update details when checking fails", async () => {
    backend.checkAppUpdate
      .mockResolvedValueOnce({
        enabled: true,
        available: true,
        version: "v1.2.4",
      })
      .mockRejectedValueOnce(new Error("manifest unavailable"));

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId("settings-update-check-button"));
    await screen.findByTestId("settings-update-install-button");

    fireEvent.click(screen.getByTestId("settings-update-check-button"));

    await waitFor(() => {
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "检查更新失败",
      );
      expect(
        screen.getByTestId("settings-update-notice"),
      ).not.toHaveTextContent("manifest unavailable");
      expect(screen.getByTestId("settings-update-notice")).toHaveAttribute(
        "role",
        "alert",
      );
      expect(
        screen.queryByTestId("settings-update-install-button"),
      ).not.toBeInTheDocument();
    });
  });

  it("shows backend manifest and signature errors as actionable check failures", async () => {
    backend.checkAppUpdate.mockRejectedValueOnce(
      new Error(
        "GitHub release missing update manifest asset Super-Dolphin-darwin-arm64.update.json",
      ),
    );

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId("settings-update-check-button"));

    await waitFor(() => {
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "检查更新失败",
      );
      expect(
        screen.getByTestId("settings-update-notice"),
      ).not.toHaveTextContent("GitHub release");
      expect(screen.getByTestId("settings-update-notice")).toHaveAttribute(
        "role",
        "alert",
      );
    });
    expect(
      screen.queryByTestId("settings-update-install-button"),
    ).not.toBeInTheDocument();
  });

  it("shows a fixed recovery action for typed update integrity failures", async () => {
    const secret = "PowerShell publisher output C:\\Users\\alice\\update.exe";
    const failure = new Error(secret);
    failure.data = {
      code: "UPDATE_INTEGRITY_INVALID",
      retryable: false,
      action: "preserve_state_export_diagnostics",
      transaction_id: "",
    };
    backend.checkAppUpdate.mockRejectedValueOnce(failure);

    renderSettingsPage();
    fireEvent.click(await screen.findByTestId("settings-update-check-button"));

    await waitFor(() => {
      const notice = screen.getByTestId("settings-update-notice");
      expect(notice).toHaveTextContent("更新完整性校验失败，请保持现场并导出诊断信息。");
      expect(notice).not.toHaveTextContent(secret);
    });
  });

  it("shows install failures and allows retry", async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({
      enabled: true,
      available: true,
      version: "v1.2.4",
    });
    backend.installLatestAppUpdate
      .mockRejectedValueOnce(new Error("permission denied"))
      .mockResolvedValueOnce({ ok: true });

    renderSettingsPage();

    fireEvent.click(await screen.findByTestId("settings-update-check-button"));
    fireEvent.click(
      await screen.findByTestId("settings-update-install-button"),
    );

    await waitFor(() => {
      expect(screen.getByTestId("settings-update-notice")).toHaveTextContent(
        "安装更新失败",
      );
      expect(
        screen.getByTestId("settings-update-notice"),
      ).not.toHaveTextContent("permission denied");
      expect(
        screen.getByTestId("settings-update-install-button"),
      ).toBeEnabled();
    });

    fireEvent.click(screen.getByTestId("settings-update-install-button"));

    await waitFor(() => {
      expect(backend.installLatestAppUpdate).toHaveBeenCalledTimes(2);
      expect(
        screen.queryByTestId("settings-update-install-button"),
      ).not.toBeInTheDocument();
    });
  });

  it("redacts typed integrity details from install failures and keeps retry available", async () => {
    backend.checkAppUpdate.mockResolvedValueOnce({
      enabled: true,
      available: true,
      version: "v1.2.4",
    });
    const secret = "PowerShell output C:\\Users\\alice\\update.exe";
    const failure = new Error(secret);
    failure.data = {
      code: "UPDATE_INTEGRITY_INVALID",
      retryable: false,
      action: "preserve_state_export_diagnostics",
      transaction_id: "",
    };
    backend.installLatestAppUpdate.mockRejectedValueOnce(failure);

    renderSettingsPage();
    fireEvent.click(await screen.findByTestId("settings-update-check-button"));
    fireEvent.click(await screen.findByTestId("settings-update-install-button"));

    await waitFor(() => {
      const notice = screen.getByTestId("settings-update-notice");
      expect(notice).toHaveTextContent("更新完整性校验失败，请保持现场并导出诊断信息。");
      expect(notice).not.toHaveTextContent(secret);
      expect(screen.getByTestId("settings-update-install-button")).toBeEnabled();
    });
  });
});
