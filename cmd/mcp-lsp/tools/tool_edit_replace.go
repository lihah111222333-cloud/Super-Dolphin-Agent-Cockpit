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
	Success              bool           `json:"success"`
	Action               string         `json:"action,omitempty"`
	Error                string         `json:"error"`
	Code                 string         `json:"code,omitempty"`
	Retryable            bool           `json:"retryable,omitempty"`
	Hint                 string         `json:"hint,omitempty"`
	Meta                 map[string]any `json:"meta,omitempty"`
	CurrentContent       string         `json:"current_content,omitempty"`
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
				Warning:              managerWarning,
				DiagnosticGeneration: managerDiagnosticGeneration(manager),
			},
		}, nil
	}
	updatedContent := file.diskContent(plan.updatedContent)
	warning := managerWarning
	lspSync, syncWarning, err := h.applyReplaceRangeUpdate(ctx, manager, path, file, updatedContent, normalizeEditVersion(req.Version))
	if err != nil {
		return h.replaceFailure(ctx, manager, path, content, plan.functionLookupLine, err), nil
	}
	if syncWarning != "" {
		warning = syncWarning
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
	envelope := newToolErrorEnvelope("lsp_edit", "", err)
	return replaceRangeFailure{
		Success:              false,
		Action:               "replace_range",
		Error:                err.Error(),
		Code:                 envelope.Code,
		Retryable:            envelope.Retryable,
		Hint:                 envelope.Hint,
		Meta:                 envelope.Meta,
		CurrentContent:       content,
		FuncStart:            functionCtx.Start,
		FuncEnd:              functionCtx.End,
		FuncBody:             functionCtx.Body,
		DiagnosticGeneration: managerDiagnosticGeneration(manager),
	}
}

func (h EditHandler) applyWorkspaceEdit(ctx context.Context, manager lspmanager.Manager, workspaceEdit *protocol.WorkspaceEdit, version int) (applyWorkspaceEditResult, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return applyWorkspaceEditResult{}, err
	}
	files, err := collectWorkspaceEditsInRoots(root, roots, workspaceEdit)
	if err != nil {
		return applyWorkspaceEditResult{}, err
	}
	unlock := lockEditFiles(sortedKeys(files))
	defer unlock()
	originals, updated, err := loadWorkspaceEditUpdatesFromFiles(files)
	if err != nil {
		return applyWorkspaceEditResult{}, err
	}
	if len(updated) == 0 {
		return applyWorkspaceEditResult{}, nil
	}
	written, err := writeWorkspaceEditFiles(originals, updated)
	if err != nil {
		return applyWorkspaceEditResult{}, withRollbackError(err, restoreFiles(originals, updated))
	}
	version = normalizeEditVersion(version)
	lspSync, warning, err := h.syncDocuments(ctx, manager, written, version)
	if err != nil {
		rollbackErr := restoreFiles(originals, updated)
		if rollbackErr == nil {
			rollbackErr = h.syncRollbackDocuments(ctx, manager, originals, written, version)
		}
		return applyWorkspaceEditResult{}, withRollbackError(err, rollbackErr)
	}
	return applyWorkspaceEditResult{
		AppliedCount: len(updated),
		LSPSync:      lspSync,
		Warning:      warning,
	}, nil
}

func validateWorkspaceEditPaths(root string, workspaceEdit *protocol.WorkspaceEdit) error {
	_, err := collectWorkspaceEdits(root, workspaceEdit)
	return err
}

func validateWorkspaceEditPathsInRoots(root string, roots []string, workspaceEdit *protocol.WorkspaceEdit) error {
	_, err := collectWorkspaceEditsInRoots(root, roots, workspaceEdit)
	return err
}

func validateCodeActionWorkspaceEditPaths(root string, actions []protocol.CodeActionResult) error {
	return validateCodeActionWorkspaceEditPathsInRoots(root, nil, actions)
}

func validateCodeActionWorkspaceEditPathsInRoots(root string, roots []string, actions []protocol.CodeActionResult) error {
	for _, result := range actions {
		if result.CodeAction == nil || result.CodeAction.Edit == nil {
			continue
		}
		if err := validateWorkspaceEditPathsInRoots(root, roots, result.CodeAction.Edit); err != nil {
			return err
		}
	}
	return nil
}

func loadWorkspaceEditUpdates(root string, workspaceEdit *protocol.WorkspaceEdit) (map[string]editableFile, map[string]string, error) {
	files, err := collectWorkspaceEdits(root, workspaceEdit)
	if err != nil {
		return nil, nil, err
	}
	return loadWorkspaceEditUpdatesFromFiles(files)
}

func loadWorkspaceEditUpdatesFromFiles(files map[string][]protocol.TextEdit) (map[string]editableFile, map[string]string, error) {
	originals := make(map[string]editableFile, len(files))
	updated := make(map[string]string, len(files))
	for _, path := range sortedKeys(files) {
		file, err := readFileWithMode(path)
		if err != nil {
			return nil, nil, err
		}
		next, err := applyTextEdits(file.content, files[path])
		if err != nil {
			return nil, nil, err
		}
		originals[path] = file
		if next != file.content {
			updated[path] = next
		}
	}
	return originals, updated, nil
}

func writeWorkspaceEditFiles(originals map[string]editableFile, guarded map[string]string) (map[string]string, error) {
	written := make(map[string]string, len(guarded))
	for _, path := range sortedKeys(guarded) {
		file := originals[path]
		written[path] = file.diskContent(guarded[path])
		if err := os.WriteFile(path, []byte(written[path]), file.mode); err != nil {
			return nil, err
		}
	}
	return written, nil
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

func (h EditHandler) syncRollbackDocuments(ctx context.Context, manager lspmanager.Manager, originals map[string]editableFile, updated map[string]string, version int) error {
	if manager == nil {
		return nil
	}
	contents := make(map[string]string, len(updated))
	for _, path := range sortedKeys(updated) {
		contents[path] = originals[path].raw
	}
	_, _, err := h.syncDocuments(ctx, manager, contents, nextEditVersion(version))
	return err
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
	switch {
	case strings.TrimSpace(req.Patch) != "":
		return buildPatchReplacePlan(content, req.Patch)
	case len(req.Edits) > 0:
		return buildEditsReplacePlan(content, req.Edits)
	case hasCoordinateReplace(req):
		return buildRangeReplacePlan(content, req)
	default:
		return replacePlan{}, errors.New("replace_range requires patch, edits, or new_text")
	}
}

func hasCoordinateReplace(req EditRequest) bool {
	if req.Line <= 0 || req.Column <= 0 {
		return false
	}
	if strings.TrimSpace(req.NewText) != "" {
		return true
	}
	return req.EndLine > 0 || req.EndColumn > 0
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
	return buildPatchReplacePlan(content, joinHunksAsPatch(normalizeHunks(hunks)))
}

func buildRangeReplacePlan(content string, req EditRequest) (replacePlan, error) {
	start, err := requirePosition(req.Line, req.Column)
	if err != nil {
		return replacePlan{}, err
	}
	end, err := resolveOptionalEndPosition(start, req.EndLine, req.EndColumn)
	if err != nil {
		return replacePlan{}, err
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
