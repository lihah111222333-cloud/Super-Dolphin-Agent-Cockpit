package codemapindex

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SemanticMarkdown 是语义校验器所需的最小代码地图输入。
type SemanticMarkdown struct {
	File  string
	Lines []string
}

var (
	markdownLinkRe          = regexp.MustCompile(`\[[^]]+\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	referenceLinkRe         = regexp.MustCompile(`^\s*\[[^]]+\]:\s*(\S+)`)
	inlineCodeRe            = regexp.MustCompile("`([^`\\n]+)`")
	repoFileRefRe           = regexp.MustCompile(`((?:(?:cmd|internal|pkg|scripts|frontend-app|sql|migrations|docs|test|tests)/[^` + "`" + `\s|),;:]+\.(?:jsx|tsx|mjs|cjs|json|yaml|toml|html|proto|txt|css|md|sql|js|ts|go|sh|ps1|yml)|(?:AGENTS\.md|CLAUDE\.md|Makefile|README(?:\.[A-Za-z-]+)?\.md|go\.(?:mod|sum)|package(?:-lock)?\.json|sqlc\.yaml)))(?::([0-9][0-9,*/-]*))?`)
	repoDirRefRe            = regexp.MustCompile(`((?:cmd|internal|pkg|scripts|frontend-app|sql|migrations|docs|test|tests)/[^` + "`" + `\s|),;:*{}<>]+/)`)
	repoBarePathRe          = regexp.MustCompile(`^(?:(?:cmd|internal|pkg|scripts|frontend-app|sql|migrations|docs|test|tests)/[^` + "`" + `\s|),;:*{}<>]+|(?:AGENTS\.md|CLAUDE\.md|Makefile|README(?:\.[A-Za-z-]+)?\.md|go\.(?:mod|sum)|package(?:-lock)?\.json|sqlc\.yaml))$`)
	codemapCountRe          = regexp.MustCompile(`<!--\s*codemap-count\s+path="([^"]+)"\s+kind="([^"]+)"\s*-->`)
	codemapAbsentRe         = regexp.MustCompile(`<!--\s*codemap-absent\s+path="([^"]+)"\s*-->`)
	policyRootRe            = regexp.MustCompile(`^docs/[^/]+(?:/[^/]+)*$`)
	policyDomainRe          = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	policyShardRe           = regexp.MustCompile(`^[a-z][a-z0-9-]*\.tsv$`)
	archtestPolicyPatternRe = regexp.MustCompile(`^internal/tool/\{[a-z0-9-]+(?:,[a-z0-9-]+)+\}$`)
)

// ValidateSemantics 校验编号卷中的导航、行号、计数与文档生命周期。
func ValidateSemantics(root string, docs []SemanticMarkdown) error {
	policy, err := loadCodemapPolicy(root)
	if err != nil {
		return err
	}
	var problems []string
	for _, doc := range docs {
		problems = append(problems, validateMarkdownFences(doc)...)
		problems = append(problems, validateMarkdownLinks(root, doc, policy)...)
		if doc.File != "13-archtest-boundaries.md" {
			problems = append(problems, validateInlineRepoRefs(root, doc, policy)...)
		}
		problems = append(problems, validateCodemapAbsences(root, doc)...)
		problems = append(problems, validateCodemapCounts(root, doc)...)
	}
	problems = append(problems, validateProjectMapLifecycle(root, policy)...)
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("codemap semantics:\n- %s", strings.Join(problems, "\n- "))
}

// narrativeLineIndexes 返回 Markdown 围栏代码块之外的行。
func narrativeLineIndexes(lines []string) []int {
	indexes := make([]int, 0, len(lines))
	fence := ""
	for index, line := range lines {
		delimiter := markdownFenceDelimiter(line)
		if delimiter != "" {
			if fence == "" {
				fence = delimiter
			} else if delimiter == fence {
				fence = ""
			}
			continue
		}
		if fence == "" {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// validateMarkdownFences 拒绝未闭合围栏，防止余下正文逃逸语义校验。
func validateMarkdownFences(doc SemanticMarkdown) []string {
	fence := ""
	start := 0
	for index, line := range doc.Lines {
		delimiter := markdownFenceDelimiter(line)
		if delimiter == "" {
			continue
		}
		if fence == "" {
			fence = delimiter
			start = index + 1
		} else if delimiter == fence {
			fence = ""
		}
	}
	if fence == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s:%d unclosed Markdown fence %s", doc.File, start, fence)}
}

// markdownFenceDelimiter 识别缩进后带语言标记的围栏。
func markdownFenceDelimiter(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		return "```"
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return "~~~"
	}
	return ""
}

// normalizeRepoRelative 把策略和索引中的仓库路径统一成 slash 形式。
func normalizeRepoRelative(relative string) string {
	return filepath.ToSlash(filepath.Clean(relative))
}
