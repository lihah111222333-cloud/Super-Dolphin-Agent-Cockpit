// Package edit 提供 patch 解析与匹配功能，用于 LSP replace_range 编辑操作。
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

// contentIndex 保存文本行内容与字节偏移索引。
// replace_range 匹配器用它在 LSP 行号、byte offset 和展示窗口之间稳定转换。
type contentIndex struct {
	raw   string
	lines []string
	start []int
	end   []int
}

// GuardContentAndReplacement 限制目标内容和替换内容的体积上限。
// 超限直接返回错误，避免编辑工具在大文件或大替换块上隐式降级。
func GuardContentAndReplacement(content string, replacement string) error {
	if len(content) > ReplaceRangeMaxContentBytes {
		return fmt.Errorf("%w: content exceeds %d bytes", ErrInvalidPatch, ReplaceRangeMaxContentBytes)
	}
	if len(replacement) > ReplaceRangeMaxReplacementBytes {
		return fmt.Errorf("%w: replacement exceeds %d bytes", ErrInvalidPatch, ReplaceRangeMaxReplacementBytes)
	}
	return nil
}

// ShouldForceBypass 按内容长度决定是否进入大内容旁路路径。
// 该判断只看内容长度，调用方仍需继续执行 GuardContentAndReplacement。
func ShouldForceBypass(contentLen int) bool {
	return contentLen > ReplaceRangeForceBypassMaxBytes
}

// OffsetToLine 将字节偏移转换成 1-based 展示行号。
// 偏移越界会返回错误，避免工具层展示错误落点。
func OffsetToLine(content string, offset int) (int, error) {
	index, err := indexContent(content)
	if err != nil {
		return 0, err
	}
	return index.lineForOffset(offset)
}

// BuildEditContext 渲染替换前后的上下文窗口。
// 返回的窗口边界用于工具回显和歧义诊断，输入 offset 必须先通过边界校验。
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
	startLine, endLine = normalizeInsertionEOFLine(content, index, startOffset, endOffset, startLine, endLine)
	windowStart, windowEnd := affectedWindow(index.lines, startLine, endLine)
	removed := splitAffectedLines(content[startOffset:endOffset])
	added := splitAffectedLines(replacement)
	before := sliceLines(index.lines, windowStart, startLine-1)
	afterStartLine := editContextAfterStartLine(content, startOffset, endOffset, startLine, endLine)
	afterDisplayLine := editContextAfterDisplayLine(startOffset, endOffset, startLine, endLine, len(added))
	displayWindowEnd := editContextDisplayWindowEnd(windowEnd, startOffset, endOffset, len(added))
	after := sliceLines(index.lines, afterStartLine, windowEnd)

	var builder strings.Builder
	fmt.Fprintf(&builder, "@@ lines %d-%d @@\n", windowStart, displayWindowEnd)
	writeContextLines(&builder, ' ', windowStart, before)
	writeContextLines(&builder, '-', startLine, removed)
	writeContextLines(&builder, '+', startLine, added)
	writeContextLines(&builder, ' ', afterDisplayLine, after)
	return strings.TrimRight(builder.String(), "\n"), windowStart, displayWindowEnd, nil
}

func normalizeInsertionEOFLine(content string, idx contentIndex, startOffset int, endOffset int, startLine int, endLine int) (int, int) {
	if startOffset == endOffset && startOffset == len(content) && strings.HasSuffix(content, "\n") {
		line := len(idx.lines) + 1
		return line, line
	}
	return startLine, endLine
}

func editContextAfterStartLine(content string, startOffset int, endOffset int, startLine int, endLine int) int {
	if startOffset == endOffset && startOffset < len(content) {
		return startLine
	}
	return endLine + 1
}

func editContextAfterDisplayLine(startOffset int, endOffset int, startLine int, endLine int, addedLineCount int) int {
	if startOffset == endOffset {
		return startLine + addedLineCount
	}
	return endLine + 1
}

func editContextDisplayWindowEnd(windowEnd int, startOffset int, endOffset int, addedLineCount int) int {
	if startOffset == endOffset {
		return windowEnd + addedLineCount
	}
	return windowEnd
}

// indexContent 建立行文本与字节偏移索引。
// 这里保留原始内容用于后续 offset 校验，同时把 CRLF 规整到展示行。
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
		line = strings.TrimSuffix(line, "\r")
		lines = append(lines, line)
		starts = append(starts, lineStart)
		ends = append(ends, idx+1)
		lineStart = idx + 1
	}
	if lineStart < len(content) {
		line := content[lineStart:]
		line = strings.TrimSuffix(line, "\r")
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

func affectedWindow(lines []string, startLine int, endLine int) (int, int) {
	if len(lines) == 0 {
		return 1, 1
	}
	windowStart := maxInt(1, startLine-ReplaceRangeMaxContextLines)
	windowEnd := minInt(len(lines), endLine+ReplaceRangeMaxContextLines)
	return windowStart, windowEnd
}

func writeContextLines(builder *strings.Builder, prefix byte, startLine int, lines []string) {
	for idx, line := range lines {
		fmt.Fprintf(builder, "%c%4d | %s\n", prefix, startLine+idx, line)
	}
}

func splitAffectedLines(text string) []string {
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
