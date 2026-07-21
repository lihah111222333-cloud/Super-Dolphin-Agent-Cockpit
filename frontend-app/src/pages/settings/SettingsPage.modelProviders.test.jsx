import { describe, expect, it } from "vitest";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import {
  backend,
  deferred,
  renderSettingsPage,
} from "./SettingsPageTestSupport.jsx";

import { installSettingsPageTestHooks } from "./SettingsPageTestSupport.jsx";

installSettingsPageTestHooks();

describe("SettingsPage model provider management", () => {
  it("renders model vendors with redacted API key status", async () => {
    renderSettingsPage();

    const card = await screen.findByTestId("settings-model-providers-card");
    expect(card).toHaveTextContent("Model Providers");
    expect(card).toHaveTextContent("OpenRouter");
    expect(card).toHaveTextContent("OPENROUTER_API_KEY");
    expect(card).toHaveTextContent("configured");
    expect(card).not.toHaveTextContent("sk-openrouter-secret");
  });

  it("saves the edited vendor registry through the facade", async () => {
    renderSettingsPage();
    const card = await screen.findByTestId("settings-model-providers-card");
    fireEvent.change(within(card).getByLabelText("Default Model"), {
      target: { value: "openai/gpt-4.1-mini" },
    });
    fireEvent.change(within(card).getByLabelText("Daily Budget USD"), {
      target: { value: "" },
    });
    fireEvent.change(within(card).getByLabelText("Token Priority"), {
      target: { value: "12" },
    });
    fireEvent.click(within(card).getByRole("button", { name: "保存厂商配置" }));

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/repo/app",
          registry: expect.objectContaining({
            vendors: expect.arrayContaining([
              expect.objectContaining({
                id: "openrouter",
                defaultModel: "openai/gpt-4.1-mini",
              }),
            ]),
          }),
        }),
      );
    });
    const payload = backend.saveModelProviders.mock.calls.at(-1)[0];
    const openrouter = payload.registry.vendors.find(
      (vendor) => vendor.id === "openrouter",
    );
    expect(openrouter.budget.dailyUsd).toBe(0);
    expect(openrouter.budget.monthlyUsd).toBe(100);
    expect(openrouter.tokenPool.priority).toBe(12);
    expect(openrouter).not.toHaveProperty("configured");
    expect(openrouter).not.toHaveProperty("maskedEnv");
    expect(openrouter).not.toHaveProperty("envStatus");
  });

  it("ignores stale model provider loads after cwd changes", async () => {
    const firstLoad = deferred();
    const secondLoad = deferred();
    const firstRegistry = {
      activeVendorId: "one-vendor",
      vendors: [
        {
          id: "one-vendor",
          label: "ProjectOneAI",
          enabled: true,
          baseURL: "https://one.example/v1",
          envKey: "ONE_API_KEY",
          codexModelProvider: "one",
          defaultModel: "one-model",
          configured: true,
          maskedEnv: "********",
          envStatus: "configured",
          budget: { dailyUsd: 1, monthlyUsd: 10 },
          tokenPool: { priority: 1 },
        },
      ],
    };
    const secondRegistry = {
      activeVendorId: "two-vendor",
      vendors: [
        {
          id: "two-vendor",
          label: "ProjectTwoAI",
          enabled: true,
          baseURL: "https://two.example/v1",
          envKey: "TWO_API_KEY",
          codexModelProvider: "two",
          defaultModel: "two-model",
          configured: true,
          maskedEnv: "********",
          envStatus: "configured",
          budget: { dailyUsd: 2, monthlyUsd: 20 },
          tokenPool: { priority: 2 },
        },
      ],
    };
    backend.listModelProviders
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise);

    const { rerenderSettingsPage } = renderSettingsPage("/repo/one");
    await waitFor(() => {
      expect(backend.listModelProviders).toHaveBeenCalledWith({
        cwd: "/repo/one",
      });
    });

    rerenderSettingsPage("/repo/two");
    await waitFor(() => {
      expect(backend.listModelProviders).toHaveBeenCalledWith({
        cwd: "/repo/two",
      });
    });

    await act(async () => {
      secondLoad.resolve(secondRegistry);
      await secondLoad.promise;
    });
    const card = await screen.findByTestId("settings-model-providers-card");
    expect(card).toHaveTextContent("ProjectTwoAI");
    expect(card).not.toHaveTextContent("ProjectOneAI");

    await act(async () => {
      firstLoad.resolve(firstRegistry);
      await firstLoad.promise;
    });
    expect(card).toHaveTextContent("ProjectTwoAI");
    expect(card).not.toHaveTextContent("ProjectOneAI");

    const saveButton = card.querySelectorAll(
      ".settings-provider-actions button",
    )[1];
    expect(saveButton).toBeTruthy();
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/repo/two",
          registry: expect.objectContaining({
            vendors: expect.arrayContaining([
              expect.objectContaining({
                id: "two-vendor",
                defaultModel: "two-model",
              }),
            ]),
          }),
        }),
      );
    });
    const payload = backend.saveModelProviders.mock.calls.at(-1)[0];
    expect(payload.registry.vendors).toHaveLength(1);
    expect(payload.registry.vendors[0].id).toBe("two-vendor");
  });

  it("keeps dirty model provider drafts when cached registry data refetches in the background", async () => {
    backend.listModelProviders
      .mockResolvedValueOnce({
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
            tokenPool: { priority: 10, fallbackVendorId: "" },
          },
        ],
      })
      .mockResolvedValueOnce({
        activeVendorId: "openrouter",
        vendors: [
          {
            id: "openrouter",
            label: "OpenRouter",
            enabled: true,
            baseURL: "https://openrouter.ai/api/v1",
            envKey: "OPENROUTER_API_KEY",
            codexModelProvider: "openrouter",
            defaultModel: "openai/gpt-4.1-refetched",
            configured: true,
            maskedEnv: "********",
            envStatus: "configured",
            budget: { dailyUsd: 5, monthlyUsd: 100 },
            tokenPool: { priority: 10, fallbackVendorId: "" },
          },
        ],
      });

    const { queryClient } = renderSettingsPage();
    const card = await screen.findByTestId("settings-model-providers-card");
    fireEvent.change(within(card).getByLabelText("Default Model"), {
      target: { value: "openai/gpt-4.1-draft" },
    });

    await act(async () => {
      await queryClient.invalidateQueries({
        predicate: (query) =>
          query.queryKey[0] === "settings" &&
          query.queryKey[1] === "modelProviders",
      });
    });

    await waitFor(() => {
      expect(backend.listModelProviders).toHaveBeenCalledTimes(2);
    });
    expect(within(card).getByLabelText("Default Model")).toHaveValue(
      "openai/gpt-4.1-draft",
    );
    expect(card).not.toHaveTextContent("openai/gpt-4.1-refetched");
  });

  it("shows missing env status without API key input fields", async () => {
    renderSettingsPage();
    const card = await screen.findByTestId("settings-model-providers-card");
    const deepseekRow = within(card).getByRole("button", { name: /DeepSeek/ });
    expect(deepseekRow).toHaveTextContent("disabled");
    expect(deepseekRow).toHaveTextContent("missing");
    fireEvent.click(deepseekRow);
    expect(within(card).getAllByText("missing").length).toBeGreaterThan(0);
    expect(within(card).queryByLabelText("API Key")).not.toBeInTheDocument();
  });

  it("does not apply a disabled configured vendor", async () => {
    backend.listModelProviders.mockResolvedValueOnce({
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
          configured: true,
          maskedEnv: "********",
          envStatus: "configured",
          budget: {},
          tokenPool: { priority: 20, fallbackVendorId: "qwen" },
        },
      ],
    });
    renderSettingsPage();
    const card = await screen.findByTestId("settings-model-providers-card");
    const deepseekRow = within(card).getByRole("button", { name: /DeepSeek/ });
    expect(deepseekRow).toHaveTextContent("disabled");
    expect(deepseekRow).toHaveTextContent("configured");
    fireEvent.click(deepseekRow);

    const applyButton = within(card).getByRole("button", { name: "应用厂商" });
    expect(applyButton).toBeDisabled();
    fireEvent.click(applyButton);
    expect(backend.applyModelProvider).not.toHaveBeenCalled();
  });

  it("applies a configured vendor and refreshes active state", async () => {
    renderSettingsPage();
    const card = await screen.findByTestId("settings-model-providers-card");
    fireEvent.click(within(card).getByRole("button", { name: "应用厂商" }));
    await waitFor(() => {
      expect(backend.applyModelProvider).toHaveBeenCalledWith({
        cwd: "/repo/app",
        vendorId: "openrouter",
      });
      expect(card).toHaveTextContent("已应用 OpenRouter");
    });
  });

  it("saves the current provider draft before applying a configured vendor", async () => {
    renderSettingsPage();
    const card = await screen.findByTestId("settings-model-providers-card");
    fireEvent.change(within(card).getByLabelText("Codex Home"), {
      target: { value: "/repo/app/.codex-openrouter" },
    });
    fireEvent.change(within(card).getByLabelText("Codex Model Provider"), {
      target: { value: "openrouter-project" },
    });

    fireEvent.click(within(card).getByRole("button", { name: "应用厂商" }));

    await waitFor(() => {
      expect(backend.saveModelProviders).toHaveBeenCalledWith(
        expect.objectContaining({ cwd: "/repo/app" }),
      );
      expect(backend.applyModelProvider).toHaveBeenCalledWith({
        cwd: "/repo/app",
        vendorId: "openrouter",
      });
    });
    const savePayload = backend.saveModelProviders.mock.calls[0][0];
    expect(savePayload.registry.vendors).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: "openrouter",
          codexHome: "/repo/app/.codex-openrouter",
          codexModelProvider: "openrouter-project",
        }),
      ]),
    );
    expect(backend.saveModelProviders.mock.invocationCallOrder[0]).toBeLessThan(
      backend.applyModelProvider.mock.invocationCallOrder[0],
    );
  });
});
