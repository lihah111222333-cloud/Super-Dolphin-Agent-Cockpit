package dreamexec

import (
	"errors"
	"strings"
)

// StripJSONFences 移除 ```json ... ``` / ``` ... ``` 包裹。
// 容忍 fence 前后空白、CRLF。fence 不存在时原样返回（仅 trim 边缘空白）。
// 不处理嵌套 fence — LLM 输出 JSON 内嵌 ``` 的概率极低，遇到再扩展。
func StripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// 去掉首行 ```（可能带 json/JSON/jsonc/任意语言标签）
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[idx+1:]
	} else {
		// 单行 ``` 无 newline，无 body
		return ""
	}
	// 去掉尾部 ``` （可能带尾部空白或 newline）
	s = strings.TrimRight(s, " \t\r\n")
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}
	return strings.TrimSpace(s)
}

// ExtractFirstJSONObject 从字符串里提取第一个 balanced { ... } 对象。
// 用于 LLM 输出夹 prose preamble / 多个对象的情况。
// 字符串内的 { } 和转义按 JSON 词法处理（识别字符串、转义符）。
// 找不到则返回错误。
func ExtractFirstJSONObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", errors.New("no JSON object found (no '{' in input)")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", errors.New("unbalanced JSON object (missing '}')")
}
