package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
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

// handleRename 处理重命名。
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

// applyWorkspaceEdit 应用工作区编辑。
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

type textEditApplication struct {
	startLine int
	startByte int
	endLine   int
	endByte   int
	newText   string
}

func (h EditHandler) applyFileEdits(ctx context.Context, uri string, edits []protocol.TextEdit, version int) (*fileEditResult, error) {
	absPath, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return nil, fmt.Errorf("resolve URI %q: %w", uri, err)
	}
	return h.applyTextEditsToPath(ctx, absPath, edits, version, nil)
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
	applications, err := buildTextEditApplications(lines, edits)
	if err != nil {
		return "", err
	}
	for _, edit := range applications {
		before := lines[edit.startLine][:edit.startByte]
		after := lines[edit.endLine][edit.endByte:]
		replacement := before + edit.newText + after
		replacementLines := strings.Split(replacement, "\n")
		newLines := make([]string, 0, edit.startLine+len(replacementLines)+(len(lines)-edit.endLine-1))
		newLines = append(newLines, lines[:edit.startLine]...)
		newLines = append(newLines, replacementLines...)
		if edit.endLine+1 < len(lines) {
			newLines = append(newLines, lines[edit.endLine+1:]...)
		}
		lines = newLines
	}
	return strings.Join(lines, "\n"), nil
}

func buildTextEditApplications(lines []string, edits []protocol.TextEdit) ([]textEditApplication, error) {
	applications := make([]textEditApplication, 0, len(edits))
	for _, edit := range edits {
		application, err := buildTextEditApplication(lines, edit)
		if err != nil {
			return nil, err
		}
		applications = append(applications, application)
	}
	sortTextEditApplications(applications)
	return applications, nil
}

func buildTextEditApplication(lines []string, edit protocol.TextEdit) (textEditApplication, error) {
	if err := validateTextEditRange(lines, edit.Range); err != nil {
		return textEditApplication{}, err
	}
	startByte, err := utf16CharacterToByteOffset(lines[edit.Range.Start.Line], edit.Range.Start.Character)
	if err != nil {
		return textEditApplication{}, fmt.Errorf("edit start: %w", err)
	}
	endByte, err := utf16CharacterToByteOffset(lines[edit.Range.End.Line], edit.Range.End.Character)
	if err != nil {
		return textEditApplication{}, fmt.Errorf("edit end: %w", err)
	}
	return textEditApplication{
		startLine: edit.Range.Start.Line,
		startByte: startByte,
		endLine:   edit.Range.End.Line,
		endByte:   endByte,
		newText:   edit.NewText,
	}, nil
}

// validateTextEditRange 校验文本编辑范围。
func validateTextEditRange(lines []string, rng protocol.Range) error {
	if rng.Start.Line < 0 || rng.End.Line < 0 {
		return fmt.Errorf("edit range line must be non-negative: L%d-L%d", rng.Start.Line, rng.End.Line)
	}
	if rng.Start.Line >= len(lines) || rng.End.Line >= len(lines) {
		return fmt.Errorf("edit range out of bounds: L%d-L%d (file has %d lines)", rng.Start.Line, rng.End.Line, len(lines))
	}
	if rng.Start.Character < 0 || rng.End.Character < 0 {
		return fmt.Errorf("edit range character must be non-negative: C%d-C%d", rng.Start.Character, rng.End.Character)
	}
	if textEditRangeReversed(rng) {
		return fmt.Errorf("edit range start after end: L%d:C%d-L%d:C%d", rng.Start.Line, rng.Start.Character, rng.End.Line, rng.End.Character)
	}
	return nil
}

func textEditRangeReversed(rng protocol.Range) bool {
	if rng.Start.Line != rng.End.Line {
		return rng.Start.Line > rng.End.Line
	}
	return rng.Start.Character > rng.End.Character
}

func utf16CharacterToByteOffset(line string, character int) (int, error) {
	units := 0
	for byteOffset, r := range line {
		if units == character {
			return byteOffset, nil
		}
		width := utf16RuneWidth(r)
		if units+width > character {
			return 0, fmt.Errorf("character %d splits UTF-16 surrogate pair", character)
		}
		units += width
	}
	if units == character {
		return len(line), nil
	}
	return 0, fmt.Errorf("character %d out of bounds (line has %d UTF-16 units)", character, units)
}

func utf16RuneWidth(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

func sortTextEditApplications(edits []textEditApplication) {
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].startLine != edits[j].startLine {
			return edits[i].startLine > edits[j].startLine
		}
		return edits[i].startByte > edits[j].startByte
	})
}
