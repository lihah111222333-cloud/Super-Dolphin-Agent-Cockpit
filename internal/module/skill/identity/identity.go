package identity

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Normalize 规范化技能。
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

// ValidateName 校验名称。
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

// RewriteFrontmatter 处理rewritefrontmatter。
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

func metaKey(line string) string {
	key, _, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(key))
}

func displayNameKey(key string) bool {
	return key == "display_name" || key == "display-name" || key == "displayname" || key == "title"
}

// Slug 处理slug。
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

// safeLegacyDisplayName 处理safelegacy显示名称。
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

func legacyDisplayRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_'
}

// CanonicalNameForAlias 为alias处理canonical名称。
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

// MatchesSkillCandidate 判断技能候选项是否匹配。
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

// Aliases 处理aliases。
func Aliases(name, displayName string) []string {
	return uniqStrings([]string{strings.TrimSpace(name), strings.TrimSpace(displayName)})
}

// AliasKey 处理alias键。
func AliasKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

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
