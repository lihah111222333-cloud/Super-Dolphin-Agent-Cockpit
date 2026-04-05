package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	editpkg "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/edit"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

type lineEndingStyle string

const (
	lineEndingLF   lineEndingStyle = "\n"
	lineEndingCRLF lineEndingStyle = "\r\n"
)

type editableFile struct {
	content    string
	raw        string
	mode       os.FileMode
	lineEnding lineEndingStyle
}

func (f editableFile) diskContent(content string) string {
	return restoreLineEndings(content, f.lineEnding)
}

func parsePatchHunks(patch string) ([]editpkg.Hunk, error) {
	patch = normalizeLineEndings(patch)
	hunks, err := editpkg.ParseMulti(patch)
	if err != nil {
		return nil, err
	}
	if len(hunks) == 1 {
		single, err := editpkg.Parse(patch)
		if err != nil {
			return nil, err
		}
		return normalizeHunks([]editpkg.Hunk{single}), nil
	}
	return normalizeHunks(hunks), nil
}

func resolveMatchMode(content string, hunk editpkg.Hunk, fallback string) string {
	lines := splitLines(content)
	pattern := splitLines(hunk.OldText)
	if len(pattern) == 0 {
		return fallback
	}
	_, mode, err := editpkg.SeekSequence(lines, pattern, 0)
	if err != nil {
		return fallback
	}
	return string(mode)
}

func collectWorkspaceEdits(workspaceEdit *protocol.WorkspaceEdit) (map[string][]protocol.TextEdit, error) {
	byFile := make(map[string][]protocol.TextEdit)
	if workspaceEdit == nil {
		return byFile, nil
	}
	for uri, edits := range workspaceEdit.Changes {
		path, err := resolveWorkspacePath(uri)
		if err != nil {
			return nil, err
		}
		byFile[path] = append(byFile[path], edits...)
	}
	for _, doc := range workspaceEdit.DocumentChanges {
		path, err := resolveWorkspacePath(doc.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		byFile[path] = append(byFile[path], doc.Edits...)
	}
	return byFile, nil
}

func applyTextEdits(content string, edits []protocol.TextEdit) (string, error) {
	type indexedEdit struct {
		edit  protocol.TextEdit
		start int
		end   int
	}
	indexed := make([]indexedEdit, 0, len(edits))
	for _, item := range edits {
		start, err := offsetForPosition(content, item.Range.Start)
		if err != nil {
			return "", err
		}
		end, err := offsetForPosition(content, item.Range.End)
		if err != nil {
			return "", err
		}
		indexed = append(indexed, indexedEdit{edit: item, start: start, end: end})
	}
	sort.Slice(indexed, func(i, j int) bool {
		if indexed[i].start == indexed[j].start {
			return indexed[i].end < indexed[j].end
		}
		return indexed[i].start < indexed[j].start
	})
	lastEnd := -1
	for _, item := range indexed {
		if item.start < lastEnd {
			return "", errors.New("workspace edit contains overlapping ranges")
		}
		lastEnd = item.end
	}
	var builder strings.Builder
	offset := 0
	for _, item := range indexed {
		builder.WriteString(content[offset:item.start])
		builder.WriteString(normalizeLineEndings(item.edit.NewText))
		offset = item.end
	}
	builder.WriteString(content[offset:])
	return builder.String(), nil
}

func offsetForPosition(content string, position protocol.Position) (int, error) {
	if position.Line < 0 || position.Character < 0 {
		return 0, errors.New("position must be non-negative")
	}
	lines := splitNormalizedLines(content)
	if position.Line >= len(lines) {
		return 0, errors.New("line is out of range")
	}
	offset := 0
	for idx := 0; idx < position.Line; idx++ {
		offset += len(lines[idx]) + 1
	}
	columnOffset, err := runeOffset(lines[position.Line], position.Character)
	if err != nil {
		return 0, err
	}
	return offset + columnOffset, nil
}

func runeOffset(line string, character int) (int, error) {
	if character == 0 {
		return 0, nil
	}
	offset := 0
	for range character {
		if offset >= len(line) {
			return 0, errors.New("column is out of range")
		}
		_, size := utf8.DecodeRuneInString(line[offset:])
		offset += size
	}
	return offset, nil
}

func resolveFilePath(path string) (string, error) {
	filePath, err := requireFilePath(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(filePath, "file://") {
		return format.AbsolutePathFromURI(filePath)
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	cleaned := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(resolved)
	}
	return cleaned, nil
}

func resolveWorkspacePath(uri string) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(uri), "file://") {
		return format.AbsolutePathFromURI(uri)
	}
	return resolveFilePath(uri)
}

func readFileWithMode(path string) (editableFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return editableFile{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return editableFile{}, err
	}
	raw := string(content)
	return editableFile{
		content:    normalizeLineEndings(raw),
		raw:        raw,
		mode:       info.Mode().Perm(),
		lineEnding: detectLineEnding(raw),
	}, nil
}

func restoreFiles(originals map[string]editableFile, updated map[string]string) error {
	restored := 0
	failed := 0
	details := make([]string, 0)
	for _, path := range sortedKeys(updated) {
		file := originals[path]
		if err := os.WriteFile(path, []byte(file.raw), file.mode); err != nil {
			failed++
			details = append(details, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		restored++
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("partial rollback: %d files restored, %d failed: %s", restored, failed, strings.Join(details, "; "))
}

func normalizeHunks(hunks []editpkg.Hunk) []editpkg.Hunk {
	for idx := range hunks {
		hunks[idx].OldText = normalizeLineEndings(hunks[idx].OldText)
		hunks[idx].NewText = normalizeLineEndings(hunks[idx].NewText)
	}
	return hunks
}

func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

func detectLineEnding(content string) lineEndingStyle {
	if strings.Contains(content, string(lineEndingCRLF)) {
		return lineEndingCRLF
	}
	return lineEndingLF
}

func restoreLineEndings(content string, lineEnding lineEndingStyle) string {
	if lineEnding == lineEndingCRLF {
		return strings.ReplaceAll(content, "\n", string(lineEndingCRLF))
	}
	return content
}

func functionBody(content string, start int, end int) string {
	lines := splitNormalizedLines(content)
	if start <= 0 || end < start || end > len(lines) {
		return ""
	}
	body := strings.Join(lines[start-1:end], "\n")
	if len(body) <= replaceRangeFuncBodyMax {
		return body
	}
	return body[:replaceRangeFuncBodyMax] + "\n...(truncated)"
}

func countLines(content string) int {
	lines := splitNormalizedLines(content)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return len(lines) - 1
	}
	return len(lines)
}

func splitLines(content string) []string {
	lines := splitNormalizedLines(content)
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func joinHunkOldText(hunks []editpkg.Hunk) string {
	items := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		items = append(items, hunk.OldText)
	}
	return strings.Join(items, "\n")
}

func joinHunkNewText(hunks []editpkg.Hunk) string {
	items := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		items = append(items, hunk.NewText)
	}
	return strings.Join(items, "\n")
}

func joinHunksAsPatch(hunks []editpkg.Hunk) string {
	blocks := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		var block strings.Builder
		block.WriteString("@@ \n")
		for _, line := range splitNormalizedLines(hunk.OldText) {
			block.WriteString("-")
			block.WriteString(line)
			block.WriteByte('\n')
		}
		for _, line := range splitNormalizedLines(hunk.NewText) {
			block.WriteString("+")
			block.WriteString(line)
			block.WriteByte('\n')
		}
		blocks = append(blocks, strings.TrimRight(block.String(), "\n"))
	}
	return strings.Join(blocks, "\n")
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok || item == "" {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
