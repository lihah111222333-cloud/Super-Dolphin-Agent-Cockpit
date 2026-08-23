package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// ResolveLSPPosition 把用户传入的 1-based rune 列转换为 LSP UTF-16 Position，并自动吸附行内有效标识符。
// LSP 协议的 character 是 UTF-16 code unit，不能直接使用人读列号减一。
func ResolveLSPPosition(ctx context.Context, filePath string, line int, column int) (protocol.Position, error) {
	return ResolveLSPPositionWithSnap(ctx, filePath, line, column, true)
}

// ResolveLSPPositionWithSnap 把用户传入的 1-based rune 列转换为 LSP UTF-16 Position，并应用 Auto-Snap。
func ResolveLSPPositionWithSnap(ctx context.Context, filePath string, line int, column int, hasCol bool) (protocol.Position, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return protocol.Position{}, err
	}
	mapping, err := loadLinePositionMapping(filePath, line)
	if err != nil {
		return protocol.Position{}, err
	}
	snappedCol := snapColumnToIdentifier(mapping.lineText, column, hasCol, mapping.runes)
	return mapping.positionFromRuneColumn(snappedCol)
}

type linePositionMapping struct {
	lineNumber   int
	lineText     string
	allLines     []string
	runes        []rune
	utf16Offsets []int
}

func loadLinePositionMapping(filePath string, line int) (linePositionMapping, error) {
	if line <= 0 {
		return linePositionMapping{}, errors.New("line must be >= 1")
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return linePositionMapping{}, err
	}
	lines := splitNormalizedLines(string(content))
	if line > len(lines) {
		return linePositionMapping{}, newLineOutOfRangeError(line, len(lines))
	}
	lineText := lines[line-1]
	runes := []rune(lineText)
	return linePositionMapping{
		lineNumber:   line,
		lineText:     lineText,
		allLines:     lines,
		runes:        runes,
		utf16Offsets: utf16OffsetsForRunes(runes),
	}, nil
}

func utf16OffsetsForRunes(runes []rune) []int {
	offsets := make([]int, len(runes)+1)
	current := 0
	for index, value := range runes {
		offsets[index] = current
		current += utf16.RuneLen(value)
	}
	offsets[len(runes)] = current
	return offsets
}

func (m linePositionMapping) positionFromRuneColumn(column int) (protocol.Position, error) {
	if column <= 0 {
		return protocol.Position{}, errors.New("column must be >= 1")
	}
	runeIndex := column - 1
	if runeIndex > len(m.runes) {
		nearby := suggestedNearbyIdentifierColumns(m.allLines, m.lineNumber, column)
		return protocol.Position{}, newPositionOutOfRangeError(m.lineNumber, column, m.lineText, len(m.runes), len(m.runes)+1, nearby)
	}
	return protocol.Position{
		Line:      m.lineNumber - 1,
		Character: m.utf16Offsets[runeIndex],
	}, nil
}

func (m linePositionMapping) positionFromRuneIndex(runeIndex int) (protocol.Position, error) {
	if runeIndex < 0 || runeIndex > len(m.runes) {
		return protocol.Position{}, fmt.Errorf("rune index %d is outside line length %d", runeIndex, len(m.runes))
	}
	return protocol.Position{Line: m.lineNumber - 1, Character: m.utf16Offsets[runeIndex]}, nil
}

func (m linePositionMapping) runeIndexFromUTF16Character(character int) (int, error) {
	if character < 0 {
		return 0, errors.New("character must be >= 0")
	}
	for index, offset := range m.utf16Offsets {
		if offset == character {
			return index, nil
		}
		if offset > character {
			return 0, fmt.Errorf("character %d splits UTF-16 code units before rune column %d", character, index+1)
		}
	}
	return 0, fmt.Errorf("character %d is outside line UTF-16 length %d", character, m.utf16Offsets[len(m.utf16Offsets)-1])
}

// resolveFilePositionRequest 解析 pos 参数、解析路径、校验位置是否在文件范围内，并执行 Auto-Snap 校准。
func resolveFilePositionRequest(ctx context.Context, params filePositionParams) (string, protocol.Position, error) {
	filePathRaw, line, col, hasCol, err := parseFilePos(params.Pos, false)
	if err != nil {
		return "", protocol.Position{}, err
	}
	filePath, err := resolveFilePath(ctx, filePathRaw)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := ResolveLSPPositionWithSnap(ctx, filePath, line, col, hasCol)
	if err != nil {
		return "", protocol.Position{}, err
	}
	return filePath, position, nil
}

// parsePos 解析 pos（file:line 或 file:line:column），允许两段式并支持自动吸附。
func parsePos(pos string) (string, int, int, error) {
	filePath, line, col, _, err := parseFilePos(pos, false)
	if err != nil {
		return "", 0, 0, err
	}
	return filePath, line, col, nil
}

// parseFilePos 解析 `file:line` 或 `file:line:col` 位置参数。
// file 工具允许两段式，inspect/xref/completion 要求列号；统一解析器让模型可在工具间复用位置格式。
func parseFilePos(pos string, requireCol bool) (string, int, int, bool, error) {
	pos = strings.TrimSpace(pos)
	if pos == "" {
		return "", 0, 0, false, errors.New("position parameter 'pos' is empty; expected 'file_path:line[:column]' (example internal/foo.go:42:9)")
	}
	lastColon := strings.LastIndex(pos, ":")
	if lastColon == -1 {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; expected 'file_path:line[:column]' (example internal/foo.go:42:9)", pos)
	}
	tailStr := pos[lastColon+1:]
	tail, ok := parsePositivePosSegment(tailStr)
	if !ok {
		return "", 0, 0, false, fmt.Errorf("invalid trailing segment %q in pos %q; expected a positive integer line or column (format 'file_path:line[:column]')", tailStr, pos)
	}
	remaining := pos[:lastColon]
	secondLastColon := strings.LastIndex(remaining, ":")
	if secondLastColon == -1 {
		return parseFileLinePos(pos, remaining, tail, requireCol)
	}
	maybeLineStr := remaining[secondLastColon+1:]
	maybeLine, ok := parsePositivePosSegment(maybeLineStr)
	if !ok {
		// 倒数第二段不是数字时，把该冒号视为路径内容，例如 Windows 盘符路径。
		return parseFileLinePos(pos, remaining, tail, requireCol)
	}
	return parseFileLineColumnPos(pos, remaining[:secondLastColon], maybeLine, tail)
}

// parsePositivePosSegment 把字符串解析为正整数，失败时返回 false。
func parsePositivePosSegment(value string) (int, bool) {
	parsed, parseErr := strconv.Atoi(value)
	return parsed, parseErr == nil && parsed > 0
}

// parseFileLinePos 解析 file:line 两段式 pos。
func parseFileLinePos(pos string, rawFilePath string, line int, requireCol bool) (string, int, int, bool, error) {
	filePath := strings.TrimSpace(rawFilePath)
	if filePath == "" {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; missing file path before ':line' (example internal/foo.go:42)", pos)
	}
	if requireCol {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; expected 'file_path:line:column' (example internal/foo.go:42:9)", pos)
	}
	return filePath, line, 0, false, nil
}

// parseFileLineColumnPos 解析 file:line:col 三段式 pos。
func parseFileLineColumnPos(pos string, rawFilePath string, line int, col int) (string, int, int, bool, error) {
	filePath := strings.TrimSpace(rawFilePath)
	if filePath == "" {
		return "", 0, 0, false, fmt.Errorf("invalid pos format %q; missing file path before ':line:column' (example internal/foo.go:42:9)", pos)
	}
	return filePath, line, col, true, nil
}

// newLineOutOfRangeError 构建行号超出文件范围的 coded error，附带元信息。
func newLineOutOfRangeError(line int, lineCount int) error {
	err := common.NewCodedToolError(
		"line_out_of_range",
		fmt.Errorf("line %d is beyond end of file with %d lines", line, lineCount),
		false,
		"next: inspect the file with native cat/head tools, then retry with an existing 1-based line in pos=<file>:<line>:<col>",
	)
	var coded *common.CodedToolError
	if errors.As(err, &coded) {
		coded.Meta = map[string]any{
			"requested_line": line,
			"line_count":     lineCount,
		}
	}
	return err
}

var positionIdentifierRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

const (
	nearbyIdentifierLineWindow       = 3
	maxNearbyIdentifierSuggestionNum = 8
)

// newPositionOutOfRangeError 构建列号超出行范围的 coded error，附带建议列位置。
func newPositionOutOfRangeError(line int, column int, lineText string, lineLength int, maxColumn int, nearbySuggestions []map[string]any) error {
	err := common.NewCodedToolError(
		"position_out_of_range",
		fmt.Errorf("column %d is beyond end of line %d, max column is %d", column, line, maxColumn),
		false,
		"next: retry with pos=<file>:<line>:<col> using a column inside the target identifier or at end of line; inspect meta.line_text, meta.suggested_columns, and meta.nearby_suggested_columns",
	)
	var coded *common.CodedToolError
	if errors.As(err, &coded) {
		meta := map[string]any{
			"line":              line,
			"line_text":         lineText,
			"line_length":       lineLength,
			"max_column":        maxColumn,
			"requested_column":  column,
			"suggested_columns": suggestedIdentifierColumns(lineText),
		}
		if len(nearbySuggestions) > 0 {
			meta["nearby_suggested_columns"] = nearbySuggestions
		}
		coded.Meta = meta
	}
	return err
}

// enrichIdentifierNotFoundError 为语言服务器的光标未命中错误补充可直接重试的一基列候选。
func enrichIdentifierNotFoundError(filePath string, position protocol.Position, cause error) error {
	message := strings.ToLower(cause.Error())
	if !strings.Contains(message, "identifier not found") && !strings.Contains(message, "no identifier found") {
		return cause
	}
	mapping, err := loadLinePositionMapping(filePath, position.Line+1)
	if err != nil {
		return cause
	}
	runeIndex, err := mapping.runeIndexFromUTF16Character(position.Character)
	if err != nil {
		return cause
	}
	coded := common.NewCodedToolError(
		"identifier_not_found",
		cause,
		false,
		"next: retry with pos=<file>:<line>:<col> using a 1-based column from meta.suggested_columns",
	)
	var typed *common.CodedToolError
	if errors.As(coded, &typed) {
		typed.Meta = map[string]any{
			"line":              mapping.lineNumber,
			"line_text":         mapping.lineText,
			"requested_column":  runeIndex + 1,
			"suggested_columns": suggestedIdentifierColumns(mapping.lineText),
		}
	}
	return coded
}

// suggestedIdentifierColumns 扫描行文本，返回标识符起始列位置建议列表。
func suggestedIdentifierColumns(lineText string) []map[string]any {
	matches := positionIdentifierRE.FindAllStringIndex(lineText, -1)
	suggestions := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		identifier := lineText[match[0]:match[1]]
		suggestions = append(suggestions, map[string]any{
			"identifier": identifier,
			"column":     utf8.RuneCountInString(lineText[:match[0]]) + 1,
		})
	}
	return suggestions
}

type nearbyIdentifierSuggestion struct {
	data     map[string]any
	distance int
	line     int
	column   int
}

// suggestedNearbyIdentifierColumns 为空行或偏移行的越界位置返回附近标识符候选。
func suggestedNearbyIdentifierColumns(lines []string, currentLine int, requestedColumn int) []map[string]any {
	if len(lines) == 0 || currentLine <= 0 {
		return nil
	}
	start, end := nearbyIdentifierLineRange(currentLine, len(lines))
	candidates := collectNearbyIdentifierSuggestions(lines, currentLine, requestedColumn, start, end)
	sortNearbyIdentifierSuggestions(candidates)
	return renderNearbyIdentifierSuggestions(candidates)
}

func nearbyIdentifierLineRange(currentLine int, lineCount int) (int, int) {
	start := max(currentLine-nearbyIdentifierLineWindow, 1)
	end := min(currentLine+nearbyIdentifierLineWindow, lineCount)
	return start, end
}

func collectNearbyIdentifierSuggestions(lines []string, currentLine int, requestedColumn int, start int, end int) []nearbyIdentifierSuggestion {
	candidates := make([]nearbyIdentifierSuggestion, 0)
	for line := start; line <= end; line++ {
		if line == currentLine {
			continue
		}
		candidates = appendLineIdentifierSuggestions(candidates, lines[line-1], currentLine, line, requestedColumn)
	}
	return candidates
}

func appendLineIdentifierSuggestions(candidates []nearbyIdentifierSuggestion, lineText string, currentLine int, line int, requestedColumn int) []nearbyIdentifierSuggestion {
	for _, suggestion := range suggestedIdentifierColumns(lineText) {
		column, _ := suggestion["column"].(int)
		identifier, _ := suggestion["identifier"].(string)
		candidates = append(candidates, nearbyIdentifierSuggestion{
			data: map[string]any{
				"line":       line,
				"column":     column,
				"identifier": identifier,
				"line_text":  lineText,
			},
			distance: lineDistance(currentLine, line)*1000 + lineDistance(requestedColumn, column),
			line:     line,
			column:   column,
		})
	}
	return candidates
}

func sortNearbyIdentifierSuggestions(candidates []nearbyIdentifierSuggestion) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		if candidates[i].line != candidates[j].line {
			return candidates[i].line < candidates[j].line
		}
		return candidates[i].column < candidates[j].column
	})
}

func renderNearbyIdentifierSuggestions(candidates []nearbyIdentifierSuggestion) []map[string]any {
	if len(candidates) > maxNearbyIdentifierSuggestionNum {
		candidates = candidates[:maxNearbyIdentifierSuggestionNum]
	}
	suggestions := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		suggestions = append(suggestions, candidate.data)
	}
	return suggestions
}

func lineDistance(a int, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

var (
	declFuncPythonRE = regexp.MustCompile(`(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)`)
	declClassRE      = regexp.MustCompile(`class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	declFuncGoRE     = regexp.MustCompile(`func\s+(?:\([^)]+\)\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	declFuncJSRE     = regexp.MustCompile(`(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)`)
	declFuncRustRE   = regexp.MustCompile(`(?:pub(?:\([^)]+\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
	declTypeRE       = regexp.MustCompile(`(?:type|struct|enum|interface|trait)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	declVarRE        = regexp.MustCompile(`(?:const|let|var|val)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	declAssignRE     = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?::=|=)`)
)

type identifierInfo struct {
	name     string
	startCol int
	endCol   int
}

type quoteScanState struct {
	inSingle bool
	inDouble bool
}

// findCommentStartColumn 返回引号外第一个受支持注释标记的一基列号。
func findCommentStartColumn(runes []rune) int {
	state := quoteScanState{}
	for i, r := range runes {
		if state.consumeQuote(runes, i) {
			continue
		}
		if state.inSingle || state.inDouble {
			continue
		}
		if isCommentMarkerAt(runes, i, r) {
			return i + 1
		}
	}
	return -1
}

// consumeQuote 消费未转义的单双引号并更新扫描状态。
func (state *quoteScanState) consumeQuote(runes []rune, index int) bool {
	if index > 0 && runes[index-1] == '\\' {
		return false
	}
	switch runes[index] {
	case '\'':
		if state.inDouble {
			return false
		}
		state.inSingle = !state.inSingle
		return true
	case '"':
		if state.inSingle {
			return false
		}
		state.inDouble = !state.inDouble
		return true
	default:
		return false
	}
}

// isCommentMarkerAt 判断当前位置是否为 Python、C 系或 SQL 注释起点。
func isCommentMarkerAt(runes []rune, index int, current rune) bool {
	if current == '#' {
		return true
	}
	if index+1 >= len(runes) {
		return false
	}
	next := runes[index+1]
	return current == '/' && (next == '/' || next == '*') || current == '-' && next == '-'
}

// isCommonLanguageKeyword 判断标识符是否属于自动吸附应跳过的通用语言关键字。
func isCommonLanguageKeyword(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "def", "class", "func", "fn", "function",
		"var", "let", "const", "val", "type", "struct", "interface", "enum", "trait", "impl",
		"package", "namespace", "module", "import", "from", "export", "as",
		"async", "await", "public", "private", "protected", "static", "final", "override", "mut", "pub",
		"inline", "virtual", "abstract", "extern", "default", "return", "yield", "if", "else", "elif",
		"for", "while", "loop", "match", "switch", "case", "break", "continue", "goto", "throw",
		"try", "catch", "finally", "except", "raise", "with", "defer", "go", "select",
		"nil", "null", "true", "false", "none", "self", "this", "super", "new", "delete",
		"sizeof", "typeof", "instanceof", "in", "is", "not", "and", "or":
		return true
	default:
		return false
	}
}

// findPrimaryIdentifier 优先返回声明模式捕获的标识符，否则返回首个有效候选。
func findPrimaryIdentifier(lineText string, candidates []identifierInfo) (identifierInfo, bool) {
	if len(candidates) == 0 {
		return identifierInfo{}, false
	}
	regexes := []*regexp.Regexp{
		declFuncPythonRE,
		declClassRE,
		declFuncGoRE,
		declFuncJSRE,
		declFuncRustRE,
		declTypeRE,
		declVarRE,
		declAssignRE,
	}
	for _, re := range regexes {
		if sub := re.FindStringSubmatch(lineText); len(sub) > 1 {
			targetName := sub[1]
			for _, cand := range candidates {
				if cand.name == targetName {
					return cand, true
				}
			}
		}
	}
	return candidates[0], true
}

// snapColumnToIdentifier 根据行文本、传入列号和是否存在显式列号，自动吸附到有效标识符的 1-based rune 列号。
func snapColumnToIdentifier(lineText string, requestedCol int, hasCol bool, runes []rune) int {
	if len(runes) == 0 {
		return max(requestedCol, 1)
	}
	if hasCol && requestedCol > len(runes)+1 {
		return requestedCol
	}

	commentStartCol := findCommentStartColumn(runes)
	allIdentifiers, validCandidates := collectIdentifierCandidates(lineText, commentStartCol)
	primary, hasPrimary := findPrimaryIdentifier(lineText, validCandidates)
	if !hasCol || requestedCol <= 0 {
		return implicitIdentifierColumn(runes, primary, hasPrimary)
	}
	return snapExplicitIdentifierColumn(runes, requestedCol, commentStartCol, allIdentifiers, validCandidates, primary, hasPrimary)
}

// collectIdentifierCandidates 收集全部标识符，并过滤注释内标识符与语言关键字。
func collectIdentifierCandidates(lineText string, commentStartCol int) ([]identifierInfo, []identifierInfo) {
	matches := positionIdentifierRE.FindAllStringIndex(lineText, -1)
	allIdentifiers := make([]identifierInfo, 0, len(matches))
	validCandidates := make([]identifierInfo, 0, len(matches))
	for _, match := range matches {
		identifier := lineText[match[0]:match[1]]
		startCol := utf8.RuneCountInString(lineText[:match[0]]) + 1
		info := identifierInfo{
			name: identifier, startCol: startCol,
			endCol: utf8.RuneCountInString(lineText[:match[1]]) + 1,
		}
		allIdentifiers = append(allIdentifiers, info)
		if (commentStartCol <= 0 || startCol < commentStartCol) && !isCommonLanguageKeyword(identifier) {
			validCandidates = append(validCandidates, info)
		}
	}
	return allIdentifiers, validCandidates
}

// implicitIdentifierColumn 为未提供显式列号的请求选择声明标识符或首个非空白字符。
func implicitIdentifierColumn(runes []rune, primary identifierInfo, hasPrimary bool) int {
	if hasPrimary {
		return primary.startCol
	}
	return firstNonSpaceColumn(runes)
}

// snapExplicitIdentifierColumn 对显式列号执行候选命中、注释和关键字校准。
func snapExplicitIdentifierColumn(
	runes []rune,
	requestedCol int,
	commentStartCol int,
	allIdentifiers, validCandidates []identifierInfo,
	primary identifierInfo,
	hasPrimary bool,
) int {
	if identifierColumnContains(validCandidates, requestedCol) {
		return requestedCol
	}
	if !hasPrimary {
		return requestedCol
	}
	if commentStartCol > 0 && requestedCol >= commentStartCol {
		return primary.startCol
	}
	if keywordColumnContains(allIdentifiers, requestedCol) {
		return primary.startCol
	}
	if requestedCol <= firstNonSpaceColumn(runes) {
		return primary.startCol
	}
	return requestedCol
}

// identifierColumnContains 判断列号是否落在任一标识符范围内。
func identifierColumnContains(identifiers []identifierInfo, column int) bool {
	for _, identifier := range identifiers {
		if column >= identifier.startCol && column <= identifier.endCol {
			return true
		}
	}
	return false
}

// keywordColumnContains 判断列号是否落在语言关键字范围内。
func keywordColumnContains(identifiers []identifierInfo, column int) bool {
	for _, identifier := range identifiers {
		if column >= identifier.startCol && column <= identifier.endCol && isCommonLanguageKeyword(identifier.name) {
			return true
		}
	}
	return false
}

// firstNonSpaceColumn 返回首个非空白字符的一基列号，空行返回一。
func firstNonSpaceColumn(runes []rune) int {
	for index, value := range runes {
		if !unicode.IsSpace(value) {
			return index + 1
		}
	}
	return 1
}
