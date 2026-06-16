package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const promptSectionsPreviewMaxRunes = 200
const maxPromptStoreListLimit int32 = 1<<31 - 1

const (
	promptBuiltinAuthor     = "builtin.registry"
	promptBuiltinSystemTag  = "builtin:system"
	promptScopeGlobalTag    = "scope.global"
	promptScopeCWDTagPrefix = "scope.cwd:"
)

type promptListInput struct {
	Keyword  string `json:"keyword,omitempty"`
	Envelope bool   `json:"envelope,omitempty"`
}

type promptGetInput struct {
	PromptKey string `json:"prompt_key"`
	Pos       string `json:"pos,omitempty"`
}

type promptTemplateDTO struct {
	ID             int64           `json:"id"`
	PromptKey      string          `json:"prompt_key"`
	Title          string          `json:"title"`
	AgentKey       string          `json:"agent_key"`
	ToolName       string          `json:"tool_name"`
	PromptText     string          `json:"prompt_text"`
	Variables      json.RawMessage `json:"variables,omitempty"`
	Tags           json.RawMessage `json:"tags,omitempty"`
	Enabled        bool            `json:"enabled"`
	ManuallyEdited bool            `json:"manually_edited"`
	CreatedBy      string          `json:"created_by"`
	UpdatedBy      string          `json:"updated_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Description    string          `json:"description"`
}

type PromptListOutput struct {
	Prompts   []promptTemplateDTO `json:"prompts"`
	Data      []promptTemplateDTO `json:"data"`
	Total     int                 `json:"total"`
	Showing   int                 `json:"showing"`
	Truncated bool                `json:"truncated"`
	Hint      string              `json:"hint,omitempty"`
}

type promptListCandidate struct {
	dto       promptTemplateDTO
	priority  int
	updatedAt time.Time
}

// HandlePromptList 列出运行时可发现的 prompt 模板，合并内置模板和数据库模板。
func HandlePromptList(store promptstore.Store, builtin contract.BuiltinPromptRegistry) ToolHandler {
	return makeHandler(store, "prompt store", func(ctx context.Context, in promptListInput) (any, error) {
		templates, err := listPromptTemplates(ctx, store, builtin, in)
		if err != nil {
			return nil, err
		}
		if in.Envelope {
			return newPromptListOutput(templates), nil
		}
		return templates, nil
	})
}

// HandlePromptGet 读取单个 prompt 模板；同 key 时内置模板优先于数据库模板。
func HandlePromptGet(store promptstore.Store, builtin contract.BuiltinPromptRegistry) ToolHandler {
	return makeHandler(store, "prompt store", func(ctx context.Context, in promptGetInput) (promptTemplateDTO, error) {
		return getPromptTemplate(ctx, store, builtin, in)
	})
}

func promptToolDefinitions(store promptstore.Store, builtin contract.BuiltinPromptRegistry) []ToolDefinition {
	return resourceToolDefinitions(resourceToolSpec{
		ListName:        "prompt_list",
		ListDescription: "List available prompt templates. Templates can be used to generate structured prompts for agents.",
		GetName:         "prompt_get",
		GetDescription:  "Get a specific prompt template by its key, including the prompt text and variables.",
		KeyField:        "prompt_key",
		KeyDescription:  "Prompt template key.",
		ListHandler:     HandlePromptList(store, builtin),
		GetHandler:      HandlePromptGet(store, builtin),
	})
}

// listPromptTemplates 先建立内置 prompt 的可见 key 集合，再查询 DB 并合并排序。
// 隐藏同 key DB 模板必须早于 keyword 过滤，防止历史 seed 通过搜索词绕过内置优先级。
func listPromptTemplates(
	ctx context.Context,
	store promptstore.Store,
	builtin contract.BuiltinPromptRegistry,
	input promptListInput,
) ([]promptTemplateDTO, error) {
	if err := requireDependency(store, "prompt store"); err != nil {
		return nil, err
	}
	cwd, err := promptToolTrustedCWD(ctx)
	if err != nil {
		return nil, err
	}
	keyword := strings.TrimSpace(input.Keyword)
	candidates, builtinKeys, err := builtinPromptTemplateDTOs(builtin, cwd, keyword)
	if err != nil {
		return nil, err
	}
	templates, err := store.List(ctx, promptstore.ListFilter{
		AgentKey:       "",
		Keyword:        "",
		CWD:            cwd,
		RuntimeVisible: true,
		Limit:          maxPromptStoreListLimit,
	})
	if err != nil {
		return nil, err
	}
	for _, template := range templates {
		key := strings.TrimSpace(template.PromptKey)
		if _, hidden := builtinKeys[key]; hidden {
			continue
		}
		if !promptTemplateRuntimeVisible(template, cwd) || !promptTemplateMatchesKeyword(template, keyword) {
			continue
		}
		candidates = append(candidates, promptListCandidate{
			dto:       promptTemplateFromStore(template),
			priority:  int(template.Priority),
			updatedAt: template.UpdatedAt,
		})
	}
	sortPromptListCandidates(candidates)
	return limitPromptListCandidates(candidates, int(resourceListLimit)), nil
}

func newPromptListOutput(templates []promptTemplateDTO) PromptListOutput {
	env := newListEnvelope(templates, int(resourceListLimit), "next: use prompt_get pos=prompt:<prompt_key> for full prompt")
	return PromptListOutput{
		Prompts:   templates,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// getPromptTemplate 先查内置 registry，只有未命中时才访问 DB。
// 这样 prompt_list 与 prompt_get 对同 key 的来源选择保持一致。
func getPromptTemplate(
	ctx context.Context,
	store promptstore.Store,
	builtin contract.BuiltinPromptRegistry,
	input promptGetInput,
) (promptTemplateDTO, error) {
	if err := requireDependency(store, "prompt store"); err != nil {
		return promptTemplateDTO{}, err
	}
	cwd, err := promptToolTrustedCWD(ctx)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	promptKey, err := resolvePromptKeyInput(input.PromptKey, input.Pos)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	if dto, ok, err := getBuiltinPromptTemplate(builtin, promptKey, cwd); err != nil {
		return promptTemplateDTO{}, err
	} else if ok {
		return dto, nil
	}
	return getStorePromptTemplate(ctx, store, promptKey, cwd)
}

// getStorePromptTemplate 读取 DB prompt，并把 not found 或不可见模板统一收口成 not found。
func getStorePromptTemplate(
	ctx context.Context,
	store promptstore.Store,
	promptKey, cwd string,
) (promptTemplateDTO, error) {
	template, err := store.Get(ctx, promptKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return promptTemplateDTO{}, promptNotFoundError(promptKey)
		}
		return promptTemplateDTO{}, err
	}
	if template == nil || !promptTemplateRuntimeVisible(*template, cwd) {
		return promptTemplateDTO{}, promptNotFoundError(promptKey)
	}
	dto := promptTemplateFromStore(*template)
	sections, err := store.ListSectionsByTemplateID(ctx, template.ID)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	if preview := promptSectionsPreview(sections); preview != "" {
		dto.PromptText = preview
	}
	return dto, nil
}

func promptNotFoundError(promptKey string) error {
	return fmt.Errorf("prompt %s not found", promptKey)
}

func promptToolTrustedCWD(ctx context.Context) (string, error) {
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.CWD) == "" {
		return "", fmt.Errorf("prompt tools require trusted cwd")
	}
	return scope.CWD, nil
}

func promptTemplateRuntimeVisible(template promptstore.PromptTemplate, cwd string) bool {
	if !template.Enabled {
		return false
	}
	return promptTagsContainScope(template.Tags, strings.TrimSpace(cwd))
}

// builtinPromptTemplateVisible 判断内置模板是否对当前工作区可见。
// 当前只把空 scope 和 global 视为全局；cwd scope 保留给后续扩展。
func builtinPromptTemplateVisible(template contract.BuiltinPromptTemplate, cwd string) bool {
	if !template.Enabled {
		return false
	}
	scope := strings.TrimSpace(template.Scope)
	if scope == "" || strings.EqualFold(scope, "global") {
		return true
	}
	if strings.HasPrefix(scope, promptScopeCWDTagPrefix) {
		return strings.TrimSpace(cwd) != "" && strings.TrimPrefix(scope, promptScopeCWDTagPrefix) == cwd
	}
	return false
}

// promptTagsContainScope 判断 prompt tags 是否包含指定 scope。
func promptTagsContainScope(raw json.RawMessage, cwd string) bool {
	if cwd == "" {
		return false
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return false
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "scope.global" || tag == "scope.cwd:"+cwd {
			return true
		}
	}
	return false
}

func mapPromptTemplates(templates []promptstore.PromptTemplate) []promptTemplateDTO {
	mapped := make([]promptTemplateDTO, 0, len(templates))
	for _, template := range templates {
		mapped = append(mapped, promptTemplateFromStore(template))
	}
	return mapped
}

// builtinPromptTemplateDTOs 返回 keyword 命中的内置候选，同时返回所有可见内置 key 用于隐藏 DB 同 key。
func builtinPromptTemplateDTOs(
	builtin contract.BuiltinPromptRegistry,
	cwd, keyword string,
) ([]promptListCandidate, map[string]struct{}, error) {
	keys := map[string]struct{}{}
	if builtin == nil {
		return nil, keys, nil
	}
	templates := builtin.ListTemplates()
	candidates := make([]promptListCandidate, 0, len(templates))
	for _, template := range templates {
		if !builtinPromptTemplateVisible(template, cwd) {
			continue
		}
		key := strings.TrimSpace(template.PromptKey)
		if key != "" {
			keys[key] = struct{}{}
		}
		if !builtinPromptMatchesKeyword(template, keyword) {
			continue
		}
		dto, err := promptTemplateFromBuiltin(template)
		if err != nil {
			return nil, nil, err
		}
		candidates = append(candidates, promptListCandidate{
			dto:      dto,
			priority: template.Priority,
		})
	}
	return candidates, keys, nil
}

// getBuiltinPromptTemplate 从注入的 registry 读取内置模板，并用 section 生成可执行预览。
// 命中时不访问 DB，确保同 key 的旧 seed 不会覆盖内置定义。
func getBuiltinPromptTemplate(
	builtin contract.BuiltinPromptRegistry,
	promptKey, cwd string,
) (promptTemplateDTO, bool, error) {
	if builtin == nil {
		return promptTemplateDTO{}, false, nil
	}
	template, ok := builtin.GetTemplate(promptKey)
	if !ok || !builtinPromptTemplateVisible(template, cwd) {
		return promptTemplateDTO{}, false, nil
	}
	dto, err := promptTemplateFromBuiltin(template)
	if err != nil {
		return promptTemplateDTO{}, false, err
	}
	sections := builtin.SectionsByTemplateID(template.ID)
	if preview := promptSectionsPreviewFromBuiltin(sections); preview != "" {
		dto.PromptText = preview
	}
	return dto, true, nil
}

func promptTemplateFromStore(template promptstore.PromptTemplate) promptTemplateDTO {
	return promptTemplateDTO{
		ID:             template.ID,
		PromptKey:      template.PromptKey,
		Title:          template.Title,
		AgentKey:       template.AgentKey,
		ToolName:       template.ToolName,
		PromptText:     template.PromptText,
		Variables:      shared.CloneRawMessage(template.Variables),
		Tags:           shared.CloneRawMessage(template.Tags),
		Enabled:        template.Enabled,
		ManuallyEdited: template.ManuallyEdited,
		CreatedBy:      template.CreatedBy,
		UpdatedBy:      template.UpdatedBy,
		CreatedAt:      template.CreatedAt,
		UpdatedAt:      template.UpdatedAt,
		Description:    template.Description,
	}
}

func promptTemplateFromBuiltin(template contract.BuiltinPromptTemplate) (promptTemplateDTO, error) {
	tags, err := builtinPromptTags(template)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	return promptTemplateDTO{
		ID:          builtinPromptTemplateID(template.ID),
		PromptKey:   template.PromptKey,
		Title:       template.Title,
		AgentKey:    template.AgentKey,
		ToolName:    template.ToolName,
		PromptText:  template.PromptText,
		Variables:   json.RawMessage("{}"),
		Tags:        tags,
		Enabled:     template.Enabled,
		CreatedBy:   promptBuiltinAuthor,
		UpdatedBy:   promptBuiltinAuthor,
		Description: template.Description,
	}, nil
}

func builtinPromptTags(template contract.BuiltinPromptTemplate) (json.RawMessage, error) {
	tags := normalizedPromptTags(template.Tags)
	tags = appendPromptTagIfMissing(tags, promptBuiltinSystemTag)
	scope := strings.TrimSpace(template.Scope)
	if scope == "" || strings.EqualFold(scope, "global") {
		tags = appendPromptTagIfMissing(tags, promptScopeGlobalTag)
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return nil, fmt.Errorf("encode builtin prompt tags: %w", err)
	}
	return raw, nil
}

func normalizedPromptTags(tags []string) []string {
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

func appendPromptTagIfMissing(tags []string, tag string) []string {
	for _, current := range tags {
		if current == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func builtinPromptTemplateID(id int64) int64 {
	switch {
	case id < 0:
		return id
	case id > 0:
		return -id
	default:
		return -1
	}
}

// promptTemplateMatchesKeyword 在工具层过滤 DB 模板，补齐 SQL 未覆盖的 description/when_to_use 字段。
func promptTemplateMatchesKeyword(template promptstore.PromptTemplate, keyword string) bool {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(template.PromptKey), keyword) ||
		strings.Contains(strings.ToLower(template.Title), keyword) ||
		strings.Contains(strings.ToLower(template.PromptText), keyword) ||
		strings.Contains(strings.ToLower(template.Description), keyword) ||
		strings.Contains(strings.ToLower(template.WhenToUse), keyword)
}

// builtinPromptMatchesKeyword 使用与 DB 模板一致的字段集合过滤内置模板。
func builtinPromptMatchesKeyword(template contract.BuiltinPromptTemplate, keyword string) bool {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(template.PromptKey), keyword) ||
		strings.Contains(strings.ToLower(template.Title), keyword) ||
		strings.Contains(strings.ToLower(template.PromptText), keyword) ||
		strings.Contains(strings.ToLower(template.Description), keyword) ||
		strings.Contains(strings.ToLower(template.WhenToUse), keyword)
}

func sortPromptListCandidates(candidates []promptListCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.priority != right.priority {
			return left.priority > right.priority
		}
		if !left.updatedAt.Equal(right.updatedAt) {
			return left.updatedAt.After(right.updatedAt)
		}
		return strings.TrimSpace(left.dto.PromptKey) < strings.TrimSpace(right.dto.PromptKey)
	})
}

func limitPromptListCandidates(candidates []promptListCandidate, limit int) []promptTemplateDTO {
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	out := make([]promptTemplateDTO, 0, limit)
	for _, candidate := range candidates[:limit] {
		out = append(out, candidate.dto)
	}
	return out
}

// promptSectionsPreview 处理promptsectionspreview。
func promptSectionsPreview(sections []promptstore.PromptTemplateSection) string {
	sorted := make([]promptstore.PromptTemplateSection, len(sections))
	copy(sorted, sections)
	sort.SliceStable(sorted, func(i, j int) bool { return promptSectionPreviewLess(sorted[i], sorted[j]) })
	blocks := make([]string, 0, len(sorted))
	for _, section := range sorted {
		if !section.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall") {
			continue
		}
		if body := strings.TrimSpace(section.Body); body != "" {
			blocks = append(blocks, body)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return truncatePromptSectionsPreview(strings.Join(blocks, "\n\n"))
}

func promptSectionsPreviewFromBuiltin(sections []contract.BuiltinPromptSection) string {
	mapped := make([]promptstore.PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		mapped = append(mapped, promptstore.PromptTemplateSection{
			ID:          section.ID,
			TemplateID:  section.TemplateID,
			SectionKey:  section.SectionKey,
			Region:      section.Region,
			Ordinal:     int32(section.Ordinal),
			Body:        section.Body,
			TriggerType: section.TriggerType,
			RecallTopic: section.RecallTopic,
			Enabled:     section.Enabled,
		})
	}
	return promptSectionsPreview(mapped)
}

func promptSectionPreviewLess(left, right promptstore.PromptTemplateSection) bool {
	if promptSectionRegionPriority(left.Region) != promptSectionRegionPriority(right.Region) {
		return promptSectionRegionPriority(left.Region) < promptSectionRegionPriority(right.Region)
	}
	if left.Ordinal != right.Ordinal {
		return left.Ordinal < right.Ordinal
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.SectionKey < right.SectionKey
}

func promptSectionRegionPriority(region string) int {
	if strings.EqualFold(strings.TrimSpace(region), "static") {
		return 0
	}
	return 1
}

func truncatePromptSectionsPreview(text string) string {
	runes := []rune(text)
	if len(runes) <= promptSectionsPreviewMaxRunes {
		return text
	}
	return string(runes[:promptSectionsPreviewMaxRunes])
}
