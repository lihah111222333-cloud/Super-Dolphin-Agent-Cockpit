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
type DisabledBuiltinToolsFn func(ctx context.Context, cwd string) []string

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
	skillStore      contract.SkillReplacementAggregator
	disabledToolsFn DisabledBuiltinToolsFn

	dynamicMu sync.RWMutex
	dynamic   map[string]DynamicSectionProvider
}

// ServiceOption configures optional dependencies of the prompt Service.
type ServiceOption func(*service)

// WithPromptHintSources injects the preference store and shared-file reader
// used to resolve the user-configurable LSP prompt hint that is prepended to
// the start system prompt.
func WithPromptHintSources(prefs uipreference.Store, sharedFiles sharedfilestore.Reader) ServiceOption {
	return func(s *service) {
		s.prefs = prefs
		s.sharedFiles = sharedFiles
	}
}

// WithSkillStore injects the skill replacement aggregator used to aggregate
// ReplacesNative declarations for cross-model native tool suppression.
func WithSkillStore(store contract.SkillReplacementAggregator) ServiceOption {
	return func(s *service) {
		s.skillStore = store
	}
}

// WithDisabledBuiltinToolsFn injects the function used to resolve soft-filtered
// builtin tools. The caller (e.g. the fx module) provides a closure over
// uistate resolution helpers, avoiding a direct import cycle between the prompt
// package and the uistate package.

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

func (s *promptService) ListPrompts(
	ctx context.Context,
	cwd, keyword string,
) ([]promptstore.PromptTemplate, error) {
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

func (s *promptService) GetPrompt(
	ctx context.Context,
	cwd, key string,
) (*promptstore.PromptTemplate, error) {
	if s.store == nil {
		return nil, errPromptStoreRequired
	}
	promptKey := strings.TrimSpace(key)
	if promptKey == "" {
		return nil, errors.New("dashboard: prompt id is required")
	}
	template, err := s.store.Get(ctx, promptKey)
	if err != nil {
		return nil, err
	}
	if !promptVisibleForRead(*template, cwd) {
		return nil, contract.ErrNotFound
	}
	return template, nil
}

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
	var template *promptstore.PromptTemplate
	err = s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		next, err := upsertPrompt(ctx, txStore, requestScope, prompt)
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

func (s *promptService) ListSections(ctx context.Context, cwd, promptKey string) ([]promptstore.PromptTemplateSection, error) {
	template, err := s.GetPrompt(ctx, cwd, promptKey)
	if err != nil {
		return nil, err
	}
	return s.store.ListSectionsByTemplateID(ctx, template.ID)
}

func (s *promptService) WriteSection(ctx context.Context, cwd string, req PromptSectionWriteRequest) (*promptstore.PromptTemplateSection, error) {
	if s.store == nil {
		return nil, errPromptStoreRequired
	}
	// sections 是 prompt_template 的附属调试数据，与 template 本身的读权限对齐：
	// 能看到（ListSections 返回）就能写。不用严格的 validatePromptScope ——
	// 用户在 cwd A 编辑过某全局模板后 scope 会被锊定在 cwd A，之后在 cwd B
	// 给同模板加分段会被误拦。GetPrompt / ListSections 用的 promptVisibleForRead
	// 已经足够作为门禅。
	promptKey := strings.TrimSpace(req.PromptKey)
	if promptKey == "" {
		return nil, errors.New("dashboard: prompt id is required")
	}
	var saved *promptstore.PromptTemplateSection
	err := s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		template, gerr := txStore.Get(ctx, promptKey)
		if gerr != nil {
			return gerr
		}
		if !promptVisibleForRead(*template, cwd) {
			return contract.ErrNotFound
		}
		section, uerr := txStore.UpsertSection(ctx, promptstore.PromptTemplateSection{
			TemplateID: template.ID,
			SectionKey: req.SectionKey,
			Region:     req.Region,
			Ordinal:    req.Ordinal,
			Body:       req.Body,
			EnableWhen: req.EnableWhen,
			Enabled:    req.Enabled,
		})
		if uerr != nil {
			return uerr
		}
		saved = section
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func (s *promptService) DeleteSection(ctx context.Context, cwd, promptKey, sectionKey string) error {
	if s.store == nil {
		return errPromptStoreRequired
	}
	// 与 WriteSection 同步：删除以读权限为门禅，不走严格的 validatePromptScope，
	// 避免全局模板被门禁在某个 cwd 后别的 cwd 编辑不了它的分段。
	pKey := strings.TrimSpace(promptKey)
	sKey := strings.TrimSpace(sectionKey)
	if pKey == "" || sKey == "" {
		return errors.New("dashboard: prompt id and section key are required")
	}
	return s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		template, gerr := txStore.Get(ctx, pKey)
		if gerr != nil {
			return gerr
		}
		if !promptVisibleForRead(*template, cwd) {
			return contract.ErrNotFound
		}
		return txStore.DeleteSection(ctx, template.ID, sKey)
	})
}

func (s *promptService) DeletePrompt(ctx context.Context, cwd, key string) error {
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
	return s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		current, err := txStore.Get(ctx, promptKey)
		if err != nil {
			return err
		}
		if err := validatePromptScope(current, requestScope); err != nil {
			return err
		}
		if err := archivePrompt(ctx, txStore, *current); err != nil {
			return err
		}
		return txStore.Delete(ctx, promptKey)
	})
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
		panic(err)
	}
}

func mustRegisterDynamicProvider(svc *service, provider DynamicSectionProvider) {
	if err := svc.RegisterDynamicProvider(provider); err != nil {
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
	return template.Enabled && promptVisibleForCWD(template, cwd)
}

func upsertPrompt(
	ctx context.Context,
	store promptstore.Store,
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
	p PromptWriteRequest,
	current *promptstore.PromptTemplate,
) (string, error) {
	if current != nil {
		return current.PromptKey, nil
	}
	base := promptKeyBase(p.AgentType, p.Name)
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

func buildPromptTemplate(
	p PromptWriteRequest,
	cwd, key string,
	current *promptstore.PromptTemplate,
) promptstore.PromptTemplate {
	baseTags := clientTagsOrDefault(p.Tags, nil)
	template := promptstore.PromptTemplate{
		PromptKey:      key,
		Title:          strings.TrimSpace(p.Name),
		AgentKey:       promptAgentType(p.AgentType),
		PromptText:     p.Content,
		Variables:      json.RawMessage("{}"),
		Tags:           withPromptScopeTag(baseTags, promptScopeForWrite(current, cwd)),
		Enabled:        true,
		ManuallyEdited: current != nil,
		CreatedBy:      promptUpdatedBy,
		UpdatedBy:      promptUpdatedBy,
		Description:    strings.TrimSpace(p.Description),
		MatchWhen:      append(json.RawMessage(nil), p.MatchWhen...),
		Priority:       p.Priority,
	}
	if current == nil {
		return template
	}
	template.CreatedBy = current.CreatedBy
	template.ToolName = current.ToolName
	template.Variables = append(json.RawMessage(nil), current.Variables...)
	template.Tags = withPromptScopeTag(clientTagsOrDefault(p.Tags, current.Tags), promptScopeForWrite(current, cwd))
	if strings.TrimSpace(p.AgentType) == "" {
		template.AgentKey = current.AgentKey
	}
	return template
}

// clientTagsOrDefault returns client-provided tags if present, otherwise falls
// back to existing (for updates) or empty (for creates).
func clientTagsOrDefault(clientTags json.RawMessage, existing json.RawMessage) json.RawMessage {
	if len(clientTags) > 0 && string(clientTags) != "null" {
		return clientTags
	}
	if len(existing) > 0 {
		return existing
	}
	return json.RawMessage("[]")
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

func validatePromptWrite(p PromptWriteRequest) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return errors.New("dashboard: prompt name is required")
	case len(p.Content) > promptMaxContentBytes:
		return fmt.Errorf("dashboard: prompt content exceeds %d bytes", promptMaxContentBytes)
	case len(p.Description) > promptMaxDescriptionBytes:
		return fmt.Errorf("dashboard: prompt description exceeds %d bytes", promptMaxDescriptionBytes)
	default:
		return nil
	}
}

func requirePromptCWD(cwd string) (string, error) {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return "", errors.New("dashboard: cwd is required")
	}
	return requestScope, nil
}

func validatePromptScope(current *promptstore.PromptTemplate, cwd string) error {
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	currentScope := promptScopeFromTags(current.Tags)
	if currentScope == "" || currentScope == requestScope {
		return nil
	}
	return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
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
