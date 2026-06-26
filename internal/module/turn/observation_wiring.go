package turn

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// recordSkillsSelected 将 PrepareTurn 选中的 skill 归一化后写入 observation，未启用 observation 时跳过。
func (s *service) recordSkillsSelected(localID string, skills []dto.SkillRef) {
	if s == nil || s.observation == nil {
		return
	}
	s.observation.SetSkillsSelected(localID, selectedSkillSlugs(skills))
}

// selectedSkillSlugs 去重保留 skill 展示名，避免同名不同大小写重复写入 observation。
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

// mapObservationTurn 建立本地 turnID 与 provider turnID 的双向映射，冲突时只告警不覆盖。
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

// turnAttachmentRefs 提取输入项中可展示的附件引用，按 path、URL、name 的优先级取第一项。
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
