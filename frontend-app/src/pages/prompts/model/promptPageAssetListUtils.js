import {
  PROMPT_ISSUE_COPY,
  dashboardPromptListResponseSchema,
  promptListResponseSchema,
} from "./promptPageViewSchemas.js";
import {
  firstPresentText,
  textValue,
  textValues,
} from "./promptPageTextUtils.js";

export function cleanPromptTags(tags) {
  return tags.filter(
    (tag) =>
      !tag.startsWith("intent:") &&
      tag !== "scope.global" &&
      !tag.startsWith("scope.cwd:") &&
      !tag.startsWith("scope://"),
  );
}

export function isReadonlyFallbackListError(error) {
  const message = firstPresentText(error?.message, error).toLowerCase();
  return (
    error?.code === -32601 ||
    message.includes("method not found") ||
    message.includes("not registered") ||
    message.includes("unknown method") ||
    message.includes("not implemented") ||
    message.includes("unimplemented")
  );
}

export function promptAssetType(tags) {
  if (tags.includes("intent:recall")) return "recall";
  if (tags.includes("intent:default_rule")) return "default_rule";
  return "expert";
}

export function promptPreviewText(item) {
  return (
    item.content ||
    item.whenToUse ||
    item.description ||
    "已保存，AI 会在相关场景中使用"
  );
}

export function promptTextList(value) {
  return textValues(value);
}

export function promptIssueMessage(issue) {
  const code = textValue(issue?.code);
  return PROMPT_ISSUE_COPY[code] || textValue(issue?.message) || code;
}

export function normalizePromptIssues(raw) {
  if (!Array.isArray(raw)) return [];
  const issues = [];
  for (const issue of raw) {
    const normalizedIssue = {
      code: textValue(issue?.code),
      severity:
        textValue(issue?.severity).toLowerCase() === "block"
          ? "block"
          : "review",
      message: promptIssueMessage(issue),
    };
    if (normalizedIssue.message) issues.push(normalizedIssue);
  }
  return issues;
}

export function normalizePromptItem(raw) {
  const tags = raw.tags;
  const assetType = promptAssetType(tags);
  const item = {
    id: raw.id,
    name: raw.name,
    content: raw.content,
    description: raw.description,
    whenToUse: raw.when_to_use,
    agentType: raw.agentType,
    priority: raw.priority,
    enabled: raw.enabled,
    scope: raw.scope,
    tags: cleanPromptTags(tags),
    assetType,
    state: raw.state,
    draftKey: raw.draft_key,
    draftStatus: raw.draft_status,
    card: raw.card,
    issues: raw.issues,
    matchWhen: raw.match_when,
  };
  item.isPendingDraft =
    item.state === "pending_confirm" ||
    Boolean(item.draftKey && item.draftStatus === "ready_to_save");
  item.preview = promptPreviewText(item);
  return item;
}

export function normalizePromptList(response) {
  return promptListResponseSchema
    .parse(response)
    .prompts.map(normalizePromptItem);
}

export function normalizeDashboardPromptItem(raw) {
  const tags = raw.tags;
  const item = {
    id: raw.prompt_key,
    name: raw.title,
    content: raw.prompt_text,
    description: raw.description,
    whenToUse: raw.when_to_use,
    agentType: raw.agent_key,
    priority: raw.priority,
    enabled: raw.enabled,
    scope: tags.includes("scope.global") ? "global" : "project",
    tags: cleanPromptTags(tags),
    assetType: promptAssetType(tags),
    matchWhen: raw.match_when,
    isPendingDraft: false,
  };
  item.preview = promptPreviewText(item);
  return item;
}

export function normalizeDashboardPromptList(response) {
  return dashboardPromptListResponseSchema
    .parse(response)
    .prompts.map(normalizeDashboardPromptItem);
}

export function promptBucket(item) {
  if (item.isPendingDraft) return "pending";
  return item.assetType === "recall" || item.assetType === "default_rule"
    ? item.assetType
    : "expert";
}

export function canForceLaunchPrompt(item) {
  return (
    promptBucket(item) === "expert" &&
    item.enabled !== false &&
    !item.isPendingDraft
  );
}

export function promptCounts(items) {
  const counts = {
    all: items.length,
    expert: 0,
    recall: 0,
    default_rule: 0,
    pending: 0,
  };
  items.forEach((item) => {
    const bucket = promptBucket(item);
    counts[bucket] = (counts[bucket] || 0) + 1;
  });
  return counts;
}
