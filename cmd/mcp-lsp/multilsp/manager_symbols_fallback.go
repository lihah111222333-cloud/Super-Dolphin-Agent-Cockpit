package multilsp

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

var (
	markdownHeadingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	jsonKeyPattern         = regexp.MustCompile(`^(\s*)"([^"]+)"\s*:\s*(.*)$`)
	yamlKeyPattern         = regexp.MustCompile(`^(\s*)([-\s]*)?["']?([A-Za-z0-9_.-]+)["']?\s*:\s*(.*)$`)
	pythonClassPattern     = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	pythonFunctionPattern  = regexp.MustCompile(`^(\s*)(?:async\s+def|def)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pythonAssignPattern    = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(?::[^=]+)?=\s*[^=].*$`)
)

type fallbackSymbol struct {
	level    int
	name     string
	kind     protocol.SymbolKind
	line     int
	startCol int
	endCol   int
}

type fallbackNode struct {
	level    int
	children []*fallbackNode
	symbol   protocol.DocumentSymbol
}

// fallbackDocumentSymbols 为无需或不适合启动 LSP 的文档生成静态符号。
// 仅覆盖 markdown/json/yaml 和受限 Python 常量文件；其他语言返回 ok=false 继续走 LSP。
func (m *manager) fallbackDocumentSymbols(ref documentRef) ([]protocol.DocumentSymbol, bool, error) {
	switch ref.languageID {
	case "markdown", "json", "yaml", "python":
	default:
		return nil, false, nil
	}
	content, err := os.ReadFile(ref.absPath)
	if err != nil {
		return nil, true, err
	}
	lines := splitLines(string(content))
	switch ref.languageID {
	case "markdown":
		return parseMarkdownSymbols(lines), true, nil
	case "json":
		return parseJSONSymbols(lines), true, nil
	case "yaml":
		return parseYAMLSymbols(lines), true, nil
	case "python":
		if !isPythonStaticFallbackPath(ref.absPath) {
			return nil, false, nil
		}
		symbols := parsePythonSymbols(lines)
		return symbols, true, nil
	default:
		return nil, false, nil
	}
}

func parseMarkdownSymbols(lines []string) []protocol.DocumentSymbol {
	items := make([]fallbackSymbol, 0)
	for lineNo, line := range lines {
		matches := markdownHeadingPattern.FindStringSubmatchIndex(line)
		if len(matches) == 0 {
			continue
		}
		level := (matches[3] - matches[2])
		name := strings.TrimSpace(line[matches[4]:matches[5]])
		items = append(items, fallbackSymbol{
			level:    level,
			name:     name,
			kind:     markdownKind(level),
			line:     lineNo,
			startCol: matches[4],
			endCol:   matches[5],
		})
	}
	return buildLevelSymbols(lines, items)
}

func parseJSONSymbols(lines []string) []protocol.DocumentSymbol {
	items := make([]fallbackSymbol, 0)
	for lineNo, line := range lines {
		matches := jsonKeyPattern.FindStringSubmatch(line)
		if len(matches) != 4 {
			continue
		}
		indent := indentWidth(matches[1])
		name := matches[2]
		value := strings.TrimSpace(matches[3])
		items = append(items, fallbackSymbol{
			level:    indent,
			name:     name,
			kind:     jsonKind(value),
			line:     lineNo,
			startCol: indent + 1,
			endCol:   indent + 1 + len(name),
		})
	}
	return buildLevelSymbols(lines, items)
}

func parseYAMLSymbols(lines []string) []protocol.DocumentSymbol {
	items := make([]fallbackSymbol, 0)
	scanner := bufio.NewScanner(strings.NewReader(strings.Join(lines, "\n")))
	lineNo := 0
	for scanner.Scan() {
		line := scanner.Text()
		matches := yamlKeyPattern.FindStringSubmatch(line)
		if len(matches) != 5 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			lineNo++
			continue
		}
		indent := indentWidth(matches[1])
		name := matches[3]
		value := strings.TrimSpace(matches[4])
		items = append(items, fallbackSymbol{
			level:    indent,
			name:     name,
			kind:     yamlKind(value),
			line:     lineNo,
			startCol: indent,
			endCol:   indent + len(name),
		})
		lineNo++
	}
	return buildLevelSymbols(lines, items)
}

func parsePythonSymbols(lines []string) []protocol.DocumentSymbol {
	items := make([]fallbackSymbol, 0)
	tripleQuote := ""
	for lineNo, line := range lines {
		if pythonLineInTripleQuotedString(line, &tripleQuote) {
			continue
		}
		if symbol, ok := pythonLineSymbol(lineNo, line); ok {
			items = append(items, symbol)
		}
	}
	return buildLevelSymbols(lines, items)
}

func isPythonStaticFallbackPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "constant.py" || base == "constants.py" ||
		strings.HasSuffix(base, "_constant.py") || strings.HasSuffix(base, "_constants.py")
}

func pythonLineInTripleQuotedString(line string, active *string) bool {
	trimmed := strings.TrimSpace(line)
	if *active != "" {
		if strings.Contains(trimmed, *active) {
			*active = ""
		}
		return true
	}
	quote, ok := firstPythonTripleQuote(trimmed)
	if !ok {
		return false
	}
	if strings.Count(trimmed, quote)%2 == 1 {
		*active = quote
	}
	return strings.HasPrefix(trimmed, quote)
}

// firstPythonTripleQuote 找到一行里最早出现的 Python 三引号。
// 静态 Python fallback 用它跳过多行字符串，避免把字符串里的 def/class 误识别成符号。
func firstPythonTripleQuote(line string) (string, bool) {
	doubleIndex := strings.Index(line, `"""`)
	singleIndex := strings.Index(line, `'''`)
	switch {
	case doubleIndex < 0 && singleIndex < 0:
		return "", false
	case singleIndex < 0 || doubleIndex >= 0 && doubleIndex < singleIndex:
		return `"""`, true
	default:
		return `'''`, true
	}
}

func pythonLineSymbol(lineNo int, line string) (fallbackSymbol, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return fallbackSymbol{}, false
	}
	if symbol, ok := pythonClassOrFunctionSymbol(lineNo, line); ok {
		return symbol, true
	}
	if symbol, ok := pythonAssignmentSymbol(lineNo, line); ok {
		return symbol, true
	}
	return fallbackSymbol{}, false
}

func pythonClassOrFunctionSymbol(lineNo int, line string) (fallbackSymbol, bool) {
	if matches := pythonClassPattern.FindStringSubmatch(line); len(matches) == 3 {
		return pythonNamedSymbol(lineNo, line, matches[1], matches[2], protocol.SymbolKindClass), true
	}
	if matches := pythonFunctionPattern.FindStringSubmatch(line); len(matches) == 3 {
		return pythonNamedSymbol(lineNo, line, matches[1], matches[2], protocol.SymbolKindFunction), true
	}
	return fallbackSymbol{}, false
}

func pythonAssignmentSymbol(lineNo int, line string) (fallbackSymbol, bool) {
	if strings.TrimLeft(line, " \t") != line {
		return fallbackSymbol{}, false
	}
	matches := pythonAssignPattern.FindStringSubmatch(line)
	if len(matches) != 2 {
		return fallbackSymbol{}, false
	}
	return pythonNamedSymbol(lineNo, line, "", matches[1], protocol.SymbolKindVariable), true
}

func pythonNamedSymbol(lineNo int, line, indent, name string, kind protocol.SymbolKind) fallbackSymbol {
	startCol := strings.Index(line, name)
	if startCol < 0 {
		startCol = indentWidth(indent)
	}
	return fallbackSymbol{
		level:    indentWidth(indent),
		name:     name,
		kind:     kind,
		line:     lineNo,
		startCol: startCol,
		endCol:   startCol + len(name),
	}
}

// buildLevelSymbols 按缩进或标题级别把扁平 fallback 符号组装成 LSP 文档符号树。
// 每个父节点的 range 会延伸到下一个同级节点前，保持前端折叠和 read_file 范围可用。
func buildLevelSymbols(lines []string, items []fallbackSymbol) []protocol.DocumentSymbol {
	if len(items) == 0 {
		return nil
	}
	root := &fallbackNode{level: -1}
	stack := []*fallbackNode{root}
	for _, item := range items {
		node := &fallbackNode{
			level: item.level,
			symbol: protocol.DocumentSymbol{
				Name:           item.name,
				Kind:           item.kind,
				Range:          newRange(item.line, 0, item.line, lineLength(lines, item.line)),
				SelectionRange: newRange(item.line, item.startCol, item.line, item.endCol),
			},
		}
		for len(stack) > 1 && item.level <= stack[len(stack)-1].level {
			finalizeFallbackNode(stack[len(stack)-1], lines, item.line-1)
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		parent.children = append(parent.children, node)
		stack = append(stack, node)
	}
	lastLine := maxInt(0, len(lines)-1)
	for len(stack) > 1 {
		finalizeFallbackNode(stack[len(stack)-1], lines, lastLine)
		stack = stack[:len(stack)-1]
	}
	return flattenFallbackNodes(root.children)
}

func flattenFallbackNodes(nodes []*fallbackNode) []protocol.DocumentSymbol {
	out := make([]protocol.DocumentSymbol, 0, len(nodes))
	for _, node := range nodes {
		symbol := node.symbol
		symbol.Children = flattenFallbackNodes(node.children)
		out = append(out, symbol)
	}
	return out
}

func finalizeFallbackNode(node *fallbackNode, lines []string, endLine int) {
	startLine := node.symbol.Range.Start.Line
	if endLine < startLine {
		endLine = startLine
	}
	node.symbol.Range.End = protocol.Position{
		Line:      endLine,
		Character: lineLength(lines, endLine),
	}
}

func markdownKind(level int) protocol.SymbolKind {
	switch level {
	case 1:
		return protocol.SymbolKindModule
	case 2:
		return protocol.SymbolKindNamespace
	default:
		return protocol.SymbolKindString
	}
}

func jsonKind(value string) protocol.SymbolKind {
	switch {
	case strings.HasPrefix(value, "{"):
		return protocol.SymbolKindObject
	case strings.HasPrefix(value, "["):
		return protocol.SymbolKindArray
	default:
		return protocol.SymbolKindKey
	}
}

func yamlKind(value string) protocol.SymbolKind {
	switch {
	case value == "":
		return protocol.SymbolKindObject
	case strings.HasPrefix(value, "["):
		return protocol.SymbolKindArray
	default:
		return protocol.SymbolKindKey
	}
}

func newRange(startLine, startChar, endLine, endChar int) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: startLine, Character: startChar},
		End:   protocol.Position{Line: endLine, Character: endChar},
	}
}

func splitLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(content, "\n")
}

func lineLength(lines []string, idx int) int {
	if idx < 0 || idx >= len(lines) {
		return 0
	}
	return len(lines[idx])
}

func indentWidth(indent string) int {
	width := 0
	for _, r := range indent {
		if r == '\t' {
			width += 2
			continue
		}
		width++
	}
	return width
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
