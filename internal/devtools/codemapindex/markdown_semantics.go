package codemapindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type markdownTarget struct {
	path     string
	fragment string
}

// validateMarkdownLinks 校验相对 Markdown 链接存在且不会提升历史资料。
func validateMarkdownLinks(root string, doc SemanticMarkdown, policy codemapPolicy) []string {
	var problems []string
	for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
		line := doc.Lines[lineIndex]
		problems = append(problems, validateInlineMarkdownLinks(root, doc.File, lineIndex+1, line, policy)...)
		if match := referenceLinkRe.FindStringSubmatch(line); len(match) == 2 {
			if problem := validateMarkdownLink(root, doc.File, lineIndex+1, match[1], policy); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	return problems
}

// validateInlineMarkdownLinks 校验一行中的 inline Markdown 链接。
func validateInlineMarkdownLinks(root, codemapFile string, lineNumber int, line string, policy codemapPolicy) []string {
	var problems []string
	for _, match := range markdownLinkRe.FindAllStringSubmatchIndex(line, -1) {
		if match[0] > 0 && isMarkdownIdentifierByte(line[match[0]-1]) {
			continue
		}
		target := line[match[2]:match[3]]
		if problem := validateMarkdownLink(root, codemapFile, lineNumber, target, policy); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems
}

// isMarkdownIdentifierByte 排除 Go 泛型调用 T](arg) 等伪 Markdown 链接。
func isMarkdownIdentifierByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

// validateMarkdownLink 校验单个相对 Markdown 链接。
func validateMarkdownLink(root, codemapFile string, lineNumber int, raw string, policy codemapPolicy) string {
	target := splitMarkdownTarget(raw)
	if target == nil {
		return ""
	}
	relative, err := markdownTargetRelative(root, codemapFile, target.path)
	if err != nil {
		return fmt.Sprintf("%s:%d markdown link escapes repository: %s", codemapFile, lineNumber, strings.Trim(raw, "<>"))
	}
	absolute, err := resolveRepoPath(root, relative)
	if err != nil {
		return fmt.Sprintf("%s:%d invalid markdown link target %s: %v", codemapFile, lineNumber, relative, err)
	}
	if isHistoricalDocumentPath(relative, policy) {
		return fmt.Sprintf("%s:%d historical document cannot be a codemap authority: %s", codemapFile, lineNumber, relative)
	}
	if _, err := os.Stat(absolute); err != nil {
		return fmt.Sprintf("%s:%d missing markdown link target: %s", codemapFile, lineNumber, relative)
	}
	if target.fragment != "" && !markdownFragmentExists(absolute, target.fragment) {
		return fmt.Sprintf("%s:%d missing markdown fragment #%s in %s", codemapFile, lineNumber, target.fragment, relative)
	}
	return ""
}

// splitMarkdownTarget 拆分路径和 fragment；外部链接不进入本地校验。
func splitMarkdownTarget(raw string) *markdownTarget {
	trimmed := strings.Trim(raw, "<>")
	if trimmed == "" || strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "mailto:") {
		return nil
	}
	parts := strings.SplitN(trimmed, "#", 2)
	target := &markdownTarget{path: parts[0]}
	if len(parts) == 2 {
		target.fragment = parts[1]
	}
	return target
}

// markdownTargetRelative 把 codemap 相对链接安全换算成仓库相对路径。
func markdownTargetRelative(root, codemapFile, linkPath string) (string, error) {
	base := filepath.Dir(filepath.Join(root, "docs", "doc", "codemap", codemapFile))
	absolute := filepath.Clean(filepath.Join(base, filepath.FromSlash(linkPath)))
	if linkPath == "" {
		absolute = filepath.Join(base, codemapFile)
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || escapesRoot(relative) {
		return "", fmt.Errorf("path escapes repository")
	}
	return filepath.ToSlash(relative), nil
}

// markdownFragmentExists 校验本地 Markdown 标题锚点。
func markdownFragmentExists(path, fragment string) bool {
	lines, err := readLines(path)
	if err != nil {
		return false
	}
	seen := make(map[string]int)
	for _, line := range lines {
		slug, ok := headingSlug(line)
		if !ok {
			continue
		}
		count := seen[slug]
		seen[slug]++
		if count > 0 {
			slug += "-" + strconv.Itoa(count)
		}
		if slug == fragment {
			return true
		}
	}
	return false
}

// headingSlug 提取 Markdown 标题并生成 GitHub 风格锚点。
func headingSlug(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	return markdownHeadingSlug(title), true
}

// markdownHeadingSlug 生成代码地图内部标题使用的 GitHub 风格 slug。
func markdownHeadingSlug(title string) string {
	var builder strings.Builder
	pendingDash := false
	for _, value := range strings.ToLower(title) {
		if unicode.IsSpace(value) {
			pendingDash = builder.Len() > 0
			continue
		}
		if !isSlugRune(value) {
			continue
		}
		if pendingDash {
			builder.WriteByte('-')
			pendingDash = false
		}
		builder.WriteRune(value)
	}
	return strings.Trim(builder.String(), "-")
}

// isSlugRune 判断标题字符是否保留在锚点中。
func isSlugRune(value rune) bool {
	return value == '-' || value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}
