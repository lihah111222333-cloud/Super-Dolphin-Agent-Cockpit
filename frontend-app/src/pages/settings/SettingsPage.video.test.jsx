import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { backend, renderSettingsPage } from "./SettingsPageTestSupport.jsx";

import { installSettingsPageTestHooks } from "./SettingsPageTestSupport.jsx";

installSettingsPageTestHooks();

describe("SettingsPage video settings", () => {
  it("reports API key load failures instead of silently ignoring them", async () => {
    backend.getVideoApiKey.mockRejectedValueOnce(
      new Error("credential store unavailable"),
    );

    renderSettingsPage();

    expect(
      await screen.findByTestId("settings-video-card"),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("settings-video-notice")).toHaveTextContent(
        "读取视频 API Key 失败",
      );
      expect(screen.getByTestId("settings-video-notice")).not.toHaveTextContent(
        "credential store unavailable",
      );
      expect(screen.getByTestId("settings-video-notice")).toHaveAttribute(
        "role",
        "alert",
      );
    });
    expect(backend.callBackend).not.toHaveBeenCalled();
  });

  it("saves API keys through the named video facade method", async () => {
    renderSettingsPage();

    const card = await screen.findByTestId("settings-video-card");
    fireEvent.change(within(card).getByLabelText("API Key"), {
      target: { value: "sk-test-video-key" },
    });
    fireEvent.click(within(card).getByRole("button", { name: "保存" }));

    await waitFor(() => {
      expect(backend.setVideoApiKey).toHaveBeenCalledWith({
        apiKey: "sk-test-video-key",
      });
    });
    expect(backend.callBackend).not.toHaveBeenCalled();
  });

  describe("matrix:FM-23 layer:frontend", () => {
    it("shows save failures from the named video facade method", async () => {
      backend.setVideoApiKey.mockRejectedValueOnce(
        new Error("credential store unavailable"),
      );

      renderSettingsPage();

      const card = await screen.findByTestId("settings-video-card");
      fireEvent.change(within(card).getByLabelText("API Key"), {
        target: { value: "sk-test-video-key" },
      });
      fireEvent.click(within(card).getByRole("button", { name: "保存" }));

      await waitFor(() => {
        expect(screen.getByTestId("settings-video-notice")).toHaveTextContent(
          "保存失败",
        );
        expect(
          screen.getByTestId("settings-video-notice"),
        ).not.toHaveTextContent("credential store unavailable");
        expect(screen.getByTestId("settings-video-notice")).toHaveAttribute(
          "role",
          "alert",
        );
      });
      expect(within(card).getByLabelText("API Key")).toHaveValue(
        "sk-test-video-key",
      );
      expect(backend.setVideoApiKey).toHaveBeenCalledWith({
        apiKey: "sk-test-video-key",
      });
      expect(backend.callBackend).not.toHaveBeenCalled();
    });
  });
});
