// Package threadprompt 管理线程运行时的 prompt 模板目录，负责内置模板与数据库模板的合并、过滤和分发。
package threadprompt

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

const (
	runtimeBuiltinAuthor        = "builtin.registry"
	runtimeBuiltinSystemTag     = "builtin:system"
	runtimeScopeGlobalTag       = "scope.global"
	runtimeScopeCWDTagPrefix    = "scope.cwd:"
	maxRuntimeCatalogStoreLimit = int32(1<<31 - 1)
	runtimeCatalogStoreLimitPad = int32(64)
)

// newRuntimeCatalog 组合数据库模板和内置模板的运行时读取视图。
// 两个来源都为空时返回 nil，让调用方显式跳过 prompt 路由而不是制造空 catalog。
func newRuntimeCatalog(store PromptStore, builtin contract.BuiltinPromptRegistry) RuntimePromptCatalog {
	if store == nil && builtin == nil {
		return nil
	}
	return &runtimePromptCatalog{store: store, builtin: builtin}
}

type runtimePromptCatalog struct {
	store   PromptStore
	builtin contract.BuiltinPromptRegistry
}

// ListTemplates 列出当前 CWD 和过滤条件可见的模板。
// 内置模板优先于同 key 数据库模板，最终结果按运行时排序并应用 limit。
func (c *runtimePromptCatalog) ListTemplates(ctx context.Context, filter RuntimeListFilter) ([]PromptTemplate, error) {
	out, builtinKeys := c.listBuiltinTemplates(filter)
	storeTemplates, err := c.listStoreTemplates(ctx, filter, builtinKeys)
	if err != nil {
		return nil, err
	}
	out = append(out, storeTemplates...)
	sortRuntimeTemplates(out)
	return limitRuntimeTemplates(out, filter.Limit), nil
}

// GetTemplate 读取单个 prompt_key 对应的模板。
// 内置模板与数据库模板同时存在时合并运行时 section，数据库读取失败会直接返回错误。
func (c *runtimePromptCatalog) GetTemplate(ctx context.Context, promptKey, cwd string) (*PromptTemplate, error) {
	promptKey = strings.TrimSpace(promptKey)
	cwd = strings.TrimSpace(cwd)
	builtin := c.builtinTemplateForKey(promptKey, cwd)
	dbTemplate, _, err := c.storeTemplateForKey(ctx, promptKey, cwd)
	if err != nil {
		return nil, err
	}
	picked := runtimePickTemplateForGet(dbTemplate, builtin)
	if picked == nil {
		return nil, fmt.Errorf("runtime prompt catalog: template %q not found: %w", promptKey, platformdb.ErrNotFound)
	}
	return cloneRuntimeTemplate(picked), nil
}

// ListSectionsByTemplateID 按templateID列出sections。
func (c *runtimePromptCatalog) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error) {
	if templateID < 0 {
		if c.builtin == nil {
			return nil, fmt.Errorf("runtime prompt catalog: builtin prompt registry is required for template_id %d", templateID)
		}
		template, ok := c.builtinTemplateByID(templateID)
		if !ok {
			return []PromptTemplateSection{}, nil
		}
		return c.builtinSectionsForTemplate(template, false), nil
	}
	if c.store == nil {
		return nil, fmt.Errorf("runtime prompt catalog: prompt store is required for template_id %d", templateID)
	}
	return c.store.ListSectionsByTemplateID(ctx, templateID)
}

// ListRecallSections 列出recallsections。
func (c *runtimePromptCatalog) ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	cwd = strings.TrimSpace(cwd)
	var out []PromptTemplateSection
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
func (c *runtimePromptCatalog) ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
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
func (c *runtimePromptCatalog) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	if c.store == nil {
		return 0, fmt.Errorf("runtime prompt catalog: prompt store is required for insert_version")
	}
	return c.store.InsertVersion(ctx, version)
}

// CanInsertPromptVersion 判断当前 catalog 是否连接了可写 prompt store。
// 运行时只读 catalog 会返回 false，调用方据此禁用版本写入入口。
func (c *runtimePromptCatalog) CanInsertPromptVersion() bool {
	return c != nil && c.store != nil
}

// listBuiltinTemplates 列出内置模板并收集其 prompt key 集合，用于后续隐藏同名数据库模板。
func (c *runtimePromptCatalog) listBuiltinTemplates(filter RuntimeListFilter) ([]PromptTemplate, map[string]struct{}) {
	keys := map[string]struct{}{}
	if c.builtin == nil {
		return nil, keys
	}
	out := make([]PromptTemplate, 0, len(c.builtin.ListTemplates()))
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
) ([]PromptTemplate, error) {
	if c.store == nil {
		return nil, nil
	}
	if strings.TrimSpace(filter.CWD) == "" {
		return nil, nil
	}
	templates, err := c.store.List(ctx, PromptListFilter{
		AgentKey: strings.TrimSpace(filter.AgentKey),
		Keyword:  c.storeListKeyword(filter),
		CWD:      strings.TrimSpace(filter.CWD),
		Limit:    c.storeListLimit(filter),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PromptTemplate, 0, len(templates))
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

// runtimeCatalogStoreLimitWithBuiltin 在 filter.Limit 基础上加内置模板数量与 pad，确保数据库查询不会因 builtin 占位而截断用户模板。
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

// builtinTemplateForKey 按 promptKey 查找内置模板，CWD 可见性检查通过后返回副本。
func (c *runtimePromptCatalog) builtinTemplateForKey(promptKey, cwd string) *PromptTemplate {
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
func (c *runtimePromptCatalog) storeTemplateForKey(ctx context.Context, promptKey, cwd string) (*PromptTemplate, bool, error) {
	if c.store == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(cwd) == "" {
		return nil, false, nil
	}
	template, err := c.store.Get(ctx, promptKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, false, nil
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

// builtinDefaultRuleSections 收集当前 CWD 可见的内置 default_rule 模板的 always 类 section。
func (c *runtimePromptCatalog) builtinDefaultRuleSections(cwd string) []PromptTemplateSection {
	if c.builtin == nil {
		return nil
	}
	var out []PromptTemplateSection
	for _, template := range c.builtin.ListTemplates() {
		mapped := runtimeBuiltinTemplateToStore(template)
		if strings.TrimSpace(mapped.AgentKey) != "default_rule" || !runtimeTemplateVisibleForRead(mapped, cwd) {
			continue
		}
		out = append(out, runtimeDefaultRuleSections(c.builtinSectionsForTemplate(template, false))...)
	}
	return out
}

func (c *runtimePromptCatalog) storeDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
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

// builtinSectionsForTemplate 把内置 prompt 模板转换为运行时可保存的 section 列表。
func (c *runtimePromptCatalog) builtinSectionsForTemplate(
	template contract.BuiltinPromptTemplate,
	recallDefaultsGlobal bool,
) []PromptTemplateSection {
	mappedTemplate := runtimeBuiltinTemplateToStore(template)
	templateTags := mappedTemplate.Tags
	if recallDefaultsGlobal {
		templateTags = runtimeEnsureGlobalScopeWhenUnscoped(templateTags)
	}
	sections := c.builtin.SectionsByTemplateID(template.ID)
	out := make([]PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, PromptTemplateSection{
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

// runtimeBuiltinTemplateToStore 将内置模板转换为运行时 PromptTemplate 格式，填充 author 和 tags。
func runtimeBuiltinTemplateToStore(template contract.BuiltinPromptTemplate) PromptTemplate {
	return PromptTemplate{
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

// runtimeBuiltinTags 根据内置模板的 Scope 字段生成规范化 tag 列表，始终包含 builtin:system 标签。
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
	if slices.Contains(tags, tag) {
		return tags
	}
	return append(tags, tag)
}

func runtimeEncodeTags(tags []string) json.RawMessage {
	raw, err := json.Marshal(tags)
	if err != nil {
		// archguard:ignore panic_count -- 内部 tag 列表只包含字符串，JSON 编码失败表示编程错误。
		panic(fmt.Sprintf("runtime prompt catalog: encode tags: %v", err))
	}
	return raw
}

func runtimeEnsureGlobalScopeWhenUnscoped(raw json.RawMessage) json.RawMessage {
	tags := templateTags(raw)
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

// runtimeTemplateMatchesFilter 判断模板是否满足运行时列表过滤条件。
// 这里统一处理 CWD 可见性、agent_key 和 intent，避免调用方各自实现导致路由结果不一致。
func runtimeTemplateMatchesFilter(template PromptTemplate, filter RuntimeListFilter) bool {
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

// runtimeStoreTemplateHiddenByBuiltin 若数据库模板的 prompt_key 与任一内置模板重合则隐藏，内置模板优先。
func runtimeStoreTemplateHiddenByBuiltin(template PromptTemplate, builtinKeys map[string]struct{}) bool {
	_, hasBuiltin := builtinKeys[strings.TrimSpace(template.PromptKey)]
	return hasBuiltin
}

// runtimePickTemplateForGet 获取模板时内置模板优先；无内置时返回数据库模板。
func runtimePickTemplateForGet(
	dbTemplate *PromptTemplate,
	builtin *PromptTemplate,
) *PromptTemplate {
	if builtin != nil {
		return builtin
	}
	return dbTemplate
}

// runtimeDefaultRuleSections 从 sections 中筛选 trigger_type=always 且 body 非空的已启用条目。
func runtimeDefaultRuleSections(sections []PromptTemplateSection) []PromptTemplateSection {
	out := make([]PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		if section.TriggerType == "always" && section.Enabled && strings.TrimSpace(section.Body) != "" {
			out = append(out, section)
		}
	}
	return out
}

// runtimeTemplateVisibleForRead 判断运行时模板是否应在当前 cwd 读取结果中可见。
func runtimeTemplateVisibleForRead(template PromptTemplate, cwd string) bool {
	if !template.Enabled {
		return false
	}
	cwd = strings.TrimSpace(cwd)
	tags := templateTags(template.Tags)
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

// sortRuntimeTemplates 按 updated_at 降序排列，同时间按 prompt_key 升序、id 升序作为稳定 tie-break。
func sortRuntimeTemplates(templates []PromptTemplate) {
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

// limitRuntimeTemplates 应用列表 limit，并返回新的切片视图。
// limit <= 0 表示不截断；调用方仍拥有原始排序结果。
func limitRuntimeTemplates(templates []PromptTemplate, limit int32) []PromptTemplate {
	if limit <= 0 || int(limit) >= len(templates) {
		return templates
	}
	protected := make([]PromptTemplate, 0, len(templates))
	others := make([]PromptTemplate, 0, len(templates))
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
	out := make([]PromptTemplate, 0, int(limit))
	out = append(out, protected...)
	remaining := min(int(limit)-len(out), len(others))
	return append(out, others[:remaining]...)
}

// runtimeTemplateIsBuiltin 判断模板是否由内置 registry 写入（通过 created_by/updated_by 标识）。
func runtimeTemplateIsBuiltin(template PromptTemplate) bool {
	return strings.TrimSpace(template.CreatedBy) == runtimeBuiltinAuthor &&
		strings.TrimSpace(template.UpdatedBy) == runtimeBuiltinAuthor
}

// cloneRuntimeTemplate 深拷贝 PromptTemplate，包括 JSON 字段，防止调用方修改影响内部状态。
func cloneRuntimeTemplate(template *PromptTemplate) *PromptTemplate {
	if template == nil {
		return nil
	}
	copy := *template
	copy.Variables = cloneRuntimeRawJSON(template.Variables)
	copy.Tags = cloneRuntimeRawJSON(template.Tags)
	copy.MatchWhen = cloneRuntimeRawJSON(template.MatchWhen)
	return &copy
}

// cloneRuntimeRawJSON 深拷贝 json.RawMessage，避免共享底层 byte slice。
func cloneRuntimeRawJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
