package promptrouting

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// FindEnabledByPromptKey returns the launchable template for a prompt key.
func FindEnabledByPromptKey(templates []contract.PromptTemplate, promptKey string) *contract.PromptTemplate {
	picked := FindByPromptKey(templates, promptKey)
	if picked == nil || !TemplateLaunchable(*picked) {
		return nil
	}
	return picked
}

// ConvertSectionsToBlocks maps injectable prompt_template_sections rows into
// the contract-layer BaseInstructionBlock shape consumed by the prompt
// assembler.
func ConvertSectionsToBlocks(sections []contract.PromptTemplateSection) []contract.BaseInstructionBlock {
	if len(sections) == 0 {
		return nil
	}
	out := make([]contract.BaseInstructionBlock, 0, len(sections))
	for _, s := range sections {
		if !s.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s.TriggerType), "recall") {
			continue
		}
		if strings.TrimSpace(s.Body) == "" {
			continue
		}
		region := contract.PromptRegionDynamic
		if strings.EqualFold(strings.TrimSpace(s.Region), "static") {
			region = contract.PromptRegionStatic
		}
		out = append(out, contract.BaseInstructionBlock{
			Key:        s.SectionKey,
			Region:     region,
			Ordinal:    s.Ordinal,
			Body:       s.Body,
			EnableWhen: append([]byte(nil), s.EnableWhen...),
		})
	}
	return out
}

// FirstEnabledByAgentKey returns the first launchable template for an agent key.
func FirstEnabledByAgentKey(templates []contract.PromptTemplate, agentKey string) *contract.PromptTemplate {
	want := strings.TrimSpace(agentKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if TemplateLaunchable(*t) && strings.EqualFold(strings.TrimSpace(t.AgentKey), want) {
			return t
		}
	}
	return nil
}

// TemplateLaunchable reports whether a prompt template can be used to launch
// a provider session.
func TemplateLaunchable(template contract.PromptTemplate) bool {
	return template.Enabled && !contract.IsRuntimeAssetPromptTemplate(template)
}

// FindByPromptKey returns the first template with the exact prompt key.
func FindByPromptKey(templates []contract.PromptTemplate, promptKey string) *contract.PromptTemplate {
	want := strings.TrimSpace(promptKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if t.PromptKey == want {
			return t
		}
	}
	return nil
}

// AutoRouteCandidates partitions enabled match_when rows into specific and
// fallback pools, each sorted by priority descending.
func AutoRouteCandidates(templates []contract.PromptTemplate) (specific, fallback []contract.PromptTemplate) {
	specific = make([]contract.PromptTemplate, 0, len(templates))
	fallback = make([]contract.PromptTemplate, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if !TemplateLaunchable(*t) {
			continue
		}
		if len(t.MatchWhen) == 0 {
			continue
		}
		if IsFallbackMatchWhen(t.MatchWhen) {
			fallback = append(fallback, *t)
			continue
		}
		if HasSpecificMatchWhen(t.MatchWhen) {
			specific = append(specific, *t)
		}
	}
	sortByPriorityDesc(specific)
	sortByPriorityDesc(fallback)
	return specific, fallback
}

// IsFallbackMatchWhen reports whether the raw JSON is the empty object `{}`.
func IsFallbackMatchWhen(raw []byte) bool {
	var expr map[string]any
	if err := json.Unmarshal(raw, &expr); err != nil {
		return false
	}
	return expr != nil && len(expr) == 0
}

// HasSpecificMatchWhen reports whether the raw JSON decodes to a non-empty
// object with at least one filter key.
func HasSpecificMatchWhen(raw []byte) bool {
	var expr map[string]any
	if err := json.Unmarshal(raw, &expr); err != nil {
		return false
	}
	return len(expr) > 0
}

func sortByPriorityDesc(rows []contract.PromptTemplate) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Priority > rows[j].Priority
	})
}
