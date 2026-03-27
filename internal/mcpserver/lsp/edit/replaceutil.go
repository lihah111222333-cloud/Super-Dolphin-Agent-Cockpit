package edit

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ReplaceRangeMaxReplacementBytes = 256 * 1024
	ReplaceRangeMaxContentBytes     = 4 * 1024 * 1024
	ReplaceRangeForceBypassMaxBytes = 2 * 1024 * 1024
	MaxReplaceRangeEdits            = 20
	ReplaceRangeMaxContextLines     = 5
)

type contentIndex struct {
	raw   string
	lines []string
	start []int
	end   []int
}

func GuardContentAndReplacement(content string, replacement string) error {
	if len(content) > ReplaceRangeMaxContentBytes {
		return fmt.Errorf("%w: content exceeds %d bytes", ErrInvalidPatch, ReplaceRangeMaxContentBytes)
	}
	if len(replacement) > ReplaceRangeMaxReplacementBytes {
		return fmt.Errorf("%w: replacement exceeds %d bytes", ErrInvalidPatch, ReplaceRangeMaxReplacementBytes)
	}
	return nil
}

// ShouldForceBypass reports whether callers should enable the internal
// large-content bypass path for replace_range follow-up operations.
func ShouldForceBypass(contentLen int) bool {
	return contentLen > ReplaceRangeForceBypassMaxBytes
}

// OffsetToLine converts a byte offset into a 1-based LSP line number.
func OffsetToLine(content string, offset int) (int, error) {
	index, err := indexContent(content)
	if err != nil {
		return 0, err
	}
	return index.lineForOffset(offset)
}

// ReplacementPreview applies the replacement and returns a compact preview
// window around the edit.
func ReplacementPreview(content string, startOffset int, endOffset int, replacement string) (string, error) {
	if err := GuardContentAndReplacement(content, replacement); err != nil {
		return "", err
	}
	if err := validateOffsets(content, startOffset, endOffset); err != nil {
		return "", err
	}
	updated := content[:startOffset] + replacement + content[endOffset:]
	startLine, err := OffsetToLine(content, startOffset)
	if err != nil {
		return "", err
	}
	index, err := indexContent(updated)
	if err != nil {
		return "", err
	}
	windowStart, windowEnd := previewWindow(index.lines, startLine, startLine)
	return renderWindow(index.lines, windowStart, windowEnd), nil
}

// BuildEditContext renders the surrounding lines and the old/new change block.
func BuildEditContext(content string, startOffset int, endOffset int, replacement string) (string, int, int, error) {
	if err := GuardContentAndReplacement(content, replacement); err != nil {
		return "", 0, 0, err
	}
	if err := validateOffsets(content, startOffset, endOffset); err != nil {
		return "", 0, 0, err
	}
	index, err := indexContent(content)
	if err != nil {
		return "", 0, 0, err
	}
	startLine, endLine, err := index.lineRangeForOffsets(startOffset, endOffset)
	if err != nil {
		return "", 0, 0, err
	}
	windowStart, windowEnd := previewWindow(index.lines, startLine, endLine)
	removed := splitPreviewLines(content[startOffset:endOffset])
	added := splitPreviewLines(replacement)
	before := sliceLines(index.lines, windowStart, startLine-1)
	after := sliceLines(index.lines, endLine+1, windowEnd)

	var builder strings.Builder
	fmt.Fprintf(&builder, "@@ lines %d-%d @@\n", windowStart, windowEnd)
	writeContextLines(&builder, ' ', windowStart, before)
	writeContextLines(&builder, '-', startLine, removed)
	writeContextLines(&builder, '+', startLine, added)
	writeContextLines(&builder, ' ', endLine+1, after)
	return strings.TrimRight(builder.String(), "\n"), windowStart, windowEnd, nil
}

func indexContent(content string) (contentIndex, error) {
	if err := GuardContentAndReplacement(content, ""); err != nil {
		return contentIndex{}, err
	}
	if content == "" {
		return contentIndex{raw: "", lines: nil}, nil
	}
	lines := make([]string, 0, strings.Count(content, "\n")+1)
	starts := make([]int, 0, len(lines))
	ends := make([]int, 0, len(lines))
	lineStart := 0
	for idx := 0; idx < len(content); idx++ {
		if content[idx] != '\n' {
			continue
		}
		line := content[lineStart:idx]
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		lines = append(lines, line)
		starts = append(starts, lineStart)
		ends = append(ends, idx+1)
		lineStart = idx + 1
	}
	if lineStart < len(content) {
		line := content[lineStart:]
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		lines = append(lines, line)
		starts = append(starts, lineStart)
		ends = append(ends, len(content))
	}
	return contentIndex{raw: content, lines: lines, start: starts, end: ends}, nil
}

func (idx contentIndex) lineForOffset(offset int) (int, error) {
	if err := validateOffsets(idx.raw, offset, offset); err != nil {
		return 0, err
	}
	if len(idx.lines) == 0 {
		return 1, nil
	}
	pos := sort.Search(len(idx.start), func(i int) bool {
		return idx.start[i] > offset
	})
	if pos == 0 {
		return 1, nil
	}
	if pos > len(idx.start) {
		return len(idx.start), nil
	}
	return pos, nil
}

func (idx contentIndex) lineRangeForOffsets(startOffset int, endOffset int) (int, int, error) {
	startLine, err := idx.lineForOffset(startOffset)
	if err != nil {
		return 0, 0, err
	}
	if endOffset == startOffset {
		return startLine, startLine, nil
	}
	endLine, err := idx.lineForOffset(endOffset - 1)
	if err != nil {
		return 0, 0, err
	}
	return startLine, endLine, nil
}

func previewWindow(lines []string, startLine int, endLine int) (int, int) {
	if len(lines) == 0 {
		return 1, 1
	}
	windowStart := maxInt(1, startLine-ReplaceRangeMaxContextLines)
	windowEnd := minInt(len(lines), endLine+ReplaceRangeMaxContextLines)
	return windowStart, windowEnd
}

func renderWindow(lines []string, startLine int, endLine int) string {
	if len(lines) == 0 {
		return ""
	}
	var builder strings.Builder
	for lineNo := startLine; lineNo <= endLine; lineNo++ {
		fmt.Fprintf(&builder, "%4d | %s\n", lineNo, lines[lineNo-1])
	}
	return strings.TrimRight(builder.String(), "\n")
}

func writeContextLines(builder *strings.Builder, prefix byte, startLine int, lines []string) {
	for idx, line := range lines {
		fmt.Fprintf(builder, "%c%4d | %s\n", prefix, startLine+idx, line)
	}
}

func splitPreviewLines(text string) []string {
	if text == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}

func sliceLines(lines []string, startLine int, endLine int) []string {
	if startLine > endLine || len(lines) == 0 {
		return nil
	}
	return append([]string(nil), lines[startLine-1:endLine]...)
}

func validateOffsets(content string, startOffset int, endOffset int) error {
	if startOffset < 0 || endOffset < 0 || startOffset > endOffset || endOffset > len(content) {
		return fmt.Errorf("%w: invalid offset range", ErrInvalidPatch)
	}
	return nil
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
