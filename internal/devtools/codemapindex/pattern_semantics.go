package codemapindex

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// validateRepositoryPattern 校验 brace/glob/placeholder pattern 的真实 owner 或展开结果。
func validateRepositoryPattern(root, codemapFile string, lineNumber int, pattern string, policy codemapPolicy) []string {
	if expanded, ok := expandBracePattern(pattern); ok {
		var problems []string
		for _, value := range expanded {
			if isRepositoryPattern(value) {
				problems = append(problems, validateRepositoryPattern(root, codemapFile, lineNumber, value, policy)...)
			} else {
				problems = appendRepoRefProblem(problems, root, codemapFile, lineNumber, value, "", policy, nil)
			}
		}
		return problems
	}
	if strings.ContainsAny(pattern, "*?[") {
		return validateRepositoryGlob(root, codemapFile, lineNumber, pattern, policy)
	}
	base := placeholderPatternOwner(pattern)
	if base == "" {
		return []string{fmt.Sprintf("%s:%d invalid repository pattern: %s", codemapFile, lineNumber, pattern)}
	}
	return appendRepoRefProblem(nil, root, codemapFile, lineNumber, base, "", policy, nil)
}

// expandBracePattern 展开一个非嵌套 {a,b} 组。
func expandBracePattern(pattern string) ([]string, bool) {
	start := strings.IndexByte(pattern, '{')
	if start < 0 {
		return nil, false
	}
	endOffset := strings.IndexByte(pattern[start+1:], '}')
	if endOffset < 0 {
		return nil, false
	}
	end := start + 1 + endOffset
	alternatives := strings.Split(pattern[start+1:end], ",")
	if len(alternatives) < 2 {
		return nil, false
	}
	expanded := make([]string, 0, len(alternatives))
	for _, alternative := range alternatives {
		expanded = append(expanded, pattern[:start]+alternative+pattern[end+1:])
	}
	return expanded, len(expanded) > 0
}

// validateRepositoryGlob 要求 glob 至少命中一个真实仓库路径。
func validateRepositoryGlob(root, codemapFile string, lineNumber int, pattern string, policy codemapPolicy) []string {
	matches, err := repositoryGlobMatches(root, pattern)
	if err != nil {
		return []string{fmt.Sprintf("%s:%d invalid repository pattern %s: %v", codemapFile, lineNumber, pattern, err)}
	}
	if len(matches) == 0 {
		return []string{fmt.Sprintf("%s:%d missing repository pattern: %s", codemapFile, lineNumber, pattern)}
	}
	var problems []string
	for _, match := range matches {
		relative, err := filepath.Rel(root, match)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d invalid repository pattern match %s: %v", codemapFile, lineNumber, pattern, err))
			continue
		}
		problems = appendRepoRefProblem(problems, root, codemapFile, lineNumber, filepath.ToSlash(relative), "", policy, nil)
	}
	return problems
}

// repositoryGlobMatches 按 slash 分段匹配 glob，并让 ** 同时支持零层和多层目录。
func repositoryGlobMatches(root, pattern string) ([]string, error) {
	segments := strings.Split(filepath.ToSlash(pattern), "/")
	var matches []string
	if err := collectRepositoryGlobMatches(root, "", segments, &matches); err != nil {
		return nil, err
	}
	return matches, nil
}

// collectRepositoryGlobMatches 只遍历 pattern 可达目录，避免为每个 glob 扫描整个仓库。
func collectRepositoryGlobMatches(root, relative string, segments []string, matches *[]string) error {
	if len(segments) == 0 {
		*matches = append(*matches, filepath.Join(root, filepath.FromSlash(relative)))
		return nil
	}
	current, err := resolveRepoPath(root, relative)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(current)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if segments[0] == "**" {
		return collectDoubleStarMatches(root, relative, segments, entries, matches)
	}
	return collectSegmentMatches(root, relative, segments, entries, matches)
}

// collectDoubleStarMatches 展开 ** 的零层与多层目录分支。
func collectDoubleStarMatches(root, relative string, segments []string, entries []os.DirEntry, matches *[]string) error {
	if err := collectRepositoryGlobMatches(root, relative, segments[1:], matches); err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.ToSlash(filepath.Join(relative, entry.Name()))
		if err := collectRepositoryGlobMatches(root, child, segments, matches); err != nil {
			return err
		}
	}
	return nil
}

// collectSegmentMatches 匹配一个普通 glob segment 并继续下钻。
func collectSegmentMatches(root, relative string, segments []string, entries []os.DirEntry, matches *[]string) error {
	for _, entry := range entries {
		matched, err := path.Match(segments[0], entry.Name())
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		child := filepath.ToSlash(filepath.Join(relative, entry.Name()))
		if len(segments) == 1 {
			*matches = append(*matches, filepath.Join(root, filepath.FromSlash(child)))
			continue
		}
		if entry.IsDir() {
			if err := collectRepositoryGlobMatches(root, child, segments[1:], matches); err != nil {
				return err
			}
		}
	}
	return nil
}

// placeholderPatternOwner 返回 ... 或 <placeholder> pattern 的现存目录 owner。
func placeholderPatternOwner(pattern string) string {
	cut := len(pattern)
	for _, marker := range []string{"...", "<"} {
		if index := strings.Index(pattern, marker); index >= 0 && index < cut {
			cut = index
		}
	}
	base := strings.TrimSuffix(pattern[:cut], "/")
	if base == "" {
		return ""
	}
	if strings.Contains(filepath.Base(base), ".") {
		return filepath.ToSlash(filepath.Dir(base))
	}
	return base
}
