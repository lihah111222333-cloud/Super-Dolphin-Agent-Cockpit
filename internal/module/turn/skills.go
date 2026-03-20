package turn

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type skillResolver struct{}

func (r *skillResolver) Resolve(selected []dto.SkillRef, candidates []dto.SkillRef, prompt string) []dto.SkillRef {
	explicit := r.normalize(selected)
	autoCandidates := r.normalize(candidates)
	resolved := make([]dto.SkillRef, 0, len(explicit)+len(autoCandidates))
	seen := make(map[string]bool, len(explicit)+len(autoCandidates))

	for _, ref := range explicit {
		key := strings.ToLower(ref.Name)
		if key == "" || seen[key] {
			continue
		}
		resolved = append(resolved, ref)
		seen[key] = true
	}
	for _, matched := range r.autoMatch(prompt, autoCandidates, seen) {
		key := strings.ToLower(matched.Name)
		if key == "" || seen[key] {
			continue
		}
		resolved = append(resolved, matched)
		seen[key] = true
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func (r *skillResolver) normalize(refs []dto.SkillRef) []dto.SkillRef {
	resolved := make([]dto.SkillRef, 0, len(refs))
	indexByName := make(map[string]int, len(refs))
	for _, ref := range refs {
		ref.Name = strings.TrimSpace(ref.Name)
		ref.Prompt = strings.TrimSpace(ref.Prompt)
		if ref.Name == "" {
			continue
		}
		key := strings.ToLower(ref.Name)
		if idx, ok := indexByName[key]; ok {
			resolved[idx].Prompt = mergePromptText(resolved[idx].Prompt, ref.Prompt)
			continue
		}
		indexByName[key] = len(resolved)
		resolved = append(resolved, ref)
	}
	return resolved
}

func (r *skillResolver) autoMatch(prompt string, refs []dto.SkillRef, seen map[string]bool) []dto.SkillRef {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" || len(refs) == 0 {
		return nil
	}
	matches := make([]dto.SkillRef, 0, len(refs))
	for _, ref := range refs {
		key := strings.ToLower(strings.TrimSpace(ref.Name))
		if key == "" || seen[key] || !matchesSkillPrompt(prompt, key) {
			continue
		}
		matches = append(matches, ref)
	}
	return matches
}

func mergePromptText(prompt, extra string) string {
	if prompt = strings.TrimSpace(prompt); prompt == "" {
		return strings.TrimSpace(extra)
	}
	if extra = strings.TrimSpace(extra); extra == "" {
		return prompt
	}
	return prompt + "\n" + extra
}

func matchesSkillPrompt(prompt string, skillName string) bool {
	if strings.Contains(prompt, "[skill:"+skillName+"]") {
		return true
	}
	if strings.Contains(prompt, "@"+skillName) {
		return true
	}
	return strings.Contains(prompt, skillName)
}
