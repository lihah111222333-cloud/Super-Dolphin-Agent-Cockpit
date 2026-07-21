import { describe, expect, it } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import {
  backend,
  deferred,
  preferenceFixture,
  renderSettingsPage,
} from "./SettingsPageTestSupport.jsx";

import { installSettingsPageTestHooks } from "./SettingsPageTestSupport.jsx";

installSettingsPageTestHooks();

describe("SettingsPage provider migration", () => {
  it("loads legacy JSON sandbox preferences from the internal preference RPC", async () => {
    const preferences = preferenceFixture({
      "settings.provider.codex.sandbox": JSON.stringify({
        type: "workspaceWrite",
        writableRoots: ["/repo/app", "/Users/test/shared"],
        networkAccess: true,
      }),
    });
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    renderSettingsPage();

    const writableRoots = await screen.findByLabelText("Writable Roots");
    await waitFor(() =>
      expect(writableRoots).toHaveValue("/repo/app\n/Users/test/shared"),
    );
    expect(screen.getByLabelText("Network Access")).toBeChecked();
  });

  it("accepts Windows absolute writable roots when saving provider settings", async () => {
    const preferences = preferenceFixture({
      "settings.provider.codex.sandbox": {
        type: "workspaceWrite",
        writableRoots: ["C:\\Users\\alice\\project", "\\\\server\\share\\repo"],
        networkAccess: false,
      },
    });
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    renderSettingsPage();

    const writableRoots = await screen.findByLabelText("Writable Roots");
    await waitFor(() =>
      expect(writableRoots).toHaveValue(
        "C:\\Users\\alice\\project\n\\\\server\\share\\repo",
      ),
    );
    backend.setPreference.mockClear();

    fireEvent.click(screen.getByRole("button", { name: "保存 Provider 设置" }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: "/repo/app",
        key: "settings.provider.codex.sandbox",
        value: {
          type: "workspaceWrite",
          writableRoots: [
            "C:\\Users\\alice\\project",
            "\\\\server\\share\\repo",
          ],
          networkAccess: false,
        },
      });
    });
    expect(screen.getByText(/新建线程时生效/)).toBeInTheDocument();
  });

  it("fails fast when the backend returns an invalid active provider preference", async () => {
    const preferences = preferenceFixture({
      "settings.provider.active": "bad-provider",
    });
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    renderSettingsPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "读取运行时偏好失败，请重试。",
    );
    expect(
      screen.queryByText(/invalid UI preference response/),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("combobox", { name: "Active Provider" }),
    ).toHaveValue("codex");
  });

  it.each([
    ["stallThresholdSec", 29, "统一超时阈值", 30],
    ["contextUsageAlerts.thresholds", [70, 70, 95], "Warn 阈值", 70],
    ["settings.provider.codex.effort", "max", "Provider Effort", "xhigh"],
    [
      "settings.provider.codex.sandbox",
      { type: "workspaceWrite", writableRoots: "bad", networkAccess: "yes" },
      "Sandbox Policy",
      "workspaceWrite",
    ],
  ])(
    "rejects malformed %s without applying a fallback value",
    async (key, value, controlName, defaultValue) => {
      const preferences = preferenceFixture({ [key]: value });
      backend.getPreference.mockImplementation(({ key: requestedKey }) =>
        Promise.resolve(preferences[requestedKey] ?? null),
      );

      renderSettingsPage();

      expect(await screen.findByRole("alert")).toHaveTextContent(
        "读取运行时偏好失败，请重试。",
      );
      expect(
        screen.queryByText(/invalid UI preference response/),
      ).not.toBeInTheDocument();
      expect(screen.getByLabelText(controlName)).toHaveValue(defaultValue);
    },
  );

  it("rejects a string boolean preference without toggling prompt visibility", async () => {
    const preferences = preferenceFixture({
      "settings.showInjectedPromptInChat": "true",
    });
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    renderSettingsPage();

    expect(
      await screen.findByText(/加载聊天注入显示开关失败/),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/invalid UI preference response/),
    ).not.toBeInTheDocument();
    expect(
      screen.getByTestId("settings-show-injected-toggle-input"),
    ).not.toBeChecked();
  });

  it("keeps active provider selection codex-only", async () => {
    const preferences = preferenceFixture({
      "settings.provider.claude.model": "sonnet",
      "settings.provider.claude.effort": "high",
      "settings.provider.claude.personality": "friendly",
      "settings.provider.claude.sandbox": { type: "readOnly" },
      "settings.provider.claude.summary": "auto",
      "settings.provider.claude.approvalPolicy": "on-failure",
    });
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    renderSettingsPage();

    const activeProvider = await screen.findByRole("combobox", {
      name: "Active Provider",
    });
    fireEvent.change(activeProvider, { target: { value: "claude" } });

    await waitFor(() => {
      expect(activeProvider).toHaveValue("codex");
    });
    expect(
      screen.queryByRole("option", { name: "Claude" }),
    ).not.toBeInTheDocument();
    expect(backend.setPreference).not.toHaveBeenCalledWith(
      expect.objectContaining({
        key: "settings.provider.active",
        value: "claude",
      }),
    );
  });

  it("loads provider summary and approval through scoped/global fallback with tombstones", async () => {
    const scopedPreferences = preferenceFixture({
      "settings.provider.codex.summary": null,
      "settings.provider.codex.approvalPolicy": { cleared: true },
    });
    const globalPreferences = preferenceFixture({
      "settings.provider.codex.summary": "concise",
      "settings.provider.codex.approvalPolicy": "never",
    });
    backend.getPreference.mockImplementation(({ cwd, key }) =>
      Promise.resolve(
        (cwd ? scopedPreferences : globalPreferences)[key] ?? null,
      ),
    );

    renderSettingsPage();

    const summaryMode = await screen.findByTestId(
      "provider-summary-mode-select",
    );
    await waitFor(() => expect(summaryMode).toHaveValue("concise"));
    expect(screen.getByTestId("provider-approval-mode-select")).toHaveValue(
      "on-request",
    );
  });

  it("does not reload runtime preferences on window focus when a runtime form is dirty", async () => {
    const reads = [];
    const preferences = preferenceFixture();
    backend.getPreference.mockImplementation(({ key }) => {
      reads.push(key);
      return Promise.resolve(preferences[key] ?? null);
    });

    renderSettingsPage("/repo/app");

    const threshold = await screen.findByLabelText("统一超时阈值");
    await waitFor(() => expect(threshold).toHaveValue(60));
    fireEvent.change(threshold, { target: { value: "45" } });
    reads.length = 0;

    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });

    await waitFor(() => expect(threshold).toHaveValue(45));
    expect(reads).toEqual([]);
  });

  it("does not reload provider properties on window focus when the provider form is dirty", async () => {
    const reads = [];
    const preferences = preferenceFixture();
    backend.getPreference.mockImplementation(({ key }) => {
      reads.push(key);
      return Promise.resolve(preferences[key] ?? null);
    });

    renderSettingsPage("/repo/app");

    const summaryMode = await screen.findByTestId(
      "provider-summary-mode-select",
    );
    await waitFor(() => expect(summaryMode).toHaveValue("detailed"));
    fireEvent.change(summaryMode, { target: { value: "concise" } });
    reads.length = 0;

    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });

    await waitFor(() => expect(summaryMode).toHaveValue("concise"));
    expect(reads).toEqual([]);
  });

  it("keeps dirty provider properties when provider preferences refetch in the background", async () => {
    const preferences = preferenceFixture();
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    const { queryClient } = renderSettingsPage("/repo/app");
    const summaryMode = await screen.findByTestId(
      "provider-summary-mode-select",
    );
    await waitFor(() => expect(summaryMode).toHaveValue("detailed"));
    fireEvent.change(summaryMode, { target: { value: "concise" } });

    preferences["settings.provider.codex.summary"] = "auto";
    backend.getPreference.mockClear();

    await act(async () => {
      await queryClient.invalidateQueries({
        predicate: (query) =>
          query.queryKey[0] === "settings" &&
          query.queryKey[1] === "provider-preferences",
      });
    });

    await waitFor(() => {
      expect(backend.getPreference).toHaveBeenCalledWith({
        cwd: "/repo/app",
        key: "settings.provider.codex.summary",
      });
    });
    expect(summaryMode).toHaveValue("concise");
  });

  it("keeps provider properties scoped to codex after an unsupported provider change", async () => {
    const staleCodexSummary = deferred();
    const preferences = preferenceFixture({
      "settings.provider.claude.model": "sonnet",
      "settings.provider.claude.effort": "high",
      "settings.provider.claude.personality": "friendly",
      "settings.provider.claude.sandbox": { type: "readOnly" },
      "settings.provider.claude.summary": "auto",
      "settings.provider.claude.approvalPolicy": "on-failure",
      "settings.provider.codex.approvalPolicy": "on-request",
    });
    backend.getPreference.mockImplementation(({ key }) => {
      if (key === "settings.provider.codex.summary")
        return staleCodexSummary.promise;
      return Promise.resolve(preferences[key] ?? null);
    });

    renderSettingsPage();

    const activeProvider = await screen.findByRole("combobox", {
      name: "Active Provider",
    });
    fireEvent.change(activeProvider, { target: { value: "claude" } });

    await waitFor(() => {
      expect(activeProvider).toHaveValue("codex");
      expect(screen.getByTestId("provider-approval-mode-select")).toHaveValue(
        "on-request",
      );
    });

    await act(async () => {
      staleCodexSummary.resolve("concise");
      await staleCodexSummary.promise;
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(activeProvider).toHaveValue("codex");
    await waitFor(() =>
      expect(screen.getByTestId("provider-summary-mode-select")).toHaveValue(
        "concise",
      ),
    );
    expect(screen.getByTestId("provider-approval-mode-select")).toHaveValue(
      "on-request",
    );
  });

  it("surfaces unsupported active provider preferences without switching provider", async () => {
    const preferences = preferenceFixture({
      "settings.provider.active": "claude",
    });
    backend.getPreference.mockImplementation(({ key }) =>
      Promise.resolve(preferences[key] ?? null),
    );

    renderSettingsPage();

    const activeProvider = await screen.findByRole("combobox", {
      name: "Active Provider",
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "读取运行时偏好失败，请重试。",
    );
    expect(
      screen.queryByText(/invalid UI preference response/),
    ).not.toBeInTheDocument();
    expect(activeProvider).toHaveValue("codex");
    expect(backend.setPreference).not.toHaveBeenCalledWith(
      expect.objectContaining({
        key: "settings.provider.active",
        value: "claude",
      }),
    );
  });

  it("hides the internal Codex model provider and clears Codex identity fields with tombstones", async () => {
    renderSettingsPage();

    const codexHome = await screen.findByLabelText("Codex Home");
    const instanceKey = screen.getByLabelText("Instance Key");
    await waitFor(() => {
      expect(codexHome).toHaveValue("/Users/test/.codex");
      expect(instanceKey).toHaveValue("desktop-main");
    });
    expect(screen.queryByLabelText("Model Provider")).not.toBeInTheDocument();

    fireEvent.change(codexHome, { target: { value: "" } });
    fireEvent.change(instanceKey, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "保存 Provider 设置" }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: "/repo/app",
        key: "settings.provider.codex.codexHome",
        value: { cleared: true },
      });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: "/repo/app",
        key: "settings.provider.codex.codexInstanceKey",
        value: { cleared: true },
      });
    });
    expect(backend.setPreference).not.toHaveBeenCalledWith(
      expect.objectContaining({
        key: "settings.provider.codex.codexModelProvider",
      }),
    );
    expect(backend.setPreference).not.toHaveBeenCalledWith(
      expect.objectContaining({
        key: "settings.provider.codex.model",
      }),
    );
    expect(backend.setPreference).not.toHaveBeenCalledWith(
      expect.objectContaining({
        key: "settings.provider.codex.effort",
      }),
    );
  });
});
