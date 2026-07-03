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
	jstsTopLevelPattern    = regexp.MustCompile(`^\s*(?:(?:export|declare|default)\s+)*(?:abstract\s+)?(?:async\s+)?(class|interface|type|enum|function)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	jstsVariablePattern    = regexp.MustCompile(`^\s*(?:(?:export|declare)\s+)*(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	jstsMethodPattern      = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|async|override)\s+)*(?:get\s+|set\s+)?(constructor|[A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>{}]*)?\([^)]*\)\s*(?::[^;{]+)?(?:\{|$)`)
	jstsFieldPattern       = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|readonly|override)\s+)+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::|=)`)
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

type jstsNode struct {
	children []*jstsNode
	symbol   protocol.DocumentSymbol
}

type jstsOpenNode struct {
	bodyDepth int
	node      *jstsNode
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

// fallbackEmptyLSPDocumentSymbols 在真实 LSP 返回空大纲时，为 JS/TS 文件补一层语法级符号。
// 它只在 LSP 已经被尝试且结果为空后触发，避免替代正常语言服务器结果。
func (m *manager) fallbackEmptyLSPDocumentSymbols(ref documentRef, symbols []protocol.DocumentSymbol) ([]protocol.DocumentSymbol, bool, error) {
	if len(symbols) > 0 || !isJSTSDocumentSymbolFallbackLanguage(ref.languageID) {
		return nil, false, nil
	}
	content, err := os.ReadFile(ref.absPath)
	if err != nil {
		return nil, true, err
	}
	fallback := parseJSTSSymbols(splitLines(string(content)))
	if len(fallback) == 0 {
		return nil, false, nil
	}
	return fallback, true, nil
}

// isJSTSDocumentSymbolFallbackLanguage 判断空 outline 补偿是否适用于当前 JS/TS 语言族。
func isJSTSDocumentSymbolFallbackLanguage(languageID string) bool {
	switch languageID {
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return true
	default:
		return false
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

// parseJSTSSymbols 从 JS/TS 文本提取顶层声明和 class 直属成员。
// 该 fallback 只服务 LSP 空结果场景，重点覆盖 Datafeed.ts 这类 class/type/const 结构。
func parseJSTSSymbols(lines []string) []protocol.DocumentSymbol {
	parser := jstsSymbolParser{lines: lines}
	return parser.parse()
}

type jstsSymbolParser struct {
	lines          []string
	braceDepth     int
	blockComment   bool
	templateString bool
	roots          []*jstsNode
	pendingClass   *jstsNode
	classes        []*jstsOpenNode
	methods        []*jstsOpenNode
}

// parse 逐行维护括号深度，提取顶层符号和当前 class 的直属成员。
func (p *jstsSymbolParser) parse() []protocol.DocumentSymbol {
	for lineNo, line := range p.lines {
		code := stripJSTSLineForStructure(line, &p.blockComment, &p.templateString)
		depthBefore := p.braceDepth
		if !p.tryAddClassMember(lineNo, line, code, depthBefore) {
			p.tryAddTopLevelSymbol(lineNo, line, code, depthBefore)
		}
		p.openPendingClassBody(code, depthBefore)
		p.braceDepth += countJSTSBraces(code)
		p.closeCompletedNodes(lineNo)
	}
	p.closeRemainingNodes(maxInt(0, len(p.lines)-1))
	return flattenJSTSSymbolNodes(p.roots)
}

// tryAddTopLevelSymbol 在顶层 brace 深度识别 JS/TS 声明，并把 class 节点压入待闭合栈。
// 它只接收顶层声明，避免把函数体或 class 内部语句误报成文档符号。
func (p *jstsSymbolParser) tryAddTopLevelSymbol(lineNo int, line, code string, depthBefore int) bool {
	if depthBefore != 0 {
		return false
	}
	if matches := jstsTopLevelPattern.FindStringSubmatchIndex(code); len(matches) == 6 {
		kindText := code[matches[2]:matches[3]]
		name := code[matches[4]:matches[5]]
		node := newJSTSSymbolNode(lineNo, line, name, jstsTopLevelKind(kindText), strings.Index(line, name))
		p.roots = append(p.roots, node)
		if kindText == "class" && strings.Contains(code, "{") {
			p.classes = append(p.classes, &jstsOpenNode{bodyDepth: depthBefore + 1, node: node})
		} else if kindText == "class" {
			p.pendingClass = node
		} else {
			p.pendingClass = nil
		}
		return true
	}
	if matches := jstsVariablePattern.FindStringSubmatchIndex(code); len(matches) == 4 {
		name := code[matches[2]:matches[3]]
		p.roots = append(p.roots, newJSTSSymbolNode(lineNo, line, name, protocol.SymbolKindConstant, strings.Index(line, name)))
		p.pendingClass = nil
		return true
	}
	return false
}

// tryAddClassMember 在当前 class 的直接作用域中识别方法和字段。
// 已进入方法体时必须跳过，避免把方法内部的局部函数或赋值误挂到 class 大纲下。
func (p *jstsSymbolParser) tryAddClassMember(lineNo int, line, code string, depthBefore int) bool {
	class := p.currentClassAtDepth(depthBefore)
	if class == nil || len(p.methods) > 0 {
		return false
	}
	if matches := jstsMethodPattern.FindStringSubmatchIndex(code); len(matches) == 4 {
		name := code[matches[2]:matches[3]]
		kind := protocol.SymbolKindMethod
		if name == "constructor" {
			kind = protocol.SymbolKindConstructor
		}
		node := newJSTSSymbolNode(lineNo, line, name, kind, strings.Index(line, name))
		class.node.children = append(class.node.children, node)
		if strings.Contains(code, "{") {
			p.methods = append(p.methods, &jstsOpenNode{bodyDepth: depthBefore + 1, node: node})
		}
		return true
	}
	if matches := jstsFieldPattern.FindStringSubmatchIndex(code); len(matches) == 4 {
		name := code[matches[2]:matches[3]]
		class.node.children = append(class.node.children, newJSTSSymbolNode(lineNo, line, name, protocol.SymbolKindField, strings.Index(line, name)))
		return true
	}
	return false
}

// currentClassAtDepth 返回与当前括号深度匹配的打开 class。
func (p *jstsSymbolParser) currentClassAtDepth(depth int) *jstsOpenNode {
	if len(p.classes) == 0 {
		return nil
	}
	class := p.classes[len(p.classes)-1]
	if class.bodyDepth != depth {
		return nil
	}
	return class
}

// openPendingClassBody 处理 `class Foo` 后下一行才出现 `{` 的常见 TS/JS 风格。
// class 节点先作为顶层符号保留，直到实际进入 body 后才允许成员挂载。
func (p *jstsSymbolParser) openPendingClassBody(code string, depthBefore int) {
	if p.pendingClass == nil || depthBefore != 0 {
		return
	}
	if !strings.Contains(code, "{") {
		return
	}
	p.classes = append(p.classes, &jstsOpenNode{bodyDepth: depthBefore + 1, node: p.pendingClass})
	p.pendingClass = nil
}

// closeCompletedNodes 在括号深度回落时收束方法或 class 的结束范围。
func (p *jstsSymbolParser) closeCompletedNodes(lineNo int) {
	for len(p.methods) > 0 && p.braceDepth < p.methods[len(p.methods)-1].bodyDepth {
		p.finalizeOpenNode(p.methods[len(p.methods)-1], lineNo)
		p.methods = p.methods[:len(p.methods)-1]
	}
	for len(p.classes) > 0 && p.braceDepth < p.classes[len(p.classes)-1].bodyDepth {
		p.finalizeOpenNode(p.classes[len(p.classes)-1], lineNo)
		p.classes = p.classes[:len(p.classes)-1]
	}
}

// closeRemainingNodes 在文件结束时收束未显式闭合的 fallback 符号范围。
func (p *jstsSymbolParser) closeRemainingNodes(lineNo int) {
	for len(p.methods) > 0 {
		p.finalizeOpenNode(p.methods[len(p.methods)-1], lineNo)
		p.methods = p.methods[:len(p.methods)-1]
	}
	for len(p.classes) > 0 {
		p.finalizeOpenNode(p.classes[len(p.classes)-1], lineNo)
		p.classes = p.classes[:len(p.classes)-1]
	}
}

// finalizeOpenNode 把打开符号的 range 结束点延伸到当前行。
func (p *jstsSymbolParser) finalizeOpenNode(open *jstsOpenNode, lineNo int) {
	if open == nil || open.node == nil {
		return
	}
	start := open.node.symbol.Range.Start.Line
	if lineNo < start {
		lineNo = start
	}
	open.node.symbol.Range.End = protocol.Position{
		Line:      lineNo,
		Character: lineLength(p.lines, lineNo),
	}
}

// newJSTSSymbolNode 构造一个带选择范围的 JS/TS fallback 符号节点。
func newJSTSSymbolNode(lineNo int, line, name string, kind protocol.SymbolKind, startCol int) *jstsNode {
	if startCol < 0 {
		startCol = strings.Index(line, strings.TrimSpace(name))
	}
	if startCol < 0 {
		startCol = indentWidth(line) - indentWidth(strings.TrimLeft(line, " \t"))
	}
	endCol := startCol + len(name)
	return &jstsNode{
		symbol: protocol.DocumentSymbol{
			Name:           name,
			Kind:           kind,
			Range:          newRange(lineNo, 0, lineNo, lineLength([]string{line}, 0)),
			SelectionRange: newRange(lineNo, startCol, lineNo, endCol),
		},
	}
}

// jstsTopLevelKind 将 JS/TS 声明关键字映射为 LSP SymbolKind。
func jstsTopLevelKind(kind string) protocol.SymbolKind {
	switch kind {
	case "class":
		return protocol.SymbolKindClass
	case "interface":
		return protocol.SymbolKindInterface
	case "enum":
		return protocol.SymbolKindEnum
	case "function":
		return protocol.SymbolKindFunction
	default:
		return protocol.SymbolKindVariable
	}
}

// flattenJSTSSymbolNodes 将内部树节点转换为 LSP 文档符号树。
func flattenJSTSSymbolNodes(nodes []*jstsNode) []protocol.DocumentSymbol {
	out := make([]protocol.DocumentSymbol, 0, len(nodes))
	for _, node := range nodes {
		symbol := node.symbol
		symbol.Children = flattenJSTSSymbolNodes(node.children)
		out = append(out, symbol)
	}
	return out
}

// stripJSTSLineForStructure 去掉 JS/TS 字符串和注释内容，只保留可用于 brace 计数的结构字符。
// blockComment 和 templateString 由调用方跨行保存，避免多行文本伪造符号或括号深度。
func stripJSTSLineForStructure(line string, blockComment *bool, templateString *bool) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		next, stop := consumeJSTSStructuralToken(line, i, &out, blockComment, templateString)
		if stop {
			break
		}
		i = next
	}
	return out.String()
}

// consumeJSTSStructuralToken 消费一段可见结构或被跳过的注释/字符串片段。
func consumeJSTSStructuralToken(line string, pos int, out *strings.Builder, blockComment *bool, templateString *bool) (int, bool) {
	if *blockComment {
		return consumeJSTSBlockComment(line, pos, blockComment)
	}
	if *templateString {
		next, closed := skipJSTSActiveTemplateLinePart(line, pos)
		if closed {
			*templateString = false
		}
		return next, false
	}
	if strings.HasPrefix(line[pos:], "//") {
		return len(line), true
	}
	if strings.HasPrefix(line[pos:], "/*") {
		*blockComment = true
		return pos + len("/*"), false
	}
	if isJSTSQuote(line[pos]) {
		out.WriteByte(' ')
		next, closed := skipJSTSQuotedLinePart(line, pos, line[pos])
		if line[pos] == '`' && !closed {
			*templateString = true
		}
		return next, false
	}
	out.WriteByte(line[pos])
	return pos + 1, false
}

func consumeJSTSBlockComment(line string, pos int, blockComment *bool) (int, bool) {
	end := strings.Index(line[pos:], "*/")
	if end < 0 {
		return len(line), true
	}
	*blockComment = false
	return pos + end + len("*/"), false
}

func isJSTSQuote(ch byte) bool {
	return ch == '\'' || ch == '"' || ch == '`'
}

// skipJSTSQuotedLinePart 跳过一段同一行内的字符串或模板文本。
func skipJSTSQuotedLinePart(line string, start int, quote byte) (int, bool) {
	for i := start + 1; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			continue
		}
		if line[i] == quote {
			return i + 1, true
		}
	}
	return len(line), false
}

// skipJSTSActiveTemplateLinePart 跳过跨行模板字符串的当前行片段。
func skipJSTSActiveTemplateLinePart(line string, start int) (int, bool) {
	for i := start; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			continue
		}
		if line[i] == '`' {
			return i + 1, true
		}
	}
	return len(line), false
}

// countJSTSBraces 统计一行净括号深度变化。
func countJSTSBraces(line string) int {
	depth := 0
	for _, r := range line {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
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
