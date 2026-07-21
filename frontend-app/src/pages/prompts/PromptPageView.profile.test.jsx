import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React from "react";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { assertPreferenceResponseShape } from "../../shared/api/preferenceResponseGuards.js";
import { PromptPageView } from "./PromptPageView.jsx";

const backend = vi.hoisted(() => ({
  commitPromptIntent: vi.fn(),
  copyTextToClipboard: vi.fn(),
  deletePrompt: vi.fn(),
  discardPromptIntent: vi.fn(),
  draftPromptIntent: vi.fn(),
  dryRunPromptIntent: vi.fn(),
  getDashboardPrompts: vi.fn(),
  getPersonalizationProfile: vi.fn(),
  getPreference: vi.fn(),
  getPrompt: vi.fn(),
  listPromptSections: vi.fn(),
  listPromptAssets: vi.fn(),
  savePersonalizationProfile: vi.fn(),
  setPreference: vi.fn(),
  writePromptSection: vi.fn(),
  writePrompt: vi.fn(),
  deletePromptSection: vi.fn(),
}));

const validatedPreferenceReader = vi.hoisted(() => vi.fn());

vi.mock("./services/promptPageService.js", () => ({
  ...backend,
  getPreference: validatedPreferenceReader,
}));
vi.mock("../../shared/api/backendApi.js", () => ({
  getPreference: backend.getPreference,
}));

function renderPromptPage(props = {}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <PromptPageView projectPath="/repo/app" {...props} />
      </QueryClientProvider>,
    ),
  };
}

function mockPromptList() {
  backend.listPromptAssets.mockResolvedValue({
    prompts: [
      {
        id: "main/reviewer",
        name: "代码审查专家",
        description: "审查代码质量",
        when_to_use: "用户要求代码审查时使用",
        content: "先检查阻塞问题",
        agentType: "coder",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        tags: ["intent:expert", "review"],
        enabled: true,
        scope: "project",
        priority: 5,
      },
    ],
  });
  backend.getPreference.mockResolvedValue("");
  backend.getPersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.savePersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.writePrompt.mockResolvedValue({});
  backend.getPrompt.mockResolvedValue({
    prompt: { content: "先检查阻塞问题" },
  });
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.dryRunPromptIntent.mockResolvedValue({
    would_use: true,
    action: "expert",
    reasons: ["matched"],
  });
  backend.getDashboardPrompts.mockResolvedValue({ prompts: [] });
  backend.listPromptSections.mockResolvedValue({ sections: [] });
  backend.writePromptSection.mockResolvedValue({ ok: true });
  backend.deletePromptSection.mockResolvedValue({ ok: true });
}

function canonicalPromptWireItem(overrides = {}) {
  return {
    id: "main/canonical-wire",
    name: "规范提示词",
    content: "严格解析",
    description: "完整后端 wire shape",
    agentType: "coder",
    when_to_use: "验证响应契约时",
    createdAt: "2026-07-11T00:00:00Z",
    updatedAt: "2026-07-11T00:00:00Z",
    enabled: true,
    scope: "project",
    tags: ["intent:expert"],
    ...overrides,
  };
}

function createDeferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPromptList();
  validatedPreferenceReader.mockImplementation(async (payload) => {
    const value = await backend.getPreference(payload);
    assertPreferenceResponseShape(payload.key, value);
    return value;
  });
});

afterEach(() => {
  cleanup();
  delete window.__SUPER_DOLPHIN_PROMPT_DEBUG__;
  vi.restoreAllMocks();
});

describe("PromptPageView module", () => {
  it("exports the prompt page view component", () => {
    expect(PromptPageView).toBeTypeOf("function");
  });

  it("keeps advanced debug disabled when browser storage is unavailable", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage unavailable");
    });

    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));

    expect(
      await screen.findByRole("dialog", { name: "编辑提示词" }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("match_when JSON")).not.toBeInTheDocument();
  });

  it("closes the editor through React Aria modal dismissal", async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));

    const dialog = await screen.findByRole("dialog", { name: "编辑提示词" });
    fireEvent.keyDown(dialog, { key: "Escape" });

    await waitFor(() => {
      expect(
        screen.queryByRole("dialog", { name: "编辑提示词" }),
      ).not.toBeInTheDocument();
    });
  });

  it("loads and saves personalization profile", async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [
        canonicalPromptWireItem({
          id: "main/role",
          name: "代码审查专家",
          tags: ["intent:expert"],
          content: "review",
        }),
        canonicalPromptWireItem({
          id: "recall/vue",
          name: "Vue 规范",
          tags: ["intent:recall"],
          content: "vue",
        }),
        canonicalPromptWireItem({
          id: "rule/default",
          name: "默认规则",
          agentType: "default_rule",
          tags: ["intent:default_rule"],
          content: "rule",
          scope: "global",
        }),
        canonicalPromptWireItem({
          id: "draft/profile",
          name: "待确认角色",
          draft_key: "draft-profile",
          draft_status: "ready_to_save",
          state: "pending_confirm",
          tags: ["intent:expert"],
          content: "draft",
          enabled: false,
        }),
      ],
    });
    backend.getPersonalizationProfile.mockResolvedValue({
      profile: {
        displayName: "小海",
        role: "后端工程师",
        background: "熟悉 Go",
        customInstructions: "回答要直接",
      },
    });
    backend.savePersonalizationProfile.mockResolvedValue({
      profile: {
        displayName: "小海",
        role: "架构师",
        background: "熟悉 Go",
        customInstructions: "回答要直接",
      },
    });

    renderPromptPage();

    expect(
      await screen.findByRole("heading", { name: "个性化" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("管理您的身份信息以及 燧元 的记忆内容"),
    ).toBeInTheDocument();
    const overview = screen.getByLabelText("个性化概览");
    const metricValue = (label) => {
      const term = Array.from(overview.querySelectorAll("dt")).find(
        (node) => node.textContent === label,
      );
      expect(term).toBeTruthy();
      return term.nextElementSibling;
    };
    await waitFor(() => expect(metricValue("定制角色")).toHaveTextContent("1"));
    expect(metricValue("知识")).toHaveTextContent("1");
    expect(metricValue("默认规则")).toHaveTextContent("1");
    expect(metricValue("待确认")).toHaveTextContent("1");
    await waitFor(() =>
      expect(backend.getPersonalizationProfile).toHaveBeenCalledWith({
        cwd: "/repo/app",
      }),
    );
    expect(within(overview).getByLabelText("昵称")).toHaveValue("小海");
    expect(within(overview).getByLabelText("职业")).toHaveValue("后端工程师");
    expect(within(overview).getByLabelText("更多关于您的信息")).toHaveValue(
      "熟悉 Go",
    );
    expect(within(overview).getByLabelText("自定义指令")).toHaveValue(
      "回答要直接",
    );

    fireEvent.change(within(overview).getByLabelText("职业"), {
      target: { value: "架构师" },
    });
    fireEvent.click(
      within(overview).getByRole("button", { name: "保存个人资料" }),
    );

    await waitFor(() =>
      expect(backend.savePersonalizationProfile).toHaveBeenCalledWith({
        cwd: "/repo/app",
        profile: {
          displayName: "小海",
          role: "架构师",
          background: "熟悉 Go",
          customInstructions: "回答要直接",
        },
      }),
    );
    expect(await screen.findByText("个人资料已保存")).toBeInTheDocument();
  });

  it("preserves profile edits made while an earlier save is pending", async () => {
    const deferredSave = createDeferred();
    backend.savePersonalizationProfile.mockReturnValueOnce(
      deferredSave.promise,
    );
    renderPromptPage();

    const overview = await screen.findByLabelText("个性化概览");
    const roleInput = within(overview).getByLabelText("职业");
    const saveButton = within(overview).getByRole("button", {
      name: "保存个人资料",
    });
    fireEvent.change(roleInput, { target: { value: "架构师" } });
    fireEvent.click(saveButton);
    await waitFor(() =>
      expect(backend.savePersonalizationProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/repo/app",
          profile: expect.objectContaining({ role: "架构师" }),
        }),
      ),
    );

    fireEvent.change(roleInput, { target: { value: "产品经理" } });
    deferredSave.resolve({
      profile: {
        displayName: "",
        role: "架构师",
        background: "",
        customInstructions: "",
      },
    });

    await waitFor(() => expect(saveButton).toBeEnabled());
    expect(roleInput).toHaveValue("产品经理");
    expect(screen.queryByText("个人资料已保存")).not.toBeInTheDocument();
  });

  it("ignores a previous project profile save after switching projects", async () => {
    const deferredSave = createDeferred();
    backend.savePersonalizationProfile.mockReturnValueOnce(
      deferredSave.promise,
    );
    backend.getPersonalizationProfile.mockImplementation(({ cwd }) =>
      Promise.resolve({
        profile: {
          displayName: "",
          role: cwd === "/repo/app" ? "A 初始角色" : "B 初始角色",
          background: "",
          customInstructions: "",
        },
      }),
    );
    const { queryClient, rerender } = renderPromptPage();

    let overview = await screen.findByLabelText("个性化概览");
    fireEvent.change(within(overview).getByLabelText("职业"), {
      target: { value: "A 保存角色" },
    });
    fireEvent.click(
      within(overview).getByRole("button", { name: "保存个人资料" }),
    );
    await waitFor(() =>
      expect(backend.savePersonalizationProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/repo/app",
          profile: expect.objectContaining({ role: "A 保存角色" }),
        }),
      ),
    );

    rerender(
      <QueryClientProvider client={queryClient}>
        <PromptPageView projectPath="/repo/next" />
      </QueryClientProvider>,
    );
    overview = await screen.findByLabelText("个性化概览");
    const nextRoleInput = within(overview).getByLabelText("职业");
    await waitFor(() => expect(nextRoleInput).toHaveValue("B 初始角色"));
    fireEvent.change(nextRoleInput, { target: { value: "B 当前草稿" } });
    deferredSave.resolve({
      profile: {
        displayName: "",
        role: "A 保存角色",
        background: "",
        customInstructions: "",
      },
    });

    await deferredSave.promise;
    await waitFor(() => expect(nextRoleInput).toHaveValue("B 当前草稿"));
    expect(screen.queryByText("个人资料已保存")).not.toBeInTheDocument();
  });

  it("does not publish profile success after save response validation rejects", async () => {
    backend.savePersonalizationProfile.mockRejectedValueOnce(
      new TypeError(
        "ui/personalization/profile/save response personalization profile response.profile.background Expected string, received array",
      ),
    );

    renderPromptPage();

    const overview = await screen.findByLabelText("个性化概览");
    fireEvent.change(within(overview).getByLabelText("职业"), {
      target: { value: "架构师" },
    });
    fireEvent.click(
      within(overview).getByRole("button", { name: "保存个人资料" }),
    );

    expect(
      await screen.findByText("个人资料保存失败，请重试。"),
    ).toBeInTheDocument();
    expect(document.body.textContent).not.toContain(
      "ui/personalization/profile/save",
    );
    expect(screen.queryByText("个人资料已保存")).not.toBeInTheDocument();
  });

  it("opens the recall wizard from the import memory action", async () => {
    renderPromptPage();

    const overview = await screen.findByLabelText("个性化概览");
    fireEvent.click(within(overview).getByRole("button", { name: "导入记忆" }));

    const dialog = await screen.findByRole("dialog", {
      name: "添加给 AI 的内容",
    });
    expect(
      within(dialog).getByRole("tab", { name: "参考资料" }),
    ).toHaveAttribute("aria-selected", "true");
  });

  it("shows inline validation reasons and blocks saving an invalid profile", async () => {
    renderPromptPage();

    const overview = await screen.findByLabelText("个性化概览");
    const saveButton = within(overview).getByRole("button", {
      name: "保存个人资料",
    });
    await waitFor(() => expect(saveButton).toBeEnabled());

    // 超长输入触发与后端一致的字符上限校验：显示具体原因并禁用保存。
    fireEvent.change(within(overview).getByLabelText("昵称"), {
      target: { value: "a".repeat(81) },
    });
    expect(await within(overview).findByRole("alert")).toHaveTextContent(
      "不能超过 80 个字符（当前 81 个）",
    );
    expect(saveButton).toBeDisabled();
    fireEvent.click(saveButton);
    expect(backend.savePersonalizationProfile).not.toHaveBeenCalled();

    // 修正后校验原因消失，保存恢复可用并能成功执行。
    fireEvent.change(within(overview).getByLabelText("昵称"), {
      target: { value: "小海" },
    });
    await waitFor(() => expect(saveButton).toBeEnabled());
    expect(within(overview).queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(saveButton);
    await waitFor(() =>
      expect(backend.savePersonalizationProfile).toHaveBeenCalledTimes(1),
    );
  });

  it("guides project selection when saving profile or importing memory without a project", async () => {
    renderPromptPage({ projectPath: "未选择项目" });

    const overview = await screen.findByLabelText("个性化概览");
    const saveButton = within(overview).getByRole("button", {
      name: "保存个人资料",
    });
    const importButton = within(overview).getByRole("button", {
      name: "导入记忆",
    });

    // 未选择项目时按钮不再原生禁用（保留焦点与点击能力），点击后给出明确引导。
    expect(saveButton).not.toBeDisabled();
    expect(importButton).not.toBeDisabled();
    expect(saveButton).toHaveAttribute("aria-disabled", "true");
    expect(importButton).toHaveAttribute("aria-disabled", "true");

    fireEvent.click(saveButton);
    expect(
      await screen.findByText("请先在聊天页选择项目，再使用个性化设置。"),
    ).toBeInTheDocument();
    expect(backend.savePersonalizationProfile).not.toHaveBeenCalled();

    fireEvent.click(importButton);
    expect(
      screen.queryByRole("dialog", { name: "添加给 AI 的内容" }),
    ).not.toBeInTheDocument();
  });

  it("labels the personalization overview as read-only when prompt assets fall back", async () => {
    const error = Object.assign(new Error("method not found"), {
      code: -32601,
    });
    backend.listPromptAssets.mockRejectedValueOnce(error);

    renderPromptPage();

    const overview = await screen.findByLabelText("个性化概览");
    await waitFor(() => {
      expect(
        within(overview).getByText(
          "prompt-assets/list 暂不可用；当前仅显示只读的提示词与参考资料。",
        ),
      ).toBeInTheDocument();
    });
    expect(
      within(overview).queryByText(/已接入提示词与参考资料/),
    ).not.toBeInTheDocument();
  });
});
