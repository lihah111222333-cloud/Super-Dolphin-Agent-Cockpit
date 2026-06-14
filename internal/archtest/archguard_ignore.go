package archtest

import (
	"go/ast"
	"go/token"
	"strings"
)

const archGuardIgnorePrefix = "archguard:ignore"

type archGuardIgnores map[int]map[string]struct{}

// collectArchGuardIgnores 收集arch守卫ignores。
func collectArchGuardIgnores(fset *token.FileSet, node *ast.File) archGuardIgnores {
	ignores := archGuardIgnores{}
	for _, group := range node.Comments {
		for _, comment := range group.List {
			metrics := parseArchGuardIgnoreMetrics(comment.Text)
			if len(metrics) == 0 {
				continue
			}
			line := fset.Position(comment.End()).Line
			ignores.add(line, metrics...)
			ignores.add(line+1, metrics...)
		}
	}
	return ignores
}

// parseArchGuardIgnoreMetrics 解析arch守卫ignore指标。
func parseArchGuardIgnoreMetrics(text string) []string {
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "//"), "/*"))
	text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
	idx := strings.Index(text, archGuardIgnorePrefix)
	if idx < 0 {
		return nil
	}
	fields := strings.Fields(text[idx:])
	if len(fields) < 2 || fields[0] != archGuardIgnorePrefix {
		return nil
	}
	parts := strings.Split(fields[1], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		metric := strings.TrimSpace(part)
		if metric != "" {
			out = append(out, metric)
		}
	}
	return out
}

func (i archGuardIgnores) add(line int, metrics ...string) {
	if line <= 0 {
		return
	}
	set := i[line]
	if set == nil {
		set = map[string]struct{}{}
		i[line] = set
	}
	for _, metric := range metrics {
		set[metric] = struct{}{}
	}
}

func (i archGuardIgnores) has(line int, metric string) bool {
	if line <= 0 {
		return false
	}
	set := i[line]
	if _, ok := set[metric]; ok {
		return true
	}
	_, ok := set["all"]
	return ok
}
