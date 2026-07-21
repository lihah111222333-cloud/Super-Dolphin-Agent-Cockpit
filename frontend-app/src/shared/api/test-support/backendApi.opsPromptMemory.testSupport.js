import { writePromptFacadePrompt, writePromptFacadePromptWithKey } from "./backendApi.opsPromptMemory.assertions.js";

export function promptWireItem(overrides = {}) {
  return {
    id: "main/reviewer",
    name: "Reviewer",
    content: "Review carefully.",
    description: "Review prompt",
    agentType: "coder",
    when_to_use: "When reviewing code.",
    createdAt: "2026-07-13T00:00:00Z",
    updatedAt: "2026-07-13T00:00:01Z",
    enabled: true,
    scope: "project",
    tags: ["review"],
    ...overrides,
  };
}

export function promptIntentDraftResponse(overrides = {}) {
  return {
    draft_key: "intent/expert/review",
    requested_kind: "expert",
    inferred_kind: "expert",
    status: "ready_to_save",
    confidence: 0.9,
    scope: "project",
    issues: [],
    card: {
      kind: "expert",
      title: "Review expert",
      summary: "Review code carefully.",
      hit_examples: ["Review this code."],
      miss_examples: [],
    },
    ...overrides,
  };
}

export async function callMemoryCenterApis(api) {
  await api.getMemorySnapshot({ cwd: "/repo/app" });
  await api.getMemoryEntry({
    cwd: "/repo/app",
    target: "private",
    path: "feedback/tdd.md",
  });
  await api.upsertMemoryEntry({
    cwd: "/repo/app",
    target: "private",
    existingPath: "feedback/tdd.md",
    name: "tdd-rule",
    description: "先写红测",
    type: "feedback",
    content: "规则",
    title: "遵守 TDD",
  });
  await api.deleteMemoryEntry({
    cwd: "/repo/app",
    target: "private",
    path: "feedback/tdd.md",
  });
  await api.setMemoryAutoDreamIntent({ cwd: "/repo/app", enabled: true });
  await callMemorySimilarityApis(api);
}

export async function callMemorySimilarityApis(api) {
  await api.mergeMemoryEntries({
    cwd: "/repo/app",
    targetA: "private",
    pathA: "a.md",
    targetB: "team",
    pathB: "b.md",
  });
  await api.ignoreMemorySimilarity({
    cwd: "/repo/app",
    targetA: "private",
    pathA: "a.md",
    targetB: "team",
    pathB: "b.md",
  });
  await api.consolidateMemorySimilarities({ cwd: "/repo/app" });
  await api.startConsolidateMemorySimilarities({
    cwd: "/repo/app",
    provider: "codex",
    model: "gpt-5.5",
    codexModelProvider: "openai",
  });
  await api.getMemoryConsolidationStatus({
    cwd: "/repo/app",
    jobId: "memory-job-1",
  });
}

export async function callPromptFacadeMethods(api) {
  await api.listPromptAssets({ cwd: "/repo/app" });
  await api.getDashboardPrompts({ cwd: "/repo/app" });
  await api.getPrompt({ cwd: "/repo/app", id: "main/reviewer" });
  await writePromptFacadePrompt(api);
  await writePromptFacadePromptWithKey(api);
  await api.deletePrompt({
    cwd: "/repo/app",
    id: "main/reviewer",
    scope: "global",
  });
  await callPromptIntentFacadeMethods(api);
}

export async function callPromptIntentFacadeMethods(api) {
  await api.getPersonalizationProfile({ cwd: "/repo/app" });
  await api.savePersonalizationProfile({
    cwd: "/repo/app",
    profile: {
      displayName: " 小海 ",
      role: "后端工程师",
      background: "熟悉 Go",
      customInstructions: "回答要直接",
    },
  });
  await api.draftPromptIntent({
    cwd: "/repo/app",
    kind: "expert",
    rawInput: "当用户要求代码审查时使用。",
    sourceType: "user_input",
    scope: "project",
    provider: "codex",
    model: "gpt-5.5",
    codexModelProvider: "openrouter",
  });
  await api.commitPromptIntent({
    cwd: "/repo/app",
    draftKey: "intent/expert/review",
  });
  await api.draftPromptIntent({
    cwd: "/repo/app",
    kind: "expert",
    rawInput: "全局使用这条提示词。",
    scope: "global",
  });
  await api.commitPromptIntent({
    cwd: "/repo/app",
    draftKey: "intent/expert/global",
    scope: "global",
  });
  await api.discardPromptIntent({
    cwd: "/repo/app",
    draft_key: "intent/expert/review",
  });
  await api.dryRunPromptIntent({
    cwd: "/repo/app",
    draftKey: "intent/expert/review",
    kind: "expert",
    card: { title: "代码审查专家" },
    question: "帮我审查这段代码",
  });
}
