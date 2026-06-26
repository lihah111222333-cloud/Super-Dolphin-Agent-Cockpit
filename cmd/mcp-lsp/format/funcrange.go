package format

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type SymbolProvider interface {
	Symbols(absPath string) ([]protocol.DocumentSymbol, error)
}

// FindEnclosingFunction 在文档符号树中查找包含指定行的函数或方法。
// 输入行号是 LSP 的 0-based 坐标，返回值转成报告使用的 1-based 行号。
func FindEnclosingFunction(symbols []protocol.DocumentSymbol, zeroBasedLine int) (startLine, endLine int, ok bool) {
	if zeroBasedLine < 0 {
		return 0, 0, false
	}
	startLine, endLine, ok = findEnclosing(symbols, zeroBasedLine)
	if !ok {
		return 0, 0, false
	}
	return startLine + 1, endLine + 1, true
}

// EnrichLocationResultsWithFuncRange 为引用结果补充函数范围。
// 同一文件内连续落在同一函数的结果只标记一次，减少 compact 输出噪声。
func EnrichLocationResultsWithFuncRange(results []protocol.LocationResult, provider SymbolProvider) {
	if len(results) == 0 || provider == nil {
		return
	}
	lastRange := make(map[string][2]int)
	for i := range results {
		location := results[i].PrimaryLocation()
		if location == nil {
			continue
		}
		start, end, isNew, ok := ResolveEnclosingFunctionRange(provider, location.URI, location.Range.Start.Line, lastRange)
		if ok && isNew {
			results[i].FuncStart = start
			results[i].FuncEnd = end
		}
	}
}

// ResolveEnclosingFunctionRange 解析 URI 对应文件中包含目标行的函数范围。
// lastRange 用于跨调用记录最近范围，避免重复展示同一个函数边界。
func ResolveEnclosingFunctionRange(provider SymbolProvider, uri string, zeroBasedLine int, lastRange map[string][2]int) (startLine, endLine int, isNew, ok bool) {
	if provider == nil {
		return 0, 0, false, false
	}
	absPath, err := AbsolutePathFromURI(uri)
	if err != nil {
		return 0, 0, false, false
	}
	symbols, err := provider.Symbols(absPath)
	if err != nil {
		return 0, 0, false, false
	}
	startLine, endLine, ok = FindEnclosingFunction(symbols, zeroBasedLine)
	if !ok {
		return 0, 0, false, false
	}
	cur := [2]int{startLine, endLine}
	prev, seen := lastRange[absPath]
	isNew = !seen || prev != cur
	if isNew {
		lastRange[absPath] = cur
	}
	return startLine, endLine, isNew, true
}

// AbsolutePathFromURI 将 file URI 或绝对路径规范化为本机绝对路径。
// 空路径、非法 URI 或非 file scheme 会返回错误，调用方不得静默兜底。
func AbsolutePathFromURI(uri string) (string, error) {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return "", fmt.Errorf("file URI is required")
	}
	if filepath.IsAbs(trimmed) {
		return platformshared.NormalizeAbsolutePath(trimmed)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse file URI: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "file") {
		return "", fmt.Errorf("unsupported URI scheme: %s", parsed.Scheme)
	}
	path := parsed.Path
	if parsed.Host != "" {
		path = "//" + parsed.Host + path
	}
	if unescaped, err := url.PathUnescape(path); err == nil && unescaped != "" {
		path = unescaped
	}
	return platformshared.NormalizeAbsolutePath(path)
}

func findEnclosing(symbols []protocol.DocumentSymbol, zeroBasedLine int) (startLine, endLine int, ok bool) {
	for i := range symbols {
		startLine, endLine, ok = findInSymbol(symbols[i], zeroBasedLine)
		if ok {
			return startLine, endLine, true
		}
	}
	return 0, 0, false
}

// findInSymbol 在单个符号及其子树中查找包含目标行的函数或方法。
// 非函数/方法符号只作为递归容器使用，不作为结果返回。
func findInSymbol(symbol protocol.DocumentSymbol, zeroBasedLine int) (startLine, endLine int, ok bool) {
	startLine, endLine, ok = documentSymbolBounds(symbol)
	if !ok || zeroBasedLine < startLine || zeroBasedLine > endLine {
		return 0, 0, false
	}
	if childStart, childEnd, childOK := findEnclosing(symbol.Children, zeroBasedLine); childOK {
		return childStart, childEnd, true
	}
	if symbol.Kind != protocol.SymbolKindFunction && symbol.Kind != protocol.SymbolKindMethod {
		return 0, 0, false
	}
	return startLine, endLine, true
}

// documentSymbolBounds 提取文档符号的有效 0-based 行范围。
// LSP 结束列为 0 时表示上一行结束，这里会修正为展示可读的闭区间。
func documentSymbolBounds(symbol protocol.DocumentSymbol) (startLine, endLine int, ok bool) {
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
