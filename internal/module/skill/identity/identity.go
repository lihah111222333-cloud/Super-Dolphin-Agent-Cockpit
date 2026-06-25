// Package identity 负责技能名称与显示名的规范化、校验和 slug 生成。
// 所有入口函数均为纯函数，不涉及 I/O。
package identity

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Normalize 对技能的 name 和 displayName 进行规范化。
// 优先使用已合法的 name；若 name 不合法且 displayName 为空，则尝试将 name 作为 legacy 显示名生成 slug。
// 返回 (规范化name, 规范化displayName, 是否成功)。
func Normalize(name, displayName string) (string, string, bool) {
	displayName, ok := normalizeDisplayName(displayName)
	if !ok {
		return "", "", false
	}
	if normalized, ok := ValidateName(name); ok {
		return normalized, displayName, true
	}
	if displayName != "" {
		return "", "", false
	}
	legacyDisplay := strings.TrimSpace(name)
	if !safeLegacyDisplayName(legacyDisplay) {
		return "", "", false
	}
	name = Slug(legacyDisplay)
	if _, ok := ValidateName(name); !ok {
		return "", "", false
	}
	return name, legacyDisplay, true
}

// ValidateName 校验技能名称是否合法：不超过 64 个字符，首字符为字母或数字，仅含字母、数字、-、_。
// 返回去除首尾空格后的名称和是否合法。
func ValidateName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", false
	}
	runes := []rune(name)
	if !unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0]) {
		return "", false
	}
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return "", false
	}
	return name, true
}

// RewriteFrontmatter 将技能文件内容中的 frontmatter name/display_name 字段替换为给定值。
// 若 name 不合法或 frontmatter 格式不符则返回 false。
func RewriteFrontmatter(content, name, displayName string) (string, bool) {
	name, ok := ValidateName(name)
	if !ok {
		return "", false
	}
	frontmatter, body, ok := splitFrontmatter(content)
	if !ok {
		return "", false
	}
	lines := rewriteIdentityLines(frontmatter, name, strings.TrimSpace(displayName))
	return "---\n" + strings.Join(lines, "\n") + "\n---\n" + body, true
}

// splitFrontmatter 将技能文件内容拆分为 frontmatter 部分和 body 部分。
// 要求内容以 "---\n" 开头，否则 ok 返回 false，body 为原始内容。
func splitFrontmatter(content string) (string, string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	frontmatter, tail, ok := strings.Cut(content[4:], "\n---")
	if !ok {
		return "", content, false
	}
	return frontmatter, strings.TrimPrefix(tail, "\n"), true
}

// rewriteIdentityLines 将 frontmatter 行列表中的 name 字段替换为给定值，并按需插入 display_name。
func rewriteIdentityLines(frontmatter, name, displayName string) []string {
	lines := strings.Split(frontmatter, "\n")
	wroteName := false
	for i, line := range lines {
		if metaKey(line) != "name" {
			continue
		}
		lines[i] = "name: " + name
		wroteName = true
		break
	}
	if !wroteName {
		lines = append([]string{"name: " + name}, lines...)
	}
	if displayName == "" {
		return lines
	}
	return upsertDisplayName(lines, displayName)
}

// upsertDisplayName 在行列表中更新或插入 display_name 字段。
func upsertDisplayName(lines []string, displayName string) []string {
	line := `display_name: "` + strings.ReplaceAll(displayName, `"`, `\"`) + `"`
	for i, raw := range lines {
		if displayNameKey(metaKey(raw)) {
			lines[i] = line
			return lines
		}
	}
	return append([]string{lines[0], line}, lines[1:]...)
}

// metaKey 从 frontmatter 行中提取小写 key，不含冒号之后的内容。
func metaKey(line string) string {
	key, _, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(key))
}

// displayNameKey 判断给定 key 是否表示 display_name 字段（支持多种写法）。
func displayNameKey(key string) bool {
	return key == "display_name" || key == "display-name" || key == "displayname" || key == "title"
}

// Slug 将任意字符串转换为合法的技能名称 slug（小写字母数字加连字符）。
// 若结果为空则返回 "skill"。
func Slug(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "skill"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case lastDash:
		default:
			b.WriteRune('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "skill"
	}
	return slug
}

// normalizeDisplayName 对显示名做基本清洗：去空格、长度不超过 120、不含控制字符。
func normalizeDisplayName(displayName string) (string, bool) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return "", true
	}
	if utf8.RuneCountInString(displayName) > 120 {
		return "", false
	}
	for _, r := range displayName {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return displayName, true
}

// safeLegacyDisplayName 判断字符串是否可作为 legacy 显示名（含空格、仅用字母数字空格连字符下划线）。
func safeLegacyDisplayName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return false
	}
	runes := []rune(name)
	if !unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0]) {
		return false
	}
	for _, r := range runes {
		if !legacyDisplayRune(r) {
			return false
		}
	}
	return strings.Contains(name, " ")
}

// legacyDisplayRune 判断字符是否为 legacy 显示名允许的字符（字母、数字、空格、-、_）。
func legacyDisplayRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_'
}

// CanonicalNameForAlias 在技能列表中查找与给定别名匹配的 canonical name。
// 匹配规则基于 AliasKey 的大小写不敏感比较。
func CanonicalNameForAlias(name string, skills []contract.SkillInfo) (string, bool) {
	key := AliasKey(name)
	if key == "" {
		return "", false
	}
	for _, skill := range skills {
		for _, alias := range Aliases(skill.Name, skill.DisplayName) {
			if AliasKey(alias) == key {
				return strings.TrimSpace(skill.Name), true
			}
		}
	}
	return "", false
}

// MatchesSkillCandidate 判断技能是否与给定的原始名或 canonical 名匹配，基于别名键的大小写不敏感比较。
func MatchesSkillCandidate(skill contract.SkillInfo, raw, canonical string) bool {
	rawKey, canonicalKey := AliasKey(raw), AliasKey(canonical)
	for _, alias := range Aliases(skill.Name, skill.DisplayName) {
		aliasKey := AliasKey(alias)
		if aliasKey != "" && (aliasKey == rawKey || aliasKey == canonicalKey) {
			return true
		}
	}
	return false
}

// Aliases 返回技能的所有有效别名（name 和 displayName 去重后的列表）。
func Aliases(name, displayName string) []string {
	return uniqStrings([]string{strings.TrimSpace(name), strings.TrimSpace(displayName)})
}

// AliasKey 将别名转换为用于比较的规范化键（小写、去空格）。
func AliasKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// uniqStrings 对字符串列表去重，保留首次出现的值，空键跳过。
func uniqStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := AliasKey(value)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(value))
	}
	return out
}
