package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	editpkg "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/edit"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
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

func (h EditHandler) handleReplaceRange(ctx context.Context, req EditRequest) (any, error) {
	path, err := resolveFilePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	file, err := readFileWithMode(path)
	if err != nil {
		return nil, err
	}
	content := file.content
	plan, err := buildReplacePlan(content, req)
	if err != nil {
		return h.replaceFailure(ctx, path, content, req.Line, err), nil
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
				DiagnosticGeneration: h.manager.CurrentDiagnosticGeneration(),
			},
		}, nil
	}
	updatedContent := file.diskContent(plan.updatedContent)
	if err := os.WriteFile(path, []byte(updatedContent), file.mode); err != nil {
		return h.replaceFailure(ctx, path, content, req.Line, err), nil
	}
	lspSync, warning, err := h.syncDocument(ctx, path, updatedContent, normalizeEditVersion(req.Version))
	if err != nil {
		_ = os.WriteFile(path, []byte(file.raw), file.mode)
		return h.replaceFailure(ctx, path, content, plan.functionLookupLine, err), nil
	}
	functionCtx := h.lookupFunctionContext(ctx, path, plan.functionLookupLine, plan.updatedContent)
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
			DiagnosticGeneration: h.manager.CurrentDiagnosticGeneration(),
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

func (h EditHandler) replaceFailure(ctx context.Context, path string, content string, line int, err error) replaceRangeFailure {
	functionCtx := h.lookupFunctionContext(ctx, path, line, content)
	return replaceRangeFailure{
		Success:              false,
		Action:               "replace_range",
		Error:                err.Error(),
		CurrentContent:       content,
		FuncStart:            functionCtx.Start,
		FuncEnd:              functionCtx.End,
		FuncBody:             functionCtx.Body,
		DiagnosticGeneration: h.manager.CurrentDiagnosticGeneration(),
	}
}

func (h EditHandler) applyWorkspaceEdit(ctx context.Context, workspaceEdit *protocol.WorkspaceEdit, version int) (applyWorkspaceEditResult, error) {
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
	written := make(map[string]string, len(updated))
	for _, path := range sortedKeys(updated) {
		file := originals[path]
		written[path] = file.diskContent(updated[path])
		if err := os.WriteFile(path, []byte(written[path]), file.mode); err != nil {
			return applyWorkspaceEditResult{}, withRollbackWarning(err, restoreFiles(originals, updated))
		}
	}
	lspSync, warning, err := h.syncDocuments(ctx, written, version)
	if err != nil {
		return applyWorkspaceEditResult{}, withRollbackWarning(err, restoreFiles(originals, updated))
	}
	return applyWorkspaceEditResult{
		AppliedCount: len(updated),
		LSPSync:      lspSync,
		Warning:      warning,
	}, nil
}

func (h EditHandler) syncDocuments(ctx context.Context, updated map[string]string, version int) (bool, string, error) {
	warnings := make([]string, 0, len(updated))
	for _, path := range sortedKeys(updated) {
		synced, warning, err := h.syncDocument(ctx, path, updated[path], version)
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

func (h EditHandler) syncDocument(ctx context.Context, path string, content string, version int) (bool, string, error) {
	if editpkg.ShouldForceBypass(len(content)) {
		if err := h.manager.BootstrapDocumentOpenOnly(ctx, path); err != nil {
			return false, "", err
		}
		return true, "used bootstrap-only LSP sync", nil
	}
	if err := h.manager.BootstrapDocument(ctx, path); err != nil {
		return false, "", err
	}
	change := protocol.TextDocumentContentChangeEvent{Text: content}
	if err := h.manager.DidChange(ctx, path, version, []protocol.TextDocumentContentChangeEvent{change}); err != nil {
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

func (h EditHandler) lookupFunctionContext(ctx context.Context, path string, line int, content string) functionContext {
	if line <= 0 {
		return functionContext{}
	}
	symbols, err := h.manager.DocumentSymbol(ctx, path)
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
