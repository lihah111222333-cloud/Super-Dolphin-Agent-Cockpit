import { describe, expect, it } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import {
  backend,
  deferred,
  renderSettingsPage,
} from "./SettingsPageTestSupport.jsx";

import { installSettingsPageTestHooks } from "./SettingsPageTestSupport.jsx";

installSettingsPageTestHooks();

describe("SettingsPage builtin tools migration", () => {
  it("loads grouped builtin tools and toggles through config/builtinTools write facade", async () => {
    backend.readBuiltinTools.mockResolvedValueOnce({
      tools: [
        {
          id: "Read",
          label: "读文件",
          description: "读取文件",
          enabled: false,
          provider: "claude",
          filterMode: "hard",
          enforcement: "native-hard",
        },
        {
          id: "WebFetch",
          label: "抓取网页",
          description: "网页",
          enabled: true,
          provider: "claude",
          filterMode: "hard",
        },
      ],
    });

    renderSettingsPage();

    await waitFor(() => {
      expect(
        screen.getByTestId("settings-builtin-tools-summary"),
      ).toHaveTextContent("已管控 1 / 2");
    });
    fireEvent.click(
      screen.getByTestId("settings-builtin-tool-group-head-native-hard"),
    );
    const readToggle = screen.getByTestId("settings-builtin-tool-input-Read");
    expect(readToggle).toBeChecked();

    fireEvent.click(readToggle);

    await waitFor(() => {
      expect(backend.writeBuiltinTool).toHaveBeenCalledWith({
        cwd: "/repo/app",
        id: "Read",
        enabled: true,
      });
    });
  });

  it("ignores stale builtin tool loads after cwd changes", async () => {
    const firstLoad = deferred();
    const secondLoad = deferred();
    backend.readBuiltinTools
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise);

    const { rerenderSettingsPage } = renderSettingsPage("/repo/one");
    await waitFor(() => {
      expect(backend.readBuiltinTools).toHaveBeenCalledWith({
        cwd: "/repo/one",
      });
    });

    rerenderSettingsPage("/repo/two");
    await waitFor(() => {
      expect(backend.readBuiltinTools).toHaveBeenCalledWith({
        cwd: "/repo/two",
      });
    });

    await act(async () => {
      secondLoad.resolve({
        tools: [
          {
            id: "TwoRead",
            label: "Project Two Read",
            description: "two",
            enabled: false,
            provider: "claude",
            filterMode: "hard",
            enforcement: "native-hard",
          },
        ],
      });
    });
    await waitFor(() => {
      expect(
        screen.getByTestId("settings-builtin-tools-summary"),
      ).toHaveTextContent("已管控 1 / 1");
    });
    await act(async () => {
      firstLoad.resolve({
        tools: [
          {
            id: "OneRead",
            label: "Project One Read",
            description: "one",
            enabled: false,
            provider: "claude",
            filterMode: "hard",
            enforcement: "native-hard",
          },
          {
            id: "OneWrite",
            label: "Project One Write",
            description: "one write",
            enabled: false,
            provider: "claude",
            filterMode: "hard",
            enforcement: "native-hard",
          },
        ],
      });
    });

    await waitFor(() => {
      expect(
        screen.getByTestId("settings-builtin-tools-summary"),
      ).toHaveTextContent("已管控 1 / 1");
    });
  });
});
