package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// PromptRegistry 管理内置和动态 prompt section 的注册与枚举。
type PromptRegistry interface {
	RegisterSection(section PromptSection) error
	RegisterDynamicProvider(provider DynamicSectionProvider) error
	UnregisterDynamicProvider(name string) bool
	Sections() []PromptSection
}

// PromptAssemblyService 暴露 prompt 组装能力的跨模块契约别名。
type PromptAssemblyService = contract.PromptAssemblyService

// Service 聚合 prompt section 注册、组装、失效和 Claude.md 来源注入能力。
type Service interface {
	PromptRegistry
	contract.PromptAssemblyService
	SectionInvalidator
	RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error
	Config() Config
}

// service 接口断言确保 prompt 运行时满足跨模块 section 失效契约。
// 该契约要求并发安全；实现侧通过 cache mutex 和 dynamicMu 保护共享状态。
var _ contract.SectionInvalidator = (*service)(nil)

// DisabledBuiltinToolsFn 返回用户在 UI 偏好中手动禁用的 builtin tool IDs。
// 该函数由上层注入，避免 prompt 包直接依赖 uistate 实现。
type DisabledBuiltinToolsFn func(ctx context.Context, cwd, provider string) []string

// service 是 prompt 模块的运行时实现，持有 section 注册表、动态 provider 和缓存。
type service struct {
	cfg              *Config
	logger           *slog.Logger
	registry         *SectionRegistry
	cache            *sectionCache
	userContextCache *userContextCache
	claudeMdProvider contract.ClaudeMdSourceProvider
	flight           singleflight.Group

	prefs           promptPreferenceReader
	sharedFiles     promptSharedFileReader
	disabledToolsFn DisabledBuiltinToolsFn

	dynamicMu sync.RWMutex
	dynamic   map[string]DynamicSectionProvider
}

// ServiceOption 注入 prompt Service 的可选依赖，避免构造函数参数继续膨胀。
type ServiceOption func(*service)

// WithPromptHintSources 注入用于解析 LSP prompt hint 的偏好存储和共享文件读取器。
func WithPromptHintSources(prefs promptPreferenceReader, sharedFiles promptSharedFileReader) ServiceOption {
	return func(s *service) {
		s.prefs = prefs
		s.sharedFiles = sharedFiles
	}
}

// WithDisabledBuiltinToolsFn 注入 builtin tools 软过滤查询函数，避免 prompt 包直接依赖 uistate。
func WithDisabledBuiltinToolsFn(fn DisabledBuiltinToolsFn) ServiceOption {
	return func(s *service) {
		s.disabledToolsFn = fn
	}
}

const (
	promptRPCLimit            = 1000
	promptUpdatedBy           = "rpc.prompts"
	promptDefaultAgent        = "main"
	promptScopeTagPrefix      = "scope.cwd:"
	promptMaxContentBytes     = 1 << 20
	promptMaxDescriptionBytes = 10 << 10
)

var (
	errPromptStoreRequired = errors.New("dashboard: prompt store is not configured")
	promptSlugPattern      = regexp.MustCompile(`[^a-z0-9]+`)
)

// Config 返回当前配置快照；nil 配置时返回零值，保持调用方读路径无 panic。
func (s *service) Config() Config {
	if s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

// RegisterSection 注册单个 prompt section，重复名称由 registry 拒绝。
func (s *service) RegisterSection(section PromptSection) error {
	return s.registry.Register(section)
}

// RegisterClaudeMdSourceProvider 设置 Claude.md 来源 provider，并清空依赖该来源的 user context 缓存。
func (s *service) RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error {
	s.claudeMdProvider = provider
	if s.userContextCache != nil {
		s.userContextCache.InvalidateAll()
	}
	return nil
}

// Sections 返回注册表中的 section 快照。
func (s *service) Sections() []PromptSection {
	return s.registry.Sections()
}

// ListPrompts 按 cwd scope 查询用户可见 prompt 模板，并过滤越界模板。
func (s *promptService) ListPrompts(
	ctx context.Context,
	cwd, keyword string,
) ([]promptTemplate, error) {
	if s.store == nil {
		return []promptTemplate{}, nil
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return nil, err
	}
	templates, err := s.store.List(ctx, promptListFilter{
		Keyword: strings.TrimSpace(keyword),
		CWD:     requestScope,
		Limit:   promptRPCLimit,
	})
	if err != nil {
		return nil, err
	}
	return filterVisiblePrompts(templates, requestScope), nil
}

// ListPromptSectionsByTemplates 批量加载模板 sections，并再次校验每个模板属于请求 cwd。
func (s *promptService) ListPromptSectionsByTemplates(
	ctx context.Context,
	cwd string,
	templates []promptTemplate,
) (map[int64][]promptTemplateSection, error) {
	sectionsByTemplateID := map[int64][]promptTemplateSection{}
	if s.store == nil || len(templates) == 0 {
		return sectionsByTemplateID, nil
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return nil, err
	}
	templateIDs := make([]int64, 0, len(templates))
	for _, template := range templates {
		if !promptVisibleForRead(template, requestScope) {
			return nil, fmt.Errorf("dashboard: prompt %q is outside cwd scope", template.PromptKey)
		}
		if template.ID != 0 {
			templateIDs = append(templateIDs, template.ID)
		}
	}
	if len(templateIDs) == 0 {
		return sectionsByTemplateID, nil
	}
	sections, err := s.store.ListSectionsByTemplateIDs(ctx, templateIDs)
	if err != nil {
		return nil, err
	}
	for _, section := range sections {
		sectionsByTemplateID[section.TemplateID] = append(sectionsByTemplateID[section.TemplateID], section)
	}
	return sectionsByTemplateID, nil
}

// GetPrompt 读取单个 prompt 模板；越过 cwd scope 时按 not found 处理。
func (s *promptService) GetPrompt(
	ctx context.Context,
	cwd, key string,
) (*promptTemplate, error) {
	if s.store == nil {
		return nil, errPromptStoreRequired
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return nil, err
	}
	promptKey := strings.TrimSpace(key)
	if promptKey == "" {
		return nil, errors.New("dashboard: prompt id is required")
	}
	template, err := s.store.Get(ctx, promptKey)
	if err != nil {
		return nil, err
	}
	if !promptVisibleForRead(*template, requestScope) {
		return nil, contract.ErrNotFound
	}
	return template, nil
}

// WritePrompt 在事务中创建或更新用户 prompt，并在成功后清空模板 catalog 缓存。
func (s *promptService) WritePrompt(
	ctx context.Context,
	cwd string,
	prompt PromptWriteRequest,
) (*promptTemplate, error) {
	if s.store == nil {
		return nil, errPromptStoreRequired
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return nil, err
	}
	if err := rejectBuiltinPromptMutation(s.builtin, prompt.ID); err != nil {
		return nil, err
	}
	var template *promptTemplate
	err = s.store.WithTx(ctx, func(txStore promptStore) error {
		next, err := upsertPrompt(ctx, txStore, s.builtin, requestScope, prompt)
		if err != nil {
			return err
		}
		template = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidatePromptTemplateCatalogs()
	return template, nil
}

// ListSections 先校验父 prompt 可见性，再返回其可编辑 section 列表。
func (s *promptService) ListSections(ctx context.Context, cwd, promptKey string) ([]promptTemplateSection, error) {
	template, err := s.GetPrompt(ctx, cwd, promptKey)
	if err != nil {
		return nil, err
	}
	return s.store.ListSectionsByTemplateID(ctx, template.ID)
}

// WriteSection 更新用户 prompt 的单个 section，并在成功后清空 section asset catalog。
func (s *promptService) WriteSection(ctx context.Context, cwd string, req PromptSectionWriteRequest) (*promptTemplateSection, error) {
	if s.store == nil {
		return nil, errPromptStoreRequired
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return nil, err
	}
	promptKey := strings.TrimSpace(req.PromptKey)
	if promptKey == "" {
		return nil, errors.New("dashboard: prompt id is required")
	}
	if err := rejectBuiltinPromptMutation(s.builtin, promptKey); err != nil {
		return nil, err
	}
	if err := validateRecallTopicForWrite(req.TriggerType, req.RecallTopic); err != nil {
		return nil, err
	}
	saved, err := writePromptSectionInTx(ctx, s.store, requestScope, promptKey, req)
	if err != nil {
		return nil, err
	}
	s.invalidateSectionAssetCatalogs()
	return saved, nil
}

// DeleteSection 在事务中校验 prompt scope 后删除 section，并刷新 section asset catalog。
func (s *promptService) DeleteSection(ctx context.Context, cwd, promptKey, sectionKey string, scope ...string) error {
	if s.store == nil {
		return errPromptStoreRequired
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return err
	}
	pKey := strings.TrimSpace(promptKey)
	sKey := strings.TrimSpace(sectionKey)
	if pKey == "" || sKey == "" {
		return errors.New("dashboard: prompt id and section key are required")
	}
	if err := rejectBuiltinPromptMutation(s.builtin, pKey); err != nil {
		return err
	}
	err = s.store.WithTx(ctx, func(txStore promptStore) error {
		template, gerr := txStore.Get(ctx, pKey)
		if gerr != nil {
			return gerr
		}
		if err := validatePromptMutationScope(template, requestScope, optionalScopeValue(scope), len(scope) > 0); err != nil {
			return err
		}
		return txStore.DeleteSection(ctx, template.ID, sKey)
	})
	if err != nil {
		return err
	}
	s.invalidateSectionAssetCatalogs()
	return nil
}

// DeletePrompt 先归档当前模板版本再删除 prompt，成功后刷新模板 catalog。
func (s *promptService) DeletePrompt(ctx context.Context, cwd, key string, scope ...string) error {
	if s.store == nil {
		return errPromptStoreRequired
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return err
	}
	promptKey := strings.TrimSpace(key)
	if promptKey == "" {
		return errors.New("dashboard: prompt id is required")
	}
	if err := rejectBuiltinPromptMutation(s.builtin, promptKey); err != nil {
		return err
	}
	err = s.store.WithTx(ctx, func(txStore promptStore) error {
		current, err := txStore.Get(ctx, promptKey)
		if err != nil {
			return err
		}
		if err := validatePromptMutationScope(current, requestScope, optionalScopeValue(scope), len(scope) > 0); err != nil {
			return err
		}
		if err := archivePrompt(ctx, txStore, *current); err != nil {
			return err
		}
		return txStore.Delete(ctx, promptKey)
	})
	if err != nil {
		return err
	}
	s.invalidatePromptTemplateCatalogs()
	return nil
}

// optionalScopeValue 返回可选 scope 的第一个值，缺省时保持空串表示未显式指定。
func optionalScopeValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// registerBuiltInSections 注册内置静态 section 和动态 slot section，启动期失败由 must* helper 直接暴露。
func (s *service) registerBuiltInSections() {
	if !parseBoolEnv(envDisableBuiltinStaticSections, false) {
		for _, section := range StaticSections() {
			mustRegisterSection(s.registry, section)
		}
	}
	for _, section := range s.dynamicSlotSections() {
		mustRegisterSection(s.registry, section)
	}
}

// mustRegisterSection 注册内置 section；启动不变量失败时 panic，避免系统带缺失 section 继续运行。
func mustRegisterSection(registry *SectionRegistry, section PromptSection) {
	if err := registry.Register(section); err != nil {
		// archguard:ignore panic_count -- builtin prompt section registration is a startup invariant.
		panic(err)
	}
}

// mustRegisterDynamicProvider 注册内置动态 provider；失败说明启动配置冲突，必须立即暴露。
func mustRegisterDynamicProvider(svc *service, provider DynamicSectionProvider) {
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		// archguard:ignore panic_count -- builtin dynamic provider registration is a startup invariant.
		panic(err)
	}
}

// filterVisiblePrompts 过滤掉当前 cwd 不可见的模板，保护项目级 prompt 不跨目录泄露。
func filterVisiblePrompts(
	templates []promptTemplate,
	cwd string,
) []promptTemplate {
	items := make([]promptTemplate, 0, len(templates))
	for _, template := range templates {
		if promptVisibleForRead(template, cwd) {
			items = append(items, template)
		}
	}
	return items
}

// promptVisibleForRead 判断模板是否可被当前 cwd 读取。
func promptVisibleForRead(template promptTemplate, cwd string) bool {
	return promptVisibleForCWD(template, cwd)
}

// upsertPrompt 执行 prompt upsert 的事务内流程：校验、归档旧版本、生成 key 并写入内容 section。
func upsertPrompt(
	ctx context.Context,
	store promptStore,
	builtin contract.BuiltinPromptRegistry,
	cwd string,
	p PromptWriteRequest,
) (*promptTemplate, error) {
	if err := validatePromptWrite(p); err != nil {
		return nil, err
	}
	current, err := lookupPromptForMutation(ctx, store, p.ID)
	if err != nil {
		return nil, err
	}
	if err := validatePromptWriteScope(current, cwd, p.Scope, p.ScopeSet); err != nil {
		return nil, err
	}
	contentSection, err := promptContentSectionTargetForWrite(ctx, store, current, p)
	if err != nil {
		return nil, err
	}
	if current != nil {
		if err := archivePrompt(ctx, store, *current); err != nil {
			return nil, err
		}
	}
	key, err := resolvePromptKey(ctx, store, builtin, p, current)
	if err != nil {
		return nil, err
	}
	template, err := buildPromptTemplate(p, cwd, key, current)
	if err != nil {
		return nil, err
	}
	if err := validatePromptDiscoverability(template, current, p.WhenToUseSet); err != nil {
		return nil, err
	}
	return storePromptTemplateAndContent(ctx, store, template, current, contentSection, p.Content)
}

// lookupPromptForMutation 读取待修改模板；空 id 表示创建新模板。
func lookupPromptForMutation(
	ctx context.Context,
	store promptStore,
	id string,
) (*promptTemplate, error) {
	key := strings.TrimSpace(id)
	if key == "" {
		return nil, nil
	}
	return store.Get(ctx, key)
}

// resolvePromptKey 为新模板生成稳定 key，遇到 builtin 或已存在 key 时追加纳秒后缀避让。
func resolvePromptKey(
	ctx context.Context,
	store promptStore,
	builtin contract.BuiltinPromptRegistry,
	p PromptWriteRequest,
	current *promptTemplate,
) (string, error) {
	if current != nil {
		return current.PromptKey, nil
	}
	base := promptKeyBase(p.AgentType, p.Name)
	if builtinPromptExists(builtin, base) {
		return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()), nil
	}
	_, err := store.Get(ctx, base)
	switch {
	case err == nil:
		return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()), nil
	case contract.IsNotFound(err):
		return base, nil
	default:
		return "", err
	}
}

// rejectBuiltinPromptMutation 阻止 RPC 修改内置 prompt，用户 prompt 只能通过持久化 store 写入。
func rejectBuiltinPromptMutation(builtin contract.BuiltinPromptRegistry, promptKey string) error {
	key := strings.TrimSpace(promptKey)
	if key == "" {
		return nil
	}
	if builtinPromptExists(builtin, key) {
		return fmt.Errorf("dashboard: builtin prompt %q is read-only", key)
	}
	return nil
}

// builtinPromptExists 查询内置 prompt registry，nil registry 视为不存在。
func builtinPromptExists(builtin contract.BuiltinPromptRegistry, promptKey string) bool {
	if builtin == nil {
		return false
	}
	_, ok := builtin.GetTemplate(strings.TrimSpace(promptKey))
	return ok
}

// buildPromptTemplate 根据写请求构造 store 模板，并保留更新场景下不可由客户端覆盖的字段。
func buildPromptTemplate(
	p PromptWriteRequest,
	cwd, key string,
	current *promptTemplate,
) (promptTemplate, error) {
	baseTags := clientTagsOrDefault(p.Tags, nil)
	scope := promptScopeForWrite(current, cwd, p.Scope, p.ScopeSet)
	whenToUse := ""
	if p.WhenToUseSet {
		whenToUse = strings.TrimSpace(p.WhenToUse)
	}
	template := promptTemplate{
		PromptKey:      key,
		Title:          strings.TrimSpace(p.Name),
		AgentKey:       promptAgentType(p.AgentType),
		PromptText:     promptTextForWrite(p, current),
		WhenToUse:      whenToUse,
		Variables:      json.RawMessage("{}"),
		Tags:           withPromptScopeKindTag(baseTags, cwd, scope),
		Enabled:        promptEnabledForWrite(p, current),
		ManuallyEdited: current != nil,
		CreatedBy:      promptUpdatedBy,
		UpdatedBy:      promptUpdatedBy,
		Description:    strings.TrimSpace(p.Description),
		Priority:       p.Priority,
	}
	if p.MatchWhenSet {
		matchWhen, err := sanitizeTemplateMatchWhen(p.MatchWhen)
		if err != nil {
			return promptTemplate{}, err
		}
		template.MatchWhen = matchWhen
	}
	if current == nil {
		return template, nil
	}
	template.CreatedBy = current.CreatedBy
	template.ToolName = current.ToolName
	template.Variables = append(json.RawMessage(nil), current.Variables...)
	template.Tags = withPromptScopeKindTag(clientTagsOrDefault(p.Tags, current.Tags), cwd, scope)
	if !p.WhenToUseSet {
		template.WhenToUse = current.WhenToUse
	}
	if !p.MatchWhenSet {
		template.MatchWhen = append(json.RawMessage(nil), current.MatchWhen...)
	}
	if strings.TrimSpace(p.AgentType) == "" {
		template.AgentKey = current.AgentKey
	}
	return template, nil
}

// promptEnabledForWrite 解析 enabled 字段，缺省更新时继承旧值、创建时默认启用。
func promptEnabledForWrite(p PromptWriteRequest, current *promptTemplate) bool {
	if p.Enabled != nil {
		return *p.Enabled
	}
	if current != nil {
		return current.Enabled
	}
	return true
}

// promptTextForWrite 区分省略 content 和显式清空 content，避免 PATCH 式更新误删正文。
func promptTextForWrite(p PromptWriteRequest, current *promptTemplate) string {
	if current != nil && !p.ContentSet && p.Content == "" {
		return current.PromptText
	}
	return p.Content
}

// sanitizeTemplateMatchWhen 移除已废弃的 tags_has 条件，并拒绝非对象或损坏 JSON。
// match_when 的 nil 表示“不参与自动路由”；显式 {} 才表示匹配全部。
func sanitizeTemplateMatchWhen(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var expr map[string]any
	if err := json.Unmarshal([]byte(trimmed), &expr); err != nil {
		return nil, fmt.Errorf("dashboard: prompt match_when must be a valid JSON object: %w", err)
	}
	if _, ok := expr["tags_has"]; !ok {
		return json.RawMessage(trimmed), nil
	}
	delete(expr, "tags_has")
	if len(expr) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(expr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: prompt match_when must be a valid JSON object: %w", err)
	}
	return json.RawMessage(encoded), nil
}

// archivePrompt 写入 prompt 当前版本快照，供更新和删除前保留审计历史。
func archivePrompt(ctx context.Context, store promptStore, current promptTemplate) error {
	_, err := store.InsertVersion(ctx, promptTemplateVersion{
		PromptKey:       current.PromptKey,
		Title:           current.Title,
		AgentKey:        current.AgentKey,
		ToolName:        current.ToolName,
		PromptText:      current.PromptText,
		Variables:       append(json.RawMessage(nil), current.Variables...),
		Tags:            append(json.RawMessage(nil), current.Tags...),
		Description:     current.Description,
		Enabled:         current.Enabled,
		CreatedBy:       current.CreatedBy,
		UpdatedBy:       current.UpdatedBy,
		SourceUpdatedAt: &current.UpdatedAt,
	})
	return err
}

// promptKeyBase 根据 agent type 和标题生成用户 prompt 的基础 key。
func promptKeyBase(agentType, name string) string {
	slug := promptSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "prompt"
	}
	return promptAgentType(agentType) + "/" + slug
}

// promptAgentType 规范化 agent type，兼容 main/root 和 sub/worker/child 别名。
func promptAgentType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", promptDefaultAgent, "root":
		return promptDefaultAgent
	case "sub", "worker", "child":
		return "sub"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// validatePromptWrite 校验用户 prompt 写入请求的必填字段、scope 和大小限制。
func validatePromptWrite(p PromptWriteRequest) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return errors.New("dashboard: prompt name is required")
	case p.ScopeSet && normalizePromptScope(p.Scope) == "":
		return errors.New("dashboard: prompt scope must be project or global")
	case len(p.Content) > promptMaxContentBytes:
		return fmt.Errorf("dashboard: prompt content exceeds %d bytes", promptMaxContentBytes)
	case len(p.Description) > promptMaxDescriptionBytes:
		return fmt.Errorf("dashboard: prompt description exceeds %d bytes", promptMaxDescriptionBytes)
	default:
		return nil
	}
}
