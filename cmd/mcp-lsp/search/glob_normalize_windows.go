//go:build windows

package search

import "strings"

// platformNormalizeSearchGlob 把 Windows 路径分隔符转成 glob 分隔符，
// 同时保留 brace 元字符前的反斜杠转义。
func platformNormalizeSearchGlob(raw string) string {
	var normalized strings.Builder
	normalized.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			normalized.WriteByte(raw[i])
			continue
		}
		if i+1 < len(raw) && isBraceGlobEscape(raw[i+1]) {
			normalized.WriteByte('\\')
			normalized.WriteByte(raw[i+1])
			i++
			continue
		}
		normalized.WriteByte('/')
	}
	return normalized.String()
}

func isBraceGlobEscape(next byte) bool {
	return next == '{' || next == '}' || next == ','
}
