package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

func shouldSkipRoutedPrompt(s *service, req *StartRequest) bool {
	switch {
	case req == nil:
		return true
	case strings.TrimSpace(req.BaseInstructions) != "":
		pkglogger.Info("router: skip, base_instructions already set by caller",
			"agent_key", req.AgentKey, "base_instructions_len", len(req.BaseInstructions))
		return true
	case s == nil || s.runtimePromptCatalog() == nil:
		pkglogger.Info("router: skip, prompt catalog not wired")
		return true
	default:
		return false
	}
}

func (s *service) runtimePromptCatalog() contract.RuntimePromptCatalog {
	if s == nil {
		return nil
	}
	return s.promptCatalog
}

// resolveRoutedPrompt 解析routedprompt。
func (s *service) resolveRoutedPrompt(ctx context.Context, req *StartRequest) error {
	if shouldSkipRoutedPrompt(s, req) {
		return nil
	}
	trustedCWD := strings.TrimSpace(req.CWD)
	if trustedCWD == "" {
		return fmt.Errorf("invalid params: prompt routing requires trusted cwd")
	}

	// Read enabled candidates once. Limit is generous; rule matching is cheap
	// and list is unlikely to exceed a few hundred rows even in large orgs.
	catalog := s.runtimePromptCatalog()
	templates, err := catalog.ListTemplates(ctx, contract.PromptRuntimeListFilter{CWD: trustedCWD, Limit: 200})
	if err != nil {
		return fmt.Errorf("router: list prompt_templates: %w", err)
	}

	// match_when auto-route: between explicit pins and main/default fallback.
	// Runs only when neither PromptKey nor AgentKey was supplied. Picks the
	// highest-priority enabled template whose match_when rules satisfy the
	// current BuildCtx.
	s.maybeAutoRouteByMatchWhen(req, templates)

	picked, err := s.pickRoutedTemplate(ctx, req, templates)
	if err != nil {
		return err
	}
	if picked == nil || !picked.Enabled {
		pkglogger.Info("router: no prompt_template matched",
			"requested_agent_key", req.AgentKey,
			"candidate_count", len(templates))
		return nil
	}
	pkglogger.Info("router: prompt_template matched",
		"prompt_key", picked.PromptKey,
		"agent_key", picked.AgentKey,
		"candidate_count", len(templates),
		"prompt_text_len", len(picked.PromptText))
	return s.applyPickedRoutedTemplate(ctx, req, picked)
}

// applyPickedRoutedTemplate 应用pickedroutedtemplate。
func (s *service) applyPickedRoutedTemplate(
	ctx context.Context,
	req *StartRequest,
	picked *contract.PromptTemplate,
) error {
	versionPromptText, blocks, err := s.routedTemplateInstructions(ctx, req, picked)
	if err != nil {
		return err
	}
	// Materialize a prompt_versions snapshot so historical analyses can
	// reproduce the exact prompt text that was injected into this thread.
	catalog := s.runtimePromptCatalog()
	// Per Risk 1 (b): still record agent_key / prompt_key for observability
	// even if version materialization fails.
	req.AgentKey = picked.AgentKey
	req.AgentTitle = picked.Title
	req.PromptKey = picked.PromptKey
	if len(blocks) > 0 {
		req.BaseInstructionBlocks = blocks
		req.BaseInstructions = ""
	} else {
		req.BaseInstructions = versionPromptText
		req.BaseInstructionBlocks = nil
	}
	if !promptCatalogCanInsertVersion(catalog) {
		req.PromptVersionID = nil
		return nil
	}
	versionID, verr := catalog.InsertVersion(ctx, contract.PromptTemplateVersion{
		PromptKey:       picked.PromptKey,
		Title:           picked.Title,
		AgentKey:        picked.AgentKey,
		ToolName:        picked.ToolName,
		PromptText:      versionPromptText,
		Variables:       append(json.RawMessage(nil), picked.Variables...),
		Tags:            append(json.RawMessage(nil), picked.Tags...),
		Description:     picked.Description,
		Enabled:         picked.Enabled,
		CreatedBy:       picked.CreatedBy,
		UpdatedBy:       picked.UpdatedBy,
		SourceUpdatedAt: &picked.UpdatedAt,
	})
	if verr != nil {
		pkglogger.Warn("router: materialize prompt_versions failed",
			"err", verr, "prompt_key", picked.PromptKey)
		req.PromptVersionID = nil
		return fmt.Errorf("router: materialize prompt_versions for %q: %w", picked.PromptKey, verr)
	}
	req.PromptVersionID = &versionID
	return nil
}

type promptVersionInsertCapability interface {
	CanInsertPromptVersion() bool
}

func promptCatalogCanInsertVersion(catalog contract.RuntimePromptCatalog) bool {
	checker, ok := catalog.(promptVersionInsertCapability)
	return !ok || checker.CanInsertPromptVersion()
}

// routedTemplateInstructions 处理routedtemplateinstructions。
func (s *service) routedTemplateInstructions(ctx context.Context, req *StartRequest, picked *contract.PromptTemplate) (string, []contract.BaseInstructionBlock, error) {
	catalog := s.runtimePromptCatalog()
	sections, serr := catalog.ListSectionsByTemplateID(ctx, picked.ID)
	if serr != nil {
		return "", nil, fmt.Errorf("router: list prompt_template_sections for %q: %w", picked.PromptKey, serr)
	}
	if len(sections) == 0 {
		return picked.PromptText, nil, nil
	}
	blocks := convertStoreSectionsToBlocks(sections)
	if len(blocks) == 0 {
		return picked.PromptText, nil, nil
	}
	if req != nil {
		blocks = contract.PrepareBaseInstructionBlocks(blocks, buildStartCtx(*req, s.cfg, s.toolRegistry), req.Prompt, s.enableWhenEval)
	} else {
		blocks = contract.PrepareBaseInstructionBlocks(blocks, contract.BuildCtx{}, "", s.enableWhenEval)
	}
	if len(blocks) == 0 {
		return "", nil, nil
	}
	return contract.TextFromBaseInstructionBlocks(blocks), blocks, nil
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
// pickRoutedTemplate 处理pickroutedtemplate。
func (s *service) pickRoutedTemplate(
	_ context.Context,
	req *StartRequest,
	templates []contract.PromptTemplate,
) (*contract.PromptTemplate, error) {
	// Explicit prompt_key beats everything else: it's the most specific pin
	// the UI can give ("use this exact row"). If it doesn't resolve, refuse
	// to fall through — the user picked this row, silently substituting a
	// different one would be worse than leaving the request untouched and
	// letting the upstream CLI use its bundled system prompt.
	if pinned := strings.TrimSpace(req.PromptKey); pinned != "" {
		picked := findEnabledByPromptKey(templates, pinned)
		if picked != nil {
			if contract.IsRuntimeAssetPromptTemplate(*picked) {
				req.PromptKeyStale = true
				pkglogger.Warn("router: pinned prompt_key targets runtime asset template",
					"prompt_key", pinned)
				return nil, nil
			}
			req.AgentKey = picked.AgentKey
			req.AgentTitle = picked.Title
			return picked, nil
		}
		// Caller pinned a prompt_key that did not resolve to an enabled
		// row (deleted or disabled). Mark the request stale so newStartResult
		// can echo the signal to the UI, which then clears its launch-prompt
		// preference. We do NOT modify req.PromptKey — keeping the original
		// pin lets the UI compare against its own pref for safety.
		req.PromptKeyStale = true
		pkglogger.Warn("router: pinned prompt_key not found",
			"prompt_key", pinned,
			"candidate_count", len(templates))
		return nil, nil
	}
	if explicit := strings.TrimSpace(req.AgentKey); explicit != "" {
		picked := firstEnabledByAgentKey(templates, explicit)
		if picked != nil {
			req.AgentTitle = picked.Title
		}
		return picked, nil
	}
	picked := findByPromptKey(templates, defaultPromptKey)
	if picked != nil && promptTemplateLaunchable(*picked) {
		req.AgentKey = picked.AgentKey
		req.AgentTitle = picked.Title
		return picked, nil
	}
	return nil, nil
}

func findEnabledByPromptKey(templates []contract.PromptTemplate, promptKey string) *contract.PromptTemplate {
	picked := findByPromptKey(templates, promptKey)
	if picked == nil || !picked.Enabled {
		return nil
	}
	return picked
}

// convertStoreSectionsToBlocks maps injectable prompt_template_sections rows into
// the contract-layer BaseInstructionBlock shape consumed by assembler.go.
// Unknown region strings degrade to Dynamic (safer: blocks end up in the
// uncached tail rather than accidentally claiming cached-prefix slots).
// convertStoreSectionsToBlocks 把存储sections转换为blocks。
func convertStoreSectionsToBlocks(sections []contract.PromptTemplateSection) []contract.BaseInstructionBlock {
	if len(sections) == 0 {
		return nil
	}
	out := make([]contract.BaseInstructionBlock, 0, len(sections))
	for _, s := range sections {
		if !s.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s.TriggerType), "recall") {
			continue
		}
		if strings.TrimSpace(s.Body) == "" {
			continue
		}
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

func firstEnabledByAgentKey(templates []contract.PromptTemplate, agentKey string) *contract.PromptTemplate {
	want := strings.TrimSpace(agentKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if promptTemplateLaunchable(*t) && strings.EqualFold(strings.TrimSpace(t.AgentKey), want) {
			return t
		}
	}
	return nil
}

func promptTemplateLaunchable(template contract.PromptTemplate) bool {
	return template.Enabled && !contract.IsRuntimeAssetPromptTemplate(template)
}

func findByPromptKey(templates []contract.PromptTemplate, promptKey string) *contract.PromptTemplate {
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
// 候选池合并注入的设计已与"对齐 Claude 主线程单提示词"的目标冲突；
// 路由现在的三档优先级为：
//   1. 显式 pin (PromptKey / AgentKey) > 2. match_when 自动路由 >
//   3. main/default 兜底。

// maybeAutoRouteByMatchWhen fills req.PromptKey when no explicit pin was
// supplied. It evaluates auto-route candidates in two stages: first the
// "specific" pool (rows whose match_when has real filter conditions), then the
// "fallback" pool (rows whose match_when is `{}`, opt-in always-match). Each
// pool is sorted by Priority DESC (stable). Splitting the pools prevents a
// high-priority `{}` row (e.g. main/general-zh priority=160) from shadowing
// structured match_when prompts — the production bug that motivated this split.
// All failure modes leave req.PromptKey untouched so the main/default
// fallback still applies.
// maybeAutoRouteByMatchWhen 按matchwhen处理maybeautoroute。
func (s *service) maybeAutoRouteByMatchWhen(req *StartRequest, templates []contract.PromptTemplate) {
	if req == nil {
		return
	}
	if strings.TrimSpace(req.PromptKey) != "" {
		return
	}
	if strings.TrimSpace(req.AgentKey) != "" {
		return
	}
	if s.matchWhenEval == nil {
		return
	}
	specific, fallback := autoRouteCandidates(templates)
	if len(specific) == 0 && len(fallback) == 0 {
		return
	}
	buildCtx := buildMatchWhenCtx(req)
	userPrompt := req.Prompt
	if s.evaluateMatchWhenPool("specific", specific, len(fallback), buildCtx, userPrompt, req) {
		return
	}
	if s.evaluateMatchWhenPool("fallback", fallback, len(specific), buildCtx, userPrompt, req) {
		return
	}
	pkglogger.Info("router: match_when no auto-route hit",
		"specific_count", len(specific),
		"fallback_count", len(fallback))
}

// evaluateMatchWhenPool walks one match_when pool (specific or fallback) in
// the order the caller provided (already priority-DESC). On the first hit it
// stamps req.PromptKey and returns true; otherwise returns false so the
// caller can move on to the next stage. The peer-pool count is logged purely
// for observability so a single line tells operators which stage fired.
func (s *service) evaluateMatchWhenPool(
	stage string,
	pool []contract.PromptTemplate,
	peerCount int,
	buildCtx contract.BuildCtx,
	userPrompt string,
	req *StartRequest,
) bool {
	for i := range pool {
		cand := &pool[i]
		if !s.matchWhenEval(cand.MatchWhen, buildCtx, userPrompt) {
			continue
		}
		specificCount, fallbackCount := len(pool), peerCount
		if stage == "fallback" {
			specificCount, fallbackCount = peerCount, len(pool)
		}
		pkglogger.Info("router: match_when auto-routed",
			"stage", stage,
			"prompt_key", cand.PromptKey,
			"priority", cand.Priority,
			"specific_count", specificCount,
			"fallback_count", fallbackCount)
		req.PromptKey = cand.PromptKey
		return true
	}
	return false
}

// autoRouteCandidates partitions enabled match_when rows into two pools:
//   - specific: match_when decodes to a non-empty object (real filter rules)
//   - fallback: match_when decodes to the empty object `{}` (opt-in always-
//     match). nil / null / invalid JSON are dropped entirely because they
//     can never satisfy EvaluateMatchWhen anyway — keeping them in the pool
//     would just be wasted iteration.
//
// Each pool is sorted by Priority DESC (stable). The caller evaluates
// specific first, then fallback, so a low-priority prompt with real structured
// match rules wins over a high-priority `{}` row.
// autoRouteCandidates 处理autoroute候选项。
func autoRouteCandidates(templates []contract.PromptTemplate) (specific, fallback []contract.PromptTemplate) {
	specific = make([]contract.PromptTemplate, 0, len(templates))
	fallback = make([]contract.PromptTemplate, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if !promptTemplateLaunchable(*t) {
			continue
		}
		if len(t.MatchWhen) == 0 {
			continue
		}
		if isFallbackMatchWhen(t.MatchWhen) {
			fallback = append(fallback, *t)
			continue
		}
		if hasSpecificMatchWhen(t.MatchWhen) {
			specific = append(specific, *t)
		}
	}
	sortByPriorityDesc(specific)
	sortByPriorityDesc(fallback)
	return specific, fallback
}

// isFallbackMatchWhen reports whether the raw JSON is the empty object `{}`.
// nil / null / non-object / invalid JSON return false: only a real `{}` body
// counts as the opt-in always-match fallback bucket. The nil check guards
// against jsonb `null` decoding to a nil map and being mistaken for `{}`.
func isFallbackMatchWhen(raw []byte) bool {
	var expr map[string]any
	if err := json.Unmarshal(raw, &expr); err != nil {
		return false
	}
	return expr != nil && len(expr) == 0
}

// hasSpecificMatchWhen reports whether the raw JSON decodes to a non-empty
// object with at least one filter key. Used to filter the specific pool —
// anything else (null, [], string, number, invalid) is dropped because
// EvaluateMatchWhen will not accept it anyway.
func hasSpecificMatchWhen(raw []byte) bool {
	var expr map[string]any
	if err := json.Unmarshal(raw, &expr); err != nil {
		return false
	}
	return len(expr) > 0
}

// sortByPriorityDesc sorts the rows in-place by Priority descending with
// stable secondary order (insertion order from the store).
func sortByPriorityDesc(rows []contract.PromptTemplate) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Priority > rows[j].Priority
	})
}

// buildMatchWhenCtx synthesizes a lightweight BuildCtx for router-phase
// evaluation. Unlike buildStartCtx it does not touch config / registry but it
// DOES resolve req.CWD through resolvePromptCWD so cwd_prefix / cwd_glob
// rules actually compare against an absolute path — the UI commonly sends
// req.CWD="." and a raw strings.HasPrefix(".", "/Users/...") would never hit.
func buildMatchWhenCtx(req *StartRequest) contract.BuildCtx {
	return contract.BuildCtx{
		CWD:          resolvePromptCWD(req.CWD),
		GitRoot:      strings.TrimSpace(req.GitRoot),
		IsWorktree:   req.IsWorktree,
		Language:     strings.TrimSpace(req.Language),
		Provider:     strings.TrimSpace(req.Provider),
		Model:        strings.TrimSpace(req.Model),
		SessionFlags: req.SessionFlags,
	}
}
