package hooks

import (
	"sort"
	"strings"
)

func newToolSet(tools []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool = strings.TrimSpace(tool); tool != "" {
			set[tool] = struct{}{}
		}
	}
	return set
}

func intersectToolSet(current map[string]struct{}, tools []string) map[string]struct{} {
	next := make(map[string]struct{})
	for _, tool := range tools {
		if tool = strings.TrimSpace(tool); tool == "" {
			continue
		}
		if _, ok := current[tool]; ok {
			next[tool] = struct{}{}
		}
	}
	return next
}

func sortedTools(set map[string]struct{}) []string {
	tools := make([]string, 0, len(set))
	for tool := range set {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	if len(tools) == 0 {
		return []string{}
	}
	return tools
}
