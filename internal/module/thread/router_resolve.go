package thread

import (
	"context"
	"encoding/json"
	"strconv"
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

func (s *service) tryCandidatePoolMerge(ctx context.Context, req *StartRequest, templates []promptstore.PromptTemplate) bool {
	switch {
	case req == nil:
		return false
	case strings.TrimSpace(req.PromptKey) != "":
		return false
	case len(req.PromptCandidates) == 0:
		return false
	default:
		return s.applyCandidatePoolMerge(ctx, req, templates)
	}
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

	// P21 multi-enable: candidate pool is the PRIMARY injection path. Every
	// row the user checked goes into the new thread's context via Claude
	// Code's claudeMd multi-source format (`Contents of <key>:\n<body>`
	// blocks). No LLM is involved — these are intentional, opted-in sources.
	//
	// The classifier below is an INDEPENDENT fallback: it handles ambiguous /
	// intent-less first turns where the pool is empty (i.e. the user has not
	// curated anything, so there is nothing to merge). The classifier and the
	// pool never run together — picking the pool means the user already chose
	// what to inject; classifier inference is redundant.
	if s.tryCandidatePoolMerge(ctx, req, templates) {
		return
	}

	// Classifier fallback: only runs when no explicit pin, no candidate pool,
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

// applyCandidatePoolMerge fans out every prompt template whose PromptKey sits
// in req.PromptCandidates into req.BaseInstructionBlocks so downstream cache
// layering (CachedPrefix / UncachedTail) stays intact. Templates migrated to
// prompt_template_sections contribute their sections verbatim (region-aware);
// legacy rows fall back to a single region=Dynamic block carrying the old
// `Contents of <key>:\n<body>` claudeMd multi-source format. Disabled rows
// and unknown keys are silently skipped. Returns true when at least one block
// was produced — caller should skip the classifier / default-fallback paths
// in that case.
func (s *service) applyCandidatePoolMerge(ctx context.Context, req *StartRequest, templates []promptstore.PromptTemplate) bool {
	order := normalizedPromptCandidateOrder(req.PromptCandidates)
	if len(order) == 0 {
		return false
	}
	blocks, mergedKeys, mergedTitles := s.mergeCandidatePoolTemplates(ctx, order, templates)
	if len(blocks) == 0 {
		return false
	}
	applyMergedCandidatePool(req, blocks, mergedKeys, mergedTitles)
	pkglogger.Info("router: candidate pool merged into base_instruction_blocks",
		"candidate_keys", mergedKeys,
		"block_count", len(blocks))
	return true
}

func normalizedPromptCandidateOrder(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, raw := range candidates {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		order = append(order, key)
	}
	return order
}

func enabledPromptTemplateIndex(templates []promptstore.PromptTemplate) map[string]*promptstore.PromptTemplate {
	byKey := make(map[string]*promptstore.PromptTemplate, len(templates))
	for i := range templates {
		t := &templates[i]
		if t.Enabled {
			byKey[t.PromptKey] = t
		}
	}
	return byKey
}

func (s *service) mergeCandidatePoolTemplates(
	ctx context.Context,
	order []string,
	templates []promptstore.PromptTemplate,
) ([]contract.BaseInstructionBlock, []string, []string) {
	byKey := enabledPromptTemplateIndex(templates)
	blocks := make([]contract.BaseInstructionBlock, 0, len(order))
	mergedKeys := make([]string, 0, len(order))
	mergedTitles := make([]string, 0, len(order))
	for _, key := range order {
		t, ok := byKey[key]
		if !ok {
			continue
		}
		templateBlocks := s.candidateTemplateBlocks(ctx, t)
		if len(templateBlocks) == 0 {
			continue
		}
		blocks = append(blocks, templateBlocks...)
		mergedKeys = append(mergedKeys, t.PromptKey)
		mergedTitles = append(mergedTitles, candidateTemplateTitle(t))
	}
	return blocks, mergedKeys, mergedTitles
}

func candidateTemplateTitle(t *promptstore.PromptTemplate) string {
	if title := strings.TrimSpace(t.Title); title != "" {
		return title
	}
	return t.PromptKey
}

func applyMergedCandidatePool(
	req *StartRequest,
	blocks []contract.BaseInstructionBlock,
	mergedKeys []string,
	mergedTitles []string,
) {
	req.BaseInstructions = ""
	req.BaseInstructionBlocks = blocks
	// Observability: no single prompt_key / prompt_version to stamp, but we
	// keep agent_key empty + AgentTitle = "候选池 (N)" so the UI badge logic
	// can distinguish pool-merge threads from default-fallback threads.
	req.AgentKey = ""
	req.AgentTitle = "候选池 · " + strconv.Itoa(len(mergedKeys)) + " 条"
	req.PromptKey = ""
	req.PromptVersionID = nil
	req.MergedCandidateKeys = mergedKeys
	req.MergedCandidateTitles = mergedTitles
}

// candidateTemplateBlocks returns the ordered BaseInstructionBlock slice that
// represents a single candidate template. Migrated templates emit one block
// per section (region-aware, ordinal-sorted); legacy templates collapse to a
// single region=Dynamic block carrying the old `Contents of <key>:\n<body>`
// format so the uncached tail keeps rendering what the UI already expects.
// A store-side error degrades to the legacy shape rather than dropping the
// template entirely — we'd rather inject something than nothing.
func (s *service) candidateTemplateBlocks(ctx context.Context, t *promptstore.PromptTemplate) []contract.BaseInstructionBlock {
	if t == nil {
		return nil
	}
	sections, err := s.promptStore.ListSectionsByTemplateID(ctx, t.ID)
	if err != nil {
		pkglogger.Warn("router: candidate pool list sections failed; using prompt_text",
			"err", err, "prompt_key", t.PromptKey, "template_id", t.ID)
	} else if len(sections) > 0 {
		converted := convertStoreSectionsToBlocks(sections)
		out := make([]contract.BaseInstructionBlock, 0, len(converted))
		for _, b := range converted {
			// Namespace the section key by the template PromptKey so the
			// debug Name ("tpl:<pkey>:<skey>") stays unique across multi-
			// template merges; assembler only uses it for traceability.
			b.Key = t.PromptKey + ":" + b.Key
			out = append(out, b)
		}
		return out
	}
	body := strings.TrimSpace(t.PromptText)
	if body == "" {
		return nil
	}
	return []contract.BaseInstructionBlock{{
		Key:    t.PromptKey,
		Region: contract.PromptRegionDynamic,
		Body:   "Contents of " + t.PromptKey + ":\n" + body,
	}}
}
