package intent

import (
	"regexp"
	"strings"
)

// promptIntentRecallTopicPattern 限制 recall topic 为小写连字符 slug，保证后续路由和去重稳定。
var promptIntentRecallTopicPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// NormalizeGeneratedCard 规范化单张 LLM 生成卡片。
// requestedKind 不合法时会尝试沿用卡片内 kind，避免创建期因为前端旧参数丢失可修复草稿。
func NormalizeGeneratedCard(requestedKind string, rawInput string, card Card) Card {
	kind, err := normalizeKind(requestedKind)
	if err != nil {
		if inferred, inferredErr := normalizeKind(card.Kind); inferredErr == nil {
			kind = inferred
		}
	}
	cards := promptIntentNormalizeGeneratedCards(kind, rawInput, []Card{card})
	return cards[0]
}

// promptIntentNormalizeGeneratedCards 对 LLM 生成的多张卡片批量规范化，包括：
// suggested_alternative 清理、recall_topic slug 化、communication 事实落实、pair programming 文案修正。
func promptIntentNormalizeGeneratedCards(requestedKind Kind, rawInput string, cards []Card) []Card {
	out := make([]Card, 0, len(cards))
	normalizePairProgramming := strings.Contains(normalizePromptIntentText(rawInput), "pair programming")
	for _, card := range cards {
		cardKind := requestedKind
		if inferredKind, err := normalizeKind(card.Kind); err == nil {
			cardKind = inferredKind
		}
		card = promptIntentNormalizeSuggestedAlternative(requestedKind, card)
		card = promptIntentNormalizeRecallTopic(cardKind, card)
		card = promptIntentNormalizeCommunicationFact(card)
		if normalizePairProgramming {
			card = promptIntentNormalizePairProgrammingCopy(card)
		}
		out = append(out, card)
	}
	return out
}

// promptIntentNormalizeSuggestedAlternative 清理与 requestedKind 相同的 suggested_alternative 字段。
func promptIntentNormalizeSuggestedAlternative(requestedKind Kind, card Card) Card {
	if card.SuggestedAlternative == nil {
		return card
	}
	if kind, err := normalizeKind(card.SuggestedAlternative.Kind); err != nil || kind == requestedKind {
		card.SuggestedAlternative = nil
	}
	return card
}

// promptIntentNormalizeRecallTopic 规范化 recall 卡片的 topic 标识。
func promptIntentNormalizeRecallTopic(kind Kind, card Card) Card {
	if kind != KindRecall {
		return card
	}
	topic := strings.TrimSpace(card.RecallTopic)
	if topic == "" {
		card.RecallTopic = ""
		return card
	}
	slug := promptIntentRecallTopicSlug(topic)
	if validPromptIntentRecallTopic(slug) {
		card.RecallTopic = slug
		return card
	}
	card.RecallTopic = ""
	return card
}

// promptIntentRecallTopicSlug 将 recall_topic 原始文本转换为 slug 格式（小写、连字符分隔）。
func promptIntentRecallTopicSlug(topic string) string {
	slug := promptSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(topic)), "-")
	return strings.Trim(slug, "-")
}

// validPromptIntentRecallTopic 判断 recall_topic slug 是否合法：长度小于 64 且符合正则格式。
func validPromptIntentRecallTopic(topic string) bool {
	topic = strings.TrimSpace(topic)
	return len(topic) < 64 && promptIntentRecallTopicPattern.MatchString(topic)
}

// promptIntentNormalizeCommunicationFact 将 communication 事实落实为生成约束。
func promptIntentNormalizeCommunicationFact(card Card) Card {
	if strings.TrimSpace(card.Kind) != string(KindExpert) || len(card.SourceFacts) == 0 {
		return card
	}
	target := promptIntentSourceFactTargetText(card)
	const communicationConstraint = "以专业、清晰、自然的方式与用户沟通，使用 Markdown、代码标识和文件路径组织说明。"
	for i, fact := range card.SourceFacts {
		if promptIntentNormalizeSourceFactCategory(fact.Category) != "communication" {
			continue
		}
		if !promptIntentSourceFactRequiresApplication(fact) || promptIntentSourceFactApplied(fact.Summary, target) {
			continue
		}
		card.SourceFacts[i].Summary = communicationConstraint
		if !promptIntentTextContains(card.Constraints, communicationConstraint) {
			card.Constraints = append(card.Constraints, communicationConstraint)
		}
		target = promptIntentSourceFactTargetText(card)
	}
	return card
}

// promptIntentTextContains 判断字符串切片中是否已包含指定值（大小写不敏感）。
func promptIntentTextContains(values []string, want string) bool {
	needle := normalizePromptIntentText(want)
	for _, value := range values {
		if normalizePromptIntentText(value) == needle {
			return true
		}
	}
	return false
}

// promptIntentNormalizePairProgrammingCopy 将卡片所有文本字段中的"结对编程"/"结队编程"替换为"协作编程"。
func promptIntentNormalizePairProgrammingCopy(card Card) Card {
	card.Title = promptIntentNaturalizePairProgramming(card.Title)
	card.Summary = promptIntentNaturalizePairProgramming(card.Summary)
	card.WhenToUse = promptIntentNaturalizePairProgramming(card.WhenToUse)
	card.WhenNotToUse = promptIntentNaturalizePairProgramming(card.WhenNotToUse)
	card.Workflow = promptIntentNaturalizePairProgrammingList(card.Workflow)
	card.Constraints = promptIntentNaturalizePairProgrammingList(card.Constraints)
	card.Output = promptIntentNaturalizePairProgramming(card.Output)
	card.SaveBoundary = promptIntentNaturalizePairProgramming(card.SaveBoundary)
	card.RecallTopic = promptIntentNaturalizePairProgramming(card.RecallTopic)
	card.RecallBody = promptIntentNaturalizePairProgramming(card.RecallBody)
	card.DefaultRuleBody = promptIntentNaturalizePairProgramming(card.DefaultRuleBody)
	card.HitExamples = promptIntentNaturalizePairProgrammingList(card.HitExamples)
	card.MissExamples = promptIntentNaturalizePairProgrammingList(card.MissExamples)
	for i := range card.ConflictingRules {
		card.ConflictingRules[i].Title = promptIntentNaturalizePairProgramming(card.ConflictingRules[i].Title)
		card.ConflictingRules[i].Summary = promptIntentNaturalizePairProgramming(card.ConflictingRules[i].Summary)
	}
	if card.SuggestedAlternative != nil {
		card.SuggestedAlternative.Reason = promptIntentNaturalizePairProgramming(card.SuggestedAlternative.Reason)
	}
	return card
}

// promptIntentNaturalizePairProgrammingList 批量替换字符串切片中的 pair programming 文案。
func promptIntentNaturalizePairProgrammingList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, promptIntentNaturalizePairProgramming(value))
	}
	return out
}

// promptIntentNaturalizePairProgramming 将单个字符串中的"结对编程"/"结队编程"替换为"协作编程"。
func promptIntentNaturalizePairProgramming(value string) string {
	replacer := strings.NewReplacer(
		"结队编程", "协作编程",
		"结对编程", "协作编程",
	)
	return replacer.Replace(value)
}
