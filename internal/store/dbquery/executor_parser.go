package dbquery

import (
	"errors"
	"strings"
)

// collectCTEInfo 解析 WITH 查询中的 CTE 名称并返回外层 SELECT。
// CTE 名称只作为当前查询的局部来源，后续表白名单扫描不能把它当作真实数据库表。
func collectCTEInfo(query string) (map[string]struct{}, string, error) {
	names := make(map[string]struct{})
	trimmed := strings.TrimSpace(query)
	index, ok := cteStartIndex(trimmed)
	if !ok {
		return names, trimmed, nil
	}
	for {
		name, next, err := readCTEName(trimmed, index)
		if err != nil {
			return nil, "", err
		}
		names[name] = struct{}{}
		index, err = skipCTEColumnsAndBody(trimmed, next)
		if err != nil {
			return nil, "", err
		}
		remainder, nextIndex, done, err := nextCTEQuery(trimmed, index)
		if err != nil {
			return nil, "", err
		}
		if done {
			return names, remainder, nil
		}
		index = nextIndex
	}
}

func cteStartIndex(query string) (int, bool) {
	lower := strings.ToLower(query)
	if !strings.HasPrefix(lower, "with") {
		return 0, false
	}
	index := skipSpaces(query, len("with"))
	if strings.HasPrefix(strings.ToLower(query[index:]), "recursive") {
		index = skipSpaces(query, index+len("recursive"))
	}
	return index, true
}

func readCTEName(value string, index int) (string, int, error) {
	index = skipSpaces(value, index)
	name, next, ok := readIdentifier(value, index)
	if !ok {
		return "", 0, errInvalidCTESyntax
	}
	return normalizeIdentifier(name), next, nil
}

// skipCTEColumnsAndBody 跳过 CTE 的列列表、AS 关键字和子查询正文。
// 括号或 AS 结构不完整时立即返回 errInvalidCTESyntax，避免后续表扫描误读半截 SQL。
func skipCTEColumnsAndBody(value string, index int) (int, error) {
	index = skipSpaces(value, index)
	if index < len(value) && value[index] == '(' {
		next, err := skipBalanced(value, index)
		if err != nil {
			return 0, err
		}
		index = skipSpaces(value, next)
	}
	if !strings.HasPrefix(strings.ToLower(value[index:]), "as") {
		return 0, errInvalidCTESyntax
	}
	index = skipSpaces(value, index+len("as"))
	index = skipMaterialized(value, index)
	if index >= len(value) || value[index] != '(' {
		return 0, errInvalidCTESyntax
	}
	return skipBalanced(value, index)
}

func nextCTEQuery(value string, index int) (string, int, bool, error) {
	index = skipSpaces(value, index)
	if index >= len(value) {
		return "", 0, false, errInvalidCTESyntax
	}
	if value[index] != ',' {
		return strings.TrimSpace(value[index:]), 0, true, nil
	}
	return "", index + 1, false, nil
}

func rowValues(fields []string, values []any) map[string]any {
	row := make(map[string]any, len(fields))
	for index, field := range fields {
		if index >= len(values) {
			break
		}
		row[field] = values[index]
	}
	return row
}

func skipSpaces(value string, index int) int {
	for index < len(value) {
		switch value[index] {
		case ' ', '\t', '\n', '\r':
			index++
		default:
			return index
		}
	}
	return index
}

func skipMaterialized(value string, index int) int {
	lower := strings.ToLower(value[index:])
	switch {
	case strings.HasPrefix(lower, "not materialized"):
		return skipSpaces(value, index+len("not materialized"))
	case strings.HasPrefix(lower, "materialized"):
		return skipSpaces(value, index+len("materialized"))
	default:
		return index
	}
}

// readIdentifier 读取普通或双引号包裹的 SQL 标识符。
// 返回的 next 指向标识符之后，调用方会继续判断点号限定名和表函数形态。
func readIdentifier(value string, index int) (string, int, bool) {
	if index >= len(value) {
		return "", index, false
	}
	if value[index] == '"' {
		for next := index + 1; next < len(value); next++ {
			if value[next] == '"' {
				return value[index : next+1], next + 1, true
			}
		}
		return "", index, false
	}
	if !isIdentifierStart(value[index]) {
		return "", index, false
	}
	next := index + 1
	for next < len(value) && isIdentifierPart(value[next]) {
		next++
	}
	return value[index:next], next, true
}

func skipBalanced(value string, index int) (int, error) {
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	for ; index < len(value); index++ {
		next, handled := advanceBalancedQuote(value, index, &inSingleQuote, &inDoubleQuote)
		if handled {
			index = next
			continue
		}
		var done bool
		depth, done = matchParen(value[index], depth)
		if done {
			return index + 1, nil
		}
	}
	return 0, errors.New("dbquery query has unbalanced parentheses")
}

// advanceBalancedQuote 推进括号扫描中的单引号和双引号状态。
// 引号内的括号不参与深度计算，防止字符串字面量破坏 CTE 子查询边界。
func advanceBalancedQuote(value string, index int, inSingleQuote, inDoubleQuote *bool) (int, bool) {
	ch := value[index]
	switch {
	case *inSingleQuote:
		if ch != '\'' {
			return index, true
		}
		if index+1 < len(value) && value[index+1] == '\'' {
			return index + 1, true
		}
		*inSingleQuote = false
		return index, true
	case *inDoubleQuote:
		if ch == '"' {
			*inDoubleQuote = false
		}
		return index, true
	case ch == '\'':
		*inSingleQuote = true
		return index, true
	case ch == '"':
		*inDoubleQuote = true
		return index, true
	default:
		return index, false
	}
}

func matchBracket(ch, open, close byte, depth int) (int, bool) {
	switch ch {
	case open:
		return depth + 1, false
	case close:
		next := depth - 1
		return next, next == 0
	default:
		return depth, false
	}
}

func matchParen(ch byte, depth int) (int, bool) {
	return matchBracket(ch, '(', ')', depth)
}

func normalizeIdentifier(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	name := parts[len(parts)-1]
	return strings.ToLower(strings.Trim(name, `"`))
}

func isIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || ch >= '0' && ch <= '9'
}
