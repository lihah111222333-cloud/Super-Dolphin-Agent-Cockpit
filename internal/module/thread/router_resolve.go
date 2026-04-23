package thread

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
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

// bindClassifier wires the optional prompt classifier. Nil and NoopClassifier
// are both safe: resolveRoutedPrompt guards on Enabled() so the existing
// single-pin path stays the exact same machine behavior when the classifier
// is off.
func (s *service) bindClassifier(c classifier.Classifier) {
	if s == nil || c == nil {
		return
	}
	s.classifier = c
}

func shouldSkipRoutedPrompt(s *service, req *StartRequest) bool {
	switch {
	case req == nil:
		return true
	case strings.TrimSpace(req.BaseInstructions) != "":
		pkglogger.Info("router: skip, base_instructions already set by caller",
			"agent_key", req.AgentKey, "base_instructions_len", len(req.BaseInstructions))
		return true
	case s == nil || s.promptStore == nil:
		pkglogger.Info("router: skip, prompt store not wired")
		return true
	default:
		return false
	}
}

func (s *service) listRoutedTemplates(ctx context.Context) ([]promptstore.PromptTemplate, bool) {
	templates, err := s.promptStore.List(ctx, promptstore.ListFilter{Limit: 200})
	if err != nil {
		pkglogger.Warn("router: list prompt_templates failed", "err", err)
		return nil, false
	}
	return templates, true
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
	if shouldSkipRoutedPrompt(s, req) {
		return
	}

	// Read enabled candidates once. Limit is generous; rule matching is cheap
	// and list is unlikely to exceed a few hundred rows even in large orgs.
	templates, ok := s.listRoutedTemplates(ctx)
	if !ok {
		return
	}

	// Classifier fallback: only runs when no explicit pin
	// and UseClassifier was opted in. Haiku scores the full enabled library
	// and stamps its pick into req.PromptKey so the normal explicit-pin branch
	// in pickRoutedTemplate takes over. Empty pick ("no strong match") leaves
	// req.PromptKey alone so the default persona fallback still applies.
	maybeClassifyPrompt(ctx, s.classifier, req, templates)

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
	s.applyPickedRoutedTemplate(ctx, req, picked)
}

func (s *service) applyPickedRoutedTemplate(
	ctx context.Context,
	req *StartRequest,
	picked *promptstore.PromptTemplate,
) {
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
	req.AgentTitle = picked.Title
	req.PromptKey = picked.PromptKey
	// Step 1: prefer structured sections; fall back to monolithic prompt_text
	// when the template has not been migrated yet. A store-side error degrades
	// to the legacy path so router routing is never blocked by this lookup.
	sections, serr := s.promptStore.ListSectionsByTemplateID(ctx, picked.ID)
	if serr != nil {
		pkglogger.Warn("router: list prompt_template_sections failed; using prompt_text",
			"err", serr, "template_id", picked.ID, "prompt_key", picked.PromptKey)
		req.BaseInstructions = picked.PromptText
		req.BaseInstructionBlocks = nil
	} else if len(sections) > 0 {
		req.BaseInstructionBlocks = convertStoreSectionsToBlocks(sections)
		req.BaseInstructions = ""
	} else {
		req.BaseInstructions = picked.PromptText
		req.BaseInstructionBlocks = nil
	}
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
			req.AgentTitle = picked.Title
			return picked
		}
		return nil
	}
	if explicit := strings.TrimSpace(req.AgentKey); explicit != "" {
		picked := firstEnabledByAgentKey(templates, explicit)
		if picked != nil {
			req.AgentTitle = picked.Title
		}
		return picked
	}
	picked := findByPromptKey(templates, defaultPromptKey)
	if picked != nil && picked.Enabled {
		req.AgentKey = picked.AgentKey
		req.AgentTitle = picked.Title
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

// convertStoreSectionsToBlocks maps enabled prompt_template_sections rows into
// the contract-layer BaseInstructionBlock shape consumed by assembler.go.
// Unknown region strings degrade to Dynamic (safer: blocks end up in the
// uncached tail rather than accidentally claiming cached-prefix slots).
func convertStoreSectionsToBlocks(sections []promptstore.PromptTemplateSection) []contract.BaseInstructionBlock {
	if len(sections) == 0 {
		return nil
	}
	out := make([]contract.BaseInstructionBlock, 0, len(sections))
	for _, s := range sections {
		region := contract.PromptRegionDynamic
		if strings.EqualFold(strings.TrimSpace(s.Region), "static") {
			region = contract.PromptRegionStatic
		}
		out = append(out, contract.BaseInstructionBlock{
			Key:        s.SectionKey,
			Region:     region,
			Ordinal:    s.Ordinal,
			Body:       s.Body,
			EnableWhen: append([]byte(nil), s.EnableWhen...),
		})
	}
	return out
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

// maybeClassifyPrompt runs the classifier when gates are satisfied and stamps
// the pick into req.PromptKey so the existing pickRoutedTemplate logic can
// take the normal explicit-pin branch. It never fails fatally: a classifier
// error is logged and the router proceeds with the empty pin (default
// fallback). This keeps the feature safe to enable globally.
func maybeClassifyPrompt(
	ctx context.Context,
	c classifier.Classifier,
	req *StartRequest,
	templates []promptstore.PromptTemplate,
) {
	userInput, ok := resolvePromptClassificationInput(req)
	if !ok {
		return
	}
	if !classifierReady(c) {
		return
	}
	candidates := classifierCandidates(templates)
	if len(candidates) == 0 {
		return
	}
	// Fast path: when tag-keyword overlap picks a clear winner (score >= 2
	// AND runner-up gap >= 1), skip the 5-15s claude -p round trip entirely.
	// The thresholds are deliberately tight so untagged rows like main/default
	// can't hijack the fast path; anything ambiguous falls through to haiku.
	if applyClassifierFastPath(req, candidates, userInput) {
		return
	}
	classifyPromptWithBackend(ctx, c, req, candidates, userInput)
}

func resolvePromptClassificationInput(req *StartRequest) (string, bool) {
	switch {
	case req == nil:
		return "", false
	case !req.UseClassifier:
		return "", false
	case strings.TrimSpace(req.PromptKey) != "":
		// Caller already pinned. Explicit pin wins over auto-classification.
		return "", false
	}
	userInput := strings.TrimSpace(req.Prompt)
	if userInput == "" {
		// No signal to classify on. Usually means eager-spawn Start without
		// a first turn; the caller will revisit on SpawnIfNeeded once user
		// input arrives.
		return "", false
	}
	return userInput, true
}

func classifierReady(c classifier.Classifier) bool {
	if c != nil && c.Enabled() {
		return true
	}
	pkglogger.Info("router: classifier opt-in but backend disabled",
		"hint", "set ENABLE_PROMPT_CLASSIFIER=true and ensure `claude` is on PATH")
	return false
}

func applyClassifierFastPath(req *StartRequest, candidates []classifier.Candidate, userInput string) bool {
	decision := classifier.FastPath(candidates, userInput)
	if !decision.Hit {
		return false
	}
	pkglogger.Info("router: classifier fast-path picked",
		"prompt_key", decision.Picked.PromptKey,
		"tag_score", decision.Score,
		"tag_gap", decision.Gap,
		"candidate_count", len(candidates))
	req.PromptKey = decision.Picked.PromptKey
	return true
}

func classifyPromptWithBackend(
	ctx context.Context,
	c classifier.Classifier,
	req *StartRequest,
	candidates []classifier.Candidate,
	userInput string,
) {
	// Prune down to top-K by tag-keyword overlap before spending LLM tokens.
	// With 11+ candidates in typical libraries, the untrimmed prompt pushes
	// haiku latency into the 10s range; a 5-row list keeps it closer to 3-5s.
	beforePrune := len(candidates)
	candidates = classifier.PruneCandidates(candidates, userInput, classifier.MaxCandidatesFromEnv())
	res, err := c.Classify(ctx, classifier.Input{UserInput: userInput, Candidates: candidates})
	if err != nil {
		pkglogger.Warn("router: classify failed",
			"err", err,
			"candidate_count", len(candidates),
			"user_input_len", len(userInput))
		return
	}
	if res.PromptKey == "" {
		pkglogger.Info("router: classifier returned empty pick",
			"reason", res.Reason,
			"latency_ms", res.Latency.Milliseconds(),
			"model", res.Model)
		return
	}
	pkglogger.Info("router: classifier picked",
		"prompt_key", res.PromptKey,
		"reason", res.Reason,
		"latency_ms", res.Latency.Milliseconds(),
		"model", res.Model,
		"candidate_count", len(candidates),
		"candidate_count_pre_prune", beforePrune)
	req.PromptKey = res.PromptKey
}

// classifierCandidates maps enabled prompt_templates to the classifier's DTO.
// Disabled rows are dropped so the classifier never picks a row that the
// subsequent pickRoutedTemplate would reject on Enabled=false.
func classifierCandidates(templates []promptstore.PromptTemplate) []classifier.Candidate {
	out := make([]classifier.Candidate, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if !t.Enabled {
			continue
		}
		out = append(out, classifier.Candidate{
			PromptKey:   t.PromptKey,
			Title:       t.Title,
			Description: t.Description,
			Tags:        decodeClassifierTags(t.Tags),
		})
	}
	return out
}

// decodeClassifierTags tolerates both the raw-JSON tags column shape and an
// empty/missing value. The prompt store stores tags as a JSON array of
// strings; anything else just yields an empty slice rather than failing.
func decodeClassifierTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	return tags
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

// d-clean: applyCandidatePoolMerge / candidateTemplateBlocks 已移除。
// 候选池合并注入的设计已与“对齐 Claude 主线程单提示词”的目标冲突；
// 路由现在的三档优先级为：
//   1. 显式 pin (PromptKey)    > 2. 分类器 opt-in > 3. main/default 兑底


