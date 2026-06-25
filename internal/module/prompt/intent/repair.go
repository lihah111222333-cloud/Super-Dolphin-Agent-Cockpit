package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// repairPromptIntentCardsIfNeeded 检查草稿卡片是否存在可修复问题，有则发起一次 dream 调用自动修复并返回修复后的卡片。
func repairPromptIntentCardsIfNeeded(
	ctx context.Context,
	dream contract.DreamExecutor,
	requestedKind Kind,
	rawInput string,
	cards []Card,
	options contract.DreamOptions,
) ([]Card, error) {
	issues := repairablePromptIntentIssues(rawInput, cards)
	if len(issues) == 0 {
		return cards, nil
	}
	prompt, err := buildPromptIntentRepairPrompt(requestedKind, rawInput, cards, issues)
	if err != nil {
		return nil, err
	}
	rawDream, err := executePromptIntentDream(ctx, dream, prompt, options)
	if err != nil {
		return nil, fmt.Errorf("prompt intent repair failed: %w", err)
	}
	repaired, err := parsePromptIntentCards(rawDream)
	if err != nil {
		return nil, fmt.Errorf("prompt intent repair returned invalid JSON: %w", err)
	}
	return repaired, nil
}

// executePromptIntentDream 执行 dream 调用，优先使用带选项接口；不支持时降级到基础接口。
func executePromptIntentDream(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := dream.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return dream.ExecuteDream(ctx, prompt)
}

// repairablePromptIntentIssues 收集所有草稿卡片中可被自动修复的问题，去重后返回。
func repairablePromptIntentIssues(rawInput string, cards []Card) []Issue {
	var issues []Issue
	for _, card := range cards {
		kind, err := normalizeKind(card.Kind)
		if err != nil {
			continue
		}
		for _, issue := range promptIntentDraftIssues(kind, rawInput, card) {
			if promptIntentRepairableIssue(issue.Code) {
				issues = append(issues, issue)
			}
		}
	}
	return dedupePromptIntentRepairIssues(issues)
}

// promptIntentRepairableIssue 判断某 issue code 是否属于可自动修复的类型。
func promptIntentRepairableIssue(code string) bool {
	switch strings.TrimSpace(code) {
	case "missing_title",
		"missing_summary",
		"missing_when_to_use",
		"missing_when_not_to_use",
		"missing_workflow",
		"missing_output",
		"missing_recall_topic",
		"missing_recall_body",
		"missing_default_rule_body",
		"missing_hit_examples",
		"missing_miss_examples",
		"vague_when_to_use",
		"vague_output",
		"missing_save_boundary",
		"missing_source_facts",
		"missing_source_fact_coverage",
		"source_fact_not_applied",
		"external_system_prompt",
		"identity_pollution",
		"tool_protocol_pollution",
		"overbroad_scope":
		return true
	default:
		return false
	}
}

// dedupePromptIntentRepairIssues 对修复问题列表去重，按 code+message 作为唯一键。
func dedupePromptIntentRepairIssues(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	seen := map[string]bool{}
	for _, issue := range issues {
		key := strings.TrimSpace(issue.Code) + "\x00" + strings.TrimSpace(issue.Message)
		if strings.TrimSpace(issue.Code) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, issue)
	}
	return out
}

// buildPromptIntentRepairPrompt 构建自动修复步骤的 LLM prompt，包含原始输入、当前卡片和待修复问题。
func buildPromptIntentRepairPrompt(requestedKind Kind, rawInput string, cards []Card, issues []Issue) (string, error) {
	cardsJSON, err := json.MarshalIndent(cards, "", "  ")
	if err != nil {
		return "", err
	}
	issuesJSON, err := json.MarshalIndent(issues, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`你是 Super-Dolphin 提示词创建助手的自动修复步骤。
你只修复创建期草稿质量问题，不能设计运行时路由器。
严格输出 JSON，不要 Markdown。

输出格式必须和上一轮一致：单一草稿输出一个对象；多个同类型独立资产输出 {"drafts":[对象, ...]}。

修复要求：
1. 只修复 repair_issues 中列出的问题，保留用户原始意图和已经正确的字段。
2. 如果用户选择类型不对，只输出一个最合适的 kind，并在 suggested_alternative 只推荐一个最合适类型；判断后的类型和 requested_kind 一致时必须省略 suggested_alternative；不要给多个类型候选，也不要用多草稿表达类型选择。
3. source_profile 和 source_facts 只用于内部质检，不要照搬到摘要；source_facts 中 preserve/translate 的关键要点必须进入保存内容。
4. 外部 system/provider/persona prompt 必须去身份、去平台、去外部工具协议；不能让 default_rule 包含模型身份、供应商身份或权限扩大描述。
5. 资料类内容必须保留原文关键事实；表格要覆盖主题、字段、关键行、单位、适用范围、可查询问题。
6. 专家能力的 when_to_use、workflow、output、save_boundary 必须具体；涉及保存、记忆、知识沉淀时只能输出建议保存条目，不能声称已经保存。
7. hit_examples 和 miss_examples 必须各至少 1 条，且用普通用户能理解的自然语言。
8. 面向普通用户写自然中文，避免错别字和生硬翻译；外部 prompt 中的 pair programming 应转写为“协作编程”或“编程协作”，不要写成“结对编程”或“结队编程”。

requested_kind: %s

repair_issues:
%s

original_cards:
%s

user_input:
%s`, requestedKind, string(issuesJSON), string(cardsJSON), rawInput), nil
}

// promptIntentSingleTypeCards 当多卡片中存在混合 kind 时，只保留最合适的一张。
func promptIntentSingleTypeCards(requestedKind Kind, rawInput string, cards []Card) []Card {
	if len(cards) <= 1 || !promptIntentHasMixedKinds(cards) {
		return cards
	}
	return []Card{promptIntentBestSingleTypeCard(requestedKind, rawInput, cards)}
}

// promptIntentHasMixedKinds 判断卡片列表中是否存在多种 kind。
func promptIntentHasMixedKinds(cards []Card) bool {
	seen := map[Kind]bool{}
	for _, card := range cards {
		kind, err := normalizeKind(card.Kind)
		if err != nil {
			continue
		}
		seen[kind] = true
	}
	return len(seen) > 1
}

// promptIntentBestSingleTypeCard 从多张卡片中选出最优的一张：优先选 requestedKind 无 block 的，
// 其次选任意无 block 的，都失败则返回第一张。
func promptIntentBestSingleTypeCard(requestedKind Kind, rawInput string, cards []Card) Card {
	if card, ok := promptIntentFirstNonBlockingCardOfKind(requestedKind, rawInput, cards); ok {
		return card
	}
	for _, card := range cards {
		kind, err := normalizeKind(card.Kind)
		if err != nil {
			continue
		}
		if !promptIntentHasBlockIssue(promptIntentDraftIssues(kind, rawInput, card)) {
			return card
		}
	}
	return cards[0]
}

// promptIntentFirstNonBlockingCardOfKind 在给定卡片列表中找到第一张 kind 匹配且无 block 问题的卡片。
func promptIntentFirstNonBlockingCardOfKind(requestedKind Kind, rawInput string, cards []Card) (Card, bool) {
	for _, card := range cards {
		kind, err := normalizeKind(card.Kind)
		if err != nil || kind != requestedKind {
			continue
		}
		if !promptIntentHasBlockIssue(promptIntentDraftIssues(kind, rawInput, card)) {
			return card, true
		}
	}
	return Card{}, false
}
