package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// PromptService 是 dashboard/RPC 层使用的 prompt 持久化接口。
// SetEnabled 暂不进入该接口，直到 store/service 合约补齐端到端实现。
type PromptService interface {
	ListPrompts(ctx context.Context, cwd, keyword string) ([]promptstore.PromptTemplate, error)
	ListPromptSectionsByTemplates(ctx context.Context, cwd string, templates []promptstore.PromptTemplate) (map[int64][]promptstore.PromptTemplateSection, error)
	GetPrompt(ctx context.Context, cwd, key string) (*promptstore.PromptTemplate, error)
	WritePrompt(ctx context.Context, cwd string, prompt PromptWriteRequest) (*promptstore.PromptTemplate, error)
	DeletePrompt(ctx context.Context, cwd, key string, scope ...string) error
	// ListSections / WriteSection / DeleteSection 只服务高级调试 UI。
	// 普通编辑仍走模板级 PromptText；section 写入会影响缓存前缀、动态尾部和 enable_when 注入边界。
	ListSections(ctx context.Context, cwd, promptKey string) ([]promptstore.PromptTemplateSection, error)
	WriteSection(ctx context.Context, cwd string, req PromptSectionWriteRequest) (*promptstore.PromptTemplateSection, error)
	DeleteSection(ctx context.Context, cwd, promptKey, sectionKey string, scope ...string) error
}

// PromptWriteRequest 是服务层 prompt 写入请求，保留字段是否显式传入的状态。
type PromptWriteRequest struct {
	ID, Name, Content, Description, AgentType string
	// ContentSet 区分未传 content（保留现有 prompt_text）和显式空串（清空 prompt_text）。
	ContentSet bool
	// WhenToUseSet 区分未传 when_to_use（保留元数据）和显式空串（清空元数据）。
	WhenToUse    string
	WhenToUseSet bool
	// MatchWhen 写入自动路由规则；MatchWhenSet 区分未传（保留）和显式 nil/空值（退出自动路由）。
	MatchWhen    json.RawMessage
	MatchWhenSet bool
	Priority     int
	Enabled      *bool
	Scope        string
	ScopeSet     bool
	// Tags 只承载客户端可见场景标签；内部 scope:// tags 在写入路径单独合并。
	Tags json.RawMessage
}

// PromptSectionWriteRequest 是高级调试 UI 的 section upsert 请求。
// PromptKey 指向父模板，SectionKey 是模板内稳定键；EnableWhen 保留原始 JSONB，空值表示始终注入。
type PromptSectionWriteRequest struct {
	PromptKey   string
	SectionKey  string
	Region      string // "static" | "dynamic"
	Ordinal     int
	Body        string
	EnableWhen  []byte
	Enabled     bool
	TriggerType string
	RecallTopic string
	Scope       string
	ScopeSet    bool
}

// promptService 把 RPC 调用桥接到 prompt store，并负责 section cache 失效。
type promptService struct {
	store    promptstore.Store
	sections contract.SectionInvalidator
	builtin  contract.BuiltinPromptRegistry
}

// promptListParams 是 prompts/list 的 RPC wire 请求体，只按 cwd 过滤可见模板。
type promptListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

// promptWriteParams 是 prompts/write 的 RPC wire 请求体。
// 指针字段保留“未传”和“显式清空”的差异，避免更新时误清空已有元数据。
type promptWriteParams struct {
	ID          string                   `json:"id,omitempty"`
	Name        string                   `json:"name"`
	Content     *string                  `json:"content,omitempty"`
	Description string                   `json:"description,omitempty"`
	AgentType   string                   `json:"agentType,omitempty"`
	WhenToUse   *string                  `json:"when_to_use,omitempty"`
	Cwd         string                   `json:"cwd,omitempty"`
	MatchWhen   promptOptionalRawMessage `json:"match_when,omitempty"`
	Priority    int                      `json:"priority,omitempty"`
	Enabled     *bool                    `json:"enabled,omitempty"`
	Scope       *string                  `json:"scope,omitempty"`
	Tags        json.RawMessage          `json:"tags,omitempty"`
}

// promptOptionalRawMessage 保留 JSON 字段是否出现，区分缺省和显式 null。
type promptOptionalRawMessage struct {
	Raw json.RawMessage
	Set bool
}

// UnmarshalJSON 记录字段已出现，并按原样保存非 null JSON。
func (m *promptOptionalRawMessage) UnmarshalJSON(data []byte) error {
	m.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		m.Raw = nil
		return nil
	}
	m.Raw = append(m.Raw[:0], data...)
	return nil
}

// promptDeleteParams 是 prompts/delete 的 RPC wire 请求体；scope 只有显式传入时才参与额外校验。
type promptDeleteParams struct {
	ID    string  `json:"id"`
	Cwd   string  `json:"cwd,omitempty"`
	Scope *string `json:"scope,omitempty"`
}

// promptGetParams 是 prompts/get 的 RPC wire 请求体，ID 对应 prompt_key。
type promptGetParams struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd,omitempty"`
}

// promptSectionListParams 是 prompt-sections/list 的 RPC wire 请求体，面向高级调试 UI。
type promptSectionListParams struct {
	PromptID string `json:"prompt_id"`
	Cwd      string `json:"cwd,omitempty"`
}

// promptSectionWriteParams 是 prompt-sections/write 的 RPC wire 请求体。
// Enabled 和 Scope 使用指针区分缺省值，防止调试 UI 的部分更新误改注入状态或范围。
type promptSectionWriteParams struct {
	PromptID    string          `json:"prompt_id"`
	SectionKey  string          `json:"section_key"`
	Region      string          `json:"region"`
	Ordinal     int             `json:"ordinal"`
	Body        string          `json:"body"`
	EnableWhen  json.RawMessage `json:"enable_when,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	TriggerType string          `json:"trigger_type,omitempty"`
	RecallTopic string          `json:"recall_topic,omitempty"`
	Cwd         string          `json:"cwd,omitempty"`
	Scope       *string         `json:"scope,omitempty"`
}

// promptSectionDeleteParams 是 prompt-sections/delete 的 RPC wire 请求体。
// Scope 为空时沿用旧删除路径，显式传入时才触发跨 cwd/scope 保护。
type promptSectionDeleteParams struct {
	PromptID   string  `json:"prompt_id"`
	SectionKey string  `json:"section_key"`
	Cwd        string  `json:"cwd,omitempty"`
	Scope      *string `json:"scope,omitempty"`
}

// promptSectionRPCItem 是 prompt section 的 RPC 响应形状，字段名保持前端兼容。
type promptSectionRPCItem struct {
	ID          int64           `json:"id"`
	PromptID    string          `json:"prompt_id"`
	SectionKey  string          `json:"section_key"`
	Region      string          `json:"region"`
	Ordinal     int             `json:"ordinal"`
	Body        string          `json:"body"`
	EnableWhen  json.RawMessage `json:"enable_when,omitempty"`
	Enabled     bool            `json:"enabled"`
	TriggerType string          `json:"trigger_type"`
	RecallTopic string          `json:"recall_topic"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// promptRPCItem 是 prompt 模板面向前端的 RPC 响应形状，隐藏内部 scope tags。
type promptRPCItem struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Content     string          `json:"content"`
	Description string          `json:"description"`
	AgentType   string          `json:"agentType"`
	WhenToUse   string          `json:"when_to_use"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	MatchWhen   json.RawMessage `json:"match_when,omitempty"`
	Priority    int             `json:"priority,omitempty"`
	Enabled     bool            `json:"enabled"`
	Scope       string          `json:"scope,omitempty"`
	Tags        json.RawMessage `json:"tags,omitempty"`
}

// promptListContentPreviewMaxRunes 限制列表页 section 预览长度，详情页仍保留完整内容。
const promptListContentPreviewMaxRunes = 200

// promptRecallTopicPattern 限定 recall topic 为短小 lowercase dash-separated slug。
var promptRecallTopicPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// promptService 接口断言保证组装服务和 RPC 服务持续满足跨模块契约。
var _ contract.PromptAssemblyService = (*service)(nil)
var _ PromptService = (*promptService)(nil)

// NewService 创建 prompt 组装服务并注册全部内置动态 provider。
func NewService(cfg *Config, logger *slog.Logger, opts ...ServiceOption) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	svc := &service{
		cfg:              cfg,
		logger:           logger,
		registry:         NewSectionRegistry(),
		cache:            newSectionCache(),
		userContextCache: newUserContextCache(),
		dynamic:          map[string]DynamicSectionProvider{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	svc.registerBuiltInSections()
	mustRegisterDynamicProvider(svc, SessionGuidanceProvider{})
	mustRegisterDynamicProvider(svc, EnvInfoProvider{})
	mustRegisterDynamicProvider(svc, LanguageProvider{})
	mustRegisterDynamicProvider(svc, MCPInstructionsProvider{})
	mustRegisterDynamicProvider(svc, OutputStyleProvider{})
	mustRegisterDynamicProvider(svc, ScratchpadProvider{})
	mustRegisterDynamicProvider(svc, FRCProvider{})
	mustRegisterDynamicProvider(svc, SummarizeToolResultsProvider{})
	mustRegisterDynamicProvider(svc, NumericLengthAnchorsProvider{})
	mustRegisterDynamicProvider(svc, TokenBudgetProvider{})
	mustRegisterDynamicProvider(svc, BriefProvider{})
	return svc
}

// newPromptService 创建不带 builtin registry 的 prompt RPC 服务，主要用于测试和基础装配。
func newPromptService(store promptstore.Store, sections ...contract.SectionInvalidator) PromptService {
	return newPromptServiceWithBuiltin(store, nil, sections...)
}

// newPromptServiceWithBuiltin 创建带 builtin registry 的 prompt RPC 服务，用于拒绝修改内置 prompt。
func newPromptServiceWithBuiltin(
	store promptstore.Store,
	builtin contract.BuiltinPromptRegistry,
	sections ...contract.SectionInvalidator,
) PromptService {
	var sectionInvalidator contract.SectionInvalidator
	if len(sections) > 0 {
		sectionInvalidator = sections[0]
	}
	return &promptService{store: store, sections: sectionInvalidator, builtin: builtin}
}

// buildPromptHandlersWithService 注册 prompt 相关 JSON-RPC handlers，并从 deps 中拾取可选依赖。
func buildPromptHandlersWithService(promptSvc PromptService, deps ...any) platformrpc.HandlerMapResult {
	var promptStore promptstore.Store
	var sectionInvalidator contract.SectionInvalidator
	var dream contract.DreamExecutor
	var builtin contract.BuiltinPromptRegistry
	var emitPromptsChanged func(uidto.UIPromptsChanged)
	for _, dep := range deps {
		switch value := dep.(type) {
		case promptstore.Store:
			promptStore = value
		case contract.SectionInvalidator:
			sectionInvalidator = value
		case contract.DreamExecutor:
			dream = value
		case contract.BuiltinPromptRegistry:
			builtin = value
		case *event.Dispatcher:
			emitPromptsChanged = contract.NewEmitter[uidto.UIPromptsChanged](value)
		}
	}
	handlers := handler.Map{
		"prompts/list": platformrpc.StrictHandler(func(ctx context.Context, p promptListParams) (any, error) { return handlePromptList(ctx, promptSvc, p) }),
		"prompt-assets/list": platformrpc.StrictHandler(func(ctx context.Context, p promptAssetListParams) (any, error) {
			return handlePromptAssetList(ctx, promptStore, p)
		}),
		"prompts/get": platformrpc.StrictHandler(func(ctx context.Context, p promptGetParams) (any, error) {
			return handlePromptGet(ctx, promptSvc, p)
		}),
		"prompts/write":  promptWriteRPCHandler(promptSvc, emitPromptsChanged),
		"prompts/delete": promptDeleteRPCHandler(promptSvc, emitPromptsChanged),
		"prompt-sections/list": platformrpc.StrictHandler(func(ctx context.Context, p promptSectionListParams) (any, error) {
			return handlePromptSectionList(ctx, promptSvc, p)
		}),
		"prompt-sections/write":  promptSectionWriteRPCHandler(promptSvc, emitPromptsChanged),
		"prompt-sections/delete": promptSectionDeleteRPCHandler(promptSvc, emitPromptsChanged),
		"prompt-intents/draft":   promptIntentDraftRPCHandler(promptStore, dream, builtin, emitPromptsChanged),
		"prompt-intents/dry-run": platformrpc.StrictHandler(func(ctx context.Context, p promptintent.DryRunParams) (any, error) {
			return promptintent.HandleDryRun(ctx, promptStore, dream, builtin, p)
		}),
		"prompt-intents/commit":  promptIntentCommitRPCHandler(promptStore, sectionInvalidator, builtin, emitPromptsChanged),
		"prompt-intents/discard": promptIntentDiscardRPCHandler(promptStore, emitPromptsChanged),
	}
	if strings.TrimSpace(os.Getenv("PROMPT_INTENT_E2E_DREAM_FIXTURE")) != "" {
		handlers["prompt-intents/e2e-health"] = platformrpc.StrictHandler(func(ctx context.Context, p promptintent.E2EHealthParams) (any, error) {
			return promptintent.HandleE2EHealth(ctx, dream, p)
		})
	}
	return platformrpc.HandlerMapResult{Handlers: handlers}
}

// handlePromptList 返回当前 cwd 可见 prompts，并为列表页填充 section preview。
func handlePromptList(ctx context.Context, promptSvc PromptService, p promptListParams) (any, error) {
	templates, err := promptSvc.ListPrompts(ctx, p.Cwd, "")
	if err != nil {
		return nil, err
	}
	sectionsByTemplateID, err := promptSvc.ListPromptSectionsByTemplates(ctx, p.Cwd, templates)
	if err != nil {
		return nil, err
	}
	return map[string]any{"prompts": promptItemsFromTemplatesWithSections(templates, sectionsByTemplateID)}, nil
}

// handlePromptGet 返回单个 prompt 及完整可编辑 section 内容。
func handlePromptGet(ctx context.Context, promptSvc PromptService, p promptGetParams) (any, error) {
	template, err := promptSvc.GetPrompt(ctx, p.Cwd, p.ID)
	if err != nil {
		return nil, err
	}
	sectionsByTemplateID, err := promptSvc.ListPromptSectionsByTemplates(ctx, p.Cwd, []promptstore.PromptTemplate{*template})
	if err != nil {
		return nil, err
	}
	return map[string]any{"prompt": promptItemFromTemplateWithFullSections(*template, sectionsByTemplateID[template.ID])}, nil
}

// handlePromptWrite 把 RPC 参数转换为服务请求并返回保存后的 prompt。
func handlePromptWrite(ctx context.Context, promptSvc PromptService, p promptWriteParams) (any, error) {
	req := promptWriteRequestFromParams(p)
	template, err := promptSvc.WritePrompt(ctx, p.Cwd, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"prompt": promptItemFromTemplate(*template)}, nil
}

// promptWriteRequestFromParams 把 RPC 参数转换为服务层请求，并保留 content/match_when/scope 的显式设置状态。
func promptWriteRequestFromParams(p promptWriteParams) PromptWriteRequest {
	content := ""
	contentSet := false
	if p.Content != nil {
		content = *p.Content
		contentSet = true
	}
	whenToUse := ""
	if p.WhenToUse != nil {
		whenToUse = *p.WhenToUse
	}
	var matchWhen json.RawMessage
	if p.MatchWhen.Set {
		matchWhen = append(json.RawMessage(nil), p.MatchWhen.Raw...)
	}
	return PromptWriteRequest{
		ID:           p.ID,
		Name:         p.Name,
		Content:      content,
		ContentSet:   contentSet,
		Description:  p.Description,
		AgentType:    p.AgentType,
		WhenToUse:    whenToUse,
		WhenToUseSet: p.WhenToUse != nil,
		MatchWhen:    matchWhen,
		MatchWhenSet: p.MatchWhen.Set,
		Priority:     p.Priority,
		Enabled:      p.Enabled,
		Scope:        stringValue(p.Scope),
		ScopeSet:     p.Scope != nil,
		Tags:         p.Tags,
	}
}

// stringValue 解引用可选字符串，nil 表示未显式传入。
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// validatePromptDiscoverability 要求新 prompt 必须有 when_to_use，更新且未显式修改时沿用旧值。
func validatePromptDiscoverability(template promptstore.PromptTemplate, current *promptstore.PromptTemplate, explicit bool) error {
	if strings.TrimSpace(template.WhenToUse) != "" || current != nil && !explicit {
		return nil
	}
	return errors.New("dashboard: prompt when_to_use is required")
}

// handlePromptDelete 删除 prompt 并返回统一 ok 响应。
func handlePromptDelete(ctx context.Context, promptSvc PromptService, p promptDeleteParams) (any, error) {
	if err := deletePromptWithOptionalScope(ctx, promptSvc, p); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// deletePromptWithOptionalScope 只在客户端显式传 scope 时启用额外 scope 校验。
func deletePromptWithOptionalScope(ctx context.Context, promptSvc PromptService, p promptDeleteParams) error {
	if p.Scope == nil {
		return promptSvc.DeletePrompt(ctx, p.Cwd, p.ID)
	}
	return promptSvc.DeletePrompt(ctx, p.Cwd, p.ID, stringValue(p.Scope))
}

// handlePromptSectionList 返回指定 prompt 的可编辑 sections。
func handlePromptSectionList(ctx context.Context, promptSvc PromptService, p promptSectionListParams) (any, error) {
	sections, err := promptSvc.ListSections(ctx, p.Cwd, p.PromptID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sections": promptSectionItemsFromStore(sections, p.PromptID)}, nil
}

// handlePromptSectionWrite 写入单个 section 并返回 store 规范化后的结果。
func handlePromptSectionWrite(ctx context.Context, promptSvc PromptService, p promptSectionWriteParams) (any, error) {
	section, err := promptSvc.WriteSection(ctx, p.Cwd, promptSectionWriteRequestFromParams(p))
	if err != nil {
		return nil, err
	}
	return map[string]any{"section": promptSectionItemFromStore(*section, p.PromptID)}, nil
}

// promptSectionWriteRequestFromParams 把 RPC section 写入参数转换为服务层请求，enabled 缺省为 true。
func promptSectionWriteRequestFromParams(p promptSectionWriteParams) PromptSectionWriteRequest {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	return PromptSectionWriteRequest{
		PromptKey:   p.PromptID,
		SectionKey:  p.SectionKey,
		Region:      p.Region,
		Ordinal:     p.Ordinal,
		Body:        p.Body,
		EnableWhen:  []byte(p.EnableWhen),
		Enabled:     enabled,
		TriggerType: p.TriggerType,
		RecallTopic: p.RecallTopic,
		Scope:       stringValue(p.Scope),
		ScopeSet:    p.Scope != nil,
	}
}

// handlePromptSectionDelete 删除单个 section 并返回统一 ok 响应。
func handlePromptSectionDelete(ctx context.Context, promptSvc PromptService, p promptSectionDeleteParams) (any, error) {
	if err := deletePromptSectionWithOptionalScope(ctx, promptSvc, p); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// deletePromptSectionWithOptionalScope 只在客户端显式传 scope 时启用额外 scope 校验。
func deletePromptSectionWithOptionalScope(ctx context.Context, promptSvc PromptService, p promptSectionDeleteParams) error {
	if p.Scope == nil {
		return promptSvc.DeleteSection(ctx, p.Cwd, p.PromptID, p.SectionKey)
	}
	return promptSvc.DeleteSection(ctx, p.Cwd, p.PromptID, p.SectionKey, stringValue(p.Scope))
}

// promptSectionItemsFromStore 将 store sections 批量转换为 RPC 响应。
func promptSectionItemsFromStore(sections []promptstore.PromptTemplateSection, promptKey string) []promptSectionRPCItem {
	out := make([]promptSectionRPCItem, 0, len(sections))
	for _, sec := range sections {
		out = append(out, promptSectionItemFromStore(sec, promptKey))
	}
	return out
}

// promptSectionItemFromStore 将单个 store section 转换为 RPC 响应。
func promptSectionItemFromStore(section promptstore.PromptTemplateSection, promptKey string) promptSectionRPCItem {
	return promptSectionRPCItem{
		ID:          section.ID,
		PromptID:    promptKey,
		SectionKey:  section.SectionKey,
		Region:      section.Region,
		Ordinal:     section.Ordinal,
		Body:        section.Body,
		EnableWhen:  json.RawMessage(section.EnableWhen),
		Enabled:     section.Enabled,
		TriggerType: section.TriggerType,
		RecallTopic: section.RecallTopic,
		CreatedAt:   section.CreatedAt,
		UpdatedAt:   section.UpdatedAt,
	}
}

// promptItemsFromTemplates 将模板列表转换为 RPC items，不附加 section preview。
func promptItemsFromTemplates(templates []promptstore.PromptTemplate) []promptRPCItem {
	return promptItemsFromTemplatesWithSections(templates, nil)
}

// promptItemsFromTemplatesWithSections 将模板转换为 RPC items，并优先用 section preview 展示内容。
func promptItemsFromTemplatesWithSections(
	templates []promptstore.PromptTemplate,
	sectionsByTemplateID map[int64][]promptstore.PromptTemplateSection,
) []promptRPCItem {
	items := make([]promptRPCItem, 0, len(templates))
	for _, template := range templates {
		sections := sectionsByTemplateID[template.ID]
		item := promptItemFromTemplate(promptTemplateWithInferredSectionIntent(template, sections))
		if preview := promptSectionsContentPreview(sections); preview != "" {
			item.Content = preview
		}
		items = append(items, item)
	}
	return items
}

// promptItemFromTemplate 将 store 模板转换为前端可见字段，并过滤内部 scope tags。
func promptItemFromTemplate(template promptstore.PromptTemplate) promptRPCItem {
	return promptRPCItem{
		ID:          template.PromptKey,
		Name:        template.Title,
		Content:     template.PromptText,
		Description: template.Description,
		AgentType:   template.AgentKey,
		WhenToUse:   template.WhenToUse,
		CreatedAt:   template.CreatedAt,
		UpdatedAt:   template.UpdatedAt,
		MatchWhen:   append(json.RawMessage(nil), template.MatchWhen...),
		Priority:    template.Priority,
		Enabled:     template.Enabled,
		Scope:       promptScopeForTemplate(template),
		Tags:        filterVisibleTags(template.Tags),
	}
}

// promptItemFromTemplateWithFullSections 返回编辑页需要的完整 section 内容。
func promptItemFromTemplateWithFullSections(template promptstore.PromptTemplate, sections []promptstore.PromptTemplateSection) promptRPCItem {
	template = promptTemplateWithInferredSectionIntent(template, sections)
	item := promptItemFromTemplate(template)
	if content := promptEditableSectionsContent(template, sections); content != "" {
		item.Content = content
	}
	return item
}

// promptTemplateIDs 提取去重后的非零 template IDs，用于批量查询 sections。
func promptTemplateIDs(templates []promptstore.PromptTemplate) []int64 {
	ids := make([]int64, 0, len(templates))
	seen := map[int64]struct{}{}
	for _, template := range templates {
		if template.ID <= 0 {
			continue
		}
		if _, ok := seen[template.ID]; ok {
			continue
		}
		seen[template.ID] = struct{}{}
		ids = append(ids, template.ID)
	}
	return ids
}

// promptSectionsContentPreview 生成列表页使用的短 section 内容预览。
func promptSectionsContentPreview(sections []promptstore.PromptTemplateSection) string {
	return promptSectionsContent(sections, promptListContentPreviewMaxRunes)
}

// promptSectionsContent 按 region/ordinal/id 排序拼接非 recall section，可按 maxRunes 截断。
func promptSectionsContent(sections []promptstore.PromptTemplateSection, maxRunes int) string {
	if len(sections) == 0 {
		return ""
	}
	sorted := make([]promptstore.PromptTemplateSection, len(sections))
	copy(sorted, sections)
	sort.SliceStable(sorted, func(i, j int) bool { return promptPreviewSectionLess(sorted[i], sorted[j]) })
	blocks := make([]string, 0, len(sorted))
	for _, section := range sorted {
		if body := promptPreviewSectionBody(section); body != "" {
			blocks = append(blocks, body)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	text := strings.Join(blocks, "\n\n")
	if maxRunes <= 0 {
		return text
	}
	return truncatePromptListContentPreview(text)
}

// promptEditableSectionsContent 返回编辑页内容；recall 模板只展示 recall section 正文。
func promptEditableSectionsContent(template promptstore.PromptTemplate, sections []promptstore.PromptTemplateSection) string {
	if promptTemplateIntentKind(template) == "recall" {
		return promptRecallSectionsContent(sections)
	}
	return promptSectionsContent(sections, 0)
}

// promptRecallSectionsContent 只拼接启用的 recall section，避免普通动态 section 混入知识正文。
func promptRecallSectionsContent(sections []promptstore.PromptTemplateSection) string {
	if len(sections) == 0 {
		return ""
	}
	sorted := make([]promptstore.PromptTemplateSection, len(sections))
	copy(sorted, sections)
	sort.SliceStable(sorted, func(i, j int) bool { return promptPreviewSectionLess(sorted[i], sorted[j]) })
	blocks := make([]string, 0, len(sorted))
	for _, section := range sorted {
		if !section.Enabled || !strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall") {
			continue
		}
		if body := strings.TrimSpace(section.Body); body != "" {
			blocks = append(blocks, body)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// promptPreviewSectionLess 定义 section preview 的稳定排序规则。
func promptPreviewSectionLess(left, right promptstore.PromptTemplateSection) bool {
	if left.TemplateID != right.TemplateID {
		return left.TemplateID < right.TemplateID
	}
	if regionPriority(left.Region) != regionPriority(right.Region) {
		return regionPriority(left.Region) < regionPriority(right.Region)
	}
	if left.Ordinal != right.Ordinal {
		return left.Ordinal < right.Ordinal
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.SectionKey < right.SectionKey
}

// promptPreviewSectionBody 返回列表预览可展示的 section 正文，跳过 recall 和禁用项。
func promptPreviewSectionBody(section promptstore.PromptTemplateSection) string {
	if !section.Enabled || strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall") {
		return ""
	}
	return strings.TrimSpace(section.Body)
}

// validateRecallTopicForWrite 校验 recall section 的 topic 命名，非 recall section 不受限制。
func validateRecallTopicForWrite(triggerType, topic string) error {
	if strings.TrimSpace(strings.ToLower(triggerType)) != "recall" {
		return nil
	}
	if !validPromptRecallTopicName(strings.TrimSpace(topic)) {
		return errors.New("dashboard: recall_topic must be lowercase dash-separated and shorter than 64 characters")
	}
	return nil
}

// validPromptRecallTopicName 要求 topic 为短小的 lowercase dash-separated slug。
func validPromptRecallTopicName(topic string) bool {
	return len(topic) < 64 && promptRecallTopicPattern.MatchString(topic)
}

// regionPriority 把 static 排在 dynamic 前面，未知 region 最后展示。
func regionPriority(region string) int {
	switch strings.TrimSpace(strings.ToLower(region)) {
	case "static":
		return 0
	case "dynamic":
		return 1
	default:
		return 2
	}
}

// truncatePromptListContentPreview 按 rune 截断列表预览，避免切断多字节字符。
func truncatePromptListContentPreview(text string) string {
	runes := []rune(text)
	if len(runes) <= promptListContentPreviewMaxRunes {
		return text
	}
	return string(runes[:promptListContentPreviewMaxRunes])
}

// filterVisibleTags 移除内部 scope tags，只返回用户可见标签。
func filterVisibleTags(raw json.RawMessage) json.RawMessage {
	tags := promptTags(raw)
	visible := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && t != "scope.global" && !strings.HasPrefix(t, promptScopeTagPrefix) {
			visible = append(visible, t)
		}
	}
	if len(visible) == 0 {
		return nil
	}
	encoded, err := json.Marshal(visible)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

// AsPromptRegistry 将聚合 Service 暴露为 PromptRegistry，供 fx 按窄接口注入。
func AsPromptRegistry(svc Service) PromptRegistry {
	return svc
}

// AsPromptAssemblyService 将聚合 Service 暴露为 prompt 组装契约。
func AsPromptAssemblyService(svc Service) contract.PromptAssemblyService {
	return svc
}

// AsDynamicSectionRegistrar 将聚合 Service 暴露为动态 section 注册契约。
func AsDynamicSectionRegistrar(svc Service) contract.DynamicSectionRegistrar {
	return svc
}
