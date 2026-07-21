import { firstText, textValue } from "./promptPageTextUtils.js";
import {
  normalizePromptIssues,
  promptTextList,
} from "./promptPageAssetListUtils.js";

export function normalizeDraftItem(
  raw = {},
  fallbackKind = "expert",
  meta = {},
) {
  const card = raw.card && typeof raw.card === "object" ? raw.card : {};
  const workflow = promptTextList(card.workflow);
  const hitExamples = promptTextList(card.hit_examples || card.hitExamples);
  const missExamples = promptTextList(card.miss_examples || card.missExamples);
  return {
    draftKey: firstText(raw.draft_key, raw.draftKey),
    kind: firstText(
      raw.inferred_kind,
      raw.inferredKind,
      raw.kind,
      card.kind,
      meta.inferredKind,
      fallbackKind,
    ),
    scope: firstText(raw.scope, card.scope, "project"),
    status: firstText(raw.status, "review"),
    title: firstText(card.title, raw.title, "未命名草稿"),
    summary: firstText(card.summary, raw.description),
    whenToUse: firstText(card.when_to_use, card.whenToUse),
    whenNotToUse: firstText(card.when_not_to_use, card.whenNotToUse),
    workflow,
    saveBoundary: firstText(card.save_boundary, card.saveBoundary),
    output: firstText(
      card.output,
      card.recall_body,
      card.recallBody,
      card.default_rule_body,
      card.defaultRuleBody,
      raw.content,
    ),
    hitExamples,
    missExamples,
    card,
    issues: normalizePromptIssues(raw.issues),
  };
}

export function normalizeDraft(raw = {}, fallbackKind = "expert") {
  const meta = { inferredKind: firstText(raw.inferred_kind, raw.inferredKind) };
  if (Array.isArray(raw.drafts) && raw.drafts.length > 0) {
    const draftOptions = raw.drafts.map((item) =>
      normalizeDraftItem(item, fallbackKind, meta),
    );
    return { ...draftOptions[0], draftOptions };
  }
  return normalizeDraftItem(raw, fallbackKind, meta);
}

export function pendingDraftFromItem(item) {
  return normalizeDraft(
    {
      draft_key: item.draftKey || item.id,
      kind: item.assetType || "expert",
      scope: item.scope || "project",
      status: item.draftStatus || "ready_to_save",
      card: item.card || {
        kind: item.assetType || "expert",
        title: item.name,
        summary: item.description,
        output: item.content,
        hit_examples: [],
        miss_examples: [],
      },
      issues: Array.isArray(item.issues) ? item.issues : [],
    },
    item.assetType || "expert",
  );
}

export function promptDraftNeedsRevision(draft) {
  const status = textValue(draft?.status).toLowerCase();
  const hasBlockIssue =
    Array.isArray(draft?.issues) &&
    draft.issues.some(
      (issue) => textValue(issue?.severity).toLowerCase() === "block",
    );
  return (
    status === "draft" ||
    status === "draft_blocked" ||
    status === "blocked" ||
    hasBlockIssue
  );
}
