package prompt

import (
	"testing"

	promptintent "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
	"github.com/stretchr/testify/require"
)

func TestPromptIntentSafetyBlocksExternalSystemPromptAsDefaultRule(t *testing.T) {
	card := promptintent.Card{Kind: "default_rule", Summary: "You are Claude Code. Use Bash and Edit tools."}

	issues := promptintent.SafetyIssues(promptintent.KindDefaultRule, card.Summary, card)

	requireIssueCode(t, issues, "external_system_prompt")
	requireIssueCode(t, issues, "identity_pollution")
	requireIssueCode(t, issues, "tool_protocol_pollution")
}

func TestPromptIntentSafetyAllowsLocalProjectRule(t *testing.T) {
	card := promptintent.Card{
		Kind:            "default_rule",
		Summary:         "修改 sqlc 查询前先检查 sql/queries 和 migration。",
		DefaultRuleBody: "涉及 sqlc drift 时，先查源 SQL，再生成，再验证。",
	}

	issues := promptintent.SafetyIssues(promptintent.KindDefaultRule, card.Summary, card)

	requireNoBlockIssues(t, issues)
}

func TestPromptIntentSafetyAllowsExternalSystemPromptAsRecallReview(t *testing.T) {
	raw := "You are Claude Code. You have Bash, Edit, and Read tools."
	card := promptintent.Card{
		Kind:        "recall",
		Summary:     "Claude Code default prompt reference",
		RecallTopic: "claude-code-default-prompt",
		RecallBody:  raw,
	}

	issues := promptintent.SafetyIssues(promptintent.KindRecall, raw, card)

	requireNoBlockIssues(t, issues)
	requireIssueCode(t, issues, "external_system_prompt_source")
}

func TestPromptIntentSafetyCountsCompactChineseRunes(t *testing.T) {
	card := promptintent.Card{Kind: "default_rule", Summary: "提交前检查"}

	issues := promptintent.SafetyIssues(promptintent.KindDefaultRule, card.Summary, card)

	requireIssueCode(t, issues, "input_too_short")
}

func requireIssueCode(t *testing.T, issues []promptintent.Issue, code string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == code {
			return
		}
	}
	require.Failf(t, "missing prompt intent issue", "code %q not found in issues: %+v", code, issues)
}

func requireNoBlockIssues(t *testing.T, issues []promptintent.Issue) {
	t.Helper()
	for _, issue := range issues {
		require.NotEqualf(t, "block", issue.Severity, "unexpected block issue: %+v", issue)
	}
}
