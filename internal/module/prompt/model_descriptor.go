package prompt

import "strings"

// ModelDescriptor 集中维护 prompt 可见的模型元数据，供 env_info 和后续模型感知 section 复用。
type ModelDescriptor struct {
	ID                string
	MarketingName     string
	KnowledgeCutoff   string
	FrontierGuidance  string
	LatestModelFamily string
}

// LookupModelDescriptor 查找模型描述；未知模型保留原始 ID，避免环境提示丢失实际模型名。
func LookupModelDescriptor(model string) ModelDescriptor {
	model = strings.TrimSpace(model)
	if model == "" {
		return ModelDescriptor{}
	}
	if descriptor, ok := knownModelDescriptor(strings.ToLower(model)); ok {
		if strings.TrimSpace(descriptor.ID) == "" {
			descriptor.ID = model
		}
		return descriptor
	}
	return ModelDescriptor{ID: model}
}

// knownModelDescriptor 返回内置模型的独立描述值，避免共享可变 map 泄漏到调用方。
func knownModelDescriptor(model string) (ModelDescriptor, bool) {
	switch model {
	case "gpt-5.5":
		return ModelDescriptor{
			ID:                "gpt-5.5",
			MarketingName:     "GPT-5.5",
			LatestModelFamily: "GPT-5.5 (model ID: gpt-5.5)",
		}, true
	case "gpt-5.4":
		return ModelDescriptor{
			ID:                "gpt-5.4",
			MarketingName:     "GPT-5.4",
			LatestModelFamily: "GPT-5.4 (model ID: gpt-5.4)",
		}, true
	default:
		return ModelDescriptor{}, false
	}
}

// IsZero 判断描述符是否没有可展示的模型信息。
func (d ModelDescriptor) IsZero() bool {
	return strings.TrimSpace(d.ID) == "" && strings.TrimSpace(d.MarketingName) == ""
}

// MetadataText 生成“营销名 + model ID”的展示文本，缺失时只展示可用字段。
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

// KnowledgeCutoffText 返回知识截止信息；已知模型但未发布时给出明确说明。
func (d ModelDescriptor) KnowledgeCutoffText() string {
	if d.IsZero() {
		return ""
	}
	if cutoff := strings.TrimSpace(d.KnowledgeCutoff); cutoff != "" {
		return cutoff
	}
	return "not published by the provider"
}

// LatestModelFamilyText 返回最新模型族展示文本。
func (d ModelDescriptor) LatestModelFamilyText() string {
	return strings.TrimSpace(d.LatestModelFamily)
}
