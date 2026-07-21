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

function mockPendingDraftPrompt(overrides = {}) {
  const name = overrides.name || "代码审查专家";
  const content = overrides.content || "先检查阻塞问题";
  const scope = overrides.scope || "project";
  backend.listPromptAssets.mockResolvedValue({
    prompts: [
      {
        id: overrides.id || "draft/reviewer",
        name,
        description: overrides.description || "",
        content,
        agentType: "main",
        when_to_use: "",
        createdAt: "2026-07-11T00:00:00Z",
        updatedAt: "2026-07-11T00:00:00Z",
        draft_key: overrides.draftKey || "intent/expert/review",
        draft_status: overrides.status || "ready_to_save",
        state: "pending_confirm",
        tags: overrides.tags || ["intent:expert"],
        scope,
        enabled: true,
        card: overrides.card || {
          kind: "expert",
          scope,
          title: name,
          output: content,
          hit_examples: [],
          miss_examples: [],
        },
        issues: overrides.issues || [],
      },
    ],
  });
}

async function openPendingDraftWizard(overrides) {
  mockPendingDraftPrompt(overrides);
  renderPromptPage();
  fireEvent.click(await screen.findByRole("button", { name: "继续确认" }));
  return screen.findByRole("dialog", { name: "添加给 AI 的内容" });
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

describe("PromptPageView wizard interactions", () => {
  it("runs prompt intent dry-run from the confirmation wizard without exposing routing internals", async () => {
    backend.dryRunPromptIntent.mockResolvedValueOnce({
      would_use: true,
      action: "expert",
      reasons: ["question provided: 如何审查这段代码？", "matched"],
    });

    await openPendingDraftWizard({
      draftKey: "intent/expert/review",
      name: "代码审查专家",
      content: "先检查阻塞问题",
    });

    fireEvent.click(screen.getByText("试问验证"));
    fireEvent.change(screen.getByLabelText("试问问题"), {
      target: { value: "如何审查这段代码？" },
    });
    fireEvent.click(screen.getByRole("button", { name: "验证" }));

    await waitFor(() => {
      expect(backend.dryRunPromptIntent).toHaveBeenCalledWith({
        cwd: "/repo/app",
        draftKey: "intent/expert/review",
        kind: "expert",
        card: expect.objectContaining({
          title: "代码审查专家",
          output: "先检查阻塞问题",
        }),
        question: "如何审查这段代码？",
      });
    });
    expect(
      await screen.findByText(/这条内容会参与专家能力匹配/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/question provided/)).not.toBeInTheDocument();
  });

  it("does not render a duplicate top-right close button in the prompt intent wizard", async () => {
    const wizard = await openPendingDraftWizard();
    expect(
      within(wizard).getAllByRole("button", { name: "关闭" }),
    ).toHaveLength(1);
  });

  it("exposes wizard scope as radios and submits the selected draft scope", async () => {
    const wizard = await openPendingDraftWizard();
    const scopeGroup = within(wizard).getByRole("radiogroup", {
      name: "草稿范围",
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
    fireEvent.change(
      within(wizard).getByLabelText("写下希望 AI 记住或使用的内容"),
      {
        target: { value: "跨项目都要使用这条审查规则。" },
      },
    );
    fireEvent.click(within(wizard).getByRole("button", { name: "帮我生成" }));

    await waitFor(() => {
      expect(backend.draftPromptIntent).toHaveBeenCalledWith(
        expect.objectContaining({
          rawInput: "跨项目都要使用这条审查规则。",
          scope: "global",
        }),
      );
    });
  });

  it("shows a waiting reminder and allows closing while prompt intent generation is still running", async () => {
    backend.draftPromptIntent.mockImplementationOnce(
      () => new Promise(() => {}),
    );

    await openPendingDraftWizard();
    fireEvent.change(screen.getByLabelText("写下希望 AI 记住或使用的内容"), {
      target: { value: "请帮我整理一个需要较长时间生成的专家能力。" },
    });
    fireEvent.click(screen.getByRole("button", { name: "帮我生成" }));

    const wizard = await screen.findByRole("dialog", {
      name: "添加给 AI 的内容",
    });
    expect(
      within(wizard).getByText("正在整理内容，可能需要一点时间。"),
    ).toBeInTheDocument();
    const closeButton = within(wizard).getByRole("button", { name: "关闭" });
    expect(closeButton).toBeEnabled();

    fireEvent.click(closeButton);
    expect(
      screen.queryByRole("dialog", { name: "添加给 AI 的内容" }),
    ).not.toBeInTheDocument();
  });

  it("saves a ready prompt intent draft and refreshes the prompt list", async () => {
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    await openPendingDraftWizard({
      draftKey: "intent/expert/ready",
      name: "代码审查专家",
      content: "先检查阻塞问题",
    });

    fireEvent.click(screen.getByRole("button", { name: "确认保存" }));

    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({
        cwd: "/repo/app",
        draftKey: "intent/expert/ready",
        scope: "project",
      });
    });
    await waitFor(() => {
      expect(
        screen.queryByRole("dialog", { name: "添加给 AI 的内容" }),
      ).not.toBeInTheDocument();
    });
    expect(
      await screen.findByText("已保存，可在新对话中被 AI 发现和使用"),
    ).toBeInTheDocument();
  });

  it("keeps a prompt intent draft open when commit response validation rejects", async () => {
    backend.commitPromptIntent.mockRejectedValueOnce(
      new TypeError(
        "ui/prompt-intents/commit response prompt intent commit response.prompt_key Expected string, received number",
      ),
    );

    await openPendingDraftWizard({
      draftKey: "intent/expert/malformed-commit",
    });
    fireEvent.click(screen.getByRole("button", { name: "确认保存" }));

    const wizard = await screen.findByRole("dialog", {
      name: "添加给 AI 的内容",
    });
    expect(
      await within(wizard).findByText("保存失败，请重试。"),
    ).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("ui/prompt-intents/commit");
    expect(
      within(wizard).getByRole("button", { name: "确认保存" }),
    ).toBeEnabled();
    expect(
      screen.queryByText("已保存，可在新对话中被 AI 发现和使用"),
    ).not.toBeInTheDocument();
  });

  it("requires explicit review confirmation before saving risky prompt intent drafts", async () => {
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    await openPendingDraftWizard({
      draftKey: "intent/expert/risky",
      name: "风险审查专家",
      content: "先检查风险",
      issues: [
        {
          code: "default_rule_conflict",
          severity: "review",
          message: "可能和已有规则冲突",
        },
      ],
    });

    const saveButton = screen.getByRole("button", { name: "确认保存" });
    expect(saveButton).toBeDisabled();
    fireEvent.click(screen.getByLabelText("我已确认这些风险，仍要保存"));
    expect(saveButton).toBeEnabled();
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({
        cwd: "/repo/app",
        draftKey: "intent/expert/risky",
        scope: "project",
        confirmRisk: true,
      });
    });
  });

  it("confirms global scope when saving a global prompt intent draft", async () => {
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    await openPendingDraftWizard({
      draftKey: "intent/expert/global",
      name: "全局审查专家",
      content: "跨项目检查问题",
      scope: "global",
      card: {
        kind: "expert",
        scope: "global",
        title: "全局审查专家",
        output: "跨项目检查问题",
      },
    });

    fireEvent.click(screen.getByRole("button", { name: "确认保存" }));

    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({
        cwd: "/repo/app",
        draftKey: "intent/expert/global",
        scope: "global",
        confirmGlobal: true,
      });
    });
  });
});
