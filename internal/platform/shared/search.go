package shared

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// LineMatcher 保存文本或正则行匹配器，并返回 rune 级起始列。
type LineMatcher struct {
	needle        string
	regex         *regexp.Regexp
	caseSensitive bool
}

// NewLineMatcher 根据查询模式创建行匹配器，空查询和非法正则会 fail-fast 返回错误。
func NewLineMatcher(query string, regexMode, caseSensitive bool) (LineMatcher, error) {
	needle := strings.TrimSpace(query)
	if needle == "" {
		return LineMatcher{}, errors.New("query is required")
	}
	if regexMode {
		if !caseSensitive {
			needle = "(?i)" + needle
		}
		re, err := regexp.Compile(needle)
		if err != nil {
			return LineMatcher{}, err
		}
		return LineMatcher{regex: re, caseSensitive: caseSensitive}, nil
	}
	if caseSensitive {
		return LineMatcher{needle: needle, caseSensitive: true}, nil
	}
	return LineMatcher{needle: strings.ToLower(needle)}, nil
}

// Find 在单行文本中查找匹配位置，返回值按 rune 计数以适配 UI 列号。
func (m LineMatcher) Find(line string) (int, bool) {
	if m.regex != nil {
		loc := m.regex.FindStringIndex(line)
		if loc == nil {
			return 0, false
		}
		return utf8.RuneCountInString(line[:loc[0]]), true
	}
	haystack := line
	needle := m.needle
	if !m.caseSensitive {
		haystack = strings.ToLower(line)
	}
	idx := strings.Index(haystack, needle)
	if idx < 0 {
		return 0, false
	}
	return utf8.RuneCountInString(line[:idx]), true
}
