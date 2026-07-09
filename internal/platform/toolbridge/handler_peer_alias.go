package toolbridge

import "strings"

type orchestrationLegacyAlias struct {
	Canonical          string
	LegacyPeerRealName string
}

// legacyOrchestrationToolAliases 是 legacy orchestration 名称的唯一事实源。
// 旧名只用于 peer realName、deny 配置匹配和 hidden lifecycle 直连拒绝；不能作为 Codex callable alias 发布。
var legacyOrchestrationToolAliases = []orchestrationLegacyAlias{
	{Canonical: "launch_agent", LegacyPeerRealName: "orchestration_launch_agent"},
	{Canonical: "send_message", LegacyPeerRealName: "orchestration_send_message"},
	{Canonical: "stop_agent", LegacyPeerRealName: "orchestration_stop_agent"},
	{Canonical: "recover_agent", LegacyPeerRealName: "orchestration_recover_agent"},
	{Canonical: "interrupt_agent", LegacyPeerRealName: "orchestration_interrupt_agent"},
	{Canonical: "list_agents", LegacyPeerRealName: "orchestration_list_agents"},
	{Canonical: "get_agent_report", LegacyPeerRealName: "orchestration_get_agent_report"},
	{Canonical: "get_agent_reports", LegacyPeerRealName: "orchestration_get_agent_reports"},
}

// OrchestrationToolAliasDenylist 返回 orchestration 控制面的 canonical 和 legacy peer 名。
func OrchestrationToolAliasDenylist() []string {
	names := make([]string, 0, len(legacyOrchestrationToolAliases)*2)
	for _, alias := range legacyOrchestrationToolAliases {
		names = append(names, alias.Canonical)
	}
	for _, alias := range legacyOrchestrationToolAliases {
		names = append(names, alias.LegacyPeerRealName)
	}
	return nonEmptyUnique(names...)
}

// legacyLSPName 返回 LSP 工具短名对应的旧版 lsp_* 名称，供兼容 surface 暴露。
func legacyLSPName(canonical string) string {
	for legacy, short := range legacyLSPToolAliases {
		if short == canonical {
			return legacy
		}
	}
	return ""
}

// legacyOrchPeerRealName 返回 orchestration 短名对应的旧 peer realName。
func legacyOrchPeerRealName(canonical string) string {
	canonical = strings.TrimSpace(canonical)
	for _, alias := range legacyOrchestrationToolAliases {
		if alias.Canonical == canonical {
			return alias.LegacyPeerRealName
		}
	}
	return ""
}

func legacyManagedLaunchPeerRealName() string {
	return legacyOrchPeerRealName("launch_agent")
}

func canonicalOrchSurfaceName(name string) string {
	name = strings.TrimSpace(name)
	for _, alias := range legacyOrchestrationToolAliases {
		if alias.LegacyPeerRealName == name {
			return alias.Canonical
		}
	}
	return name
}

func isLegacyOrchPeerRealName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && canonicalOrchSurfaceName(name) != name
}
