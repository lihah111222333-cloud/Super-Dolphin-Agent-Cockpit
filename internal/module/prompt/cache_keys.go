package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func sectionInputCacheKey(section PromptSection, input SectionContext) (string, bool) {
	switch section.CachePolicy {
	case Uncached:
		return strings.TrimSpace(section.Name), strings.TrimSpace(section.Name) != ""
	case InputScoped:
		encoded, err := json.Marshal(inputScopedCacheDependency(section, input))
		if err != nil {
			return section.Name, true
		}
		digest := sha256.Sum256(encoded)
		return section.Name + ":" + hex.EncodeToString(digest[:]), true
	default:
		if dependency := cacheByNameSectionDependency(section, input); dependency != nil {
			encoded, err := json.Marshal(dependency)
			if err == nil {
				digest := sha256.Sum256(encoded)
				return section.Name + ":" + hex.EncodeToString(digest[:]), true
			}
		}
		return section.Name, true
	}
}

func inputScopedCacheDependency(section PromptSection, input SectionContext) any {
	dependency := inputScopedSectionDependency(section, input)
	if section.Name != DynamicSectionAgentMemory {
		return dependency
	}
	scope := ""
	if input.Start != nil && input.Turn == nil {
		scope = strings.ToLower(strings.TrimSpace(input.Start.AgentMemoryScope))
	}
	return struct {
		Dependency       any    `json:"dependency"`
		AgentMemoryScope string `json:"agentMemoryScope,omitempty"`
	}{
		Dependency:       dependency,
		AgentMemoryScope: scope,
	}
}
