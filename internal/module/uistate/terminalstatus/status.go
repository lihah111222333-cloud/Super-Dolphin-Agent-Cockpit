package terminalstatus

import "strings"

// Status 统一推导 turn/completed 的终态，避免 sidebar 和 timeline 对失败事件各自解释。
func Status(success bool, status, _, _ string) string {
	if normalized := strings.TrimSpace(status); normalized != "" {
		return normalized
	}
	if success {
		return "completed"
	}
	return "failed"
}
