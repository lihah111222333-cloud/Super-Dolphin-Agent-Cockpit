package thread

import (
	"context"
	"encoding/json"
	"strings"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// bindPromptStore wires the prompt_templates store post-construction. Kept as
// a setter (rather than a NewService parameter) to avoid churning the
// constructor signature; module.go calls it from registerSubscriptions.
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
		pkglogger.Info("router: skip, base_instructions already set by caller",
			"agent_key", req.AgentKey, "base_instructions_len", len(req.BaseInstructions))
		return
	}
	if s == nil || s.promptStore == nil {
		pkglogger.Info("router: skip, prompt store not wired")
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
		pkglogger.Info("router: no prompt_template matched",
			"requested_agent_key", req.AgentKey,
			"candidate_count", len(templates))
		return
	}
	pkglogger.Info("router: prompt_template matched",
		"prompt_key", picked.PromptKey,
		"agent_key", picked.AgentKey,
		"candidate_count", len(templates),
		"prompt_text_len", len(picked.PromptText))

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
	// Per Risk 1 (b): still record agent_key / prompt_key for observability
	// even if version materialization fails.
	req.AgentKey = picked.AgentKey
	req.PromptKey = picked.PromptKey
	req.BaseInstructions = picked.PromptText
	if verr != nil {
		pkglogger.Warn("router: materialize prompt_versions failed",
			"err", verr, "prompt_key", picked.PromptKey)
		req.PromptVersionID = nil
		return
	}
	req.PromptVersionID = &versionID
}

// defaultPromptKey is the prompt_template used when the caller does not pin
// an agent_key. It stamps a baseline persona on ad-hoc threads (e.g. user
// opens a fresh conversation from the UI without picking a specialist). The
// key is hardcoded rather than configurable because this harness's contract
// is "agent_key is the identity key" — anything else is the default.
const defaultPromptKey = "main/default"

// pickRoutedTemplate selects the prompt_template to inject as the new agent
// process's --system-prompt. This harness deliberately does NOT classify user
// intent: it is a process-lifecycle layer, not a routing brain.
//
//   - Explicit AgentKey (set by the caller, e.g. orchestration_launch_agent
//     with agent_key=sql-expert, or the UI's agent picker): look up the first
//     enabled template whose agent_key matches.
//   - Empty AgentKey (user opens a fresh thread without picking a specialist):
//     fall back to the hardcoded defaultPromptKey persona. When picked, stamp
//     req.AgentKey so downstream observability / persistence sees a concrete
//     identity instead of "".
//
// Tag-based keyword classification lived here previously and has been removed
// — upstream CLIs (Claude Code / Codex) perform their own in-session intent
// handling; duplicating it at the harness layer created the "user-created
// prompt is permanently shadowed by main/default fallback" footgun.
func (s *service) pickRoutedTemplate(
	_ context.Context,
	req *StartRequest,
	templates []promptstore.PromptTemplate,
) *promptstore.PromptTemplate {
	// Explicit prompt_key beats everything else: it's the most specific pin
	// the UI can give ("use this exact row"). If it doesn't resolve, refuse
	// to fall through — the user picked this row, silently substituting a
	// different one would be worse than leaving the request untouched and
	// letting the upstream CLI use its bundled system prompt.
	if pinned := strings.TrimSpace(req.PromptKey); pinned != "" {
		picked := findEnabledByPromptKey(templates, pinned)
		if picked != nil {
			req.AgentKey = picked.AgentKey
			return picked
		}
		return nil
	}
	if explicit := strings.TrimSpace(req.AgentKey); explicit != "" {
		return firstEnabledByAgentKey(templates, explicit)
	}
	picked := findByPromptKey(templates, defaultPromptKey)
	if picked != nil && picked.Enabled {
		req.AgentKey = picked.AgentKey
		return picked
	}
	return nil
}

func findEnabledByPromptKey(templates []promptstore.PromptTemplate, promptKey string) *promptstore.PromptTemplate {
	picked := findByPromptKey(templates, promptKey)
	if picked == nil || !picked.Enabled {
		return nil
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
		if t.Enabled && strings.EqualFold(strings.TrimSpace(t.AgentKey), want) {
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
