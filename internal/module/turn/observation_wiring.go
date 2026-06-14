package turn

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *service) recordSkillsSelected(localID string, skills []dto.SkillRef) {
	if s == nil || s.observation == nil {
		return
	}
	s.observation.SetSkillsSelected(localID, selectedSkillSlugs(skills))
}

func selectedSkillSlugs(skills []dto.SkillRef) []string {
	slugs := make([]string, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		slug := strings.TrimSpace(skill.Name)
		key := strings.ToLower(slug)
		if slug == "" || key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		slugs = append(slugs, slug)
	}
	return slugs
}

// mapObservationTurn 映射observationturn。
func (s *service) mapObservationTurn(localID, providerID string) {
	if s == nil || s.observation == nil {
		return
	}
	localID = strings.TrimSpace(localID)
	providerID = strings.TrimSpace(providerID)
	if localID == "" || providerID == "" {
		return
	}
	if s.observation.MapTurn(localID, providerID) {
		return
	}
	if s.logger != nil {
		s.logger.Warn("turn: observation turn mapping rejected", "local_id", localID, "provider_id", providerID)
	}
}

// turnAttachmentRefs 处理turnattachmentrefs。
func turnAttachmentRefs(inputs []dto.InputItem) []string {
	refs := make([]string, 0, len(inputs))
	for _, item := range inputs {
		for _, candidate := range []string{strings.TrimSpace(item.Path), strings.TrimSpace(item.URL), strings.TrimSpace(item.Name)} {
			if candidate != "" {
				refs = append(refs, candidate)
				break
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}
