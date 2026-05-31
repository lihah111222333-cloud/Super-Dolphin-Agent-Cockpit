package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type renameResult struct {
	Success       bool               `json:"success"`
	Message       string             `json:"message"`
	AffectedFiles []renameFileChange `json:"affected_files"`
	TotalEdits    int                `json:"total_edits"`
	Warning       string             `json:"warning,omitempty"`
}

type renameFileChange struct {
	FilePath  string `json:"file_path"`
	EditCount int    `json:"edit_count"`
}

func (h EditHandler) handleRename(ctx context.Context, req EditRequest) (any, error) {
	if strings.TrimSpace(req.NewName) == "" {
		return nil, fmt.Errorf("rename requires new_name")
	}
	if strings.TrimSpace(req.Pos) == "" {
		return nil, fmt.Errorf("rename requires pos (file_path:line:column)")
	}
	filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{Pos: req.Pos, LanguageID: req.LanguageID})
	if err != nil {
		return nil, fmt.Errorf("resolve rename position: %w", err)
	}
	manager, err := managerForFile(ctx, h.registry, filePath, req.LanguageID)
	if err != nil {
		return nil, fmt.Errorf("rename manager: %w", err)
	}
	edit, err := manager.Rename(ctx, filePath, position, req.NewName)
	if err != nil {
		return nil, fmt.Errorf("LSP rename: %w", err)
	}
	if edit == nil {
		return renameResult{Success: true, Message: "rename returned no changes"}, nil
	}
	affected, totalEdits, warning, err := h.applyWorkspaceEdit(ctx, edit, normalizeEditVersion(req.Version))
	if err != nil {
		return nil, fmt.Errorf("apply rename edits: %w", err)
	}
	return renameResult{
		Success:       true,
		Message:       fmt.Sprintf("renamed to %q across %d file(s)", req.NewName, len(affected)),
		AffectedFiles: affected,
		TotalEdits:    totalEdits,
		Warning:       warning,
	}, nil
}

func (h EditHandler) applyWorkspaceEdit(ctx context.Context, edit *protocol.WorkspaceEdit, version int) ([]renameFileChange, int, string, error) {
	changes := mergeWorkspaceEditChanges(edit)
	if len(changes) == 0 {
		return nil, 0, "", nil
	}

	type writtenFile struct {
		path     string
		original []byte
		mode     os.FileMode
	}
	var written []writtenFile
	var warnings []string

	rollback := func() {
		for _, wf := range written {
			_ = os.WriteFile(wf.path, wf.original, wf.mode)
		}
	}

	affected := make([]renameFileChange, 0, len(changes))
	totalEdits := 0
	for uri, edits := range changes {
		result, err := h.applyFileEdits(ctx, uri, edits, version)
		if err != nil {
			rollback()
			return nil, 0, "", err
		}
		if result == nil {
			continue
		}
		written = append(written, writtenFile{path: result.path, original: result.original, mode: result.mode})
		if result.warning != "" {
			warnings = append(warnings, result.warning)
		}
		affected = append(affected, renameFileChange{FilePath: result.path, EditCount: len(edits)})
		totalEdits += len(edits)
	}

	warning := strings.Join(warnings, "; ")
	return affected, totalEdits, warning, nil
}

type fileEditResult struct {
	path     string
	original []byte
	mode     os.FileMode
	warning  string
}

func (h EditHandler) applyFileEdits(ctx context.Context, uri string, edits []protocol.TextEdit, version int) (*fileEditResult, error) {
	absPath, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return nil, fmt.Errorf("resolve URI %q: %w", uri, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", absPath, err)
	}
	original, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", absPath, err)
	}
	updated, err := applyTextEdits(string(original), edits)
	if err != nil {
		return nil, fmt.Errorf("apply edits to %s: %w", absPath, err)
	}
	if updated == string(original) {
		return nil, nil
	}
	if err := os.WriteFile(absPath, []byte(updated), info.Mode()); err != nil {
		return nil, fmt.Errorf("write %s: %w", absPath, err)
	}
	result := &fileEditResult{path: absPath, original: original, mode: info.Mode()}
	mgr, mErr := managerForFile(ctx, h.registry, absPath, "")
	if mErr == nil && mgr != nil {
		syncErr := mgr.DidChange(ctx, absPath, version, []protocol.TextDocumentContentChangeEvent{
			{Text: updated},
		})
		if syncErr != nil {
			result.warning = fmt.Sprintf("LSP sync %s: %v", absPath, syncErr)
		}
	}
	return result, nil
}
func mergeWorkspaceEditChanges(edit *protocol.WorkspaceEdit) map[string][]protocol.TextEdit {
	merged := make(map[string][]protocol.TextEdit)
	for uri, edits := range edit.Changes {
		merged[uri] = append(merged[uri], edits...)
	}
	for _, docEdit := range edit.DocumentChanges {
		uri := docEdit.TextDocument.URI
		merged[uri] = append(merged[uri], docEdit.Edits...)
	}
	return merged
}

func applyTextEdits(content string, edits []protocol.TextEdit) (string, error) {
	if len(edits) == 0 {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	sortEditsReverse(edits)
	for _, edit := range edits {
		startLine := edit.Range.Start.Line
		startChar := edit.Range.Start.Character
		endLine := edit.Range.End.Line
		endChar := edit.Range.End.Character
		if startLine >= len(lines) || endLine >= len(lines) {
			return "", fmt.Errorf("edit range out of bounds: L%d-L%d (file has %d lines)", startLine, endLine, len(lines))
		}
		if startChar > len(lines[startLine]) {
			startChar = len(lines[startLine])
		}
		if endChar > len(lines[endLine]) {
			endChar = len(lines[endLine])
		}
		before := lines[startLine][:startChar]
		after := lines[endLine][endChar:]
		replacement := before + edit.NewText + after
		replacementLines := strings.Split(replacement, "\n")
		newLines := make([]string, 0, startLine+len(replacementLines)+(len(lines)-endLine-1))
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, replacementLines...)
		if endLine+1 < len(lines) {
			newLines = append(newLines, lines[endLine+1:]...)
		}
		lines = newLines
	}
	return strings.Join(lines, "\n"), nil
}

func sortEditsReverse(edits []protocol.TextEdit) {
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].Range.Start.Line != edits[j].Range.Start.Line {
			return edits[i].Range.Start.Line > edits[j].Range.Start.Line
		}
		return edits[i].Range.Start.Character > edits[j].Range.Start.Character
	})
}
