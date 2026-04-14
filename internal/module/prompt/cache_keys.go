package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func sectionInputCacheKey(section PromptSection, input SectionContext) (string, bool) {
	switch section.CachePolicy {
	case Uncached:
		return "", false
	case InputScoped:
		encoded, err := json.Marshal(inputScopedSectionDependency(section, input))
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
