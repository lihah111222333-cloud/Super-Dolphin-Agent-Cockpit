package thread

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/router"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Setters wire optional dependencies post-construction to avoid churning
// NewService / NewServiceWithPromptAssembly signatures. Called from
// registerSubscriptions in module.go (best-effort, both deps optional).

func (s *service) bindRouterBackend(backend router.Backend) {
	if s == nil || backend == nil {
		return
	}
	s.routerBackend = backend
}

func (s *service) bindPromptStore(store promptstore.Store) {
	if s == nil || store == nil {
		return
	}
	s.promptStore = store
}

// resolveRoutedPrompt is called from service.Start after normalizeStartRequest.
// It fills req.BaseInstructions / req.AgentKey / req.PromptVersionID based on
// either an explicit req.AgentKey or router classification.
//
// Contract:
//   - If req.BaseInstructions is non-empty (manual override), skip everything.
//   - If promptStore or routerBackend is nil (fx did not wire them), skip
//     silently — caller falls back to provider default.
//   - Any error along the pipeline is logged and degraded per Risk 1 (c)+(b):
//     record agent_key if we got one, leave prompt_version_id nil.
func (s *service) resolveRoutedPrompt(ctx context.Context, req *StartRequest) {
	if req == nil {
		return
	}
	if strings.TrimSpace(req.BaseInstructions) != "" {
		return
	}
	if s == nil || s.promptStore == nil {
		return
	}

	// Read enabled candidates once. Limit is generous; rule matching is cheap
	// and list is unlikely to exceed a few hundred rows even in large orgs.
	templates, err := s.promptStore.List(ctx, promptstore.ListFilter{Limit: 200})
	if err != nil {
		pkglogger.Warn("router: list prompt_templates failed", "err", err)
		return
	}

	picked := s.pickRoutedTemplate(ctx, req, templates)
	if picked == nil || !picked.Enabled {
		return
	}

	// Materialize a prompt_versions snapshot so historical analyses can
	// reproduce the exact prompt text that was injected into this thread.
	versionID, verr := s.promptStore.InsertVersion(ctx, promptstore.PromptTemplateVersion{
		PromptKey:       picked.PromptKey,
		Title:           picked.Title,
		AgentKey:        picked.AgentKey,
		ToolName:        picked.ToolName,
		PromptText:      picked.PromptText,
		Variables:       append(json.RawMessage(nil), picked.Variables...),
		Tags:            append(json.RawMessage(nil), picked.Tags...),
		Description:     picked.Description,
		Enabled:         picked.Enabled,
		CreatedBy:       picked.CreatedBy,
		UpdatedBy:       picked.UpdatedBy,
		SourceUpdatedAt: &picked.UpdatedAt,
	})
	// Per Risk 1 (b): still record agent_key for observability even if
	// version materialization fails.
	req.AgentKey = picked.AgentKey
	req.BaseInstructions = picked.PromptText
	if verr != nil {
		pkglogger.Warn("router: materialize prompt_versions failed",
			"err", verr, "prompt_key", picked.PromptKey)
		req.PromptVersionID = nil
		return
	}
	req.PromptVersionID = &versionID
}

// pickRoutedTemplate returns the prompt_template selected either by the
// caller-pinned AgentKey or by the router classifier. Returns nil when
// nothing matches (the caller should then fall back to the provider default).
// When the router picks a template, req.AgentKey is stamped with the match.
func (s *service) pickRoutedTemplate(
	ctx context.Context,
	req *StartRequest,
	templates []promptstore.PromptTemplate,
) *promptstore.PromptTemplate {
	if explicit := strings.TrimSpace(req.AgentKey); explicit != "" {
		return firstEnabledByAgentKey(templates, explicit)
	}
	if s.routerBackend == nil {
		return nil
	}
	candidates := toRouterCandidates(templates)
	userInput := strings.TrimSpace(req.Prompt)
	if userInput == "" {
		userInput = strings.TrimSpace(req.Name)
	}
	decision, cerr := s.routerBackend.Classify(ctx, userInput, candidates)
	if cerr != nil {
		pkglogger.Warn("router: classify failed", "err", cerr)
		return nil
	}
	if !decision.Matched {
		return nil
	}
	picked := findByPromptKey(templates, decision.PromptKey)
	if picked != nil {
		req.AgentKey = picked.AgentKey
	}
	return picked
}

func firstEnabledByAgentKey(templates []promptstore.PromptTemplate, agentKey string) *promptstore.PromptTemplate {
	want := strings.TrimSpace(agentKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if t.Enabled && t.AgentKey == want {
			return t
		}
	}
	return nil
}

func findByPromptKey(templates []promptstore.PromptTemplate, promptKey string) *promptstore.PromptTemplate {
	want := strings.TrimSpace(promptKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if t.PromptKey == want {
			return t
		}
	}
	return nil
}

func toRouterCandidates(templates []promptstore.PromptTemplate) []router.Candidate {
	out := make([]router.Candidate, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if !t.Enabled {
			continue
		}
		out = append(out, router.Candidate{
			PromptKey:   t.PromptKey,
			AgentKey:    t.AgentKey,
			Title:       t.Title,
			Description: t.Description,
			Tags:        parseTagsJSON(t.Tags),
		})
	}
	return out
}

// parseTagsJSON decodes `tags` jsonb into a flat []string. Accepts either a
// plain string array or an array of {"value":"..."} objects. Malformed input
// yields an empty slice; RuleRouter treats that as "no tags" and simply
// skips the candidate without error.
func parseTagsJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err == nil {
		return asStrings
	}
	var asObjects []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &asObjects); err == nil {
		out := make([]string, 0, len(asObjects))
		for _, o := range asObjects {
			if s := strings.TrimSpace(o.Value); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
