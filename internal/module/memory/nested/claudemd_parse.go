// Package nested 见 claudemd_sources.go。
package nested

import (
	"encoding/json"
	"os"
	"strings"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	"golang.org/x/text/unicode/norm"
)

// parseClaudeRuleContent 解析规则文件 frontmatter 和正文。
// 没有 frontmatter 时仍清理 HTML 注释并返回正文；有 frontmatter 时提取 description/globs。
func parseClaudeRuleContent(content string) (claudeRuleMetadata, string) {
	frontmatter, body, ok := parse.SplitFrontmatter(parse.StripUTF8BOM(content))
	if !ok {
		return claudeRuleMetadata{}, strings.TrimSpace(parse.StripHTMLComments(content))
	}
	metadata := parseClaudeRuleMetadata(frontmatter)
	return metadata, strings.TrimSpace(parse.StripHTMLComments(body))
}

// parseBoolEnv 解析环境变量布尔值。
// 未设置或无法识别时返回 fallback，避免环境脏值意外开启额外目录扫描。
func parseBoolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// normalizeStringSlice 规范化字符串列表。
// 它会做 NFC、空白折叠、大小写去重，保证 glob、路径和候选 key 稳定。
func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(norm.NFC.String(strings.TrimSpace(value))), " ")
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// parseScalar 解析规则 frontmatter 标量。
// 优先兼容 JSON 字符串，失败时裁剪简单引号，适配手写 markdown 元数据。
func parseScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.Trim(strings.TrimSpace(raw), "\"'")
}

// parseStringList 解析规则 frontmatter 中的路径列表。
// 支持 JSON 数组、方括号和逗号分隔格式，最终交给 normalizeStringSlice 清洗。
func parseStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return normalizeStringSlice(decoded)
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := parseScalar(part); value != "" {
			values = append(values, value)
		}
	}
	return normalizeStringSlice(values)
}
