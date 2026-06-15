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
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

type PromptRegistry interface {
	RegisterSection(section PromptSection) error
	RegisterDynamicProvider(provider DynamicSectionProvider) error
	UnregisterDynamicProvider(name string) bool
	Sections() []PromptSection
}

type PromptAssemblyService = contract.PromptAssemblyService

type Service interface {
	PromptRegistry
	contract.PromptAssemblyService
	SectionInvalidator
	RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error
	Config() Config
}

// Compile-time assertion: *service satisfies contract.SectionInvalidator.
// The interface declares concurrent-safe semantics; *service backs that
// with the cache mutex (see cache.go) and the dynamicMu RWMutex.
var _ contract.SectionInvalidator = (*service)(nil)

// DisabledBuiltinToolsFn is the signature of a function that returns the
// sorted list of tool IDs the user has manually disabled via UI preferences.
// It is injected to break the import cycle between prompt and uistate.
type DisabledBuiltinToolsFn func(ctx context.Context, cwd, provider string) []string

type service struct {
	cfg              *Config
	logger           *slog.Logger
	registry         *SectionRegistry
	cache            *sectionCache
	userContextCache *userContextCache
	claudeMdProvider contract.ClaudeMdSourceProvider
	flight           singleflight.Group

	prefs           uipreference.Store
	sharedFiles     sharedfilestore.Reader
	disabledToolsFn DisabledBuiltinToolsFn

	dynamicMu sync.RWMutex
	dynamic   map[string]DynamicSectionProvider
}

// ServiceOption configures optional dependencies of the prompt Service.
type ServiceOption func(*service)

// WithPromptHintSources injects the preference store and shared-file reader
// used to resolve the user-configurable LSP prompt hint that is prepended to
// the start system prompt.
// WithPromptHintSources 设置prompthintsources。
func WithPromptHintSources(prefs uipreference.Store, sharedFiles sharedfilestore.Reader) ServiceOption {
	return func(s *service) {
		s.prefs = prefs
		s.sharedFiles = sharedFiles
	}
}

// WithDisabledBuiltinToolsFn injects the function used to resolve soft-filtered
// builtin tools. The caller (e.g. the fx module) provides a closure over
// uistate resolution helpers, avoiding a direct import cycle between the prompt
// package and the uistate package.

// WithDisabledBuiltinToolsFn 设置disabledbuiltin工具fn。
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

// Config 处理配置。
func (s *service) Config() Config {
	if s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

// RegisterSection 注册section。
func (s *service) RegisterSection(section PromptSection) error {
	return s.registry.Register(section)
}

// RegisterClaudeMdSourceProvider 注册claudemdsourceprovider。
func (s *service) RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error {
	s.claudeMdProvider = provider
	if s.userContextCache != nil {
		s.userContextCache.InvalidateAll()
	}
	return nil
}

// Sections 处理sections。
func (s *service) Sections() []PromptSection {
	return s.registry.Sections()
}

// ListPrompts 列出prompts。
func (s *promptService) ListPrompts(
	ctx context.Context,
	cwd, keyword string,
) ([]promptstore.PromptTemplate, error) {
	if s.store == nil {
		return []promptstore.PromptTemplate{}, nil
	}
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return nil, err
	}
	templates, err := s.store.List(ctx, promptstore.ListFilter{
		Keyword: strings.TrimSpace(keyword),
		CWD:     requestScope,
		Limit:   promptRPCLimit,
	})
	if err != nil {
		return nil, err
	}
	return filterVisiblePrompts(templates, requestScope), nil
}

// ListPromptSectionsByTemplates 按templates列出promptsections。
func (s *promptService) ListPromptSectionsByTemplates(
	ctx context.Context,
	cwd string,
	templates []promptstore.PromptTemplate,
) (map[int64][]promptstore.PromptTemplateSection, error) {
	sectionsByTemplateID := map[int64][]promptstore.PromptTemplateSection{}
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

// GetPrompt 读取prompt。
func (s *promptService) GetPrompt(
	ctx context.Context,
	cwd, key string,
) (*promptstore.PromptTemplate, error) {
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

// WritePrompt 写入prompt。
func (s *promptService) WritePrompt(
	ctx context.Context,
	cwd string,
	prompt PromptWriteRequest,
) (*promptstore.PromptTemplate, error) {
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
	var template *promptstore.PromptTemplate
	err = s.store.WithTx(ctx, func(txStore promptstore.Store) error {
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

// ListSections 列出sections。
func (s *promptService) ListSections(ctx context.Context, cwd, promptKey string) ([]promptstore.PromptTemplateSection, error) {
	template, err := s.GetPrompt(ctx, cwd, promptKey)
	if err != nil {
		return nil, err
	}
	return s.store.ListSectionsByTemplateID(ctx, template.ID)
}

// WriteSection 写入section。
func (s *promptService) WriteSection(ctx context.Context, cwd string, req PromptSectionWriteRequest) (*promptstore.PromptTemplateSection, error) {
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

// DeleteSection 删除section。
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
	err = s.store.WithTx(ctx, func(txStore promptstore.Store) error {
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

// DeletePrompt 删除prompt。
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
	err = s.store.WithTx(ctx, func(txStore promptstore.Store) error {
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

func optionalScopeValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

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

func mustRegisterSection(registry *SectionRegistry, section PromptSection) {
	if err := registry.Register(section); err != nil {
		// archguard:ignore panic_count -- builtin prompt section registration is a startup invariant.
		panic(err)
	}
}

func mustRegisterDynamicProvider(svc *service, provider DynamicSectionProvider) {
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		// archguard:ignore panic_count -- builtin dynamic provider registration is a startup invariant.
		panic(err)
	}
}

func filterVisiblePrompts(
	templates []promptstore.PromptTemplate,
	cwd string,
) []promptstore.PromptTemplate {
	items := make([]promptstore.PromptTemplate, 0, len(templates))
	for _, template := range templates {
		if promptVisibleForRead(template, cwd) {
			items = append(items, template)
		}
	}
	return items
}

func promptVisibleForRead(template promptstore.PromptTemplate, cwd string) bool {
	return promptVisibleForCWD(template, cwd)
}

// upsertPrompt 处理upsertprompt。
func upsertPrompt(
	ctx context.Context,
	store promptstore.Store,
	builtin contract.BuiltinPromptRegistry,
	cwd string,
	p PromptWriteRequest,
) (*promptstore.PromptTemplate, error) {
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
	template := buildPromptTemplate(p, cwd, key, current)
	if err := validatePromptDiscoverability(template, current, p.WhenToUseSet); err != nil {
		return nil, err
	}
	return storePromptTemplateAndContent(ctx, store, template, current, contentSection, p.Content)
}

func lookupPromptForMutation(
	ctx context.Context,
	store promptstore.Store,
	id string,
) (*promptstore.PromptTemplate, error) {
	key := strings.TrimSpace(id)
	if key == "" {
		return nil, nil
	}
	return store.Get(ctx, key)
}

func resolvePromptKey(
	ctx context.Context,
	store promptstore.Store,
	builtin contract.BuiltinPromptRegistry,
	p PromptWriteRequest,
	current *promptstore.PromptTemplate,
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

func builtinPromptExists(builtin contract.BuiltinPromptRegistry, promptKey string) bool {
	if builtin == nil {
		return false
	}
	_, ok := builtin.GetTemplate(strings.TrimSpace(promptKey))
	return ok
}

// buildPromptTemplate 构建prompttemplate。
func buildPromptTemplate(
	p PromptWriteRequest,
	cwd, key string,
	current *promptstore.PromptTemplate,
) promptstore.PromptTemplate {
	baseTags := clientTagsOrDefault(p.Tags, nil)
	scope := promptScopeForWrite(current, cwd, p.Scope, p.ScopeSet)
	whenToUse := ""
	if p.WhenToUseSet {
		whenToUse = strings.TrimSpace(p.WhenToUse)
	}
	template := promptstore.PromptTemplate{
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
		template.MatchWhen = sanitizeTemplateMatchWhen(p.MatchWhen)
	}
	if current == nil {
		return template
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
	return template
}

func promptEnabledForWrite(p PromptWriteRequest, current *promptstore.PromptTemplate) bool {
	if p.Enabled != nil {
		return *p.Enabled
	}
	if current != nil {
		return current.Enabled
	}
	return true
}

func promptTextForWrite(p PromptWriteRequest, current *promptstore.PromptTemplate) string {
	if current != nil && !p.ContentSet && p.Content == "" {
		return current.PromptText
	}
	return p.Content
}

// sanitizeTemplateMatchWhen 清理templatematchwhen。
func sanitizeTemplateMatchWhen(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var expr map[string]any
	if err := json.Unmarshal([]byte(trimmed), &expr); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	if _, ok := expr["tags_has"]; !ok {
		return append(json.RawMessage(nil), raw...)
	}
	delete(expr, "tags_has")
	if len(expr) == 0 {
		return nil
	}
	encoded, err := json.Marshal(expr)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func archivePrompt(ctx context.Context, store promptstore.Store, current promptstore.PromptTemplate) error {
	_, err := store.InsertVersion(ctx, promptstore.PromptTemplateVersion{
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

// validatePromptWrite 校验promptwrite。
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
