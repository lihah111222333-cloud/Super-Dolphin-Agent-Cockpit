// Package intent 负责提示词意图识别与草稿质量校验，包含安全检查、规范化、去重、来源事实提取和提交流程。
package intent

import "strings"

// providerIdentityTerms 是表示 provider/模型身份的关键词，用于检测内容是否含有身份污染。
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

// externalToolProtocolTerms 是外部工具协议关键词，检测内容是否引用了平台专属工具。
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

// externalSystemPromptTerms 是外部 system/provider/persona prompt 特征词，用于判断输入是否来自外部提示词。
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

// overbroadScopeTerms 是适用范围过宽的关键词，触发 review 级别问题提示。
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

// SafetyIssues 对用户原始输入和生成的草稿卡片执行安全校验，返回所有安全问题列表。
// 输入太短、包含外部身份或工具协议时直接 block；适用范围过宽则标记为 review。
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

// externalSystemPromptIssues 根据 kind 决定外部 system prompt 的问题级别：
// DefaultRule 若卡片内容仍含外部特征则 block，否则 review；Recall 只 review；其余类型 review。
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

// promptIntentDefaultRuleStillLooksExternal 判断 default_rule 卡片内容是否仍含外部特征，三项任一命中即视为未脱敏。
func promptIntentDefaultRuleStillLooksExternal(cardText string) bool {
	return promptIntentLooksLikeExternalSystemPrompt(cardText) ||
		promptIntentContainsProviderIdentity(cardText) ||
		promptIntentContainsExternalToolProtocol(cardText)
}

// promptIntentLooksLikeExternalSystemPrompt 判断文本是否包含外部系统提示词特征词。
func promptIntentLooksLikeExternalSystemPrompt(text string) bool {
	return containsAnyPromptIntentTerm(text, externalSystemPromptTerms)
}

// promptIntentContainsProviderIdentity 判断文本是否含有模型/供应商身份声明。
func promptIntentContainsProviderIdentity(text string) bool {
	return containsAnyPromptIntentTerm(text, providerIdentityTerms)
}

// promptIntentContainsExternalToolProtocol 判断文本是否含有外部工具协议关键词。
func promptIntentContainsExternalToolProtocol(text string) bool {
	return containsAnyPromptIntentTerm(text, externalToolProtocolTerms)
}

// promptIntentLooksOverbroad 判断文本是否含有适用范围过宽的关键词。
func promptIntentLooksOverbroad(text string) bool {
	return containsAnyPromptIntentTerm(text, overbroadScopeTerms)
}

// containsAnyPromptIntentTerm 检查文本是否包含词表中的任意一项（大小写不敏感）。
func containsAnyPromptIntentTerm(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, normalizePromptIntentText(term)) {
			return true
		}
	}
	return false
}

// normalizePromptIntentText 将文本统一转为小写并去除首尾空白，用于大小写不敏感的关键词匹配。
func normalizePromptIntentText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

// compactRuneLen 返回去除所有空白字符后的 rune 数量，用于判断有效内容长度。
func compactRuneLen(text string) int {
	count := 0
	for _, r := range text {
		if !isPromptIntentWhitespace(r) {
			count++
		}
	}
	return count
}

// isPromptIntentWhitespace 判断 rune 是否为空白字符。
func isPromptIntentWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}
