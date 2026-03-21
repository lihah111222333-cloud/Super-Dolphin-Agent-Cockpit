package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
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
	errPromptTxRequired    = errors.New("dashboard: prompt transaction runner is not configured")
	promptSlugPattern      = regexp.MustCompile(`[^a-z0-9]+`)
)

// TODO(P8): prompts are global today; cwd is reserved for future project-scoped templates.
type promptListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

// TODO(P8): prompts are global today; cwd is reserved for future project-scoped templates.
type promptWriteParams struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Content     string `json:"content,omitempty"`
	Description string `json:"description,omitempty"`
	AgentType   string `json:"agentType,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

// TODO(P8): prompts are global today; cwd is reserved for future project-scoped templates.
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

type promptTxRunner interface {
	WithStore(ctx context.Context, fn func(promptstore.Store) error) error
}

type sqlcPromptTxRunner struct {
	queries *sqlc.Queries
}

func NewPromptHandlers(store promptstore.Store, queries *sqlc.Queries) rpc.HandlerMapResult {
	return buildPromptHandlers(store, newPromptTxRunner(queries))
}

func buildPromptHandlers(store promptstore.Store, txRunner promptTxRunner) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"prompts/list": rpc.StrictHandler(func(ctx context.Context, p promptListParams) (any, error) {
			items, err := listPromptItems(ctx, store, p.Cwd)
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompts": items}, nil
		}),
		"prompts/write": rpc.StrictHandler(func(ctx context.Context, p promptWriteParams) (any, error) {
			item, err := writePrompt(ctx, store, txRunner, p)
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompt": item}, nil
		}),
		"prompts/delete": rpc.StrictHandler(func(ctx context.Context, p promptDeleteParams) (any, error) {
			if err := deletePrompt(ctx, store, txRunner, p.ID, p.Cwd); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
	}}
}

func newPromptTxRunner(queries *sqlc.Queries) promptTxRunner {
	if queries == nil {
		return nil
	}
	return sqlcPromptTxRunner{queries: queries}
}

func (r sqlcPromptTxRunner) WithStore(ctx context.Context, fn func(promptstore.Store) error) error {
	if r.queries == nil {
		return errPromptTxRequired
	}
	return sqlc.WithTx(ctx, r.queries, func(txq *sqlc.Queries) error {
		return fn(promptstore.NewStore(txq))
	})
}

func listPromptItems(ctx context.Context, store promptstore.Store, cwd string) ([]promptRPCItem, error) {
	if store == nil {
		return []promptRPCItem{}, nil
	}
	templates, err := store.List(ctx, promptstore.ListFilter{Limit: promptRPCLimit})
	if err != nil {
		return nil, err
	}
	items := make([]promptRPCItem, 0, len(templates))
	for _, template := range templates {
		if !template.Enabled {
			continue
		}
		if !promptVisibleForCWD(template, cwd) {
			continue
		}
		items = append(items, promptItemFromTemplate(template))
	}
	return items, nil
}

func writePrompt(ctx context.Context, store promptstore.Store, txRunner promptTxRunner, p promptWriteParams) (*promptRPCItem, error) {
	if store == nil {
		return nil, errPromptStoreRequired
	}
	if txRunner == nil {
		return nil, errPromptTxRequired
	}
	var template *promptstore.PromptTemplate
	err := txRunner.WithStore(ctx, func(txStore promptstore.Store) error {
		next, err := upsertPrompt(ctx, txStore, p)
		if err != nil {
			return err
		}
		template = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	item := promptItemFromTemplate(*template)
	return &item, nil
}

func upsertPrompt(ctx context.Context, store promptstore.Store, p promptWriteParams) (*promptstore.PromptTemplate, error) {
	if err := validatePromptWrite(p); err != nil {
		return nil, err
	}
	current, err := lookupPrompt(ctx, store, p.ID)
	if err != nil {
		return nil, err
	}
	if err := validatePromptScope(current, p.Cwd); err != nil {
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
	return store.Upsert(ctx, buildPromptTemplate(p, key, current))
}

func deletePrompt(ctx context.Context, store promptstore.Store, txRunner promptTxRunner, id, cwd string) error {
	if store == nil {
		return errPromptStoreRequired
	}
	key := strings.TrimSpace(id)
	if key == "" {
		return errors.New("dashboard: prompt id is required")
	}
	if txRunner == nil {
		return errPromptTxRequired
	}
	return txRunner.WithStore(ctx, func(txStore promptstore.Store) error {
		current, err := txStore.Get(ctx, key)
		if err != nil {
			return err
		}
		if err := validatePromptScope(current, cwd); err != nil {
			return err
		}
		if err := archivePrompt(ctx, txStore, *current); err != nil {
			return err
		}
		return txStore.Delete(ctx, key)
	})
}

func lookupPrompt(ctx context.Context, store promptstore.Store, id string) (*promptstore.PromptTemplate, error) {
	key := strings.TrimSpace(id)
	if key == "" {
		return nil, nil
	}
	return store.Get(ctx, key)
}

func resolvePromptKey(
	ctx context.Context,
	store promptstore.Store,
	p promptWriteParams,
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
	p promptWriteParams,
	key string,
	current *promptstore.PromptTemplate,
) promptstore.PromptTemplate {
	template := promptstore.PromptTemplate{
		PromptKey:   key,
		Title:       strings.TrimSpace(p.Name),
		AgentKey:    promptAgentType(p.AgentType),
		PromptText:  p.Content,
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), promptScopeForWrite(current, p.Cwd)),
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
	template.Tags = withPromptScopeTag(current.Tags, promptScopeForWrite(current, p.Cwd))
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

func promptItemFromTemplate(template promptstore.PromptTemplate) promptRPCItem {
	return promptRPCItem{
		ID:          template.PromptKey,
		Name:        template.Title,
		Content:     template.PromptText,
		Description: template.Description,
		AgentType:   template.AgentKey,
		CreatedAt:   template.CreatedAt,
		UpdatedAt:   template.UpdatedAt,
	}
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

func validatePromptWrite(p promptWriteParams) error {
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
		if strings.HasPrefix(tag, promptScopeTagPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(tag, promptScopeTagPrefix))
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
