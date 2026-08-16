package tools

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	// defaultFunctionModeLimit 是函数窗口读取的默认最大行数。
	defaultFunctionModeLimit = 300

	// lineWindowReason* 标记 read_file 降级到行窗口的原因，最终写入 ATTR.reason。
	lineWindowReasonExplicit        = "explicit"
	lineWindowReasonBatch           = "batch"
	lineWindowReasonNoLSP           = "no symbol provider available"
	lineWindowReasonNoSymbols       = "no symbols returned by language server"
	lineWindowReasonOutsideFunction = "line is outside any function"
)

type readRenderMeta struct {
	file, scope, reason, symbol, hint string
	total, start, end, requestedStart int
}

// renderLineWindowWithinBudget 在单文件读取超出输出预算时按完整 ROW 缩短窗口。
func renderLineWindowWithinBudget(displayPath, content string, req readFileRequest, reason string, budgetBytes int) string {
	rendered := renderLineWindow(displayPath, content, req, reason)
	if fitsReadTextBudget(rendered, budgetBytes) {
		return rendered
	}
	lines := protocolSourceLines(content)
	if len(lines) == 0 {
		return rendered
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
	return renderReadBudgetOmission(displayPath, start, len(lines), budgetBytes)
}

// renderLineWindow 渲染 1-based 行窗口，并自动向上包含紧邻注释。
func renderLineWindow(displayPath, content string, req readFileRequest, reason string) string {
	lines := protocolSourceLines(content)
	if len(lines) == 0 {
		return renderReadRows(readRenderMeta{file: displayPath, scope: "lines", reason: reason}, nil)
	}

	start := clampOffset(req.line, len(lines))
	if req.line <= 0 {
		start = 1
	}
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
	meta := readRenderMeta{
		file: displayPath, scope: "lines", reason: reason,
		total: len(lines), start: start, end: end, requestedStart: requestedStart,
		hint: lineWindowContinuationHint(displayPath, start, end, len(lines), req.limit),
	}
	return renderReadRows(meta, lines[start-1:end])
}

func protocolSourceLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := splitNormalizedLines(content)
	if strings.HasSuffix(strings.ReplaceAll(content, "\r\n", "\n"), "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func renderReadRows(meta readRenderMeta, rows []string) string {
	showing := len(rows)
	lines := []string{lineprotocol.HeaderLine(meta.total, showing, showing < meta.total, "line")}
	lines = append(lines, lineprotocol.FieldsRecord("ATTR",
		lineprotocol.Field{Key: "file", Value: meta.file},
		lineprotocol.Field{Key: "scope", Value: meta.scope},
		lineprotocol.Field{Key: "start_line", Value: strconv.Itoa(meta.start)},
		lineprotocol.Field{Key: "end_line", Value: strconv.Itoa(meta.end)},
		lineprotocol.Field{Key: "requested_start_line", Value: strconv.Itoa(meta.requestedStart)},
		lineprotocol.Field{Key: "reason", Value: meta.reason},
		lineprotocol.Field{Key: "symbol", Value: meta.symbol},
	))
	for index, row := range rows {
		lines = append(lines, lineprotocol.FieldsRecord("ROW",
			lineprotocol.Field{Key: "line", Value: strconv.Itoa(meta.start + index)},
			lineprotocol.Field{Key: "text", Value: row},
		))
	}
	if meta.hint != "" {
		lines = append(lines, lineprotocol.TextRecord("HINT", meta.hint))
	}
	return strings.Join(lines, "\n")
}

func renderBatchReadResponse(response batchReadResponse) string {
	rows := make([]string, 0)
	for _, item := range response.Data {
		if !item.Success {
			return lineprotocol.ErrorLine("invalid_batch_read", false) + "\n" + lineprotocol.TextRecord("MESSAGE", item.FilePath+": "+item.Error)
		}
		doc, err := lineprotocol.Parse(item.Content)
		if err != nil {
			return lineprotocol.ErrorLine("invalid_batch_read", false) + "\n" + lineprotocol.TextRecord("MESSAGE", item.FilePath+": "+err.Error())
		}
		for _, record := range doc.Records {
			if record.Kind != "ROW" {
				continue
			}
			text, hasText := record.Fields["text"]
			if len(record.Fields) != 2 || record.Fields["line"] == "" || !hasText {
				return lineprotocol.ErrorLine("invalid_batch_read", false) + "\n" + lineprotocol.TextRecord("MESSAGE", item.FilePath+": invalid source ROW fields")
			}
			rows = append(rows, lineprotocol.FieldsRecord("ROW", lineprotocol.Field{Key: "file", Value: item.FilePath}, lineprotocol.Field{Key: "line", Value: record.Fields["line"]}, lineprotocol.Field{Key: "text", Value: text}))
		}
	}
	lines := append([]string{lineprotocol.HeaderLine(response.rowTotal, len(rows), len(rows) < response.rowTotal, "line")}, rows...)
	if len(rows) < response.rowTotal {
		lines = append(lines, lineprotocol.TextRecord("HINT", batchReadTruncatedHint))
	}
	return strings.Join(lines, "\n")
}

func lineWindowContinuationHint(displayPath string, start, end, total, limit int) string {
	if end < total {
		return fmt.Sprintf("next: file action=read_file pos=%s:%d limit=%d", displayPath, end+1, max(limit, 1))
	}
	if start > 1 {
		return fmt.Sprintf("previous: file action=read_file pos=%s:1 limit=%d", displayPath, start-1)
	}
	return ""
}

// fitsReadTextBudget 判断渲染结果是否落在工具输出预算内。
func fitsReadTextBudget(text string, budgetBytes int) bool {
	return budgetBytes <= 0 || len([]byte(text)) <= budgetBytes
}

// appendReadBudgetTruncation 在被预算裁剪的输出末尾追加协议内 WARNING。
func appendReadBudgetTruncation(rendered string, budgetBytes int) string {
	return rendered + "\n" + lineprotocol.TextRecord("WARNING", fmt.Sprintf("read_file rows reduced to fit output budget %d bytes", budgetBytes))
}

// truncateRenderedReadText 在函数窗口极端超限时返回完整协议记录，不截断 escape 或 ROW。
func truncateRenderedReadText(rendered, displayPath string, nextLine int, budgetBytes int) string {
	if fitsReadTextBudget(rendered, budgetBytes) {
		return rendered
	}
	return renderReadBudgetOmission(displayPath, max(nextLine-1, 1), max(nextLine, 1), budgetBytes)
}

func renderReadBudgetOmission(displayPath string, start, total, budgetBytes int) string {
	meta := readRenderMeta{
		file: displayPath, scope: "lines", reason: "output_budget", total: total,
		hint: fmt.Sprintf("next: file action=read_file pos=%s:%d limit=1", displayPath, start),
	}
	return renderReadRows(meta, nil) + "\n" +
		lineprotocol.TextRecord("WARNING", fmt.Sprintf("source line omitted because it exceeds output budget %d bytes", budgetBytes))
}

// renderFunctionWindow 渲染完整函数窗口，并在超出 limit 时保留可重试提示。
func renderFunctionWindow(displayPath, content, name string, startLine, endLine, limit int) string {
	lines := protocolSourceLines(content)
	if content == "" || len(lines) == 0 {
		return renderReadRows(readRenderMeta{file: displayPath, scope: "function", symbol: name}, nil)
	}
	start := clampOffset(startLine, len(lines))
	end := clampOffset(endLine, len(lines))
	end = max(end, start)
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
	hint := ""
	if capped {
		hint = fmt.Sprintf("next: file action=read_file pos=%s:%d scope=lines limit=%d", displayPath, renderEnd+1, end-renderEnd)
	}
	return renderReadRows(readRenderMeta{
		file: displayPath, scope: "function", reason: "symbol_window", symbol: strings.TrimSpace(name),
		total: fullLineCount, start: commentStart, end: renderEnd, requestedStart: start, hint: hint,
	}, lines[commentStart-1:renderEnd])
}

// enclosingFunctionName 返回包含目标行的最内层函数名。
func enclosingFunctionName(symbols []protocol.DocumentSymbol, zeroBasedLine int) string {
	name, ok := findFunctionName(symbols, zeroBasedLine)
	if !ok {
		return ""
	}
	return name
}

// findFunctionName 深度优先查找包含目标行的函数或方法名。
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

// symbolBounds 把 LSP symbol range 转成可比较的 0-based 行范围。
// LSP 结束列为 0 时表示上一行结束，需回退一行避免范围多吃下一声明。
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

func singleLineCommentPrefixes() []string {
	return []string{"//", "#", "--"}
}

// isCommentLine 判断上一行是否属于可随读取窗口一起带出的注释。
// 只识别常见单行注释和块注释边界，避免把空行或普通代码误并入上下文。
func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, prefix := range singleLineCommentPrefixes() {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	for _, item := range blockCommentSuffixes() {
		if isBlockCommentMarkerMatch(trimmed, item.Prefix, item.Suffix) {
			return true
		}
	}
	return false
}

// isBlockCommentMarkerMatch 判断一行是否包含块注释边界。
// 它同时支持单行块注释和多行块注释的首尾标记，供向上扩展窗口时维护块状态。
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

// isLicenseLine 识别文件头 license/copyright 行。
// 读取函数附近上下文时不把 license 当作业务注释向下粘连，避免窗口被文件头撑大。
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

type blockCommentSuffix struct {
	Suffix string
	Prefix string
}

func blockCommentSuffixes() []blockCommentSuffix {
	return []blockCommentSuffix{
		{"*/", "/*"},
		{`"""`, `"""`},
		{"'''", "'''"},
	}
}

// checkBlockCommentMarker 检查上一行是否是块注释结束或完整块注释。
// 返回 marker 让 expandStartToIncludeComments 能继续向上追溯多行块的起点。
func checkBlockCommentMarker(line string) (isBlock bool, marker string, singleLine bool) {
	trimmed := strings.TrimSpace(line)
	for _, item := range blockCommentSuffixes() {
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

// expandStartToIncludeComments 把行窗口起点向上扩展到相邻注释块。
// 最多回看固定行数，防止异常长文件头或未闭合块注释把 read_file 响应拖出预算。
func expandStartToIncludeComments(lines []string, startLine int) int {
	const maxCommentExpandLines = 20
	current := startLine

	inMultiLineBlock := false
	var blockStartMarker string

	for range maxCommentExpandLines {
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
