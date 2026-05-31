package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	editpkg "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/edit"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
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

// ToPlainText extends editEnvelope.ToPlainText with the edit-context
// window and (only for relaxed match modes) a verification nudge. The
// raw replaced/replacement bodies stay in structuredContent for any
// diff-aware UI but no longer bloat the LLM-facing channel.
func (r replaceRangeResult) ToPlainText() string {
	base := r.editEnvelope.ToPlainText()
	if !r.Success || !r.Applied {
		return base
	}
	context := strings.TrimSpace(r.EditContext)
	if context == "" {
		return base
	}
	return base + "\n\nEdit context:\n" + context
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
		fmt.Fprintf(sb, "Next step: file action=read_file file_path=%s offset=1 limit=%d (file has %d lines; narrow the window with a smaller limit)\n",
			r.FilePath, minInt(r.LineCount, 200), r.LineCount)
		return
	}
	fmt.Fprintf(sb, "Next step: file action=read_file file_path=%s\n", r.FilePath)
}

func (h EditHandler) handleReplaceRange(ctx context.Context, req EditRequest) (any, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePathInRoots(root, roots, req.FilePath)
	if err != nil {
		return nil, err
	}
	unlock := lockEditFile(path)
	defer unlock()
	file, err := readFileWithMode(path)
	if err != nil {
		return nil, err
	}
	manager, managerWarning, err := h.replaceRangeManager(ctx, path, req.LanguageID)
	if err != nil {
		return nil, err
	}
	content := file.content
	plan, err := buildReplacePlan(content, req)
	if err != nil {
		return h.replaceFailure(ctx, manager, path, content, 0, err), err
	}
	if plan.updatedContent == content {
		return h.buildNoChangeResult(manager, path, managerWarning), nil
	}
	updatedContent := file.diskContent(plan.updatedContent)
	warning := managerWarning
	lspSync, syncWarning, err := h.applyReplaceRangeUpdate(ctx, manager, path, file, updatedContent, normalizeEditVersion(req.Version))
	if err != nil {
		return h.replaceFailure(ctx, manager, path, content, plan.functionLookupLine, err), err
	}
	if syncWarning != "" {
		warning = syncWarning
	}
	functionCtx := h.lookupFunctionContext(ctx, manager, path, plan.functionLookupLine, plan.updatedContent)
	return h.buildAppliedResult(path, plan, lspSync, warning, functionCtx), nil
}

func (h EditHandler) buildNoChangeResult(manager lspmanager.Manager, path string, warning string) replaceRangeResult {
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Success:              true,
			Status:               "no_change",
			Message:              "replacement did not change file content",
			Applied:              false,
			Persisted:            false,
			RequiresApply:        false,
			FilePath:             path,
			Warning:              warning,
			DiagnosticGeneration: managerDiagnosticGeneration(manager),
		},
	}
}

func (h EditHandler) buildAppliedResult(path string, plan replacePlan, lspSync bool, warning string, functionCtx functionContext) replaceRangeResult {
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Success:              true,
			Status:               "applied",
			Message:              "replacement applied",
			Applied:              true,
			AppliedCount:         1,
			Persisted:            true,
			RequiresApply:        false,
			LSPSync:              lspSync,
			MatchedBy:            plan.matchedBy,
			FilePath:             path,
			AffectedStartLine:    plan.affectedStartLine,
			AffectedEndLine:      plan.affectedEndLine,
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

func (h EditHandler) applyReplaceRangeUpdate(ctx context.Context, manager lspmanager.Manager, path string, file editableFile, updatedContent string, version int) (bool, string, error) {
	if err := os.WriteFile(path, []byte(updatedContent), file.mode); err != nil {
		return false, "", err
	}
	if manager == nil {
		return false, "", nil
	}
	lspSync, warning, err := h.syncDocument(ctx, manager, path, updatedContent, version)
	if err == nil {
		return lspSync, warning, nil
	}
	rollbackErr := os.WriteFile(path, []byte(file.raw), file.mode)
	if rollbackErr == nil {
		rollbackErr = h.syncRollbackDocument(ctx, manager, path, file.raw, version)
	}
	return false, "", withRollbackError(err, rollbackErr)
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

func (h EditHandler) replaceFailure(ctx context.Context, manager lspmanager.Manager, path string, content string, line int, err error) replaceRangeFailure {
	functionCtx := h.lookupFunctionContext(ctx, manager, path, line, content)
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
	written, err := readFileWithMode(path)
	if err != nil {
		return false, "", err
	}
	content = written.raw
	if editpkg.ShouldForceBypass(len(content)) {
		if err := manager.BootstrapDocumentOpenOnly(ctx, path); err != nil {
			return false, "", err
		}
		return true, "used bootstrap-only LSP sync", nil
	}
	if err := manager.BootstrapDocument(ctx, path); err != nil {
		return false, "", err
	}
	synced, err := readFileWithMode(path)
	if err != nil {
		return false, "", err
	}
	content = synced.raw
	change := protocol.TextDocumentContentChangeEvent{Text: content}
	if err := manager.DidChange(ctx, path, version, []protocol.TextDocumentContentChangeEvent{change}); err != nil {
		return false, "", err
	}
	if countLines(content) > didChangeLargeFileLineThreshold {
		return true, "full document sync exceeded large-file line threshold", nil
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

func (h EditHandler) lookupFunctionContext(ctx context.Context, manager lspmanager.Manager, path string, line int, content string) functionContext {
	if manager == nil {
		return functionContext{}
	}
	if line <= 0 {
		return functionContext{}
	}
	symbols, err := manager.DocumentSymbol(ctx, path)
	if err != nil {
		return functionContext{}
	}
	start, end, ok := format.FindEnclosingFunction(symbols, line-1)
	if !ok {
		return functionContext{}
	}
	body := functionBody(content, start, end)
	return functionContext{Start: start, End: end, Body: body}
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
