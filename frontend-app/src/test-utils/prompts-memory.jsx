import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, vi } from "vitest";
import { normalizeMemorySnapshot as normalizeMemorySnapshotForFacade } from "../adapters/memoryAdapter.js";

function mockPromptAssetWorkflow(ctx) {
  const { backend, canonicalPromptRPCItem } = ctx;
  backend.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "~/.codex",
        "settings.provider.codex.codexInstanceKey": "default",
        "settings.provider.codex.codexModelProvider": "openrouter",
      }[key] ?? null,
    ),
  );
  let prompts = [
    canonicalPromptRPCItem({
      id: "main/reviewer",
      name: "代码审查专家",
      content: "先检查阻塞问题",
      description: "审查代码质量",
      when_to_use: "Use for code review.",
      agentType: "coder",
      tags: ["intent:expert", "review"],
      scope: "project",
      enabled: true,
    }),
    canonicalPromptRPCItem({
      id: "intent/recall/ready",
      draft_key: "intent/recall/ready",
      name: "价格表资料",
      content: "价格资料内容",
      description: "待确认的资料",
      tags: ["intent:recall", "pricing"],
      scope: "project",
      enabled: false,
      state: "pending_confirm",
      draft_status: "ready_to_save",
      card: {
        kind: "recall",
        title: "价格表资料",
        summary: "待确认的资料",
        output: "价格资料内容",
      },
    }),
  ];
  backend.listPromptAssets.mockImplementation(() =>
    Promise.resolve({ prompts }),
  );
  backend.writePrompt.mockImplementation(({ id, name, content }) => {
    prompts = prompts.map((item) =>
      item.id === id ? { ...item, name, content } : item,
    );
    return Promise.resolve({ prompt: { id } });
  });
  backend.deletePrompt.mockImplementation(({ id }) => {
    prompts = prompts.filter((item) => item.id !== id);
    return Promise.resolve({ deleted: true });
  });
  backend.draftPromptIntent.mockResolvedValue({
    draft_key: "intent/expert/review",
    kind: "expert",
    scope: "project",
    status: "review",
    card: {
      kind: "expert",
      title: "代码风险审查",
      summary: "识别阻塞风险",
      output: "先列阻塞问题，再给修改建议",
      hit_examples: ["审查这段代码"],
      miss_examples: ["解释一个概念"],
    },
    issues: [],
  });
  backend.commitPromptIntent.mockResolvedValue({
    prompt: { id: "main/code-risk-review" },
  });
}

async function openPromptAssetsPage(ctx) {
  const { App, waitForBackendThreadHeading } = ctx;
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText("提示词"));
  expect(await screen.findByText("代码审查专家")).toBeInTheDocument();
}

async function openPromptWizardFromPendingCard(cardName = "价格表资料") {
  const pendingCard = (await screen.findByText(cardName)).closest("article");
  const continueButton = within(pendingCard).getByRole("button", {
    name: "继续确认",
  });
  fireEvent.click(continueButton);
  const wizard = await screen.findByRole("dialog", {
    name: "添加给 AI 的内容",
  });
  return { continueButton, pendingCard, wizard };
}

async function editAndDeleteReviewerPrompt(ctx) {
  const { backend } = ctx;
  const card = screen.getByText("代码审查专家").closest("article");
  backend.getPrompt.mockResolvedValueOnce({
    prompt: { content: "完整审查提示词" },
  });
  fireEvent.click(within(card).getByRole("button", { name: "复制" }));
  await waitFor(() => {
    expect(backend.getPrompt).toHaveBeenCalledWith({
      cwd: "/repo/app",
      id: "main/reviewer",
    });
    expect(backend.copyTextToClipboard).toHaveBeenCalledWith("完整审查提示词");
  });
  expect(await screen.findByText("已复制提示词内容")).toBeInTheDocument();
  fireEvent.click(within(card).getByRole("button", { name: "编辑" }));
  const editor = await screen.findByRole("dialog", { name: "编辑提示词" });
  expect(editor).toBeInTheDocument();
  expect(within(editor).getByText("可用范围：这个项目")).toBeInTheDocument();
  expect(within(editor).getByLabelText("保存后 AI 会看到什么")).toHaveValue(
    "先检查阻塞问题",
  );
  expect(within(editor).queryByLabelText("Agent Key")).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText("场景标签")).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText("排序权重")).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("名称"), {
    target: { value: "代码风险审查" },
  });
  fireEvent.change(screen.getByLabelText("AI 使用时怎么做"), {
    target: { value: "先列阻塞问题，再给修改建议" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await waitFor(() => {
    expect(backend.writePrompt).toHaveBeenCalledWith(
      expect.objectContaining({
        cwd: "/repo/app",
        id: "main/reviewer",
        name: "代码风险审查",
        agentType: "coder",
        content: "先列阻塞问题，再给修改建议",
        scope: "project",
        enabled: true,
      }),
    );
  });

  await screen.findByText("代码风险审查");
}

async function handlePendingPromptDraft(ctx) {
  const { backend } = ctx;
  const { pendingCard, wizard: pendingDialog } =
    await openPromptWizardFromPendingCard("价格表资料");
  expect(screen.getAllByText("价格表资料").length).toBeGreaterThanOrEqual(1);
  fireEvent.click(
    within(pendingDialog).getAllByRole("button", { name: "关闭" }).at(-1),
  );

  fireEvent.click(within(pendingCard).getByRole("button", { name: "丢弃" }));
  await waitFor(() => {
    expect(backend.discardPromptIntent).toHaveBeenCalledWith({
      cwd: "/repo/app",
      draftKey: "intent/recall/ready",
    });
  });
}

async function createGeneratedPromptIntent(ctx) {
  const { backend } = ctx;
  const { wizard } = await openPromptWizardFromPendingCard("价格表资料");
  fireEvent.click(within(wizard).getByRole("tab", { name: "专家能力" }));
  fireEvent.change(screen.getByLabelText("写下希望 AI 记住或使用的内容"), {
    target: { value: "当用户要求代码审查时，先检查阻塞问题。" },
  });
  expect(
    screen.queryByRole("button", { name: "整理草稿" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "帮我生成" }));
  expect(await screen.findByText("代码风险审查")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "确认保存" }));
  await waitFor(() => {
    expect(backend.draftPromptIntent).toHaveBeenCalledWith({
      cwd: "/repo/app",
      kind: "expert",
      rawInput: "当用户要求代码审查时，先检查阻塞问题。",
      sourceType: "user_input",
      scope: "project",
      provider: "codex",
      model: "gpt-5.5",
      codexModelProvider: "openrouter",
    });
    expect(backend.commitPromptIntent).toHaveBeenCalledWith({
      cwd: "/repo/app",
      draftKey: "intent/expert/review",
      scope: "project",
    });
  });
}

function createSimilaritySnapshots() {
  const group = {
    nameA: "A",
    targetA: "private",
    pathA: "feedback/a.md",
    nameB: "B",
    targetB: "team",
    pathB: "feedback/b.md",
    score: 0.88,
  };
  // 与真实 facade 输出一致：parse + transform 后的扁平 { overview, entries } 形态。
  const snapshotWithSimilar = normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      projectRoot: "/repo/app",
      health: {
        preferenceCount: 2,
        projectCount: 0,
        maxPerCategory: 15,
        similarGroups: [group],
      },
    },
    private: { entries: [] },
    team: { entries: [] },
  });
  const snapshotWithoutSimilar = {
    ...snapshotWithSimilar,
    overview: {
      ...snapshotWithSimilar.overview,
      health: {
        ...snapshotWithSimilar.overview.health,
        similarGroups: [],
      },
    },
  };
  return { snapshotWithSimilar, snapshotWithoutSimilar };
}

async function openMemoryCenterWithSimilarity(ctx) {
  const { App, waitForBackendThreadHeading } = ctx;
  render(<App />);
  await waitForBackendThreadHeading();
  await waitFor(() => {
    expect(
      screen.getByLabelText("记忆中心").querySelector("i"),
    ).toHaveAttribute("title", "1 条待整合相似记忆");
  });

  fireEvent.click(screen.getByLabelText("记忆中心"));
  expect(await screen.findByText("1 组条目内容相似")).toBeInTheDocument();
}

async function runConsolidationUntilSimilaritiesClear(ctx, clearSimilarities) {
  const { backend } = ctx;
  vi.useFakeTimers();
  try {
    fireEvent.click(screen.getByRole("button", { name: "一键整合全部" }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(backend.startConsolidateMemorySimilarities).toHaveBeenCalledWith(
      expect.objectContaining({
        cwd: "/repo/app",
        provider: "codex",
        codexModelProvider: "openai",
      }),
    );
    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledWith({
      cwd: "/repo/app",
      jobId: "memory-job-live",
    });
    expect(screen.getByRole("button", { name: "后台整合中" })).toBeDisabled();

    clearSimilarities();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
    await waitFor(() => {
      expect(screen.getByText("已整合 1 组")).toBeInTheDocument();
      expect(backend.getMemorySnapshot).toHaveBeenLastCalledWith({
        cwd: "/repo/app",
      });
    });
  } finally {
    vi.useRealTimers();
  }
}

function expectSimilarityWarningCleared() {
  expect(screen.queryByText("1 组条目内容相似")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "一键整合全部" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("已整合 1 组")).toBeInTheDocument();
  expect(screen.getByLabelText("记忆中心").querySelector("i")).toBeNull();
}

export function createPromptsMemoryFactory(ctx) {
  return {
    mockPromptAssetWorkflow: () => mockPromptAssetWorkflow(ctx),
    openPromptAssetsPage: () => openPromptAssetsPage(ctx),
    openPromptWizardFromPendingCard,
    editAndDeleteReviewerPrompt: () => editAndDeleteReviewerPrompt(ctx),
    handlePendingPromptDraft: () => handlePendingPromptDraft(ctx),
    createGeneratedPromptIntent: () => createGeneratedPromptIntent(ctx),
    createSimilaritySnapshots,
    openMemoryCenterWithSimilarity: () => openMemoryCenterWithSimilarity(ctx),
    runConsolidationUntilSimilaritiesClear: (clearSimilarities) =>
      runConsolidationUntilSimilaritiesClear(ctx, clearSimilarities),
    expectSimilarityWarningCleared,
  };
}
