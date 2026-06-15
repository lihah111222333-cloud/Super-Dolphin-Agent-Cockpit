package intent

import "strings"

var providerIdentityTerms = []string{
	"you are claude",
	"你是 claude",
	"你是claude",
	"claude code",
	"you are cursor",
	"你是 cursor",
	"你是cursor",
	"lovable",
	"chatgpt",
	"codex cli",
	"operate exclusively in trae",
}

var externalToolProtocolTerms = []string{
	"bash tool",
	"bash tools",
	"bash and edit tools",
	"edit tool",
	"edit tools",
	"cursor rules",
	"mcp__",
	"read_file",
	"write_file",
	"str_replace_editor",
	"computer use",
}

var externalSystemPromptTerms = []string{
	"you are claude",
	"you are cursor",
	"you are chatgpt",
	"you are codex",
	"you are an ai assistant",
	"you are a powerful agentic ai coding assistant",
	"operate exclusively in trae",
	"system prompt",
	"developer message",
	"developer instructions",
	"persona prompt",
	"provider prompt",
	"你是 claude",
	"你是claude",
	"你是 cursor",
	"你是cursor",
	"你是 chatgpt",
	"你是chatgpt",
}

var overbroadScopeTerms = []string{
	"always",
	"everything",
	"all tasks",
	"every task",
	"any request",
	"所有任务",
	"任何任务",
	"所有请求",
	"总是",
}

// SafetyIssues 处理safetyissues。
func SafetyIssues(kind Kind, rawInput string, card Card) []Issue {
	rawText := normalizePromptIntentText(rawInput)
	cardText := normalizePromptIntentText(strings.Join([]string{
		card.Title,
		card.Summary,
		card.WhenToUse,
		card.WhenNotToUse,
		strings.Join(card.Workflow, "\n"),
		strings.Join(card.Constraints, "\n"),
		card.Output,
		card.RecallTopic,
		card.RecallBody,
		card.DefaultRuleBody,
		strings.Join(card.HitExamples, "\n"),
		strings.Join(card.MissExamples, "\n"),
	}, "\n"))

	var issues []Issue
	if compactRuneLen(rawInput) < 12 {
		issues = append(issues, Issue{Code: "input_too_short", Severity: "block", Message: "输入太短，无法判断用途"})
	}
	if promptIntentLooksLikeExternalSystemPrompt(rawText) {
		issues = append(issues, externalSystemPromptIssues(kind, cardText)...)
	}
	if kind != KindRecall && promptIntentContainsProviderIdentity(cardText) {
		issues = append(issues, Issue{Code: "identity_pollution", Severity: "block", Message: "专家能力/默认规则不能包含模型或供应商身份声明"})
	}
	if kind != KindRecall && promptIntentContainsExternalToolProtocol(cardText) {
		issues = append(issues, Issue{Code: "tool_protocol_pollution", Severity: "block", Message: "专家能力/默认规则不能包含外部工具协议"})
	}
	if promptIntentLooksOverbroad(cardText) {
		issues = append(issues, Issue{Code: "overbroad_scope", Severity: "review", Message: "适用范围过宽，需要收窄"})
	}
	return issues
}

func externalSystemPromptIssues(kind Kind, cardText string) []Issue {
	switch kind {
	case KindDefaultRule:
		if promptIntentDefaultRuleStillLooksExternal(cardText) {
			return []Issue{{Code: "external_system_prompt", Severity: "block", Message: "外部 system/provider/persona prompt 不能原文启用为默认规则"}}
		}
		return []Issue{{Code: "external_system_prompt_source", Severity: "review", Message: "外部 system/provider/persona prompt 只能在去身份、去平台、去外部工具协议后提炼为规则"}}
	case KindRecall:
		return []Issue{{Code: "external_system_prompt_source", Severity: "review", Message: "外部 system/provider/persona prompt 只能作为资料保存，启用前需确认授权和来源"}}
	default:
		return []Issue{{Code: "external_system_prompt_source", Severity: "review", Message: "外部 system/provider/persona prompt 只能在去身份、去平台、去外部工具协议后提炼为能力"}}
	}
}

func promptIntentDefaultRuleStillLooksExternal(cardText string) bool {
	return promptIntentLooksLikeExternalSystemPrompt(cardText) ||
		promptIntentContainsProviderIdentity(cardText) ||
		promptIntentContainsExternalToolProtocol(cardText)
}

func promptIntentLooksLikeExternalSystemPrompt(text string) bool {
	return containsAnyPromptIntentTerm(text, externalSystemPromptTerms)
}

func promptIntentContainsProviderIdentity(text string) bool {
	return containsAnyPromptIntentTerm(text, providerIdentityTerms)
}

func promptIntentContainsExternalToolProtocol(text string) bool {
	return containsAnyPromptIntentTerm(text, externalToolProtocolTerms)
}

func promptIntentLooksOverbroad(text string) bool {
	return containsAnyPromptIntentTerm(text, overbroadScopeTerms)
}

func containsAnyPromptIntentTerm(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, normalizePromptIntentText(term)) {
			return true
		}
	}
	return false
}

func normalizePromptIntentText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func compactRuneLen(text string) int {
	count := 0
	for _, r := range text {
		if !isPromptIntentWhitespace(r) {
			count++
		}
	}
	return count
}

func isPromptIntentWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}
