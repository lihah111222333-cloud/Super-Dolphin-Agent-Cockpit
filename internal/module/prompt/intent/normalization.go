package intent

import (
	"regexp"
	"strings"
)

var promptIntentRecallTopicPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

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

func promptIntentNormalizeSuggestedAlternative(requestedKind Kind, card Card) Card {
	if card.SuggestedAlternative == nil {
		return card
	}
	if kind, err := normalizeKind(card.SuggestedAlternative.Kind); err != nil || kind == requestedKind {
		card.SuggestedAlternative = nil
	}
	return card
}

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

func promptIntentRecallTopicSlug(topic string) string {
	slug := promptSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(topic)), "-")
	return strings.Trim(slug, "-")
}

func validPromptIntentRecallTopic(topic string) bool {
	topic = strings.TrimSpace(topic)
	return len(topic) < 64 && promptIntentRecallTopicPattern.MatchString(topic)
}

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
