package skill

import (
	"context"
	"strings"

	skillidentity "github.com/anthropic-ai/super-agent-v3/internal/module/skill/identity"
)

type matchItem struct {
	Name         string   `json:"name"`
	MatchedBy    string   `json:"matched_by"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
}

// MatchPreview 预览当前输入会自动匹配哪些 skill。
// 它同时合并 agent/thread 配置技能和本地触发词匹配，返回结果只用于 UI 预览不改变状态。
func (s *service) MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error) {
	resolvedThreadID := resolveSkillMatchPreviewThreadID(agentID, threadID)
	matches, err := s.newSkillsAutoMatchCollector(ctx)(resolvedThreadID, text, input)
	if err != nil {
		return nil, err
	}
	items := make([]matchItem, 0, len(matches))
	for _, m := range matches {
		if name := strings.TrimSpace(m.Name); name != "" {
			items = append(items, matchItem{Name: name, MatchedBy: m.MatchedBy, MatchedTerms: append([]string(nil), m.MatchedTerms...)})
		}
	}
	return map[string]any{"thread_id": resolvedThreadID, "matches": items}, nil
}

func resolveSkillMatchPreviewThreadID(agentID, threadID string) string {
	if tid := strings.TrimSpace(threadID); tid != "" {
		return tid
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
		cwd, err := requireCWD(ctx)
		if err != nil {
			return nil, err
		}
		skills, err := s.ListSkills(WithCWD(ctx, cwd))
		if err != nil {
			return nil, err
		}
		prompt := strings.ToLower(strings.TrimSpace(joinMatchText(text, input)))
		matches, err := s.collectConfiguredAutoMatchedSkills(ctx, resolvedID, skills)
		if err != nil {
			return nil, err
		}
		matches = append(matches, collectLocalAutoMatchedSkills(prompt, skills)...)
		return dedupeAutoMatchedSkills(matches), nil
	}
}

func (s *service) collectConfiguredAutoMatchedSkills(ctx context.Context, resolvedID string, skills []SkillInfo) ([]autoMatchedSkill, error) {
	resolvedID = strings.TrimSpace(resolvedID)
	if resolvedID == "" {
		return nil, nil
	}
	config, err := s.readConfiguredSkillState(ctx, resolvedID)
	if err != nil {
		return nil, err
	}
	// 目前配置读取仍是 agent/thread 绑定 skill 的兼容来源。
	// provider context 暂不能区分显式配置和强制配置，因此这里只标记为 configured。
	items := make([]autoMatchedSkill, 0)
	for _, name := range configuredSkillNames(config) {
		if canonicalName, ok := skillidentity.CanonicalNameForAlias(name, skills); ok {
			name = canonicalName
		}
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

// configuredSkillNames 从配置 payload 中提取 skill 名称列表。
// 只接受字符串数组或 []any 中的字符串，其他形态视为未配置。
func configuredSkillNames(config any) []string {
	payload, ok := config.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := payload["skills"]
	if !ok {
		return nil
	}
	switch skills := raw.(type) {
	case []string:
		return uniqStrings(skills)
	case []any:
		names := make([]string, 0, len(skills))
		for _, item := range skills {
			if name, _ := item.(string); strings.TrimSpace(name) != "" {
				names = append(names, name)
			}
		}
		return uniqStrings(names)
	default:
		return nil
	}
}

func collectLocalAutoMatchedSkills(prompt string, skills []SkillInfo) []autoMatchedSkill {
	matches := make([]autoMatchedSkill, 0, len(skills))
	for _, s := range skills {
		if kind, terms := classifySkillMatch(prompt, s); kind != "" {
			matches = append(matches, autoMatchedSkill{Name: s.Name, MatchedBy: kind, MatchedTerms: terms})
		}
	}
	return matches
}

// dedupeAutoMatchedSkills 按 skill 名称和匹配来源去重。
// 同一个 skill 可以同时以 configured 和 trigger 方式出现，二者需要保留给 UI 区分。
func dedupeAutoMatchedSkills(matches []autoMatchedSkill) []autoMatchedSkill {
	uniq := make([]autoMatchedSkill, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if name := strings.TrimSpace(match.Name); name != "" {
			key := strings.ToLower(name + "\x00" + strings.TrimSpace(match.MatchedBy))
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				match.Name = name
				uniq = append(uniq, match)
			}
		}
	}
	return uniq
}

// joinMatchText 合并用户文本和附件输入中的可匹配字段。
// URL 不参与匹配，避免仅因链接地址中的词触发 skill。
func joinMatchText(text string, input []UserInput) string {
	parts := []string{strings.TrimSpace(text)}
	for _, item := range input {
		for _, v := range []string{item.Text, item.Name, item.Path, item.Content} {
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// classifySkillMatch 分类技能match。
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
	aliases := skillidentity.Aliases(skill.Name, skill.DisplayName)
	candidates := make([]string, 0, len(aliases)*2)
	for _, alias := range aliases {
		candidates = append(candidates, "@"+alias, "[skill:"+alias+"]")
	}
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
		if term := strings.TrimSpace(raw); term != "" && strings.Contains(prompt, strings.ToLower(term)) {
			found = append(found, term)
		}
	}
	return uniqStrings(found)
}
