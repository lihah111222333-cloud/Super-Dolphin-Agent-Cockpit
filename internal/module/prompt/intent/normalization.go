package intent

import "strings"

// NormalizeGeneratedCard 规范化generatedcard。
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

func promptIntentNormalizeGeneratedCards(requestedKind Kind, rawInput string, cards []Card) []Card {
	out := make([]Card, 0, len(cards))
	normalizePairProgramming := strings.Contains(normalizePromptIntentText(rawInput), "pair programming")
	for _, card := range cards {
		card = promptIntentNormalizeSuggestedAlternative(requestedKind, card)
		card = promptIntentNormalizeCommunicationFact(card)
		if normalizePairProgramming {
			card = promptIntentNormalizePairProgrammingCopy(card)
		}
		out = append(out, card)
	}
	return out
}

func promptIntentNormalizeSuggestedAlternative(requestedKind Kind, card Card) Card {
	if card.SuggestedAlternative == nil {
		return card
	}
	if kind, err := normalizeKind(card.SuggestedAlternative.Kind); err != nil || kind == requestedKind {
		card.SuggestedAlternative = nil
	}
	return card
}

// promptIntentNormalizeCommunicationFact 处理promptintentnormalizecommunicationfact。
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

func promptIntentTextContains(values []string, want string) bool {
	needle := normalizePromptIntentText(want)
	for _, value := range values {
		if normalizePromptIntentText(value) == needle {
			return true
		}
	}
	return false
}

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

func promptIntentNaturalizePairProgrammingList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, promptIntentNaturalizePairProgramming(value))
	}
	return out
}

func promptIntentNaturalizePairProgramming(value string) string {
	replacer := strings.NewReplacer(
		"结队编程", "协作编程",
		"结对编程", "协作编程",
	)
	return replacer.Replace(value)
}
