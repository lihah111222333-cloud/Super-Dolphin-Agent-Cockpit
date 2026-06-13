package tools

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

// filterListAgentSnapshots 处理过滤条件list代理snapshots。
func filterListAgentSnapshots(agents []contract.AgentSnapshot, in ListAgentsInput, cwdFilter string) []contract.AgentSnapshot {
	states := parseAgentStateFilter(in.State)
	limit := in.Limit
	if limit < 0 {
		limit = 0
	}
	filtered := make([]contract.AgentSnapshot, 0, len(agents))
	for _, agent := range agents {
		if !includeAgentSnapshotInList(agent, states, in.IncludeInactive, cwdFilter) {
			continue
		}
		if !in.IncludeReports {
			agent.LastReport = ""
		}
		filtered = append(filtered, agent)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func includeAgentSnapshotInList(agent contract.AgentSnapshot, states map[string]struct{}, includeInactive bool, cwdFilter string) bool {
	agentCWD := normalizeListAgentCWD(agent.Cwd)
	if cwdFilter != "" && agentCWD != cwdFilter {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(agent.State))
	if len(states) > 0 {
		_, ok := states[state]
		return ok
	}
	return includeInactive || !inactiveAgentListState(state)
}

// listAgentsCWDFilter 列出代理工作目录过滤条件。
func listAgentsCWDFilter(ctx context.Context, inputCWD string) (string, error) {
	if scope, ok := mcpcommon.ToolScopeFromContext(ctx); ok && scope.CWD != "" {
		return normalizeListAgentCWD(scope.CWD), nil
	}
	cwd := strings.TrimSpace(inputCWD)
	if cwd == "" {
		return "", nil
	}
	if cwd != inputCWD {
		return "", fmt.Errorf("list_agents cwd must not contain surrounding whitespace")
	}
	if !filepath.IsAbs(cwd) {
		if looksLikePosixAbsolutePath(cwd) {
			return path.Clean(cwd), nil
		}
		return "", fmt.Errorf("list_agents cwd must be an absolute path")
	}
	return filepath.Clean(cwd), nil
}

func looksLikePosixAbsolutePath(cwd string) bool {
	return strings.HasPrefix(cwd, "/")
}

func normalizeListAgentCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return cwd
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	if looksLikePosixAbsolutePath(cwd) {
		return path.Clean(cwd)
	}
	return cwd
}

// parseAgentStateFilter 解析代理状态过滤条件。
func parseAgentStateFilter(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	states := make(map[string]struct{})
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' ' || r == '\t' || r == '\n'
	}) {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			states[part] = struct{}{}
		}
	}
	return states
}

func inactiveAgentListState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "stopped", "archived", "failed", "expired":
		return true
	default:
		return false
	}
}
