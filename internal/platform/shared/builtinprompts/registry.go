package builtinprompts

import (
	"encoding/json"
	"sort"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Registry 持有内置 prompt 模板和 section 的只读索引。
type Registry struct {
	templates            []contract.BuiltinPromptTemplate
	templatesByPromptKey map[string]contract.BuiltinPromptTemplate
	sectionsByTemplateID map[int64][]contract.BuiltinPromptSection
}

// newRegistry 按稳定顺序为模板和 section 分配内置负 ID。
func newRegistry(loaded []loadedTemplate) *Registry {
	sort.SliceStable(loaded, loadedTemplateLess(loaded))

	registry := &Registry{
		templates:            make([]contract.BuiltinPromptTemplate, 0, len(loaded)),
		templatesByPromptKey: map[string]contract.BuiltinPromptTemplate{},
		sectionsByTemplateID: map[int64][]contract.BuiltinPromptSection{},
	}
	nextSectionID := firstBuiltinSectionID
	for i, item := range loaded {
		templateID := builtinTemplateID(item.Config, i)
		template := buildTemplate(templateID, item.Config)
		registry.templates = append(registry.templates, template)
		registry.templatesByPromptKey[template.PromptKey] = template
		registry.sectionsByTemplateID[templateID] = buildSections(templateID, &nextSectionID, item.Sections)
	}
	return registry
}

// loadedTemplateLess 定义内置模板排序规则，显式 ID 优先且按 ID 降序排列。
func loadedTemplateLess(loaded []loadedTemplate) func(i, j int) bool {
	return func(i, j int) bool {
		left, right := loaded[i].Config, loaded[j].Config
		if left.ID != nil && right.ID != nil && *left.ID != *right.ID {
			return *left.ID > *right.ID
		}
		if left.ID != nil && right.ID == nil {
			return true
		}
		if left.ID == nil && right.ID != nil {
			return false
		}
		return left.PromptKey < right.PromptKey
	}
}

// builtinTemplateID 返回显式模板 ID，未配置时按排序下标生成稳定负 ID。
func builtinTemplateID(cfg templateConfig, index int) int64 {
	if cfg.ID != nil {
		return *cfg.ID
	}
	return firstBuiltinID - int64(index)
}

// buildTemplate 把加载后的 JSON 配置转换为 contract DTO。
func buildTemplate(id int64, cfg templateConfig) contract.BuiltinPromptTemplate {
	return contract.BuiltinPromptTemplate{
		ID:          id,
		PromptKey:   cfg.PromptKey,
		Kind:        cfg.Kind,
		Title:       cfg.Title,
		AgentKey:    cfg.AgentKey,
		ToolName:    cfg.ToolName,
		PromptText:  cfg.PromptText,
		WhenToUse:   cfg.WhenToUse,
		Description: cfg.Description,
		Tags:        copyStrings(cfg.Tags),
		Enabled:     boolValue(cfg.Enabled, false),
		Scope:       cfg.Scope,
		MatchWhen:   copyRawJSON(cfg.MatchWhen),
		Priority:    cfg.Priority,
	}
}

// buildSections 为模板 section 分配稳定负 ID 并转换为 contract DTO。
func buildSections(templateID int64, nextID *int64, loaded []loadedSection) []contract.BuiltinPromptSection {
	sections := make([]contract.BuiltinPromptSection, 0, len(loaded))
	for _, item := range loaded {
		sections = append(sections, buildSection(templateID, *nextID, item))
		*nextID = *nextID - 1
	}
	return sections
}

// buildSection 构造单个内置 prompt section DTO。
func buildSection(templateID, id int64, item loadedSection) contract.BuiltinPromptSection {
	cfg := item.Config
	return contract.BuiltinPromptSection{
		ID:          id,
		TemplateID:  templateID,
		SectionKey:  cfg.SectionKey,
		Region:      cfg.Region,
		Ordinal:     cfg.Ordinal,
		Body:        item.Body,
		EnableWhen:  copyRawJSON(cfg.EnableWhen),
		Enabled:     boolValue(cfg.Enabled, true),
		TriggerType: cfg.TriggerType,
		RecallTopic: cfg.RecallTopic,
	}
}

// ListTemplates 返回模板列表拷贝，避免调用方改写 registry 内部切片或 JSON。
func (r *Registry) ListTemplates() []contract.BuiltinPromptTemplate {
	out := make([]contract.BuiltinPromptTemplate, len(r.templates))
	copy(out, r.templates)
	for i := range out {
		out[i].Tags = copyStrings(out[i].Tags)
		out[i].MatchWhen = copyRawJSON(out[i].MatchWhen)
	}
	return out
}

// GetTemplate 按 prompt_key 返回模板拷贝。
func (r *Registry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	template, ok := r.templatesByPromptKey[promptKey]
	if !ok {
		return contract.BuiltinPromptTemplate{}, false
	}
	template.Tags = copyStrings(template.Tags)
	template.MatchWhen = copyRawJSON(template.MatchWhen)
	return template, true
}

// SectionsByTemplateID 返回模板 section 拷贝，避免调用方改写 enable_when JSON。
func (r *Registry) SectionsByTemplateID(templateID int64) []contract.BuiltinPromptSection {
	sections := r.sectionsByTemplateID[templateID]
	out := make([]contract.BuiltinPromptSection, len(sections))
	copy(out, sections)
	for i := range out {
		out[i].EnableWhen = copyRawJSON(out[i].EnableWhen)
	}
	return out
}

// copyStrings 复制字符串切片，nil 保持 nil。
func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// copyRawJSON 复制 RawMessage，防止调用方共享底层字节切片。
func copyRawJSON(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(json.RawMessage, len(in))
	copy(out, in)
	return out
}

// boolValue 解析可选 bool，nil 时返回调用方给定默认值。
func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
