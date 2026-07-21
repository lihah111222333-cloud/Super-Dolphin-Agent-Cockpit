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

function dashboardPromptWireItem(overrides = {}) {
  return {
    id: 17,
    prompt_key: "legacy/prompt",
    title: "旧提示词",
    agent_key: "main",
    tool_name: "",
    prompt_text: "legacy readonly data",
    when_to_use: "",
    variables: {},
    tags: ["intent:expert", "scope.cwd:/repo/app"],
    enabled: true,
    manually_edited: false,
    priority: 0,
    created_by: "",
    updated_by: "",
    created_at: "2026-07-11T00:00:00Z",
    updated_at: "2026-07-11T00:00:00Z",
    description: "",
    ...overrides,
  };
}

function promptItemWithout(field) {
  const item = canonicalPromptWireItem();
  delete item[field];
  return item;
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

describe("PromptPageView backend wiring", () => {
  it("surfaces a malformed active prompt preference instead of coercing it to empty", async () => {
    backend.getPreference.mockResolvedValue(42);

    renderPromptPage();

    expect(
      await screen.findByText("同步失败，显示的是上次成功的数据。"),
    ).toBeInTheDocument();
    expect(document.body.textContent).not.toContain(
      "invalid UI preference response",
    );
    expect(screen.queryByText("main/reviewer")).not.toBeInTheDocument();
  });

  it("loads prompt assets for the dashboard and saves edits with the backend payload shape", async () => {
    renderPromptPage();

    expect(screen.getByRole("status")).toHaveTextContent("正在加载提示词...");

    await waitFor(() => {
      expect(backend.listPromptAssets).toHaveBeenCalledWith({
        cwd: "/repo/app",
      });
      expect(backend.getPreference).toHaveBeenCalledWith({
        cwd: "/repo/app",
        key: "settings.activePromptKey",
      });
    });
    expect(await screen.findByText("代码审查专家")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "编辑" }));
    fireEvent.change(screen.getByRole("textbox", { name: "名称" }), {
      target: { value: "审查提示词" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => {
      expect(backend.writePrompt).toHaveBeenCalledWith({
        cwd: "/repo/app",
        id: "main/reviewer",
        name: "审查提示词",
        description: "审查代码质量",
        agentType: "coder",
        priority: 5,
        when_to_use: "用户要求代码审查时使用",
        content: "先检查阻塞问题",
        tags: ["review"],
        enabled: true,
        scope: "project",
      });
    });
    expect(
      await screen.findByText("提示词已保存：审查提示词"),
    ).toBeInTheDocument();
  });

  it("keeps the current project editor and draft when a previous project save resolves late", async () => {
    let resolveSave;
    backend.writePrompt.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSave = resolve;
        }),
    );
    const { queryClient, rerender } = renderPromptPage();

    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
    let editor = await screen.findByRole("dialog", { name: "编辑提示词" });
    fireEvent.change(within(editor).getByRole("textbox", { name: "名称" }), {
      target: { value: "旧项目保存" },
    });
    fireEvent.click(within(editor).getByRole("button", { name: "保存" }));
    await waitFor(() =>
      expect(backend.writePrompt).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/repo/app",
          name: "旧项目保存",
        }),
      ),
    );

    rerender(
      <QueryClientProvider client={queryClient}>
        <PromptPageView projectPath="/repo/next" />
      </QueryClientProvider>,
    );
    editor = await screen.findByRole("dialog", { name: "编辑提示词" });
    fireEvent.change(within(editor).getByRole("textbox", { name: "名称" }), {
      target: { value: "新项目草稿" },
    });

    resolveSave({});

    await waitFor(() => {
      const currentEditor = screen.getByRole("dialog", { name: "编辑提示词" });
      expect(
        within(currentEditor).getByRole("textbox", { name: "名称" }),
      ).toHaveValue("新项目草稿");
    });
    expect(
      screen.queryByText("提示词已保存：旧项目保存"),
    ).not.toBeInTheDocument();
  });

  it("blocks saving an empty agent type instead of defaulting it to main", async () => {
    window.__SUPER_DOLPHIN_PROMPT_DEBUG__ = true;
    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
    const editor = await screen.findByRole("dialog", { name: "编辑提示词" });
    fireEvent.change(
      within(editor).getByRole("textbox", { name: "Agent Key" }),
      {
        target: { value: "" },
      },
    );
    fireEvent.click(within(editor).getByRole("button", { name: "保存" }));

    expect(
      await within(editor).findByText("请填写 Agent Key"),
    ).toBeInTheDocument();
    expect(backend.writePrompt).not.toHaveBeenCalled();
    delete window.__SUPER_DOLPHIN_PROMPT_DEBUG__;
  });

  it("does not write back priority zero when the canonical item omits priority", async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [promptItemWithout("priority")],
    });
    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(backend.writePrompt).toHaveBeenCalledTimes(1));
    expect(backend.writePrompt.mock.calls[0][0]).not.toHaveProperty("priority");
  });

  it("exposes editor scope as radios and saves the selected scope", async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
    const editor = await screen.findByRole("dialog", { name: "编辑提示词" });
    const scopeGroup = within(editor).getByRole("radiogroup", {
      name: "可用范围",
    });
    const projectScope = within(scopeGroup).getByRole("radio", {
      name: "这个项目",
    });
    const globalScope = within(scopeGroup).getByRole("radio", {
      name: "全局可用",
    });

    expect(projectScope).toBeChecked();
    expect(globalScope).not.toBeChecked();

    fireEvent.click(globalScope);
    expect(projectScope).not.toBeChecked();
    expect(globalScope).toBeChecked();
    fireEvent.click(within(editor).getByRole("button", { name: "保存" }));

    await waitFor(() => {
      expect(backend.writePrompt).toHaveBeenCalledWith(
        expect.objectContaining({
          id: "main/reviewer",
          scope: "global",
        }),
      );
    });
  });

  it("shows created, started, and disabled prompt lifecycle states", async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [
        canonicalPromptWireItem({
          id: "main/created",
          name: "已创建助手",
          description: "尚未强制使用",
          content: "普通能力",
          tags: ["intent:expert"],
          enabled: true,
          scope: "project",
        }),
        canonicalPromptWireItem({
          id: "main/started",
          name: "已启动助手",
          description: "当前强制使用",
          content: "启动能力",
          tags: ["intent:expert"],
          enabled: true,
          scope: "project",
        }),
        canonicalPromptWireItem({
          id: "main/stopped",
          name: "已停用助手",
          description: "已经停用",
          content: "停用能力",
          tags: ["intent:expert"],
          enabled: false,
          scope: "project",
        }),
      ],
    });
    backend.getPreference.mockResolvedValue("main/started");

    renderPromptPage();

    const createdCard = (await screen.findByText("已创建助手")).closest(
      "article",
    );
    const startedCard = screen.getByText("已启动助手").closest("article");
    const stoppedCard = screen.getByText("已停用助手").closest("article");
    expect(within(createdCard).getByText("已创建")).toBeInTheDocument();
    expect(within(startedCard).getByText("已启动")).toBeInTheDocument();
    expect(within(startedCard).getByText("强制使用")).toBeInTheDocument();
    expect(within(stoppedCard).getByText("已停用")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "启用中" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("已创建助手")).toBeInTheDocument();
    expect(screen.getByText("已启动助手")).toBeInTheDocument();
    expect(screen.getByText("已停用助手")).toBeInTheDocument();
    expect(
      screen.queryByRole("tablist", { name: "提示词分类" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /全部范围/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /全部状态/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "添加给 AI 的内容" }),
    ).not.toBeInTheDocument();
  });

  it("falls back to readonly dashboard prompts when prompt-assets/list is not registered", async () => {
    const missingMethodError = new Error("method not found");
    missingMethodError.code = -32601;
    backend.listPromptAssets.mockRejectedValueOnce(missingMethodError);
    backend.getDashboardPrompts.mockResolvedValueOnce({
      prompts: [dashboardPromptWireItem()],
    });

    renderPromptPage();

    expect(await screen.findByText("旧提示词")).toBeInTheDocument();
    expect(screen.getByText(/只读模式/)).toBeInTheDocument();
    expect(backend.getDashboardPrompts).toHaveBeenCalledWith({
      cwd: "/repo/app",
    });
    expect(screen.getByRole("button", { name: "查看" })).toBeInTheDocument();
  });

  it("fails fast on malformed prompt-assets/list responses without readonly fallback", async () => {
    backend.listPromptAssets.mockResolvedValueOnce({ prompts: {} });

    renderPromptPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "加载提示词失败",
    );
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
    expect(screen.queryByText(/只读模式/)).not.toBeInTheDocument();
  });

  it("does not publish prompt assets after the public RPC validator rejects a nested field", async () => {
    backend.listPromptAssets.mockRejectedValueOnce(
      new TypeError(
        "ui/prompt-assets/list response prompt assets response.prompts[0].enabled Expected boolean, received string",
      ),
    );

    renderPromptPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "加载提示词失败，请重试。",
    );
    expect(document.body.textContent).not.toContain("ui/prompt-assets/list");
    expect(screen.queryByText("代码审查专家")).not.toBeInTheDocument();
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
  });

  it.each([
    ["empty item", {}],
    ...[
      "content",
      "description",
      "agentType",
      "when_to_use",
      "createdAt",
      "updatedAt",
    ].map((field) => [`missing stable ${field}`, promptItemWithout(field)]),
    [
      "missing id",
      {
        name: "缺少 ID",
        content: "不能启动",
        description: "",
        agentType: "coder",
        when_to_use: "测试时",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        enabled: true,
        scope: "project",
        tags: ["intent:expert"],
        priority: 1,
      },
    ],
    [
      "string boolean",
      {
        id: "main/string-boolean",
        name: "错误布尔值",
        content: "不能启动",
        description: "",
        agentType: "coder",
        when_to_use: "测试时",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        enabled: "false",
        scope: "project",
        tags: ["intent:expert"],
        priority: 1,
      },
    ],
    [
      "unknown scope",
      {
        id: "main/unknown-scope",
        name: "错误范围",
        content: "不能启动",
        description: "",
        agentType: "coder",
        when_to_use: "测试时",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        enabled: true,
        scope: "workspace",
        tags: ["intent:expert"],
        priority: 1,
      },
    ],
    [
      "unknown prompt kind",
      {
        id: "main/unknown-kind",
        name: "错误类别",
        content: "不能启动",
        description: "",
        agentType: "coder",
        when_to_use: "测试时",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        enabled: true,
        scope: "project",
        tags: ["intent:unknown"],
        priority: 1,
      },
    ],
    [
      "missing required name",
      {
        id: "main/missing-name",
        content: "不能启动",
        description: "",
        agentType: "coder",
        when_to_use: "测试时",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        enabled: true,
        scope: "project",
        tags: ["intent:expert"],
        priority: 1,
      },
    ],
  ])("rejects a malformed prompt item: %s", async (_label, item) => {
    backend.listPromptAssets.mockResolvedValueOnce({ prompts: [item] });

    renderPromptPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "加载提示词失败",
    );
    expect(screen.queryByText(item.name || "未命名")).not.toBeInTheDocument();
    expect(screen.queryByRole("article")).not.toBeInTheDocument();
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
  });

  it("renders a canonical prompt-assets/list item", async () => {
    backend.listPromptAssets.mockResolvedValueOnce({
      prompts: [
        {
          id: "main/canonical",
          name: "规范提示词",
          content: "严格解析",
          description: "完整后端 wire shape",
          agentType: "coder",
          when_to_use: "验证响应契约时",
          createdAt: "2026-07-11T00:00:00Z",
          updatedAt: "2026-07-11T00:00:00Z",
          enabled: true,
          scope: "project",
          tags: ["intent:expert", "contract"],
          priority: 3,
        },
      ],
    });

    renderPromptPage();

    expect(await screen.findByText("规范提示词")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows sync failure when readonly fallback dashboard prompts are malformed after fallback is selected", async () => {
    const missingMethodError = new Error("method not found");
    missingMethodError.code = -32601;
    backend.listPromptAssets
      .mockRejectedValueOnce(missingMethodError)
      .mockRejectedValueOnce(missingMethodError);
    backend.getDashboardPrompts
      .mockResolvedValueOnce({
        prompts: [dashboardPromptWireItem()],
      })
      .mockResolvedValueOnce({
        prompts: [dashboardPromptWireItem({ id: "17" })],
      });

    renderPromptPage();

    expect(await screen.findByText("旧提示词")).toBeInTheDocument();
    window.dispatchEvent(new Event("focus"));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "同步失败，显示的是上次成功的数据",
    );
    expect(screen.getByText("旧提示词")).toBeInTheDocument();
    expect(screen.getByText(/只读模式/)).toBeInTheDocument();
  });

  it("copies saved prompt content after reading the complete prompt body", async () => {
    backend.getPrompt.mockResolvedValueOnce({
      prompt: { content: "完整提示词内容" },
    });

    renderPromptPage();
    const card = (await screen.findByText("代码审查专家")).closest("article");
    fireEvent.click(within(card).getByRole("button", { name: "复制" }));

    await waitFor(() => {
      expect(backend.getPrompt).toHaveBeenCalledWith({
        cwd: "/repo/app",
        id: "main/reviewer",
      });
      expect(backend.copyTextToClipboard).toHaveBeenCalledWith(
        "完整提示词内容",
      );
    });
    expect(await screen.findByText("已复制提示词内容")).toBeInTheDocument();
  });

  it("edits match_when JSON in advanced debug and blocks invalid JSON before saving", async () => {
    window.__SUPER_DOLPHIN_PROMPT_DEBUG__ = true;
    backend.listPromptAssets.mockResolvedValueOnce({
      prompts: [
        {
          id: "main/reviewer",
          name: "代码审查专家",
          content: "先检查阻塞问题",
          description: "",
          agentType: "main",
          when_to_use: "",
          createdAt: "2026-07-11T00:00:00Z",
          updatedAt: "2026-07-11T00:00:00Z",
          tags: ["intent:expert", "review"],
          enabled: true,
          scope: "project",
          match_when: { language: "zh" },
        },
      ],
    });

    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));

    const matchWhenInput = await screen.findByLabelText("match_when JSON");
    expect(matchWhenInput).toHaveValue('{\n  "language": "zh"\n}');

    fireEvent.change(matchWhenInput, { target: { value: "{bad json" } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(
      await screen.findByText(/自动匹配条件不是合法 JSON/),
    ).toBeInTheDocument();
    expect(backend.writePrompt).not.toHaveBeenCalled();

    fireEvent.change(matchWhenInput, {
      target: { value: '{"tags_has":["review"]}' },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => {
      expect(backend.writePrompt).toHaveBeenCalledWith(
        expect.objectContaining({
          match_when: { tags_has: ["review"] },
        }),
      );
    });
    delete window.__SUPER_DOLPHIN_PROMPT_DEBUG__;
  });

  it("does not render the sections panel in the prompt editor", async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));

    const editor = await screen.findByRole("dialog", { name: "编辑提示词" });
    expect(within(editor).queryByText("提示词分段")).not.toBeInTheDocument();
    expect(
      within(editor).getByLabelText("AI 使用时怎么做"),
    ).toBeInTheDocument();
    expect(backend.listPromptSections).not.toHaveBeenCalled();
  });

  it("does not render a top-right close button in the prompt editor", async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));

    const editor = await screen.findByRole("dialog", { name: "编辑提示词" });
    expect(
      within(editor).queryByLabelText("关闭编辑器"),
    ).not.toBeInTheDocument();
    expect(
      within(editor).getByRole("button", { name: "取消" }),
    ).toBeInTheDocument();
    expect(
      within(editor).getByRole("button", { name: "保存" }),
    ).toBeInTheDocument();
  });
});
