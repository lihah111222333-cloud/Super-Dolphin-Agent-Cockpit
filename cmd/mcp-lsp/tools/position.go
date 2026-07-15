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
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// ResolveLSPPosition 把用户传入的 1-based rune 列转换为 LSP UTF-16 Position。
// LSP 协议的 character 是 UTF-16 code unit，不能直接使用人读列号减一。
func ResolveLSPPosition(ctx context.Context, filePath string, line int, column int) (protocol.Position, error) {
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
	return mapping.positionFromRuneColumn(column)
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

// resolveFilePositionRequest 解析 pos 参数、解析路径、校验位置是否在文件范围内。
func resolveFilePositionRequest(ctx context.Context, params filePositionParams) (string, protocol.Position, error) {
	filePathRaw, line, col, err := parsePos(params.Pos)
	if err != nil {
		return "", protocol.Position{}, err
	}
	filePath, err := resolveFilePath(ctx, filePathRaw)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := ResolveLSPPosition(ctx, filePath, line, col)
	if err != nil {
		return "", protocol.Position{}, err
	}
	return filePath, position, nil
}

// parsePos 解析三段式 pos（file:line:column），缺少 column 时报错。
func parsePos(pos string) (string, int, int, error) {
	filePath, line, col, hasCol, err := parseFilePos(pos, true)
	if err != nil {
		return "", 0, 0, err
	}
	if !hasCol {
		return "", 0, 0, fmt.Errorf("invalid pos format %q; expected 'file_path:line:column' (example internal/foo.go:42:9)", pos)
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
		"next: file action=read_file pos=<file>:1 limit=200, then retry with an existing 1-based line in pos=<file>:<line>:<col>",
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
			"column":     match[0] + 1,
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
