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

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"golang.org/x/sync/singleflight"
)

type PromptRegistry interface {
	RegisterSection(section PromptSection) error
	RegisterDynamicProvider(provider DynamicSectionProvider) error
	UnregisterDynamicProvider(name string) bool
	Sections() []PromptSection
}

type Registry = PromptRegistry

type PromptAssemblyService = contract.PromptAssemblyService

type AssemblyService = PromptAssemblyService

type Service interface {
	PromptRegistry
	contract.PromptAssemblyService
	SectionInvalidator
	RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error
	Config() Config
}

type service struct {
	cfg              *Config
	logger           *slog.Logger
	registry         *SectionRegistry
	cache            *sectionCache
	userContextCache *userContextCache
	claudeMdProvider contract.ClaudeMdSourceProvider
	flight           singleflight.Group

	dynamicMu sync.RWMutex
	dynamic   map[string]DynamicSectionProvider
}

var _ contract.PromptAssemblyService = (*service)(nil)

func NewService(cfg *Config, logger *slog.Logger) Service {
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
	mustRegisterDynamicProvider(svc, AntModelOverrideStubProvider{})
	return svc
}

func AsPromptRegistry(svc Service) PromptRegistry {
	return svc
}

func AsPromptAssemblyService(svc Service) contract.PromptAssemblyService {
	return svc
}

func AsDynamicSectionRegistrar(svc Service) contract.DynamicSectionRegistrar {
	return svc
}

func (s *service) Config() Config {
	if s == nil || s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

func (s *service) RegisterSection(section PromptSection) error {
	return s.registry.Register(section)
}

func (s *service) RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error {
	s.claudeMdProvider = provider
	if s.userContextCache != nil {
		s.userContextCache.InvalidateAll()
	}
	return nil
}

func (s *service) Sections() []PromptSection {
	return s.registry.Sections()
}

func (s *service) registerBuiltInSections() {
	for _, section := range StaticSections() {
		mustRegisterSection(s.registry, section)
	}
	for _, section := range s.dynamicSlotSections() {
		mustRegisterSection(s.registry, section)
	}
}

func mustRegisterSection(registry *SectionRegistry, section PromptSection) {
	if err := registry.Register(section); err != nil {
		panic(err)
	}
}

func mustRegisterDynamicProvider(svc *service, provider DynamicSectionProvider) {
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		panic(err)
	}
}

// ============================================================================
// p20.1 — prompts/list|write|delete 宿主 RPC 恢复（merge-in-place）
// ============================================================================
//
// 2026-03-25 commit c50ef009 把 dashboard/prompt_rpc.go 一起删掉，导致 `prompts/list
// |write|delete` 宿主 RPC 404。p20.1 方案 B：不新建 `rpc.go`，把 handler / scope /
// archive 逻辑直接收回 prompt owner 的 service.go 尾部合并。
//
// 关键不变量：
//   - 前端 SystemPromptPage.js 吃的返回 shape 是 {id, name, content, description,
//     agentType, createdAt, updatedAt}；不能退回 MCP 的 prompt_key/title/prompt_text。
//   - scope.cwd:<cwd> tag 语义继承 c50ef009^ 的 prompt_service.go —— 跨项目 write/
//     delete 必须被拒绝，避免项目间 prompt 互踩。
//   - Upsert 前旧版本要归档到 prompt_template_versions，不允许直接覆盖丢历史。

const (
	promptRPCLimit            = 1000
	promptUpdatedBy           = "rpc.prompts"
	promptDefaultAgent        = "main"
	promptScopeTagPrefix      = "scope.cwd:"
	promptMaxContentBytes     = 1 << 20
	promptMaxDescriptionBytes = 10 << 10
)

var (
	errPromptStoreRequired = errors.New("prompt: prompt store is not configured")
	promptSlugPattern      = regexp.MustCompile(`[^a-z0-9]+`)
)

// PromptWriteRequest 是 prompts/write RPC 的域对象（从 wire params 投影）。
type PromptWriteRequest struct {
	ID, Name, Content, Description, AgentType string
}

type promptListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type promptWriteParams struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Content     string `json:"content,omitempty"`
	Description string `json:"description,omitempty"`
	AgentType   string `json:"agentType,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

type promptDeleteParams struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd,omitempty"`
}

type promptRPCItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Content     string    `json:"content"`
	Description string    `json:"description"`
	AgentType   string    `json:"agentType"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NewPromptHandlers p20.1：prompts/list|write|delete handler 集合工厂。
func NewPromptHandlers(store promptstore.Store) rpc.HandlerMapResult {
	svc := newPromptRPCService(store)
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"prompts/list": rpc.StrictHandler(func(ctx context.Context, p promptListParams) (any, error) {
			templates, err := svc.ListPrompts(ctx, p.Cwd, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompts": promptItemsFromTemplates(templates)}, nil
		}),
		"prompts/write": rpc.StrictHandler(func(ctx context.Context, p promptWriteParams) (any, error) {
			template, err := svc.WritePrompt(ctx, p.Cwd, PromptWriteRequest{
				ID:          p.ID,
				Name:        p.Name,
				Content:     p.Content,
				Description: p.Description,
				AgentType:   p.AgentType,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompt": promptItemFromTemplate(*template)}, nil
		}),
		"prompts/delete": rpc.StrictHandler(func(ctx context.Context, p promptDeleteParams) (any, error) {
			if err := svc.DeletePrompt(ctx, p.Cwd, p.ID); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
	}}
}

type promptRPCService struct {
	store promptstore.Store
}

func newPromptRPCService(store promptstore.Store) *promptRPCService {
	return &promptRPCService{store: store}
}

func (s *promptRPCService) ListPrompts(ctx context.Context, cwd, keyword string) ([]promptstore.PromptTemplate, error) {
	if s.store == nil {
		return []promptstore.PromptTemplate{}, nil
	}
	templates, err := s.store.List(ctx, promptstore.ListFilter{
		Keyword: strings.TrimSpace(keyword),
		Limit:   promptRPCLimit,
	})
	if err != nil {
		return nil, err
	}
	return filterVisiblePrompts(templates, cwd), nil
}

func (s *promptRPCService) WritePrompt(ctx context.Context, cwd string, req PromptWriteRequest) (*promptstore.PromptTemplate, error) {
	if s.store == nil {
		return nil, errPromptStoreRequired
	}
	var template *promptstore.PromptTemplate
	err := s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		next, err := upsertPromptWithArchive(ctx, txStore, cwd, req)
		if err != nil {
			return err
		}
		template = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	return template, nil
}

func (s *promptRPCService) DeletePrompt(ctx context.Context, cwd, key string) error {
	if s.store == nil {
		return errPromptStoreRequired
	}
	promptKey := strings.TrimSpace(key)
	if promptKey == "" {
		return errors.New("prompt: prompt id is required")
	}
	return s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		current, err := txStore.Get(ctx, promptKey)
		if err != nil {
			return err
		}
		if err := validatePromptScope(current, cwd); err != nil {
			return err
		}
		if err := archivePrompt(ctx, txStore, *current); err != nil {
			return err
		}
		return txStore.Delete(ctx, promptKey)
	})
}

func filterVisiblePrompts(templates []promptstore.PromptTemplate, cwd string) []promptstore.PromptTemplate {
	out := make([]promptstore.PromptTemplate, 0, len(templates))
	for _, t := range templates {
		if t.Enabled && promptVisibleForCWD(t, cwd) {
			out = append(out, t)
		}
	}
	return out
}

func upsertPromptWithArchive(ctx context.Context, store promptstore.Store, cwd string, p PromptWriteRequest) (*promptstore.PromptTemplate, error) {
	if err := validatePromptWrite(p); err != nil {
		return nil, err
	}
	current, err := lookupPromptForMutation(ctx, store, p.ID)
	if err != nil {
		return nil, err
	}
	if err := validatePromptScope(current, cwd); err != nil {
		return nil, err
	}
	if current != nil {
		if err := archivePrompt(ctx, store, *current); err != nil {
			return nil, err
		}
	}
	key, err := resolvePromptKey(ctx, store, p, current)
	if err != nil {
		return nil, err
	}
	return store.Upsert(ctx, buildPromptTemplate(p, cwd, key, current))
}

func lookupPromptForMutation(ctx context.Context, store promptstore.Store, id string) (*promptstore.PromptTemplate, error) {
	key := strings.TrimSpace(id)
	if key == "" {
		return nil, nil
	}
	current, err := store.Get(ctx, key)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return current, nil
}

func resolvePromptKey(ctx context.Context, store promptstore.Store, p PromptWriteRequest, current *promptstore.PromptTemplate) (string, error) {
	if current != nil {
		return current.PromptKey, nil
	}
	base := promptKeyBase(p.AgentType, p.Name)
	_, err := store.Get(ctx, base)
	switch {
	case err == nil:
		return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()), nil
	case platformdb.IsNotFound(err):
		return base, nil
	default:
		return "", err
	}
}

func buildPromptTemplate(p PromptWriteRequest, cwd, key string, current *promptstore.PromptTemplate) promptstore.PromptTemplate {
	template := promptstore.PromptTemplate{
		PromptKey:   key,
		Title:       strings.TrimSpace(p.Name),
		AgentKey:    promptAgentType(p.AgentType),
		PromptText:  p.Content,
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), promptScopeForWrite(current, cwd)),
		Enabled:     true,
		CreatedBy:   promptUpdatedBy,
		UpdatedBy:   promptUpdatedBy,
		Description: strings.TrimSpace(p.Description),
	}
	if current == nil {
		return template
	}
	template.CreatedBy = current.CreatedBy
	template.ToolName = current.ToolName
	template.Variables = append(json.RawMessage(nil), current.Variables...)
	template.Tags = withPromptScopeTag(current.Tags, promptScopeForWrite(current, cwd))
	if strings.TrimSpace(p.AgentType) == "" {
		template.AgentKey = current.AgentKey
	}
	return template
}

func archivePrompt(ctx context.Context, store promptstore.Store, current promptstore.PromptTemplate) error {
	return store.InsertVersion(ctx, promptstore.PromptTemplateVersion{
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
}

func promptKeyBase(agentType, name string) string {
	slug := promptSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "prompt"
	}
	return promptAgentType(agentType) + "/" + slug
}

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

func validatePromptWrite(p PromptWriteRequest) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return errors.New("prompt: prompt name is required")
	case len(p.Content) > promptMaxContentBytes:
		return fmt.Errorf("prompt: prompt content exceeds %d bytes", promptMaxContentBytes)
	case len(p.Description) > promptMaxDescriptionBytes:
		return fmt.Errorf("prompt: prompt description exceeds %d bytes", promptMaxDescriptionBytes)
	default:
		return nil
	}
}

func validatePromptScope(current *promptstore.PromptTemplate, cwd string) error {
	if current == nil {
		return nil
	}
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return nil
	}
	currentScope := promptScopeFromTags(current.Tags)
	if currentScope == "" || currentScope == requestScope {
		return nil
	}
	return fmt.Errorf("prompt: prompt %q is outside cwd scope", current.PromptKey)
}

func promptVisibleForCWD(template promptstore.PromptTemplate, cwd string) bool {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return true
	}
	storedScope := promptScopeFromTags(template.Tags)
	return storedScope == "" || storedScope == requestScope
}

func promptScopeForWrite(current *promptstore.PromptTemplate, cwd string) string {
	if value := strings.TrimSpace(cwd); value != "" {
		return value
	}
	if current == nil {
		return ""
	}
	return promptScopeFromTags(current.Tags)
}

func promptScopeFromTags(raw json.RawMessage) string {
	for _, tag := range promptTags(raw) {
		if value, ok := strings.CutPrefix(tag, promptScopeTagPrefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func withPromptScopeTag(raw json.RawMessage, cwd string) json.RawMessage {
	tags := promptTags(raw)
	next := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" && !strings.HasPrefix(tag, promptScopeTagPrefix) {
			next = append(next, tag)
		}
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		next = append(next, promptScopeTagPrefix+cwd)
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return json.RawMessage("[]")
	}
	return json.RawMessage(encoded)
}

func promptTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return []string{}
	}
	return tags
}

func promptItemsFromTemplates(templates []promptstore.PromptTemplate) []promptRPCItem {
	items := make([]promptRPCItem, 0, len(templates))
	for _, t := range templates {
		items = append(items, promptItemFromTemplate(t))
	}
	return items
}

func promptItemFromTemplate(t promptstore.PromptTemplate) promptRPCItem {
	return promptRPCItem{
		ID:          t.PromptKey,
		Name:        t.Title,
		Content:     t.PromptText,
		Description: t.Description,
		AgentType:   t.AgentKey,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}
