package tools

import (
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultFunctionModeLimit = 300

	lineWindowReasonExplicit        = "explicit"
	lineWindowReasonBatch           = "batch"
	lineWindowReasonNoLSP           = "no symbol provider available"
	lineWindowReasonNoSymbols       = "no symbols returned by language server"
	lineWindowReasonOutsideFunction = "line is outside any function"
)

func renderReadContent(content string, offset, limit int, _ bool) string {
	req := readFileRequest{rawPath: "file", line: offset, limit: limit}
	return renderLineWindow("file", content, req, lineWindowReasonExplicit)
}

// renderLineWindowWithinBudget 在单文件读取超出输出预算时按整行缩短窗口。
// 它保留普通 read_file 的行号和下一段读取提示，避免预算中间件把整段结果
// 替换成 result_too_large。
func renderLineWindowWithinBudget(displayPath, content string, req readFileRequest, reason string, budgetBytes int) string {
	rendered := renderLineWindow(displayPath, content, req, reason)
	if fitsReadTextBudget(rendered, budgetBytes) {
		return rendered
	}
	lines := splitNormalizedLines(content)
	if content == "" {
		lines = []string{}
	}
	if len(lines) == 0 {
		return truncateRenderedReadText(rendered, displayPath, 1, budgetBytes)
	}
	start := clampOffset(req.line, len(lines))
	if req.line <= 0 {
		start = 1
	}
	maxLines := len(lines) - start + 1
	if req.limit > 0 && req.limit < maxLines {
		maxLines = req.limit
	}
	best := ""
	for low, high := 1, maxLines; low <= high; {
		mid := (low + high) / 2
		candidateReq := req
		candidateReq.line = start
		candidateReq.limit = mid
		candidate := appendReadBudgetTruncation(renderLineWindow(displayPath, content, candidateReq, reason), budgetBytes)
		if fitsReadTextBudget(candidate, budgetBytes) {
			best = candidate
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	if best != "" {
		return best
	}
	candidateReq := req
	candidateReq.line = start
	candidateReq.limit = 1
	return truncateRenderedReadText(appendReadBudgetTruncation(renderLineWindow(displayPath, content, candidateReq, reason), budgetBytes), displayPath, start+1, budgetBytes)
}

// renderLineWindow 渲染行window。
func renderLineWindow(displayPath, content string, req readFileRequest, reason string) string {
	lines := splitNormalizedLines(content)
	if content == "" {
		lines = []string{}
	}
	if len(lines) == 0 {
		return "TEXT\n\n[scope=file 0 lines]"
	}

	if req.line <= 0 && req.limit <= 0 {
		rendered := format.RenderLineNumberedText(strings.Join(lines, "\n"), 1)
		return fmt.Sprintf("TEXT\n%s\n\n[scope=file %d lines]", rendered, len(lines))
	}

	start := clampOffset(req.line, len(lines))
	requestedStart := start
	actualLimit := shared.ClampLimit(req.limit, 1, maxReadFileLimit, defaultReadFileLimit)

	expandedStart := expandStartToIncludeComments(lines, start)
	if expandedStart < start {
		actualLimit += start - expandedStart
		if actualLimit > maxReadFileLimit {
			actualLimit = maxReadFileLimit
		}
		start = expandedStart
	}

	end := minInt(start+actualLimit-1, len(lines))
	segment := strings.Join(lines[start-1:end], "\n")
	rendered := format.RenderLineNumberedText(segment, start)
	footer := renderLineWindowFooter(displayPath, requestedStart, start, end, len(lines), req.limit, reason)
	return fmt.Sprintf("TEXT\n%s\n\n%s", rendered, footer)
}

func fitsReadTextBudget(text string, budgetBytes int) bool {
	return budgetBytes <= 0 || len([]byte(text)) <= budgetBytes
}

func appendReadBudgetTruncation(rendered string, budgetBytes int) string {
	return fmt.Sprintf("%s\n[truncated to fit output budget %d bytes]", rendered, budgetBytes)
}

// truncateRenderedReadText 是极端长单行的兜底裁剪：整行无法放入预算时，
// 先保住预算与下一段读取提示，再截断可见文本。
func truncateRenderedReadText(rendered, displayPath string, nextLine int, budgetBytes int) string {
	if fitsReadTextBudget(rendered, budgetBytes) {
		return rendered
	}
	nextPath := strings.TrimSpace(displayPath)
	if nextPath == "" {
		nextPath = "file"
	}
	if nextLine <= 0 {
		nextLine = 1
	}
	hint := fmt.Sprintf("\n[truncated to fit output budget %d bytes; use pos=%q to continue]", budgetBytes, fmt.Sprintf("%s:%d", nextPath, nextLine))
	remaining := budgetBytes - len([]byte(hint))
	if remaining <= 0 {
		return truncateUTF8Bytes(hint, budgetBytes)
	}
	return truncateUTF8Bytes(rendered, remaining) + hint
}

// truncateUTF8Bytes 在字节上限内截断文本，并避免切断 UTF-8 rune；
// 调用方负责把关键提示先预留进预算。
func truncateUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(text)) <= maxBytes {
		return text
	}
	const suffix = "..."
	if maxBytes <= len(suffix) {
		return suffix[:maxBytes]
	}
	keep := maxBytes - len(suffix)
	cut := 0
	for idx := range text {
		if idx > keep {
			break
		}
		cut = idx
	}
	return text[:cut] + suffix
}

// renderLineWindowFooter 渲染行windowfooter。
func renderLineWindowFooter(displayPath string, requestedStart, start, end, total, limit int, reason string) string {
	parts := []string{fmt.Sprintf("scope=lines L%d-L%d of %d total", start, end, total)}
	if limit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%d", limit))
	}
	switch reason {
	case "", lineWindowReasonExplicit, lineWindowReasonBatch:
	default:
		parts = append(parts, reason)
	}
	if end < total {
		nextPath := strings.TrimSpace(displayPath)
		if nextPath == "" {
			nextPath = "file"
		}
		parts = append(parts, fmt.Sprintf("use pos=%q to continue", fmt.Sprintf("%s:%d", nextPath, end+1)))
	}
	if start < requestedStart {
		parts = append(parts, fmt.Sprintf("auto-expanded upward from L%d to include adjacent comments", requestedStart))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// renderFunctionWindow 渲染函数window。
func renderFunctionWindow(content, name string, startLine, endLine, limit int) string {
	lines := splitNormalizedLines(content)
	if content == "" || len(lines) == 0 {
		return "TEXT\n\n[scope=file 0 lines]"
	}
	start := clampOffset(startLine, len(lines))
	end := clampOffset(endLine, len(lines))
	if end < start {
		end = start
	}

	commentStart := expandStartToIncludeComments(lines, start)
	fullLineCount := end - commentStart + 1
	actualLimit := shared.ClampLimit(limit, 1, maxReadFileLimit, defaultFunctionModeLimit)
	capped := fullLineCount > actualLimit
	renderEnd := end
	if capped {
		renderEnd = commentStart + actualLimit - 1
	}
	if renderEnd > len(lines) {
		renderEnd = len(lines)
	}

	segment := strings.Join(lines[commentStart-1:renderEnd], "\n")
	rendered := format.RenderLineNumberedText(segment, commentStart)
	footer := renderFunctionFooter(name, commentStart, end, renderEnd, fullLineCount, capped)
	return fmt.Sprintf("TEXT\n%s\n\n%s", rendered, footer)
}

func renderFunctionFooter(name string, start, end, renderEnd, fullLineCount int, capped bool) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "unknown"
	}
	if capped {
		return fmt.Sprintf("[scope=function %s L%d-L%d capped to %d; pass limit=%d for full]", label, start, end, renderEnd-start+1, fullLineCount)
	}
	return fmt.Sprintf("[scope=function %s L%d-L%d]", label, start, end)
}

func enclosingFunctionName(symbols []protocol.DocumentSymbol, zeroBasedLine int) string {
	name, ok := findFunctionName(symbols, zeroBasedLine)
	if !ok {
		return ""
	}
	return name
}

// findFunctionName 查找函数名称。
func findFunctionName(symbols []protocol.DocumentSymbol, zeroBasedLine int) (string, bool) {
	for _, symbol := range symbols {
		start, end, ok := symbolBounds(symbol)
		if !ok || zeroBasedLine < start || zeroBasedLine > end {
			continue
		}
		if name, ok := findFunctionName(symbol.Children, zeroBasedLine); ok {
			return name, true
		}
		if symbol.Kind == protocol.SymbolKindFunction || symbol.Kind == protocol.SymbolKindMethod {
			return symbol.Name, true
		}
	}
	return "", false
}

// symbolBounds 处理符号边界。
func symbolBounds(symbol protocol.DocumentSymbol) (startLine, endLine int, ok bool) {
	startLine = symbol.Range.Start.Line
	endLine = symbol.Range.End.Line
	if startLine < 0 || endLine < startLine {
		return 0, 0, false
	}
	if symbol.Range.End.Character == 0 && endLine > startLine {
		endLine--
	}
	if endLine < startLine {
		endLine = startLine
	}
	return startLine, endLine, true
}

var singleLineCommentPrefixes = []string{"//", "#", "--"}

// isCommentLine 判断comment行是否可用。
func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, prefix := range singleLineCommentPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	for _, item := range blockCommentSuffixes {
		if isBlockCommentMarkerMatch(trimmed, item.Prefix, item.Suffix) {
			return true
		}
	}
	return false
}

// isBlockCommentMarkerMatch 判断blockcommentmarkermatch是否可用。
func isBlockCommentMarkerMatch(trimmed, prefix, suffix string) bool {
	firstIdx := strings.Index(trimmed, prefix)
	lastIdx := strings.LastIndex(trimmed, suffix)
	if firstIdx != -1 && firstIdx < lastIdx {
		isSingle := strings.HasPrefix(trimmed, prefix)
		if len(suffix) == 3 {
			return isSingle && len(trimmed) > 3
		}
		return isSingle
	}
	return strings.HasPrefix(trimmed, prefix) || strings.HasSuffix(trimmed, suffix)
}

// isLicenseLine 判断license行是否可用。
func isLicenseLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "copyright") && (strings.Contains(lower, "(c)") || strings.Contains(lower, "202") || strings.Contains(lower, "201")) {
		return true
	}
	if strings.Contains(lower, "licensed under") || strings.Contains(lower, "spdx-license-identifier") {
		return true
	}
	return false
}

var blockCommentSuffixes = []struct {
	Suffix string
	Prefix string
}{
	{"*/", "/*"},
	{`"""`, `"""`},
	{"'''", "'''"},
}

// checkBlockCommentMarker 处理checkblockcommentmarker。
func checkBlockCommentMarker(line string) (isBlock bool, marker string, singleLine bool) {
	trimmed := strings.TrimSpace(line)
	for _, item := range blockCommentSuffixes {
		if strings.HasSuffix(trimmed, item.Suffix) {
			firstIdx := strings.Index(trimmed, item.Prefix)
			lastIdx := strings.LastIndex(trimmed, item.Suffix)
			containsPrefixBeforeSuffix := firstIdx != -1 && firstIdx < lastIdx

			if containsPrefixBeforeSuffix {
				isSingle := strings.HasPrefix(trimmed, item.Prefix)
				if item.Suffix == `"""` || item.Suffix == "'''" {
					isSingle = isSingle && len(trimmed) > 3
				}
				if isSingle {
					return true, item.Prefix, true
				}
				return false, "", false
			}

			return true, item.Prefix, false
		}
	}
	return false, "", false
}

func shouldStopOnBlankLine(trimmed string, inBlock bool) bool {
	return trimmed == "" && !inBlock
}

func shouldStopOnNonComment(line string) bool {
	return !isCommentLine(line) || isLicenseLine(line)
}

// expandStartToIncludeComments 把expand起点处理为includecomments。
func expandStartToIncludeComments(lines []string, startLine int) int {
	const maxCommentExpandLines = 20
	current := startLine

	inMultiLineBlock := false
	var blockStartMarker string

	for i := 0; i < maxCommentExpandLines; i++ {
		prevIdx := current - 2
		if prevIdx < 0 {
			break
		}

		prevLine := lines[prevIdx]
		trimmedPrev := strings.TrimSpace(prevLine)

		if shouldStopOnBlankLine(trimmedPrev, inMultiLineBlock) {
			break
		}

		if inMultiLineBlock {
			if strings.Contains(trimmedPrev, blockStartMarker) {
				inMultiLineBlock = false
				if !strings.HasPrefix(trimmedPrev, blockStartMarker) {
					break
				}
			}
			current--
			continue
		}

		isBlock, marker, isSingle := checkBlockCommentMarker(prevLine)
		if isBlock {
			current--
			if !isSingle {
				inMultiLineBlock = true
				blockStartMarker = marker
			}
			continue
		}

		if shouldStopOnNonComment(prevLine) {
			break
		}
		current--
	}
	return current
}
