package threadprompt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	runtimeBuiltinAuthor        = "builtin.registry"
	runtimeBuiltinSystemTag     = "builtin:system"
	runtimeScopeGlobalTag       = "scope.global"
	runtimeScopeCWDTagPrefix    = "scope.cwd:"
	maxRuntimeCatalogStoreLimit = int32(1<<31 - 1)
	runtimeCatalogStoreLimitPad = int32(64)
)

// RuntimeListFilter filters runtime prompt catalog reads.
type RuntimeListFilter = contract.PromptRuntimeListFilter

// RuntimePromptCatalog is the thread-facing prompt catalog port.
type RuntimePromptCatalog = contract.RuntimePromptCatalog

// NewRuntimeCatalog 创建运行时catalog。
func NewRuntimeCatalog(store contract.PromptStore, builtin contract.BuiltinPromptRegistry) contract.RuntimePromptCatalog {
	if store == nil && builtin == nil {
		return nil
	}
	return &runtimePromptCatalog{store: store, builtin: builtin}
}

type runtimePromptCatalog struct {
	store   contract.PromptStore
	builtin contract.BuiltinPromptRegistry
}

// ListTemplates 列出templates。
func (c *runtimePromptCatalog) ListTemplates(ctx context.Context, filter RuntimeListFilter) ([]contract.PromptTemplate, error) {
	out, builtinKeys := c.listBuiltinTemplates(filter)
	storeTemplates, err := c.listStoreTemplates(ctx, filter, builtinKeys)
	if err != nil {
		return nil, err
	}
	out = append(out, storeTemplates...)
	sortRuntimeTemplates(out)
	return limitRuntimeTemplates(out, filter.Limit), nil
}

// GetTemplate 读取template。
func (c *runtimePromptCatalog) GetTemplate(ctx context.Context, promptKey, cwd string) (*contract.PromptTemplate, error) {
	promptKey = strings.TrimSpace(promptKey)
	cwd = strings.TrimSpace(cwd)
	builtin := c.builtinTemplateForKey(promptKey, cwd)
	dbTemplate, _, err := c.storeTemplateForKey(ctx, promptKey, cwd)
	if err != nil {
		return nil, err
	}
	picked := runtimePickTemplateForGet(dbTemplate, builtin)
	if picked == nil {
		return nil, fmt.Errorf("runtime prompt catalog: template %q not found: %w", promptKey, contract.ErrNotFound)
	}
	return cloneRuntimeTemplate(picked), nil
}

// ListSectionsByTemplateID 按templateID列出sections。
func (c *runtimePromptCatalog) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]contract.PromptTemplateSection, error) {
	if templateID < 0 {
		if c.builtin == nil {
			return nil, fmt.Errorf("runtime prompt catalog: builtin prompt registry is required for template_id %d", templateID)
		}
		template, ok := c.builtinTemplateByID(templateID)
		if !ok {
			return []contract.PromptTemplateSection{}, nil
		}
		return c.builtinSectionsForTemplate(template, false), nil
	}
	if c.store == nil {
		return nil, fmt.Errorf("runtime prompt catalog: prompt store is required for template_id %d", templateID)
	}
	return c.store.ListSectionsByTemplateID(ctx, templateID)
}

// ListRecallSections 列出recallsections。
func (c *runtimePromptCatalog) ListRecallSections(ctx context.Context, cwd string) ([]contract.PromptTemplateSection, error) {
	cwd = strings.TrimSpace(cwd)
	var out []contract.PromptTemplateSection
	if c.builtin != nil {
		for _, template := range c.builtin.ListTemplates() {
			mapped := runtimeBuiltinTemplateToStore(template)
			if !runtimeTemplateVisibleForRead(mapped, cwd) {
				continue
			}
			for _, section := range c.builtinSectionsForTemplate(template, true) {
				if section.TriggerType == "recall" && section.Enabled && strings.TrimSpace(section.RecallTopic) != "" {
					out = append(out, section)
				}
			}
		}
	}
	if c.store != nil {
		sections, err := c.store.ListRecallSections(ctx, cwd)
		if err != nil {
			return nil, err
		}
		out = append(out, sections...)
	}
	return effectiveRecallSections(out), nil
}

// ListDefaultRuleSections 列出defaultrulesections。
func (c *runtimePromptCatalog) ListDefaultRuleSections(ctx context.Context, cwd string) ([]contract.PromptTemplateSection, error) {
	cwd = strings.TrimSpace(cwd)
	out := c.builtinDefaultRuleSections(cwd)
	sections, err := c.storeDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	out = append(out, sections...)
	return effectiveDefaultRuleSections(out), nil
}

// InsertVersion 插入版本。
func (c *runtimePromptCatalog) InsertVersion(ctx context.Context, version contract.PromptTemplateVersion) (int64, error) {
	if c.store == nil {
		return 0, fmt.Errorf("runtime prompt catalog: prompt store is required for insert_version")
	}
	return c.store.InsertVersion(ctx, version)
}

// CanInsertPromptVersion 判断insertprompt版本是否可用。
func (c *runtimePromptCatalog) CanInsertPromptVersion() bool {
	return c != nil && c.store != nil
}

func (c *runtimePromptCatalog) listBuiltinTemplates(filter RuntimeListFilter) ([]contract.PromptTemplate, map[string]struct{}) {
	keys := map[string]struct{}{}
	if c.builtin == nil {
		return nil, keys
	}
	out := make([]contract.PromptTemplate, 0, len(c.builtin.ListTemplates()))
	for _, template := range c.builtin.ListTemplates() {
		mapped := runtimeBuiltinTemplateToStore(template)
		if key := strings.TrimSpace(mapped.PromptKey); key != "" {
			keys[key] = struct{}{}
		}
		if !runtimeTemplateMatchesFilter(mapped, filter) {
			continue
		}
		out = append(out, mapped)
	}
	return out, keys
}

// listStoreTemplates 列出存储templates。
func (c *runtimePromptCatalog) listStoreTemplates(
	ctx context.Context,
	filter RuntimeListFilter,
	builtinKeys map[string]struct{},
) ([]contract.PromptTemplate, error) {
	if c.store == nil {
		return nil, nil
	}
	if strings.TrimSpace(filter.CWD) == "" {
		return nil, nil
	}
	templates, err := c.store.List(ctx, contract.PromptListFilter{
		AgentKey: strings.TrimSpace(filter.AgentKey),
		Keyword:  c.storeListKeyword(filter),
		CWD:      strings.TrimSpace(filter.CWD),
		Limit:    c.storeListLimit(filter),
	})
	if err != nil {
		return nil, err
	}
	out := make([]contract.PromptTemplate, 0, len(templates))
	for _, template := range templates {
		if runtimeStoreTemplateHiddenByBuiltin(template, builtinKeys) {
			continue
		}
		if runtimeTemplateMatchesFilter(template, filter) {
			out = append(out, template)
		}
	}
	return c.storeTemplatesWithInferredSectionIntents(ctx, out)
}

func (c *runtimePromptCatalog) storeListKeyword(filter RuntimeListFilter) string {
	if strings.TrimSpace(filter.Keyword) != "" {
		return ""
	}
	return strings.TrimSpace(filter.Keyword)
}

// storeListLimit 保存listlimit。
func (c *runtimePromptCatalog) storeListLimit(filter RuntimeListFilter) int32 {
	if filter.Limit <= 0 {
		return maxRuntimeCatalogStoreLimit
	}
	if strings.TrimSpace(filter.Keyword) != "" && filter.Limit > 0 {
		return maxRuntimeCatalogStoreLimit
	}
	if c.builtin != nil && filter.Limit > 0 {
		return runtimeCatalogStoreLimitWithBuiltin(filter.Limit, len(c.builtin.ListTemplates()))
	}
	return filter.Limit
}

func runtimeCatalogStoreLimitWithBuiltin(limit int32, builtinCount int) int32 {
	if limit <= 0 {
		return maxRuntimeCatalogStoreLimit
	}
	extra := int64(builtinCount) + int64(runtimeCatalogStoreLimitPad)
	if extra < 0 {
		extra = int64(runtimeCatalogStoreLimitPad)
	}
	storeLimit := int64(limit) + extra
	if storeLimit > int64(maxRuntimeCatalogStoreLimit) {
		return maxRuntimeCatalogStoreLimit
	}
	return int32(storeLimit)
}

func (c *runtimePromptCatalog) builtinTemplateForKey(promptKey, cwd string) *contract.PromptTemplate {
	if c.builtin == nil {
		return nil
	}
	template, ok := c.builtin.GetTemplate(promptKey)
	if !ok {
		return nil
	}
	mapped := runtimeBuiltinTemplateToStore(template)
	if !runtimeTemplateVisibleForRead(mapped, cwd) {
		return nil
	}
	return &mapped
}

// storeTemplateForKey 为键保存template。
func (c *runtimePromptCatalog) storeTemplateForKey(ctx context.Context, promptKey, cwd string) (*contract.PromptTemplate, bool, error) {
	if c.store == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(cwd) == "" {
		return nil, false, nil
	}
	template, err := c.store.Get(ctx, promptKey)
	if err != nil {
		if contract.IsNotFound(err) {
			return missingStoreTemplate()
		}
		return nil, false, err
	}
	if template == nil || !runtimeTemplateVisibleForRead(*template, cwd) {
		return nil, false, nil
	}
	copy := *template
	copy, err = c.storeTemplateWithInferredSectionIntent(ctx, copy)
	if err != nil {
		return nil, false, err
	}
	return &copy, true, nil
}

func missingStoreTemplate() (*contract.PromptTemplate, bool, error) {
	return nil, false, nil
}

func (c *runtimePromptCatalog) builtinDefaultRuleSections(cwd string) []contract.PromptTemplateSection {
	if c.builtin == nil {
		return nil
	}
	var out []contract.PromptTemplateSection
	for _, template := range c.builtin.ListTemplates() {
		mapped := runtimeBuiltinTemplateToStore(template)
		if strings.TrimSpace(mapped.AgentKey) != "default_rule" || !runtimeTemplateVisibleForRead(mapped, cwd) {
			continue
		}
		out = append(out, runtimeDefaultRuleSections(c.builtinSectionsForTemplate(template, false))...)
	}
	return out
}

func (c *runtimePromptCatalog) storeDefaultRuleSections(ctx context.Context, cwd string) ([]contract.PromptTemplateSection, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.store.ListDefaultRuleSections(ctx, cwd)
}

func (c *runtimePromptCatalog) builtinTemplateByID(templateID int64) (contract.BuiltinPromptTemplate, bool) {
	for _, template := range c.builtin.ListTemplates() {
		if runtimeBuiltinTemplateID(template.ID) == templateID {
			return template, true
		}
	}
	return contract.BuiltinPromptTemplate{}, false
}

// builtinSectionsForTemplate 为template处理builtinsections。
func (c *runtimePromptCatalog) builtinSectionsForTemplate(
	template contract.BuiltinPromptTemplate,
	recallDefaultsGlobal bool,
) []contract.PromptTemplateSection {
	mappedTemplate := runtimeBuiltinTemplateToStore(template)
	templateTags := mappedTemplate.Tags
	if recallDefaultsGlobal {
		templateTags = runtimeEnsureGlobalScopeWhenUnscoped(templateTags)
	}
	sections := c.builtin.SectionsByTemplateID(template.ID)
	out := make([]contract.PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, contract.PromptTemplateSection{
			ID:                  runtimeBuiltinTemplateID(section.ID),
			TemplateID:          runtimeBuiltinTemplateID(section.TemplateID),
			SectionKey:          section.SectionKey,
			Region:              section.Region,
			Ordinal:             section.Ordinal,
			Body:                section.Body,
			EnableWhen:          cloneRuntimeRawJSON(section.EnableWhen),
			Enabled:             section.Enabled,
			TriggerType:         section.TriggerType,
			RecallTopic:         section.RecallTopic,
			TemplatePromptKey:   mappedTemplate.PromptKey,
			TemplateTitle:       mappedTemplate.Title,
			TemplateDescription: mappedTemplate.Description,
			TemplateWhenToUse:   mappedTemplate.WhenToUse,
			TemplateTags:        cloneRuntimeRawJSON(templateTags),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Ordinal != out[j].Ordinal {
			return out[i].Ordinal < out[j].Ordinal
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].SectionKey < out[j].SectionKey
	})
	return out
}

func runtimeBuiltinTemplateToStore(template contract.BuiltinPromptTemplate) contract.PromptTemplate {
	return contract.PromptTemplate{
		ID:          runtimeBuiltinTemplateID(template.ID),
		PromptKey:   template.PromptKey,
		Title:       template.Title,
		AgentKey:    template.AgentKey,
		ToolName:    template.ToolName,
		PromptText:  template.PromptText,
		WhenToUse:   template.WhenToUse,
		Variables:   json.RawMessage("{}"),
		Tags:        runtimeBuiltinTags(template),
		Enabled:     template.Enabled,
		MatchWhen:   cloneRuntimeRawJSON(template.MatchWhen),
		Priority:    template.Priority,
		CreatedBy:   runtimeBuiltinAuthor,
		UpdatedBy:   runtimeBuiltinAuthor,
		Description: template.Description,
	}
}

func runtimeBuiltinTemplateID(id int64) int64 {
	switch {
	case id < 0:
		return id
	case id > 0:
		return -id
	default:
		return -1
	}
}

func runtimeBuiltinTags(template contract.BuiltinPromptTemplate) json.RawMessage {
	tags := runtimeNormalizedTags(template.Tags)
	tags = runtimeAppendTagIfMissing(tags, runtimeBuiltinSystemTag)
	if strings.EqualFold(strings.TrimSpace(template.Scope), "global") {
		tags = runtimeAppendTagIfMissing(tags, runtimeScopeGlobalTag)
	}
	return runtimeEncodeTags(tags)
}

func runtimeNormalizedTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func runtimeAppendTagIfMissing(tags []string, tag string) []string {
	for _, current := range tags {
		if current == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func runtimeEncodeTags(tags []string) json.RawMessage {
	raw, err := json.Marshal(tags)
	if err != nil {
		return json.RawMessage("[]")
	}
	return raw
}

func runtimeEnsureGlobalScopeWhenUnscoped(raw json.RawMessage) json.RawMessage {
	tags := contract.PromptTemplateTags(raw)
	hasScope := false
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == runtimeScopeGlobalTag || strings.HasPrefix(tag, runtimeScopeCWDTagPrefix) {
			hasScope = true
			break
		}
	}
	if hasScope {
		return cloneRuntimeRawJSON(raw)
	}
	return runtimeEncodeTags(runtimeAppendTagIfMissing(tags, runtimeScopeGlobalTag))
}

// runtimeTemplateMatchesFilter 处理运行时templatematches过滤条件。
func runtimeTemplateMatchesFilter(template contract.PromptTemplate, filter RuntimeListFilter) bool {
	if !runtimeTemplateVisibleForRead(template, filter.CWD) {
		return false
	}
	if agentKey := strings.TrimSpace(filter.AgentKey); agentKey != "" && strings.TrimSpace(template.AgentKey) != agentKey {
		return false
	}
	keyword := strings.ToLower(strings.TrimSpace(filter.Keyword))
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(template.PromptKey), keyword) ||
		strings.Contains(strings.ToLower(template.Title), keyword) ||
		strings.Contains(strings.ToLower(template.PromptText), keyword) ||
		strings.Contains(strings.ToLower(template.Description), keyword) ||
		strings.Contains(strings.ToLower(template.WhenToUse), keyword)
}

func runtimeStoreTemplateHiddenByBuiltin(template contract.PromptTemplate, builtinKeys map[string]struct{}) bool {
	_, hasBuiltin := builtinKeys[strings.TrimSpace(template.PromptKey)]
	return hasBuiltin
}

func runtimePickTemplateForGet(
	dbTemplate *contract.PromptTemplate,
	builtin *contract.PromptTemplate,
) *contract.PromptTemplate {
	if builtin != nil {
		return builtin
	}
	return dbTemplate
}

func runtimeDefaultRuleSections(sections []contract.PromptTemplateSection) []contract.PromptTemplateSection {
	out := make([]contract.PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		if section.TriggerType == "always" && section.Enabled && strings.TrimSpace(section.Body) != "" {
			out = append(out, section)
		}
	}
	return out
}

// runtimeTemplateVisibleForRead 为read处理运行时templatevisible。
func runtimeTemplateVisibleForRead(template contract.PromptTemplate, cwd string) bool {
	if !template.Enabled {
		return false
	}
	cwd = strings.TrimSpace(cwd)
	tags := contract.PromptTemplateTags(template.Tags)
	hasCWDTag := false
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		switch {
		case tag == runtimeScopeGlobalTag:
			return true
		case strings.HasPrefix(tag, runtimeScopeCWDTagPrefix):
			hasCWDTag = true
			if cwd != "" && strings.TrimPrefix(tag, runtimeScopeCWDTagPrefix) == cwd {
				return true
			}
		}
	}
	return !hasCWDTag
}

func sortRuntimeTemplates(templates []contract.PromptTemplate) {
	sort.SliceStable(templates, func(i, j int) bool {
		left := templates[i]
		right := templates[j]
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if strings.TrimSpace(left.PromptKey) != strings.TrimSpace(right.PromptKey) {
			return strings.TrimSpace(left.PromptKey) < strings.TrimSpace(right.PromptKey)
		}
		return left.ID < right.ID
	})
}

// limitRuntimeTemplates 处理limit运行时templates。
func limitRuntimeTemplates(templates []contract.PromptTemplate, limit int32) []contract.PromptTemplate {
	if limit <= 0 || int(limit) >= len(templates) {
		return templates
	}
	protected := make([]contract.PromptTemplate, 0, len(templates))
	others := make([]contract.PromptTemplate, 0, len(templates))
	for _, template := range templates {
		if runtimeTemplateIsBuiltin(template) {
			protected = append(protected, template)
			continue
		}
		others = append(others, template)
	}
	if len(protected) >= int(limit) {
		return protected[:limit]
	}
	out := make([]contract.PromptTemplate, 0, int(limit))
	out = append(out, protected...)
	remaining := int(limit) - len(out)
	if remaining > len(others) {
		remaining = len(others)
	}
	return append(out, others[:remaining]...)
}

func runtimeTemplateIsBuiltin(template contract.PromptTemplate) bool {
	return strings.TrimSpace(template.CreatedBy) == runtimeBuiltinAuthor &&
		strings.TrimSpace(template.UpdatedBy) == runtimeBuiltinAuthor
}

func cloneRuntimeTemplate(template *contract.PromptTemplate) *contract.PromptTemplate {
	if template == nil {
		return nil
	}
	copy := *template
	copy.Variables = cloneRuntimeRawJSON(template.Variables)
	copy.Tags = cloneRuntimeRawJSON(template.Tags)
	copy.MatchWhen = cloneRuntimeRawJSON(template.MatchWhen)
	return &copy
}

func cloneRuntimeRawJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
