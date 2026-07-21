import { textValue } from "./promptPageTextUtils.js";

function dryRunKindLabel(kind) {
  if (kind === "recall") return "参考资料";
  if (kind === "default_rule") return "默认规则";
  return "专家能力";
}

export function promptDryRunSummary(result, draft) {
  if (!result) return "";
  const wouldUse = Boolean(
    result.would_use ?? result.wouldUse ?? result.matched ?? result.should_use,
  );
  const kind = textValue(
    result.kind || result.action || draft?.kind || "expert",
  );
  if (wouldUse) return `这条内容会参与${dryRunKindLabel(kind)}匹配。`;
  return "这条内容暂不会被当前问题命中。";
}

export function promptDraftHasReviewIssues(draft) {
  return (
    Array.isArray(draft?.issues) &&
    draft.issues.some(
      (issue) => textValue(issue?.severity).toLowerCase() === "review",
    )
  );
}
