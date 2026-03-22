package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

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

// TODO(P8): add SetEnabled once the store/service contract exists end-to-end.
type PromptService interface {
	ListPrompts(ctx context.Context, cwd, keyword string) ([]promptstore.PromptTemplate, error)
	GetPrompt(ctx context.Context, cwd, key string) (*promptstore.PromptTemplate, error)
	WritePrompt(ctx context.Context, cwd string, prompt PromptWriteRequest) (*promptstore.PromptTemplate, error)
	DeletePrompt(ctx context.Context, cwd, key string) error
}

type PromptWriteRequest struct {
	ID, Name, Content, Description, AgentType string
}

type promptService struct {
	store promptstore.Store
}

var _ PromptService = (*promptService)(nil)

func NewPromptService(store promptstore.Store) PromptService {
	return newPromptService(store)
}

func newPromptService(store promptstore.Store) PromptService {
	return &promptService{store: store}
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
		return nil, platformdb.ErrNotFound
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
	var template *promptstore.PromptTemplate
	err := s.store.WithTx(ctx, func(txStore promptstore.Store) error {
		next, err := upsertPrompt(ctx, txStore, cwd, prompt)
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

func (s *promptService) DeletePrompt(ctx context.Context, cwd, key string) error {
	if s.store == nil {
		return errPromptStoreRequired
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
		if err := validatePromptScope(current, cwd); err != nil {
			return err
		}
		if err := archivePrompt(ctx, txStore, *current); err != nil {
			return err
		}
		return txStore.Delete(ctx, promptKey)
	})
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
	case platformdb.IsNotFound(err):
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
		return errors.New("dashboard: prompt name is required")
	case len(p.Content) > promptMaxContentBytes:
		return fmt.Errorf("dashboard: prompt content exceeds %d bytes", promptMaxContentBytes)
	case len(p.Description) > promptMaxDescriptionBytes:
		return fmt.Errorf("dashboard: prompt description exceeds %d bytes", promptMaxDescriptionBytes)
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
