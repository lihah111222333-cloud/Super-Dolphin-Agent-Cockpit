package kernel

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// LineMatcher matches literal or regular-expression queries against text lines.
type LineMatcher struct {
	needle        string
	regex         *regexp.Regexp
	caseSensitive bool
}

// NewLineMatcher 创建行matcher。
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

// Find 查找平台shared。
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
