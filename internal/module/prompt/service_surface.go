package prompt

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// TODO(P8): add SetEnabled once the store/service contract exists end-to-end.
type PromptService interface {
	ListPrompts(ctx context.Context, cwd, keyword string) ([]promptstore.PromptTemplate, error)
	GetPrompt(ctx context.Context, cwd, key string) (*promptstore.PromptTemplate, error)
	WritePrompt(ctx context.Context, cwd string, prompt PromptWriteRequest) (*promptstore.PromptTemplate, error)
	DeletePrompt(ctx context.Context, cwd, key string) error
	// ListSections / WriteSection / DeleteSection back the advanced-debug UI.
	// Ordinary users never touch these — the per-template PromptText editor
	// remains the primary path. Sections power the Step 1/2/3b cached-prefix
	// / uncached-tail / enable_when feature gate.
	ListSections(ctx context.Context, cwd, promptKey string) ([]promptstore.PromptTemplateSection, error)
	WriteSection(ctx context.Context, cwd string, req PromptSectionWriteRequest) (*promptstore.PromptTemplateSection, error)
	DeleteSection(ctx context.Context, cwd, promptKey, sectionKey string) error
}

type PromptWriteRequest struct {
	ID, Name, Content, Description, AgentType string
	// MatchWhen feeds the router's match_when auto-route rung. nil / empty
	// means opt-out (template will not participate in auto-routing); any
	// other raw JSONB value is evaluated per EvaluateMatchWhen.
	MatchWhen json.RawMessage
	Priority  int
	// Tags carries client-visible scene tags (e.g. ["代码审查","bug"]).
	// Internal scope:// tags are managed separately and merged on write.
	Tags json.RawMessage
}

// PromptSectionWriteRequest is the advanced-debug upsert payload. PromptKey
// identifies the parent template (= prompt_templates.prompt_key); SectionKey
// is the stable per-template identifier used for dedup / delete. EnableWhen
// accepts raw JSONB bytes (nil / "{}" / "null" all mean "always inject").
type PromptSectionWriteRequest struct {
	PromptKey  string
	SectionKey string
	Region     string // "static" | "dynamic"
	Ordinal    int
	Body       string
	EnableWhen []byte
	Enabled    bool
}

type promptService struct {
	store promptstore.Store
}

type promptListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type promptWriteParams struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name"`
	Content     string          `json:"content,omitempty"`
	Description string          `json:"description,omitempty"`
	AgentType   string          `json:"agentType,omitempty"`
	Cwd         string          `json:"cwd,omitempty"`
	MatchWhen   json.RawMessage `json:"match_when,omitempty"`
	Priority    int             `json:"priority,omitempty"`
	Tags        json.RawMessage `json:"tags,omitempty"`
}

type promptDeleteParams struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd,omitempty"`
}

type promptSectionListParams struct {
	PromptID string `json:"prompt_id"`
	Cwd      string `json:"cwd,omitempty"`
}

type promptSectionWriteParams struct {
	PromptID   string          `json:"prompt_id"`
	SectionKey string          `json:"section_key"`
	Region     string          `json:"region"`
	Ordinal    int             `json:"ordinal"`
	Body       string          `json:"body"`
	EnableWhen json.RawMessage `json:"enable_when,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`
	Cwd        string          `json:"cwd,omitempty"`
}

type promptSectionDeleteParams struct {
	PromptID   string `json:"prompt_id"`
	SectionKey string `json:"section_key"`
	Cwd        string `json:"cwd,omitempty"`
}

type promptSectionRPCItem struct {
	ID         int64           `json:"id"`
	PromptID   string          `json:"prompt_id"`
	SectionKey string          `json:"section_key"`
	Region     string          `json:"region"`
	Ordinal    int             `json:"ordinal"`
	Body       string          `json:"body"`
	EnableWhen json.RawMessage `json:"enable_when,omitempty"`
	Enabled    bool            `json:"enabled"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type promptRPCItem struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Content     string          `json:"content"`
	Description string          `json:"description"`
	AgentType   string          `json:"agentType"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	MatchWhen   json.RawMessage `json:"match_when,omitempty"`
	Priority    int             `json:"priority,omitempty"`
	Tags        json.RawMessage `json:"tags,omitempty"`
}

var _ contract.PromptAssemblyService = (*service)(nil)
var _ PromptService = (*promptService)(nil)

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

func registerPromptHandlers(store promptstore.Store) rpc.HandlerMapResult {
	return buildPromptHandlersWithService(newPromptService(store))
}

func newPromptService(store promptstore.Store) PromptService {
	return &promptService{store: store}
}

func buildPromptHandlersWithService(promptSvc PromptService) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"prompts/list": rpc.StrictHandler(func(ctx context.Context, p promptListParams) (any, error) {
			templates, err := promptSvc.ListPrompts(ctx, p.Cwd, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompts": promptItemsFromTemplates(templates)}, nil
		}),
		"prompts/write": rpc.StrictHandler(func(ctx context.Context, p promptWriteParams) (any, error) {
			template, err := promptSvc.WritePrompt(ctx, p.Cwd, PromptWriteRequest{
				ID:          p.ID,
				Name:        p.Name,
				Content:     p.Content,
				Description: p.Description,
				AgentType:   p.AgentType,
				MatchWhen:   append(json.RawMessage(nil), p.MatchWhen...),
				Priority:    p.Priority,
				Tags:        p.Tags,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompt": promptItemFromTemplate(*template)}, nil
		}),
		"prompts/delete": rpc.StrictHandler(func(ctx context.Context, p promptDeleteParams) (any, error) {
			if err := promptSvc.DeletePrompt(ctx, p.Cwd, p.ID); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
		"prompt_sections/list": rpc.StrictHandler(func(ctx context.Context, p promptSectionListParams) (any, error) {
			sections, err := promptSvc.ListSections(ctx, p.Cwd, p.PromptID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"sections": promptSectionItemsFromStore(sections, p.PromptID)}, nil
		}),
		"prompt_sections/write": rpc.StrictHandler(func(ctx context.Context, p promptSectionWriteParams) (any, error) {
			enabled := true
			if p.Enabled != nil {
				enabled = *p.Enabled
			}
			section, err := promptSvc.WriteSection(ctx, p.Cwd, PromptSectionWriteRequest{
				PromptKey:  p.PromptID,
				SectionKey: p.SectionKey,
				Region:     p.Region,
				Ordinal:    p.Ordinal,
				Body:       p.Body,
				EnableWhen: []byte(p.EnableWhen),
				Enabled:    enabled,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"section": promptSectionItemFromStore(*section, p.PromptID)}, nil
		}),
		"prompt_sections/delete": rpc.StrictHandler(func(ctx context.Context, p promptSectionDeleteParams) (any, error) {
			if err := promptSvc.DeleteSection(ctx, p.Cwd, p.PromptID, p.SectionKey); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
	}}
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
		ID:         section.ID,
		PromptID:   promptKey,
		SectionKey: section.SectionKey,
		Region:     section.Region,
		Ordinal:    section.Ordinal,
		Body:       section.Body,
		EnableWhen: json.RawMessage(section.EnableWhen),
		Enabled:    section.Enabled,
		CreatedAt:  section.CreatedAt,
		UpdatedAt:  section.UpdatedAt,
	}
}

func promptItemsFromTemplates(templates []promptstore.PromptTemplate) []promptRPCItem {
	items := make([]promptRPCItem, 0, len(templates))
	for _, template := range templates {
		items = append(items, promptItemFromTemplate(template))
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
		CreatedAt:   template.CreatedAt,
		UpdatedAt:   template.UpdatedAt,
		MatchWhen:   append(json.RawMessage(nil), template.MatchWhen...),
		Priority:    template.Priority,
		Tags:        filterVisibleTags(template.Tags),
	}
}

// filterVisibleTags strips internal scope:// tags, returning only user-visible tags.
func filterVisibleTags(raw json.RawMessage) json.RawMessage {
	tags := promptTags(raw)
	visible := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && !strings.HasPrefix(t, promptScopeTagPrefix) {
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

func AsPromptRegistry(svc Service) PromptRegistry {
	return svc
}

func AsPromptAssemblyService(svc Service) contract.PromptAssemblyService {
	return svc
}

func AsDynamicSectionRegistrar(svc Service) contract.DynamicSectionRegistrar {
	return svc
}
