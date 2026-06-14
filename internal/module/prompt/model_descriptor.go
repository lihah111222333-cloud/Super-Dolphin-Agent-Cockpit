package prompt

import "strings"

// ModelDescriptor centralizes prompt-visible model metadata so env_info and
// future model-aware sections can reuse the same source of truth.
type ModelDescriptor struct {
	ID                string
	MarketingName     string
	KnowledgeCutoff   string
	FrontierGuidance  string
	LatestModelFamily string
}

var knownModelDescriptors = map[string]ModelDescriptor{
	"gpt-5.5": {
		ID:                "gpt-5.5",
		MarketingName:     "GPT-5.5",
		LatestModelFamily: "GPT-5.5 (model ID: gpt-5.5)",
	},
	"gpt-5.4": {
		ID:                "gpt-5.4",
		MarketingName:     "GPT-5.4",
		LatestModelFamily: "GPT-5.4 (model ID: gpt-5.4)",
	},
}

// LookupModelDescriptor 处理lookup模型descriptor。
func LookupModelDescriptor(model string) ModelDescriptor {
	model = strings.TrimSpace(model)
	if model == "" {
		return ModelDescriptor{}
	}
	if descriptor, ok := knownModelDescriptors[strings.ToLower(model)]; ok {
		if strings.TrimSpace(descriptor.ID) == "" {
			descriptor.ID = model
		}
		return descriptor
	}
	return ModelDescriptor{ID: model}
}

// IsZero 判断zero是否可用。
func (d ModelDescriptor) IsZero() bool {
	return strings.TrimSpace(d.ID) == "" && strings.TrimSpace(d.MarketingName) == ""
}

// MetadataText 处理元数据文本。
func (d ModelDescriptor) MetadataText() string {
	id := strings.TrimSpace(d.ID)
	marketingName := strings.TrimSpace(d.MarketingName)
	switch {
	case marketingName != "" && id != "" && marketingName != id:
		return marketingName + " (model ID: " + id + ")"
	case id != "":
		return "model ID: " + id
	case marketingName != "":
		return marketingName
	default:
		return ""
	}
}

// KnowledgeCutoffText 处理knowledgecutoff文本。
func (d ModelDescriptor) KnowledgeCutoffText() string {
	if d.IsZero() {
		return ""
	}
	if cutoff := strings.TrimSpace(d.KnowledgeCutoff); cutoff != "" {
		return cutoff
	}
	return "not published by the provider"
}

// LatestModelFamilyText 处理latest模型family文本。
func (d ModelDescriptor) LatestModelFamilyText() string {
	return strings.TrimSpace(d.LatestModelFamily)
}
