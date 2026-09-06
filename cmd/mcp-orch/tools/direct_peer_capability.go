package tools

import "strings"

// LegacyDirectPeerToolNames 返回 legacy transport 必须隐藏的全部 direct-peer 工具。
func LegacyDirectPeerToolNames() []string {
	return []string{"manage_direct_peer", "send_direct_peer", "read_direct_peer", "read_direct_peer_events"}
}

// IsLegacyDirectPeerTool 判断工具是否在 legacy transport 上被阻断。
func IsLegacyDirectPeerTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "manage_direct_peer", "send_direct_peer", "read_direct_peer", "read_direct_peer_events":
		return true
	default:
		return false
	}
}

// TaskScopedDirectPeerToolNames 返回需要可信 task/agent caller scope 的 direct-peer 工具。
// operator grant management 有独立的 operator-only contract，因此不包含在内。
func TaskScopedDirectPeerToolNames() []string {
	return []string{"send_direct_peer", "read_direct_peer", "read_direct_peer_events"}
}

// IsTaskScopedDirectPeerTool 判断 standalone transport 是否需要 task attribution 才能暴露工具。
func IsTaskScopedDirectPeerTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "send_direct_peer", "read_direct_peer", "read_direct_peer_events":
		return true
	default:
		return false
	}
}
