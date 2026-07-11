package tools

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// filterListAgentSnapshots 按 state/cwd/active 规则过滤快照，并在 include_reports=false 时清空报告正文。
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

// includeAgentSnapshotInList 判断单个快照是否通过 list_agents 的状态和 cwd 过滤。
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

// listAgentsCWDFilter 解析 list_agents 的 cwd 过滤值。
// 可信工具 scope 中的 CWD 优先于用户入参，避免子 agent 越过父级工作区边界。
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

// looksLikePosixAbsolutePath 识别在非 Unix 平台也应按 POSIX 规则清理的绝对路径。
func looksLikePosixAbsolutePath(cwd string) bool {
	return strings.HasPrefix(cwd, "/")
}

// normalizeListAgentCWD 规范化 cwd 字符串，保留无法识别的相对值供后续精确比较。
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

// parseAgentStateFilter 把逗号、分号、竖线或空白分隔的状态过滤串规范成集合。
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

// inactiveAgentListState 判断快照状态是否默认从 list_agents 结果中隐藏。
func inactiveAgentListState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "stopped", "archived", "failed", "expired":
		return true
	default:
		return false
	}
}
