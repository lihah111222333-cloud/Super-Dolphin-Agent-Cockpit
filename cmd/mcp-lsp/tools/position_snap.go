package tools

import (
	"strings"
	"unicode"
)

const (
	nearbyIdentifierLineWindow       = 3
	maxNearbyIdentifierSuggestionNum = 8
	positionKeywords                 = " def class func fn function var let const val type struct interface enum trait impl package namespace module import from export as async await public private protected static final override mut pub inline virtual abstract extern default return yield if else elif for while loop match switch case break continue goto throw try catch finally except raise with defer go select nil null true false none self this super new delete sizeof typeof instanceof in is not and or "
	positionDeclarationKeywords      = " def class func fn function type struct enum interface trait const let var val "
)

type identifierInfo struct {
	name     string
	startCol int
	endCol   int
}

type positionQuoteState struct {
	single bool
	double bool
}

// findCommentStartColumn 返回未被引号包围的注释起始一基列。
func findCommentStartColumn(_ string, runes []rune) int {
	state := positionQuoteState{}
	for index, value := range runes {
		if state.consume(value, index > 0 && runes[index-1] == '\\') || state.inside() {
			continue
		}
		if isPositionCommentStart(runes, index) {
			return index + 1
		}
	}
	return -1
}

// consume 更新引号状态并报告当前字符是否被引号消费。
func (state *positionQuoteState) consume(value rune, escaped bool) bool {
	if escaped {
		return false
	}
	switch value {
	case '\'':
		if !state.double {
			state.single = !state.single
			return true
		}
	case '"':
		if !state.single {
			state.double = !state.double
			return true
		}
	}
	return false
}

func (state positionQuoteState) inside() bool {
	return state.single || state.double
}

// isPositionCommentStart 判断指定位置是否是支持的行内注释起点。
func isPositionCommentStart(runes []rune, index int) bool {
	value := runes[index]
	if value == '#' {
		return true
	}
	if index+1 >= len(runes) {
		return false
	}
	next := runes[index+1]
	return (value == '/' && (next == '/' || next == '*')) || (value == '-' && next == '-')
}

// suggestedIdentifierColumns 扫描行文本，返回标识符起始列位置建议列表。
func suggestedIdentifierColumns(lineText string) []map[string]any {
	identifiers := scanPositionIdentifiers(lineText)
	suggestions := make([]map[string]any, 0, len(identifiers))
	for _, identifier := range identifiers {
		suggestions = append(suggestions, map[string]any{
			"identifier": identifier.name,
			"column":     identifier.startCol,
		})
	}
	return suggestions
}

func scanPositionIdentifiers(lineText string) []identifierInfo {
	runes := []rune(lineText)
	identifiers := make([]identifierInfo, 0)
	for index := 0; index < len(runes); {
		if !isPositionIdentifierStart(runes[index]) {
			index++
			continue
		}
		start := index
		index++
		for index < len(runes) && isPositionIdentifierPart(runes[index]) {
			index++
		}
		identifiers = append(identifiers, identifierInfo{
			name:     string(runes[start:index]),
			startCol: start + 1,
			endCol:   index + 1,
		})
	}
	return identifiers
}

func isPositionIdentifierStart(value rune) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isPositionIdentifierPart(value rune) bool {
	return isPositionIdentifierStart(value) || value >= '0' && value <= '9'
}

func isPositionKeyword(name string) bool {
	return strings.Contains(positionKeywords, " "+strings.ToLower(name)+" ")
}

// findPrimaryIdentifier 选择声明或赋值语句的主标识符，找不到时保留首个候选。
func findPrimaryIdentifier(lineText string, candidates []identifierInfo) (identifierInfo, bool) {
	if len(candidates) == 0 {
		return identifierInfo{}, false
	}
	targetName := declaredPositionIdentifier(lineText)
	for _, candidate := range candidates {
		if candidate.name == targetName {
			return candidate, true
		}
	}
	return candidates[0], true
}

func declaredPositionIdentifier(lineText string) string {
	identifiers := scanPositionIdentifiers(lineText)
	for index, identifier := range identifiers {
		if !strings.Contains(positionDeclarationKeywords, " "+strings.ToLower(identifier.name)+" ") {
			continue
		}
		if identifier.name == "func" {
			return positionFunctionName(lineText, identifiers, index)
		}
		if index+1 < len(identifiers) {
			return identifiers[index+1].name
		}
	}
	return positionAssignmentName(lineText, identifiers)
}

// positionFunctionName 从函数声明或 Go receiver 声明中解析函数名。
func positionFunctionName(lineText string, identifiers []identifierInfo, keywordIndex int) string {
	next := keywordIndex + 1
	if next >= len(identifiers) {
		return ""
	}
	start := identifiers[keywordIndex].endCol - 1
	open, close := positionReceiverBounds([]rune(lineText), start)
	if open >= 0 && close > open {
		for next < len(identifiers) && identifiers[next].startCol-1 <= close {
			next++
		}
	}
	if next >= len(identifiers) {
		return ""
	}
	return identifiers[next].name
}

// positionReceiverBounds 返回函数 receiver 括号的起止 rune 索引。
func positionReceiverBounds(runes []rune, start int) (int, int) {
	open := -1
	for index := start; index < len(runes); index++ {
		if runes[index] == '(' {
			open = index
			break
		}
		if isPositionIdentifierStart(runes[index]) {
			return -1, -1
		}
	}
	if open < 0 {
		return -1, -1
	}
	depth := 0
	for index := open; index < len(runes); index++ {
		switch runes[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return open, index
			}
		}
	}
	return -1, -1
}

func positionAssignmentName(lineText string, identifiers []identifierInfo) string {
	if len(identifiers) == 0 {
		return ""
	}
	runes := []rune(lineText)
	end := identifiers[0].endCol - 1
	if end > len(runes) {
		return ""
	}
	suffix := strings.TrimSpace(string(runes[end:]))
	if strings.HasPrefix(suffix, "=") || strings.HasPrefix(suffix, ":=") {
		return identifiers[0].name
	}
	return ""
}

// snapColumnToIdentifier 根据行文本、传入列号和是否存在显式列号，自动吸附到有效标识符的 1-based rune 列号。
func snapColumnToIdentifier(lineText string, requestedCol int, hasCol bool, runes []rune) int {
	if len(runes) == 0 {
		return max(requestedCol, 1)
	}
	if hasCol && requestedCol > len(runes)+1 {
		return requestedCol
	}
	all, valid, commentStart := collectPositionCandidates(lineText, runes)
	primary, hasPrimary := findPrimaryIdentifier(lineText, valid)
	if !hasCol || requestedCol <= 0 {
		return snapImplicitColumn(primary, hasPrimary, runes)
	}
	return snapExplicitColumn(requestedCol, all, valid, commentStart, primary, hasPrimary, runes)
}

func collectPositionCandidates(lineText string, runes []rune) ([]identifierInfo, []identifierInfo, int) {
	commentStart := findCommentStartColumn(lineText, runes)
	all := scanPositionIdentifiers(lineText)
	valid := make([]identifierInfo, 0, len(all))
	for _, identifier := range all {
		inComment := commentStart > 0 && identifier.startCol >= commentStart
		if !inComment && !isPositionKeyword(identifier.name) {
			valid = append(valid, identifier)
		}
	}
	return all, valid, commentStart
}

func snapImplicitColumn(primary identifierInfo, hasPrimary bool, runes []rune) int {
	if hasPrimary {
		return primary.startCol
	}
	return firstNonSpaceColumn(runes)
}

// snapExplicitColumn 将显式列号保留或吸附到主标识符。
func snapExplicitColumn(requestedCol int, all, valid []identifierInfo, commentStart int, primary identifierInfo, hasPrimary bool, runes []rune) int {
	if identifier := identifierAtColumn(valid, requestedCol); identifier != nil {
		return requestedCol
	}
	if !hasPrimary {
		return requestedCol
	}
	if commentStart > 0 && requestedCol >= commentStart {
		return primary.startCol
	}
	if keywordAtColumn(all, requestedCol) || requestedCol <= firstNonSpaceColumn(runes) {
		return primary.startCol
	}
	return requestedCol
}

func identifierAtColumn(identifiers []identifierInfo, column int) *identifierInfo {
	for index := range identifiers {
		if column >= identifiers[index].startCol && column <= identifiers[index].endCol {
			return &identifiers[index]
		}
	}
	return nil
}

func keywordAtColumn(identifiers []identifierInfo, column int) bool {
	identifier := identifierAtColumn(identifiers, column)
	return identifier != nil && isPositionKeyword(identifier.name)
}

func firstNonSpaceColumn(runes []rune) int {
	for index, value := range runes {
		if !unicode.IsSpace(value) {
			return index + 1
		}
	}
	return 1
}
