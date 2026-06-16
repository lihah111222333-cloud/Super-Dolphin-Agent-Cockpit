package prompt

import (
	"context"
	"encoding/json"
	"errors"
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
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SetEnabled intentionally stays outside this surface until the store/service
// contract exists end-to-end.
type PromptService interface {
	ListPrompts(ctx context.Context, cwd, keyword string) ([]promptstore.PromptTemplate, error)
	ListPromptSectionsByTemplates(ctx context.Context, cwd string, templates []promptstore.PromptTemplate) (map[int64][]promptstore.PromptTemplateSection, error)
	GetPrompt(ctx context.Context, cwd, key string) (*promptstore.PromptTemplate, error)
	WritePrompt(ctx context.Context, cwd string, prompt PromptWriteRequest) (*promptstore.PromptTemplate, error)
	DeletePrompt(ctx context.Context, cwd, key string, scope ...string) error
	// ListSections / WriteSection / DeleteSection back the advanced-debug UI.
	// Ordinary users never touch these — the per-template PromptText editor
	// remains the primary path. Sections power the Step 1/2/3b cached-prefix
	// / uncached-tail / enable_when feature gate.
	ListSections(ctx context.Context, cwd, promptKey string) ([]promptstore.PromptTemplateSection, error)
	WriteSection(ctx context.Context, cwd string, req PromptSectionWriteRequest) (*promptstore.PromptTemplateSection, error)
	DeleteSection(ctx context.Context, cwd, promptKey, sectionKey string, scope ...string) error
}

type PromptWriteRequest struct {
	ID, Name, Content, Description, AgentType string
	// ContentSet distinguishes omitted content (preserve existing prompt_text)
	// from an explicit empty string (clear prompt_text).
	ContentSet bool
	// WhenToUseSet distinguishes an omitted field (preserve existing metadata)
	// from an explicit empty string (clear metadata).
	WhenToUse    string
	WhenToUseSet bool
	// MatchWhen feeds the router's match_when auto-route rung. MatchWhenSet
	// distinguishes omitted updates (preserve existing metadata) from explicit
	// nil / empty updates (opt-out from auto-routing).
	MatchWhen    json.RawMessage
	MatchWhenSet bool
	Priority     int
	Enabled      *bool
	Scope        string
	ScopeSet     bool
	// Tags carries client-visible scene tags (e.g. ["代码审查","bug"]).
	// Internal scope:// tags are managed separately and merged on write.
	Tags json.RawMessage
}

// PromptSectionWriteRequest is the advanced-debug upsert payload. PromptKey
// identifies the parent template (= prompt_templates.prompt_key); SectionKey
// is the stable per-template identifier used for dedup / delete. EnableWhen
// accepts raw JSONB bytes (nil / "{}" / "null" all mean "always inject").
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

type promptService struct {
	store    promptstore.Store
	sections contract.SectionInvalidator
	builtin  contract.BuiltinPromptRegistry
}

type promptListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

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

type promptOptionalRawMessage struct {
	Raw json.RawMessage
	Set bool
}

// UnmarshalJSON 解码JSON。
func (m *promptOptionalRawMessage) UnmarshalJSON(data []byte) error {
	m.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		m.Raw = nil
		return nil
	}
	m.Raw = append(m.Raw[:0], data...)
	return nil
}

type promptDeleteParams struct {
	ID    string  `json:"id"`
	Cwd   string  `json:"cwd,omitempty"`
	Scope *string `json:"scope,omitempty"`
}

type promptGetParams struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd,omitempty"`
}

type promptSectionListParams struct {
	PromptID string `json:"prompt_id"`
	Cwd      string `json:"cwd,omitempty"`
}

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

type promptSectionDeleteParams struct {
	PromptID   string  `json:"prompt_id"`
	SectionKey string  `json:"section_key"`
	Cwd        string  `json:"cwd,omitempty"`
	Scope      *string `json:"scope,omitempty"`
}

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

const promptListContentPreviewMaxRunes = 200

var promptRecallTopicPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var _ contract.PromptAssemblyService = (*service)(nil)
var _ PromptService = (*promptService)(nil)

// NewService 创建服务。
func NewService(cfg *Config, logger *pkglogger.Logger, opts ...ServiceOption) Service {
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

// buildPromptHandlersWithService 构建带服务的prompt处理器。
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

func handlePromptWrite(ctx context.Context, promptSvc PromptService, p promptWriteParams) (any, error) {
	req := promptWriteRequestFromParams(p)
	template, err := promptSvc.WritePrompt(ctx, p.Cwd, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"prompt": promptItemFromTemplate(*template)}, nil
}

// promptWriteRequestFromParams 从params处理promptwrite请求。
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

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validatePromptDiscoverability(template promptstore.PromptTemplate, current *promptstore.PromptTemplate, explicit bool) error {
	if strings.TrimSpace(template.WhenToUse) != "" || current != nil && !explicit {
		return nil
	}
	return errors.New("dashboard: prompt when_to_use is required")
}

func handlePromptDelete(ctx context.Context, promptSvc PromptService, p promptDeleteParams) (any, error) {
	if err := deletePromptWithOptionalScope(ctx, promptSvc, p); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func deletePromptWithOptionalScope(ctx context.Context, promptSvc PromptService, p promptDeleteParams) error {
	if p.Scope == nil {
		return promptSvc.DeletePrompt(ctx, p.Cwd, p.ID)
	}
	return promptSvc.DeletePrompt(ctx, p.Cwd, p.ID, stringValue(p.Scope))
}

func handlePromptSectionList(ctx context.Context, promptSvc PromptService, p promptSectionListParams) (any, error) {
	sections, err := promptSvc.ListSections(ctx, p.Cwd, p.PromptID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sections": promptSectionItemsFromStore(sections, p.PromptID)}, nil
}

func handlePromptSectionWrite(ctx context.Context, promptSvc PromptService, p promptSectionWriteParams) (any, error) {
	section, err := promptSvc.WriteSection(ctx, p.Cwd, promptSectionWriteRequestFromParams(p))
	if err != nil {
		return nil, err
	}
	return map[string]any{"section": promptSectionItemFromStore(*section, p.PromptID)}, nil
}

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

func handlePromptSectionDelete(ctx context.Context, promptSvc PromptService, p promptSectionDeleteParams) (any, error) {
	if err := deletePromptSectionWithOptionalScope(ctx, promptSvc, p); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func deletePromptSectionWithOptionalScope(ctx context.Context, promptSvc PromptService, p promptSectionDeleteParams) error {
	if p.Scope == nil {
		return promptSvc.DeleteSection(ctx, p.Cwd, p.PromptID, p.SectionKey)
	}
	return promptSvc.DeleteSection(ctx, p.Cwd, p.PromptID, p.SectionKey, stringValue(p.Scope))
}

func promptSectionItemsFromStore(sections []promptstore.PromptTemplateSection, promptKey string) []promptSectionRPCItem {
	out := make([]promptSectionRPCItem, 0, len(sections))
	for _, sec := range sections {
		out = append(out, promptSectionItemFromStore(sec, promptKey))
	}
	return out
}

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

func promptItemFromTemplateWithFullSections(template promptstore.PromptTemplate, sections []promptstore.PromptTemplateSection) promptRPCItem {
	template = promptTemplateWithInferredSectionIntent(template, sections)
	item := promptItemFromTemplate(template)
	if content := promptEditableSectionsContent(template, sections); content != "" {
		item.Content = content
	}
	return item
}

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

func promptSectionsContentPreview(sections []promptstore.PromptTemplateSection) string {
	return promptSectionsContent(sections, promptListContentPreviewMaxRunes)
}

// promptSectionsContent 处理promptsections内容。
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

func promptEditableSectionsContent(template promptstore.PromptTemplate, sections []promptstore.PromptTemplateSection) string {
	if promptTemplateIntentKind(template) == "recall" {
		return promptRecallSectionsContent(sections)
	}
	return promptSectionsContent(sections, 0)
}

// promptRecallSectionsContent 处理promptrecallsections内容。
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

func promptPreviewSectionBody(section promptstore.PromptTemplateSection) string {
	if !section.Enabled || strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall") {
		return ""
	}
	return strings.TrimSpace(section.Body)
}

func validateRecallTopicForWrite(triggerType, topic string) error {
	if strings.TrimSpace(strings.ToLower(triggerType)) != "recall" {
		return nil
	}
	if !validPromptRecallTopicName(strings.TrimSpace(topic)) {
		return errors.New("dashboard: recall_topic must be lowercase dash-separated and shorter than 64 characters")
	}
	return nil
}

func validPromptRecallTopicName(topic string) bool {
	return len(topic) < 64 && promptRecallTopicPattern.MatchString(topic)
}

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

func truncatePromptListContentPreview(text string) string {
	runes := []rune(text)
	if len(runes) <= promptListContentPreviewMaxRunes {
		return text
	}
	return string(runes[:promptListContentPreviewMaxRunes])
}

// filterVisibleTags strips internal scope tags, returning only user-visible tags.
// filterVisibleTags 处理过滤条件visibletags。
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
		return json.RawMessage("[]")
	}
	return json.RawMessage(encoded)
}

// AsPromptRegistry 把prompt处理为prompt注册表。
func AsPromptRegistry(svc Service) PromptRegistry {
	return svc
}

// AsPromptAssemblyService 把prompt处理为promptassembly服务。
func AsPromptAssemblyService(svc Service) contract.PromptAssemblyService {
	return svc
}

// AsDynamicSectionRegistrar 把prompt处理为dynamicsectionregistrar。
func AsDynamicSectionRegistrar(svc Service) contract.DynamicSectionRegistrar {
	return svc
}
