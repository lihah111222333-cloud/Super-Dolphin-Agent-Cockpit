package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	editpkg "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/edit"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
	memorymod "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
)

type applyWorkspaceEditResult struct {
	AppliedCount int
	LSPSync      bool
	Warning      string
}

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
	Success              bool   `json:"success"`
	Action               string `json:"action,omitempty"`
	Error                string `json:"error"`
	CurrentContent       string `json:"current_content,omitempty"`
	FuncStart            int    `json:"func_start,omitempty"`
	FuncEnd              int    `json:"func_end,omitempty"`
	FuncBody             string `json:"func_body,omitempty"`
	DiagnosticGeneration uint64 `json:"diagnostic_generation,omitempty"`
}

type functionContext struct {
	Start int
	End   int
	Body  string
}

type teamMemoryToolContext struct {
	guard    *memorymod.TeamMemoryGuard
	teamRoot string
}

func guardTeamMemoryWrite(path, content string) error {
	ctx, ok, err := resolveTeamMemoryToolContext(path)
	if err != nil || !ok {
		return err
	}
	_, err = ctx.guard.ValidateWrite(path, content)
	return err
}

func filterTeamMemoryBatchWrites(updated map[string]string) (map[string]string, string, error) {
	if len(updated) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]string, len(updated))
	grouped := make(map[string]map[string]string)
	contexts := make(map[string]teamMemoryToolContext)
	for _, path := range sortedKeys(updated) {
		ctx, ok, err := resolveTeamMemoryToolContext(path)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			allowed[path] = updated[path]
			continue
		}
		if _, exists := grouped[ctx.teamRoot]; !exists {
			grouped[ctx.teamRoot] = make(map[string]string)
			contexts[ctx.teamRoot] = ctx
		}
		grouped[ctx.teamRoot][path] = updated[path]
	}
	warnings := make([]string, 0, len(grouped))
	for _, teamRoot := range sortedKeys(grouped) {
		result := contexts[teamRoot].guard.FilterPushFiles(grouped[teamRoot])
		for path, content := range result.Allowed {
			allowed[path] = content
		}
		if warning := formatTeamMemorySkippedWarning(result.Skipped); warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if len(allowed) == 0 {
		allowed = nil
	}
	return allowed, strings.Join(warnings, "; "), nil
}

func resolveTeamMemoryToolContext(path string) (teamMemoryToolContext, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return teamMemoryToolContext{}, false, nil
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return teamMemoryToolContext{}, false, err
	}
	if !strings.EqualFold(filepath.Ext(absolutePath), ".md") {
		return teamMemoryToolContext{}, false, nil
	}
	teamRoot, ok := detectTeamMemoryRoot(absolutePath)
	if !ok {
		return teamMemoryToolContext{}, false, nil
	}
	cfg := memorymod.NewConfig(nil)
	cfg.ProjectRoot = filepath.Dir(teamRoot)
	ctx := teamMemoryToolContext{
		guard:    memorymod.NewTeamMemoryGuard(memorymod.NewTeamMemoryManager(cfg)),
		teamRoot: teamRoot,
	}
	return ctx, true, nil
}

func detectTeamMemoryRoot(path string) (string, bool) {
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "team" {
			projectRoot := filepath.Dir(dir)
			if projectRoot == dir || strings.TrimSpace(projectRoot) == "" {
				return "", false
			}
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
	}
}

func formatTeamMemorySkippedWarning(skipped []memorymod.TeamMemSkippedFile) string {
	if len(skipped) == 0 {
		return ""
	}
	paths := make([]string, 0, len(skipped))
	for _, item := range skipped {
		if path := strings.TrimSpace(item.Path); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return "skipped team memory files due to secret detection"
	}
	return fmt.Sprintf("skipped %d team memory file(s) due to secret detection: %s", len(paths), strings.Join(paths, ", "))
}

func (h EditHandler) handleReplaceRange(ctx context.Context, req EditRequest) (any, error) {
	path, err := resolveFilePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	file, err := readFileWithMode(path)
	if err != nil {
		return nil, err
	}
	manager, err := h.registry.GetManagerForFile(ctx, path)
	if err != nil {
		return nil, err
	}
	content := file.content
	plan, err := buildReplacePlan(content, req)
	if err != nil {
		return h.replaceFailure(ctx, manager, path, content, req.Line, err), nil
	}
	if plan.updatedContent == content {
		return replaceRangeResult{
			editEnvelope: editEnvelope{
				Success:              true,
				Action:               "replace_range",
				Status:               "no_change",
				Message:              "replacement did not change file content",
				Applied:              false,
				Persisted:            false,
				RequiresApply:        false,
				DiagnosticGeneration: manager.CurrentDiagnosticGeneration(),
			},
		}, nil
	}
	updatedContent := file.diskContent(plan.updatedContent)
	if err := guardTeamMemoryWrite(path, updatedContent); err != nil {
		return h.replaceFailure(ctx, manager, path, content, req.Line, err), nil
	}
	if err := os.WriteFile(path, []byte(updatedContent), file.mode); err != nil {
		return h.replaceFailure(ctx, manager, path, content, req.Line, err), nil
	}
	lspSync, warning, err := h.syncDocument(ctx, manager, path, updatedContent, normalizeEditVersion(req.Version))
	if err != nil {
		_ = os.WriteFile(path, []byte(file.raw), file.mode)
		return h.replaceFailure(ctx, manager, path, content, plan.functionLookupLine, err), nil
	}
	functionCtx := h.lookupFunctionContext(ctx, manager, path, plan.functionLookupLine, plan.updatedContent)
	return replaceRangeResult{
		editEnvelope: editEnvelope{
			Success:              true,
			Action:               "replace_range",
			Status:               "applied",
			Message:              "replacement applied",
			Applied:              true,
			AppliedCount:         1,
			Persisted:            true,
			RequiresApply:        false,
			LSPSync:              lspSync,
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
	}, nil
}

func (h EditHandler) replaceFailure(ctx context.Context, manager lspmanager.Manager, path string, content string, line int, err error) replaceRangeFailure {
	functionCtx := h.lookupFunctionContext(ctx, manager, path, line, content)
	return replaceRangeFailure{
		Success:              false,
		Action:               "replace_range",
		Error:                err.Error(),
		CurrentContent:       content,
		FuncStart:            functionCtx.Start,
		FuncEnd:              functionCtx.End,
		FuncBody:             functionCtx.Body,
		DiagnosticGeneration: manager.CurrentDiagnosticGeneration(),
	}
}

func (h EditHandler) applyWorkspaceEdit(ctx context.Context, manager lspmanager.Manager, workspaceEdit *protocol.WorkspaceEdit, version int) (applyWorkspaceEditResult, error) {
	files, err := collectWorkspaceEdits(workspaceEdit)
	if err != nil {
		return applyWorkspaceEditResult{}, err
	}
	originals := make(map[string]editableFile, len(files))
	updated := make(map[string]string, len(files))
	for _, path := range sortedKeys(files) {
		file, err := readFileWithMode(path)
		if err != nil {
			return applyWorkspaceEditResult{}, err
		}
		next, err := applyTextEdits(file.content, files[path])
		if err != nil {
			return applyWorkspaceEditResult{}, err
		}
		originals[path] = file
		if next != file.content {
			updated[path] = next
		}
	}
	if len(updated) == 0 {
		return applyWorkspaceEditResult{}, nil
	}
	guarded, guardWarning, err := filterTeamMemoryBatchWrites(updated)
	if err != nil {
		return applyWorkspaceEditResult{}, err
	}
	if len(guarded) == 0 {
		return applyWorkspaceEditResult{Warning: guardWarning}, nil
	}
	written := make(map[string]string, len(guarded))
	for _, path := range sortedKeys(guarded) {
		file := originals[path]
		written[path] = file.diskContent(guarded[path])
		if err := os.WriteFile(path, []byte(written[path]), file.mode); err != nil {
			return applyWorkspaceEditResult{}, withRollbackWarning(err, restoreFiles(originals, guarded))
		}
	}
	lspSync, warning, err := h.syncDocuments(ctx, manager, written, version)
	if err != nil {
		return applyWorkspaceEditResult{}, withRollbackWarning(err, restoreFiles(originals, guarded))
	}
	if guardWarning != "" && warning != "" {
		warning = guardWarning + "; " + warning
	} else if guardWarning != "" {
		warning = guardWarning
	}
	return applyWorkspaceEditResult{
		AppliedCount: len(guarded),
		LSPSync:      lspSync,
		Warning:      warning,
	}, nil
}

func (h EditHandler) syncDocuments(ctx context.Context, manager lspmanager.Manager, updated map[string]string, version int) (bool, string, error) {
	warnings := make([]string, 0, len(updated))
	for _, path := range sortedKeys(updated) {
		synced, warning, err := h.syncDocument(ctx, manager, path, updated[path], version)
		if err != nil {
			return false, strings.Join(warnings, "; "), err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if !synced {
			return false, strings.Join(warnings, "; "), nil
		}
	}
	return true, strings.Join(warnings, "; "), nil
}

func (h EditHandler) syncDocument(ctx context.Context, manager lspmanager.Manager, path string, content string, version int) (bool, string, error) {
	if editpkg.ShouldForceBypass(len(content)) {
		if err := manager.BootstrapDocumentOpenOnly(ctx, path); err != nil {
			return false, "", err
		}
		return true, "used bootstrap-only LSP sync", nil
	}
	if err := manager.BootstrapDocument(ctx, path); err != nil {
		return false, "", err
	}
	change := protocol.TextDocumentContentChangeEvent{Text: content}
	if err := manager.DidChange(ctx, path, version, []protocol.TextDocumentContentChangeEvent{change}); err != nil {
		return false, "", err
	}
	if countLines(content) > didChangeLargeFileLineThreshold {
		return true, "full document sync exceeded large-file line threshold", nil
	}
	return true, "", nil
}

func withRollbackWarning(err error, rollbackErr error) error {
	if err == nil {
		return rollbackErr
	}
	if rollbackErr == nil {
		return err
	}
	return fmt.Errorf("%w; rollback warning: %v", err, rollbackErr)
}

func (h EditHandler) lookupFunctionContext(ctx context.Context, manager lspmanager.Manager, path string, line int, content string) functionContext {
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

func buildReplacePlan(content string, req EditRequest) (replacePlan, error) {
	switch {
	case strings.TrimSpace(req.Patch) != "":
		return buildPatchReplacePlan(content, req.Patch)
	case len(req.Edits) > 0:
		return buildEditsReplacePlan(content, req.Edits)
	case strings.TrimSpace(req.NewText) != "":
		return buildRangeReplacePlan(content, req)
	default:
		return replacePlan{}, errors.New("replace_range requires patch, edits, or new_text")
	}
}

func buildPatchReplacePlan(content string, patch string) (replacePlan, error) {
	hunks, err := parsePatchHunks(patch)
	if err != nil {
		return replacePlan{}, err
	}
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

func buildEditsReplacePlan(content string, edits []ReplaceEdit) (replacePlan, error) {
	if len(edits) > editpkg.MaxReplaceRangeEdits {
		return replacePlan{}, fmt.Errorf("edits exceeds %d items", editpkg.MaxReplaceRangeEdits)
	}
	hunks := make([]editpkg.Hunk, 0, len(edits))
	for _, item := range edits {
		if item.OldString == "" {
			return replacePlan{}, errors.New("old_string is required for edits")
		}
		hunks = append(hunks, editpkg.Hunk{OldText: item.OldString, NewText: item.NewString})
	}
	return buildPatchReplacePlan(content, joinHunksAsPatch(hunks))
}

func buildRangeReplacePlan(content string, req EditRequest) (replacePlan, error) {
	start, err := requirePosition(req.Line, req.Column)
	if err != nil {
		return replacePlan{}, err
	}
	end := start
	if req.EndLine > 0 && req.EndColumn > 0 {
		end, err = requirePosition(req.EndLine, req.EndColumn)
		if err != nil {
			return replacePlan{}, err
		}
	}
	startOffset, err := offsetForPosition(content, start)
	if err != nil {
		return replacePlan{}, err
	}
	endOffset, err := offsetForPosition(content, end)
	if err != nil {
		return replacePlan{}, err
	}
	if err := editpkg.GuardContentAndReplacement(content, req.NewText); err != nil {
		return replacePlan{}, err
	}
	req.NewText = normalizeLineEndings(req.NewText)
	editContext, affectedStart, affectedEnd, err := editpkg.BuildEditContext(content, startOffset, endOffset, req.NewText)
	if err != nil {
		return replacePlan{}, err
	}
	line, err := editpkg.OffsetToLine(content, startOffset)
	if err != nil {
		return replacePlan{}, err
	}
	return replacePlan{
		updatedContent:     content[:startOffset] + req.NewText + content[endOffset:],
		matchedBy:          "coordinates",
		resolvedStart:      startOffset,
		resolvedEnd:        endOffset,
		resolvedLSPLine:    line,
		affectedStartLine:  affectedStart,
		affectedEndLine:    affectedEnd,
		editContext:        editContext,
		replaced:           content[startOffset:endOffset],
		replacement:        req.NewText,
		functionLookupLine: line,
	}, nil
}
