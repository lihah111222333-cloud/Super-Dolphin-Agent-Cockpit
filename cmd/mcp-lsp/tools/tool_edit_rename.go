package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// renameResult 描述一次 LSP rename 的落盘结果和受影响文件。
type renameResult struct {
	Success       bool               `json:"success"`
	Message       string             `json:"message"`
	AffectedFiles []renameFileChange `json:"affected_files"`
	TotalEdits    int                `json:"total_edits"`
	Warning       string             `json:"warning,omitempty"`
}

// renameFileChange 汇总单个文件里的 rename edit 数量。
type renameFileChange struct {
	FilePath  string `json:"file_path"`
	EditCount int    `json:"edit_count"`
}

// handleRename 调用 LSP rename 并应用跨文件 WorkspaceEdit。
// 位置、新名称或 manager 任一缺失都直接报错，避免在错误符号上批量改名。
func (h EditHandler) handleRename(ctx context.Context, req EditRequest) (any, error) {
	if strings.TrimSpace(req.NewName) == "" {
		return nil, fmt.Errorf("rename requires new_name")
	}
	if strings.TrimSpace(req.Pos) == "" {
		return nil, fmt.Errorf("rename requires pos (file_path:line:column)")
	}
	roots, err := trustedWorkspaceEditRoots(ctx)
	if err != nil {
		return nil, err
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
	affected, totalEdits, warning, err := h.applyWorkspaceEdit(ctx, roots, edit, normalizeEditVersion(req.Version))
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

// applyWorkspaceEdit 逐文件应用 WorkspaceEdit；任一文件失败则回滚已写文件。
func (h EditHandler) applyWorkspaceEdit(ctx context.Context, roots []string, edit *protocol.WorkspaceEdit, version int) ([]renameFileChange, int, string, error) {
	changes := mergeWorkspaceEditChanges(edit)
	if len(changes) == 0 {
		return nil, 0, "", nil
	}
	if err := validateWorkspaceEditFiles(ctx, roots, changes); err != nil {
		return nil, 0, "", err
	}

	type writtenFile struct {
		path     string
		original []byte
		mode     os.FileMode
	}
	var written []writtenFile
	var warnings []string

	rollback := func() error {
		var rollbackErr error
		for i := len(written) - 1; i >= 0; i-- {
			wf := written[i]
			if err := os.WriteFile(wf.path, wf.original, wf.mode); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
				continue
			}
			if err := h.syncRollbackFile(ctx, wf.path, string(wf.original), version); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		return rollbackErr
	}

	affected := make([]renameFileChange, 0, len(changes))
	totalEdits := 0
	for uri, edits := range changes {
		result, err := h.applyFileEdits(ctx, uri, edits, version)
		if err != nil {
			return nil, 0, "", withRollbackError(err, rollback())
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

// syncRollbackFile 重新解析目标文件的 manager，并把磁盘回滚状态同步回 LSP buffer。
func (h EditHandler) syncRollbackFile(ctx context.Context, path string, content string, version int) error {
	manager, err := managerForFile(ctx, h.registry, path, "")
	if err != nil {
		return err
	}
	return h.syncRollbackDocument(ctx, manager, path, content, version)
}

// fileEditResult 保存单文件写入后的回滚材料和同步警告。
type fileEditResult struct {
	path     string
	original []byte
	mode     os.FileMode
	warning  string
}

// textEditApplication 是按字节偏移可直接应用到当前文本的 TextEdit。
type textEditApplication struct {
	startLine int
	startByte int
	endLine   int
	endByte   int
	newText   string
}

// applyFileEdits 把 URI 定位到本地文件后复用通用 TextEdit 写入路径。
func (h EditHandler) applyFileEdits(ctx context.Context, uri string, edits []protocol.TextEdit, version int) (*fileEditResult, error) {
	absPath, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return nil, fmt.Errorf("resolve URI %q: %w", uri, err)
	}
	return h.applyTextEditsToPath(ctx, absPath, edits, version, nil)
}

// mergeWorkspaceEditChanges 合并 changes 与 documentChanges，统一后续落盘循环。
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

// applyTextEdits 按从后向前的顺序应用 TextEdit，避免早期编辑改变后续位置。
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

// buildTextEditApplications 校验并转换全部 TextEdit，再按安全应用顺序排序。
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

// buildTextEditApplication 把 LSP UTF-16 位置转换成 Go 字符串字节偏移。
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

// validateTextEditRange 校验 TextEdit 行列范围，拒绝越界或反向区间。
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

// textEditRangeReversed 判断 LSP range 是否起点晚于终点。
func textEditRangeReversed(rng protocol.Range) bool {
	if rng.Start.Line != rng.End.Line {
		return rng.Start.Line > rng.End.Line
	}
	return rng.Start.Character > rng.End.Character
}

// utf16CharacterToByteOffset 将 LSP UTF-16 character 偏移转换为 Go 字节偏移。
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

// utf16RuneWidth 返回 rune 在 LSP UTF-16 坐标里的宽度。
func utf16RuneWidth(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// sortTextEditApplications 按倒序排列编辑，保证应用时未处理 range 不被前序修改扰动。
func sortTextEditApplications(edits []textEditApplication) {
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].startLine != edits[j].startLine {
			return edits[i].startLine > edits[j].startLine
		}
		return edits[i].startByte > edits[j].startByte
	})
}
