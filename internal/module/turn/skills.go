package turn

import (
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// normalizeSkillRefs 把多组 SkillRef 合并去重，去重键包含名称和版本。
// 同 (name, version) 视为同一引用，会保留单条元数据；
// version 不同则保留为两条，避免 hash 已变的新版本被旧版本静默覆盖。
func normalizeSkillRefs(groups ...[]dto.SkillRef) []dto.SkillRef {
	total := 0
	for _, refs := range groups {
		total += len(refs)
	}
	resolved := make([]dto.SkillRef, 0, total)
	indexByKey := make(map[string]int, total)
	for _, refs := range groups {
		for _, ref := range refs {
			ref.Name = strings.TrimSpace(ref.Name)
			ref.Key = strings.TrimSpace(ref.Key)
			ref.Scope = strings.TrimSpace(ref.Scope)
			ref.PersonalType = strings.TrimSpace(ref.PersonalType)
			ref.Path = strings.TrimSpace(ref.Path)
			ref.Version = strings.TrimSpace(ref.Version)
			// 生产 turn payload 只保留 skill 元数据；旧客户端可能仍带 Prompt 字段，
			// 但正文由 provider-native discovery 负责，不能从这里透传正文。
			ref.Prompt = ""
			if ref.Name == "" {
				continue
			}
			key := skillDedupKey(ref)
			if key == "" {
				continue
			}
			if idx, ok := indexByKey[key]; ok {
				resolved[idx] = mergeSkillRefMetadata(resolved[idx], ref)
				continue
			}
			indexByKey[key] = len(resolved)
			resolved = append(resolved, ref)
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// skillDedupKey 构造 provider-native skill metadata 的去重键。
//
// 名称、版本和可选作用域共同决定同一条引用，避免同名不同版本或不同来源的 skill
// 被折叠成单条。空 name 返回 ""，调用方应跳过；空 version 退化为 `name@`，
// 保持旧 payload 的 name-only 兼容行为。
//
// provider-native 选择链路会把 UI 的 key/scope/personalType/path 纳入去重键，
// 确保项目级与个人级同名 skill 在 turn payload 中不会退化成单条 name-only 引用。
func skillDedupKey(ref dto.SkillRef) string {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	if name == "" {
		return ""
	}
	if key := strings.ToLower(strings.TrimSpace(ref.Key)); key != "" {
		return "key:" + key + "@" + strings.TrimSpace(ref.Version)
	}
	scope := strings.ToLower(strings.TrimSpace(ref.Scope))
	personalType := strings.ToLower(strings.TrimSpace(ref.PersonalType))
	path := strings.ToLower(strings.TrimSpace(ref.Path))
	if scope != "" || personalType != "" || path != "" {
		return "ref:" + scope + ":" + personalType + ":" + name + ":" + path + "@" + strings.TrimSpace(ref.Version)
	}
	return name + "@" + strings.TrimSpace(ref.Version)
}

// mergeSkillRefMetadata 用后续 ref 补齐已有 ref 的空元数据，已有字段不被覆盖。
func mergeSkillRefMetadata(existing, next dto.SkillRef) dto.SkillRef {
	if strings.TrimSpace(existing.Summary) == "" {
		existing.Summary = strings.TrimSpace(next.Summary)
	}
	if strings.TrimSpace(existing.Key) == "" {
		existing.Key = strings.TrimSpace(next.Key)
	}
	if strings.TrimSpace(existing.Scope) == "" {
		existing.Scope = strings.TrimSpace(next.Scope)
	}
	if strings.TrimSpace(existing.PersonalType) == "" {
		existing.PersonalType = strings.TrimSpace(next.PersonalType)
	}
	if strings.TrimSpace(existing.Path) == "" {
		existing.Path = strings.TrimSpace(next.Path)
	}
	if existing.Source == "" {
		existing.Source = next.Source
	}
	return existing
}
