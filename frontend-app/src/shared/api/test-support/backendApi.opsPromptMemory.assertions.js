import { expect } from "vitest";
import { RPC_METHODS } from "../backendApi.js";

export function expectMemoryCenterCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_GET, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_GET, {
    cwd: "/repo/app",
    target: "private",
    path: "feedback/tdd.md",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, {
    cwd: "/repo/app",
    target: "private",
    existingPath: "feedback/tdd.md",
    name: "tdd-rule",
    description: "先写红测",
    type: "feedback",
    content: "规则",
    title: "遵守 TDD",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, {
    cwd: "/repo/app",
    target: "private",
    path: "feedback/tdd.md",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT, {
    cwd: "/repo/app",
    enabled: true,
  });
  expectMemorySimilarityCalls(callAPI);
}

export function expectMemoryCenterValidation(api) {
  expect(() => api.getMemoryEntry({ cwd: "/repo/app", path: "" })).toThrow("path is required");
  expect(() =>
    api.upsertMemoryEntry({
      cwd: "/repo/app",
      name: "x",
      description: "d",
      type: "feedback",
      content: "",
    }),
  ).toThrow("content is required");
  expect(() => api.setMemoryAutoDreamIntent({ enabled: true })).toThrow("cwd is required");
  expect(() => api.setMemoryAutoDreamIntent({})).toThrow("enabled is required");
  expect(() =>
    api.mergeMemoryEntries({
      cwd: "/repo/app",
      targetA: "private",
      pathA: "a.md",
      targetB: "team",
    }),
  ).toThrow("pathB is required");
}

export function expectMemorySimilarityCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_MERGE, {
    cwd: "/repo/app",
    targetA: "private",
    pathA: "a.md",
    targetB: "team",
    pathB: "b.md",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE, {
    cwd: "/repo/app",
    targetA: "private",
    pathA: "a.md",
    targetB: "team",
    pathB: "b.md",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL, { cwd: "/repo/app" });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START, {
    cwd: "/repo/app",
    provider: "codex",
    model: "gpt-5.5",
    model_provider: "openai",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, {
    cwd: "/repo/app",
    jobId: "memory-job-1",
  });
}

export function expectPromptFacadeCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_ASSETS_LIST, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_PROMPTS, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_GET, {
    cwd: "/repo/app",
    id: "main/reviewer",
  });
  expectPromptWriteCall(callAPI);
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_DELETE, {
    cwd: "/repo/app",
    id: "main/reviewer",
    scope: "global",
  });
  expectPromptIntentFacadeCalls(callAPI);
}

export function expectPromptFacadeValidation(api) {
  expect(() => api.listPromptAssets({ cwd: "" })).toThrow("cwd is required");
  expect(() => api.getPrompt({ cwd: "/repo/app", id: "" })).toThrow("id is required");
  expect(() => api.writePrompt({ cwd: "/repo/app", name: "" })).toThrow("name is required");
  expect(() => api.writePrompt({ cwd: "/repo/app", name: "Missing identity" })).toThrow("id or key is required");
  expect(() => api.commitPromptIntent({ cwd: "/repo/app", draftKey: "" })).toThrow("draft_key is required");
  expect(() => api.dryRunPromptIntent({ cwd: "/repo/app", draftKey: "d1", question: "" })).toThrow(
    "question is required",
  );
  expect(() => api.getPersonalizationProfile({ cwd: "" })).toThrow("cwd is required");
  expect(() => api.savePersonalizationProfile({ cwd: "", profile: {} })).toThrow("cwd is required");
  expect(() => api.savePersonalizationProfile({ cwd: "/repo/app", profile: null })).toThrow(
    "profile must be an object",
  );
}

export function expectPromptIntentFacadeCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PERSONALIZATION_PROFILE_GET, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PERSONALIZATION_PROFILE_SAVE, {
    cwd: "/repo/app",
    profile: {
      displayName: " 小海 ",
      role: "后端工程师",
      background: "熟悉 Go",
      customInstructions: "回答要直接",
    },
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DRAFT, {
    cwd: "/repo/app",
    kind: "expert",
    raw_input: "当用户要求代码审查时使用。",
    source_type: "user_input",
    provider: "codex",
    model: "gpt-5.5",
    model_provider: "openrouter",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_COMMIT, {
    cwd: "/repo/app",
    draft_key: "intent/expert/review",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DRAFT, {
    cwd: "/repo/app",
    kind: "expert",
    raw_input: "全局使用这条提示词。",
    source_type: "user_input",
    enable_global: true,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_COMMIT, {
    cwd: "/repo/app",
    draft_key: "intent/expert/global",
    enable_global: true,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DISCARD, {
    cwd: "/repo/app",
    draft_key: "intent/expert/review",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, {
    cwd: "/repo/app",
    draft_key: "intent/expert/review",
    kind: "expert",
    card: { title: "代码审查专家" },
    question: "帮我审查这段代码",
  });
}

export function expectPromptWriteCall(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_WRITE, {
    cwd: "/repo/app",
    id: "main/reviewer",
    name: "代码审查专家",
    description: "审查代码质量",
    agentType: "coder",
    when_to_use: "Use for code review.",
    content: "先检查阻塞问题",
    tags: ["review"],
    enabled: true,
    scope: "global",
    priority: 5,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_WRITE, {
    cwd: "/repo/app",
    id: "project/reviewer",
    name: "Reviewer",
    content: "Check risks first",
    tags: [],
    agentType: "main",
    scope: "project",
  });
}

export async function writePromptFacadePrompt(api) {
  await api.writePrompt({
    cwd: "/repo/app",
    id: "main/reviewer",
    name: "代码审查专家",
    description: "审查代码质量",
    agentType: "coder",
    when_to_use: "Use for code review.",
    content: "先检查阻塞问题",
    tags: ["review"],
    enabled: true,
    scope: "global",
    priority: 5,
  });
}

export async function writePromptFacadePromptWithKey(api) {
  await api.writePrompt({
    cwd: "/repo/app",
    key: "project/reviewer",
    name: "Reviewer",
    content: "Check risks first",
  });
}
