package skill

import (
	"context"
	"strings"
)

type matchItem struct {
	Name         string   `json:"name"`
	MatchedBy    string   `json:"matched_by"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
}

func (s *service) MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error) {
	resolvedThreadID := resolveSkillMatchPreviewThreadID(agentID, threadID)
	collector := s.newSkillsAutoMatchCollector(ctx)
	matches, err := collector(resolvedThreadID, text, input)
	if err != nil {
		return nil, err
	}
	items := make([]matchItem, 0, len(matches))
	for _, match := range matches {
		if name := strings.TrimSpace(match.Name); name != "" {
			items = append(items, matchItem{Name: name, MatchedBy: match.MatchedBy, MatchedTerms: append([]string(nil), match.MatchedTerms...)})
		}
	}
	return map[string]any{"thread_id": resolvedThreadID, "matches": items}, nil
}

func resolveSkillMatchPreviewThreadID(agentID, threadID string) string {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		return threadID
	}
	return strings.TrimSpace(agentID)
}

type autoMatchedSkill struct {
	Name         string
	MatchedBy    string
	MatchedTerms []string
}

func (s *service) newSkillsAutoMatchCollector(ctx context.Context) func(string, string, []UserInput) ([]autoMatchedSkill, error) {
	return func(resolvedID, text string, input []UserInput) ([]autoMatchedSkill, error) {
		skills, err := s.ListSkills(ctx)
		if err != nil {
			return nil, err
		}
		prompt := strings.ToLower(strings.TrimSpace(joinMatchText(text, input)))
		matches, err := s.collectConfiguredAutoMatchedSkills(ctx, resolvedID)
		if err != nil {
			return nil, err
		}
		matches = append(matches, collectLocalAutoMatchedSkills(prompt, skills)...)
		return dedupeAutoMatchedSkills(matches), nil
	}
}

func (s *service) collectConfiguredAutoMatchedSkills(ctx context.Context, resolvedID string) ([]autoMatchedSkill, error) {
	resolvedID = strings.TrimSpace(resolvedID)
	if resolvedID == "" {
		return nil, nil
	}
	config, err := s.readConfiguredSkillState(ctx, resolvedID)
	if err != nil {
		return nil, err
	}
	// TODO(P7): replace config-read derived matches with a provider-backed matcher once provider context can express explicit vs force configured bindings.
	items := make([]autoMatchedSkill, 0)
	for _, name := range configuredSkillNames(config) {
		items = append(items, autoMatchedSkill{Name: name, MatchedBy: "configured"})
	}
	return items, nil
}

func (s *service) readConfiguredSkillState(ctx context.Context, resolvedID string) (any, error) {
	if s.readConfigState != nil {
		return s.readConfigState(ctx, resolvedID)
	}
	return s.ReadConfig(ctx, resolvedID)
}

func configuredSkillNames(config any) []string {
	payload, _ := config.(map[string]any)
	raw, ok := payload["skills"]
	if !ok {
		return nil
	}
	switch skills := raw.(type) {
	case []string:
		return normalizeSkillNames(skills)
	case []any:
		names := make([]string, 0, len(skills))
		for _, item := range skills {
			if name, _ := item.(string); strings.TrimSpace(name) != "" {
				names = append(names, name)
			}
		}
		return normalizeSkillNames(names)
	default:
		return nil
	}
}

func collectLocalAutoMatchedSkills(prompt string, skills []SkillInfo) []autoMatchedSkill {
	matches := make([]autoMatchedSkill, 0, len(skills))
	for _, skill := range skills {
		if kind, terms := classifySkillMatch(prompt, skill); kind != "" {
			matches = append(matches, autoMatchedSkill{Name: skill.Name, MatchedBy: kind, MatchedTerms: terms})
		}
	}
	return matches
}

func dedupeAutoMatchedSkills(matches []autoMatchedSkill) []autoMatchedSkill {
	uniq := make([]autoMatchedSkill, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := strings.TrimSpace(match.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name + "\x00" + strings.TrimSpace(match.MatchedBy))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		match.Name = name
		uniq = append(uniq, match)
	}
	return uniq
}

func joinMatchText(text string, input []UserInput) string {
	parts := []string{strings.TrimSpace(text)}
	for _, item := range input {
		for _, value := range []string{item.Text, item.Name, item.Path, item.Content} {
			if value = strings.TrimSpace(value); value != "" {
				parts = append(parts, value)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func classifySkillMatch(prompt string, skill SkillInfo) (string, []string) {
	if terms := matchedTerms(prompt, skill.ForceWords); len(terms) > 0 {
		return "force", terms
	}
	if terms := explicitTerms(prompt, skill); len(terms) > 0 {
		for _, term := range terms {
			if strings.HasPrefix(strings.ToLower(term), "@") {
				return "force", terms
			}
		}
		return "explicit", terms
	}
	if terms := matchedTerms(prompt, skill.TriggerWords); len(terms) > 0 {
		return "trigger", terms
	}
	return "", nil
}

func explicitTerms(prompt string, skill SkillInfo) []string {
	candidates := append([]string{"@" + skill.Name, "[skill:" + skill.Name + "]"}, skill.TriggerWords...)
	explicit := make([]string, 0, len(candidates))
	for _, term := range matchedTerms(prompt, candidates) {
		lower := strings.ToLower(strings.TrimSpace(term))
		if strings.HasPrefix(lower, "@") || strings.HasPrefix(lower, "[skill:") {
			explicit = append(explicit, term)
		}
	}
	return uniqStrings(explicit)
}

func matchedTerms(prompt string, terms []string) []string {
	found := make([]string, 0, len(terms))
	for _, raw := range terms {
		term := strings.TrimSpace(raw)
		if term != "" && strings.Contains(prompt, strings.ToLower(term)) {
			found = append(found, term)
		}
	}
	return uniqStrings(found)
}

func normalizeSkillNames(names []string) []string { return uniqStrings(names) }
