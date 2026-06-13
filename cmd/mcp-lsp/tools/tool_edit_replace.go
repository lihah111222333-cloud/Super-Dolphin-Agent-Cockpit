package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	editpkg "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/edit"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

const (
	editDiskConfirmTimeout    = 750 * time.Millisecond
	editFunctionLookupTimeout = 500 * time.Millisecond
	editLSPSyncTimeout        = 2 * time.Second
	editRecoveryLogFile       = "mcp-lsp-edit-recovery.jsonl"
)

type replacePlan struct {
	updatedContent     string
	matchedBy          string
	resolvedStart      int
	resolvedEnd        int
	resolvedLSPLine    int
	affectedStartLine  int
	affectedEndLine    int
	editContext        string
	replaced           string
	replacement        string
	functionLookupLine int
}

type replaceRangeResult struct {
	editEnvelope
	MatchedBy         string `json:"matched_by,omitempty"`
	ResolvedStart     int    `json:"resolved_start_offset,omitempty"`
	ResolvedEnd       int    `json:"resolved_end_offset,omitempty"`
	ResolvedLSPLine   int    `json:"resolved_lsp_line,omitempty"`
	AffectedStartLine int    `json:"affected_start_line,omitempty"`
	AffectedEndLine   int    `json:"affected_end_line,omitempty"`
	EditContext       string `json:"edit_context,omitempty"`
	Replaced          string `json:"replaced,omitempty"`
	Replacement       string `json:"replacement,omitempty"`
	ReplacedLen       int    `json:"replaced_len,omitempty"`
	ReplacementLen    int    `json:"replacement_len,omitempty"`
	FuncStart         int    `json:"func_start,omitempty"`
	FuncEnd           int    `json:"func_end,omitempty"`
	FuncBody          string `json:"func_body,omitempty"`
}

type replaceRangeFailure struct {
	Success              bool           `json:"success"`
	Error                string         `json:"error"`
	Code                 string         `json:"code,omitempty"`
	Retryable            bool           `json:"retryable,omitempty"`
	Hint                 string         `json:"hint,omitempty"`
	Meta                 map[string]any `json:"meta,omitempty"`
	FilePath             string         `json:"file_path,omitempty"`
	LineCount            int            `json:"line_count,omitempty"`
	FuncStart            int            `json:"func_start,omitempty"`
	FuncEnd              int            `json:"func_end,omitempty"`
	FuncBody             string         `json:"func_body,omitempty"`
	DiagnosticGeneration uint64         `json:"diagnostic_generation,omitempty"`
}

type functionContext struct {
	Start int
	End   int
	Body  string
}

type replaceSyncResult struct {
	lspSync bool
	warning string
	err     error
}

type editDiskConfirmResult struct {
	confirmed bool
	warning   string
	diffBytes int
	err       error
}

type replaceUpdateDecision struct {
	lspSync bool
	warning string
	err     error
}

// ToPlainText extends editEnvelope.ToPlainText with the edit-context
// window and (only for relaxed match modes) a verification nudge. The
// raw replaced/replacement bodies stay in structuredContent for any
// diff-aware UI but no longer bloat the LLM-facing channel.
func (r replaceRangeResult) ToPlainText() string {
	base := r.editEnvelope.ToPlainText()
	if !strings.EqualFold(strings.TrimSpace(r.Status), "applied") {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	if r.AffectedStartLine > 0 && r.FilePath != "" {
		fmt.Fprintf(&sb, "\nApplied at: %s:%d-L%d\n", r.FilePath, r.AffectedStartLine, r.AffectedEndLine)
	} else {
		sb.WriteByte('\n')
	}
	appendMatchedByNotice(&sb, r.MatchedBy)
	context := strings.TrimSpace(r.EditContext)
	if context == "" {
		return strings.TrimSpace(sb.String())
	}
	return strings.TrimSpace(sb.String()) + "\n\nEdit context:\n" + context
}

func (r replaceRangeFailure) ToPlainText() string {
	var sb strings.Builder
	header := "Tool error in \"edit\""
	if r.Code != "" {
		fmt.Fprintf(&sb, "%s [%s]: %s\n", header, r.Code, strings.TrimSpace(r.Error))
	} else {
		fmt.Fprintf(&sb, "%s: %s\n", header, strings.TrimSpace(r.Error))
	}
	if hint := strings.TrimSpace(r.Hint); hint != "" {
		fmt.Fprintf(&sb, "Hint: %s\n", hint)
	}
	if r.Retryable {
		sb.WriteString("Retryable: yes\n")
	}
	appendCandidateLocations(&sb, r.Meta)
	appendFailureNextStep(&sb, r)
	return strings.TrimSpace(sb.String())
}

func appendCandidateLocations(sb *strings.Builder, meta map[string]any) {
	cands, ok := meta["candidate_locations"].([]string)
	if !ok || len(cands) == 0 {
		return
	}
	sb.WriteString("Candidate locations:\n")
	for _, entry := range cands {
		fmt.Fprintf(sb, "  - %s\n", entry)
	}
}

// appendFailureNextStep gives the model a copy-pasteable file.read_file
// call instead of dumping the whole file. We don't try to compute the
// correct offset here (the patch text already tells the model what line
// it expected); we just point at the file with the known length so the
// model can pick a window itself.
func appendFailureNextStep(sb *strings.Builder, r replaceRangeFailure) {
	if r.FilePath == "" {
		return
	}
	if r.LineCount > 0 {
		fmt.Fprintf(sb, "next: file action=read_file pos=%s:1 limit=%d (file has %d lines; narrow the window with a smaller limit)\n",
			r.FilePath, minInt(r.LineCount, 200), r.LineCount)
		return
	}
	fmt.Fprintf(sb, "next: file action=read_file pos=%s\n", r.FilePath)
}

func (h EditHandler) handleReplaceRange(ctx context.Context, req EditRequest) (any, error) {
	log := newEditStageLogger("replace_range", req.FilePath)
	stage := log.Started("workspace_roots")
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		log.Failed("workspace_roots", stage, err)
		return nil, err
	}
	log.Completed("workspace_roots", stage, "root", root, "roots_count", len(roots))

	stage = log.Started("resolve_path")
	path, err := resolveWorkspacePathInRoots(root, roots, req.FilePath)
	if err != nil {
		log.Failed("resolve_path", stage, err)
		return nil, err
	}
	log.setFilePath(path)
	log.Completed("resolve_path", stage)

	stage = log.Started("file_lock")
	unlock := lockEditFile(path)
	log.Completed("file_lock", stage)
	defer func() {
		unlock()
		log.Completed("file_unlock", time.Now())
	}()

	stage = log.Started("read_file")
	file, err := readFileWithMode(path)
	if err != nil {
		log.Failed("read_file", stage, err)
		return nil, err
	}
	log.Completed("read_file", stage, "content_bytes", len(file.content), "raw_bytes", len(file.raw))

	stage = log.Started("manager_lookup", "language_id", req.LanguageID)
	manager, managerWarning, err := h.replaceRangeManager(ctx, path, req.LanguageID)
	if err != nil {
		log.Failed("manager_lookup", stage, err)
		return nil, err
	}
	log.Completed("manager_lookup", stage, "manager_available", manager != nil, "warning", managerWarning != "")

	content := file.content
	stage = log.Started("build_plan", "content_bytes", len(content), "patch_bytes", len(req.Patch))
	plan, err := buildReplacePlan(content, req)
	if err != nil {
		log.Failed("build_plan", stage, err)
		return h.replaceFailure(ctx, manager, path, content, 0, err, log), err
	}
	log.Completed("build_plan", stage,
		"matched_by", plan.matchedBy,
		"resolved_lsp_line", plan.resolvedLSPLine,
		"affected_start_line", plan.affectedStartLine,
		"affected_end_line", plan.affectedEndLine,
		"replaced_bytes", len(plan.replaced),
		"replacement_bytes", len(plan.replacement),
	)
	if plan.updatedContent == content {
		log.Skipped("apply_update", "no_change")
		return h.buildNoChangeResult(manager, path, managerWarning), nil
	}
	updatedContent := file.diskContent(plan.updatedContent)
	warning := managerWarning
	version := normalizeEditVersion(req.Version)
	stage = log.Started("apply_update", "version", version, "updated_bytes", len(updatedContent))
	lspSync, syncWarning, err := h.applyReplaceRangeUpdate(ctx, manager, path, file, updatedContent, version, log)
	if err != nil {
		log.Failed("apply_update", stage, err)
		return h.replaceFailure(ctx, manager, path, content, plan.functionLookupLine, err, log), err
	}
	log.Completed("apply_update", stage, "lsp_sync", lspSync, "sync_warning", syncWarning != "")
	if syncWarning != "" {
		warning = syncWarning
	}
	functionCtx := h.lookupFunctionContextWithLog(ctx, manager, path, plan.functionLookupLine, plan.updatedContent, log)
	log.Completed("replace_range", log.started, "result_status", "applied")
	return h.buildAppliedResult(path, plan, lspSync, warning, functionCtx), nil
}

func (h EditHandler) buildNoChangeResult(manager lspmanager.Manager, path string, warning string) replaceRangeResult {
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Status:               "no_change",
			Message:              "replacement did not change file content",
			Persisted:            false,
			FilePath:             path,
			Warning:              warning,
			DiagnosticGeneration: managerDiagnosticGeneration(manager),
		},
	}
}

func (h EditHandler) buildAppliedResult(path string, plan replacePlan, lspSync bool, warning string, functionCtx functionContext) replaceRangeResult {
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Status:               "applied",
			Message:              "replacement applied",
			AppliedCount:         1,
			Persisted:            true,
			LSPSync:              lspSync,
			FilePath:             path,
			Warning:              warning,
			DiagnosticGeneration: h.registry.CurrentDiagnosticGeneration(),
		},
		MatchedBy:         plan.matchedBy,
		ResolvedStart:     plan.resolvedStart,
		ResolvedEnd:       plan.resolvedEnd,
		ResolvedLSPLine:   plan.resolvedLSPLine,
		AffectedStartLine: plan.affectedStartLine,
		AffectedEndLine:   plan.affectedEndLine,
		EditContext:       plan.editContext,
		Replaced:          plan.replaced,
		Replacement:       plan.replacement,
		ReplacedLen:       len(plan.replaced),
		ReplacementLen:    len(plan.replacement),
		FuncStart:         functionCtx.Start,
		FuncEnd:           functionCtx.End,
		FuncBody:          functionCtx.Body,
	}
}

func (h EditHandler) replaceRangeManager(ctx context.Context, path string, languageID string) (lspmanager.Manager, string, error) {
	manager, err := managerForFile(ctx, h.registry, path, languageID)
	if err == nil {
		return manager, "", nil
	}
	if normalizeLanguageIDOverride(languageID) == "" && errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		return nil, fmt.Sprintf("LSP sync skipped: %v", err), nil
	}
	return nil, "", err
}

func (h EditHandler) replaceFailure(ctx context.Context, manager lspmanager.Manager, path string, content string, line int, err error, log *editStageLogger) replaceRangeFailure {
	functionCtx := h.lookupFunctionContextWithLog(ctx, manager, path, line, content, log)
	envelope := newToolErrorEnvelope("edit", "", err)
	meta := envelope.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	if ambig := asAmbiguousMatchError(err); ambig != nil {
		meta["candidate_locations"] = candidateLocationStrings(path, ambig.Candidates)
		meta["hunk_index"] = ambig.HunkIndex + 1
	}
	return replaceRangeFailure{
		Success:              false,
		Error:                err.Error(),
		Code:                 envelope.Code,
		Retryable:            envelope.Retryable,
		Hint:                 envelope.Hint,
		Meta:                 meta,
		FilePath:             path,
		LineCount:            countLines(content),
		FuncStart:            functionCtx.Start,
		FuncEnd:              functionCtx.End,
		FuncBody:             functionCtx.Body,
		DiagnosticGeneration: managerDiagnosticGeneration(manager),
	}
}

func asAmbiguousMatchError(err error) *editpkg.AmbiguousMatchError {
	var ambig *editpkg.AmbiguousMatchError
	if errors.As(err, &ambig) {
		return ambig
	}
	return nil
}

// candidateLocationStrings turns the typed match candidates into
// path:line:col-friendly strings the LLM can lift into a follow-up
// read_file or pinpoint a context line near. The format mirrors the
// rest of the LSP suite's plain-text output ("path:line" + optional
// match-mode tag) so the model needs no extra formatting rules.
func candidateLocationStrings(path string, candidates []editpkg.CandidateLocation) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		entry := fmt.Sprintf("%s:%d-L%d", path, candidate.StartLine, candidate.EndLine)
		if candidate.MatchedBy != "" && candidate.MatchedBy != "exact" {
			entry += " [" + candidate.MatchedBy + "]"
		}
		out = append(out, entry)
	}
	return out
}

func (h EditHandler) syncRollbackDocument(ctx context.Context, manager lspmanager.Manager, path string, content string, version int) error {
	if manager == nil {
		return nil
	}
	_, _, err := h.syncDocument(ctx, manager, path, content, nextEditVersion(version))
	return err
}

func (h EditHandler) syncDocument(ctx context.Context, manager lspmanager.Manager, path string, content string, version int) (bool, string, error) {
	if editpkg.ShouldForceBypass(len(content)) {
		if err := manager.BootstrapDocumentOpenOnly(ctx, path); err != nil {
			return false, "", err
		}
		return true, "used bootstrap-only LSP sync", nil
	}
	change := protocol.TextDocumentContentChangeEvent{Text: content}
	if err := manager.DidChange(ctx, path, version, []protocol.TextDocumentContentChangeEvent{change}); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func withRollbackError(err error, rollbackErr error) error {
	if err == nil {
		return rollbackErr
	}
	if rollbackErr == nil {
		return err
	}
	return fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
}

func nextEditVersion(version int) int {
	if version <= 0 {
		return defaultEditVersion + 1
	}
	return version + 1
}

func managerDiagnosticGeneration(manager lspmanager.Manager) uint64 {
	if manager == nil {
		return 0
	}
	return manager.CurrentDiagnosticGeneration()
}

func buildReplacePlan(content string, req EditRequest) (replacePlan, error) {
	if strings.TrimSpace(req.Patch) == "" {
		return replacePlan{}, errors.New("edit requires patch")
	}
	return buildPatchReplacePlan(content, req.Patch)
}

func buildPatchReplacePlan(content string, patch string) (replacePlan, error) {
	hunks, err := parsePatchHunks(patch)
	if err != nil {
		return replacePlan{}, err
	}
	return buildHunksReplacePlan(content, hunks)
}

func buildHunksReplacePlan(content string, hunks []editpkg.Hunk) (replacePlan, error) {
	matches, err := editpkg.MatchContext(content, hunks)
	if err != nil {
		return replacePlan{}, err
	}
	updated := content
	contexts := make([]string, 0, len(matches))
	modes := make([]string, 0, len(matches))
	for idx := range hunks {
		match := matches[idx]
		hunk := hunks[idx]
		modes = append(modes, resolveMatchMode(updated, hunk, match.MatchedBy))
		contexts = append(contexts, match.EditContext)
		updated = updated[:match.ResolvedStartOffset] + hunk.NewText + updated[match.ResolvedEndOffset:]
	}
	first := matches[0]
	last := matches[len(matches)-1]
	return replacePlan{
		updatedContent:     updated,
		matchedBy:          strings.Join(uniqueStrings(modes), ","),
		resolvedStart:      first.ResolvedStartOffset,
		resolvedEnd:        last.ResolvedEndOffset,
		resolvedLSPLine:    first.ResolvedLSPLine,
		affectedStartLine:  first.AffectedStartLine,
		affectedEndLine:    last.AffectedEndLine,
		editContext:        strings.Join(contexts, "\n\n"),
		replaced:           joinHunkOldText(hunks),
		replacement:        joinHunkNewText(hunks),
		functionLookupLine: first.ResolvedLSPLine,
	}, nil
}
