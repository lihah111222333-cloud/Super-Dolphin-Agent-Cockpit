package prompt

import (
	"path/filepath"
	"strings"
)

// matchWhenStringValue 从 JSON 条件值中取字符串，类型不匹配时返回空串表示条件不成立。
func matchWhenStringValue(want any) string {
	value, ok := want.(string)
	if !ok {
		return ""
	}
	return value
}

// matchCWDGlob 用 slash 归一化后的路径执行 cwd_glob 匹配。
func matchCWDGlob(pattern string, cwd string) bool {
	if pattern == "" || cwd == "" {
		return false
	}
	matched, err := filepath.Match(filepath.ToSlash(pattern), filepath.ToSlash(cwd))
	return err == nil && matched
}

// matchCWDPrefix 用 slash 归一化后的路径执行 cwd_prefix 匹配。
func matchCWDPrefix(prefix string, cwd string) bool {
	if prefix == "" || cwd == "" {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(cwd), filepath.ToSlash(prefix))
}

// matchTagsHas 在用户提示中做大小写不敏感的子串探测。
func matchTagsHas(keyword string, userPrompt string) bool {
	if keyword == "" || userPrompt == "" {
		return false
	}
	return strings.Contains(strings.ToLower(userPrompt), strings.ToLower(keyword))
}
