import { describe, expect, it } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import {
  backend,
  deferred,
  renderSettingsPage,
} from "./SettingsPageTestSupport.jsx";

import { installSettingsPageTestHooks } from "./SettingsPageTestSupport.jsx";

installSettingsPageTestHooks();

describe("SettingsPage prompt settings", () => {
  it("ignores stale prompt settings loads after cwd changes", async () => {
    const firstLoad = deferred();
    const secondLoad = deferred();
    backend.readLspPromptHint
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise);

    const { rerenderSettingsPage } = renderSettingsPage("/repo/one");
    await waitFor(() => {
      expect(backend.readLspPromptHint).toHaveBeenCalledWith({
        cwd: "/repo/one",
      });
    });

    rerenderSettingsPage("/repo/two");
    await waitFor(() => {
      expect(backend.readLspPromptHint).toHaveBeenCalledWith({
        cwd: "/repo/two",
      });
    });

    await act(async () => {
      secondLoad.resolve({
        hint: "project two effective prompt",
        defaultHint: "project two default prompt",
        overrideHint: "project two override prompt",
        usingDefault: false,
      });
    });
    await waitFor(() => {
      expect(screen.getByTestId("settings-lsp-prompt-input")).toHaveValue(
        "project two override prompt",
      );
      expect(screen.getByTestId("settings-lsp-effective-output")).toHaveValue(
        "project two effective prompt",
      );
    });

    await act(async () => {
      firstLoad.resolve({
        hint: "project one effective prompt",
        defaultHint: "project one default prompt",
        overrideHint: "project one override prompt",
        usingDefault: false,
      });
    });

    expect(screen.getByTestId("settings-lsp-prompt-input")).toHaveValue(
      "project two override prompt",
    );
    expect(screen.getByTestId("settings-lsp-effective-output")).toHaveValue(
      "project two effective prompt",
    );
  });
});
