package toolbridge

import "strings"

type toolAlias struct {
	Canonical string
	Legacy    string
}

var orchestrationToolAliasRegistry = []toolAlias{
	{Canonical: "launch_agent", Legacy: "orchestration_launch_agent"},
	{Canonical: "send_message", Legacy: "orchestration_send_message"},
	{Canonical: "stop_agent", Legacy: "orchestration_stop_agent"},
	{Canonical: "recover_agent", Legacy: "orchestration_recover_agent"},
	{Canonical: "interrupt_agent", Legacy: "orchestration_interrupt_agent"},
	{Canonical: "list_agents", Legacy: "orchestration_list_agents"},
	{Canonical: "get_agent_report", Legacy: "orchestration_get_agent_report"},
	{Canonical: "get_agent_reports", Legacy: "orchestration_get_agent_reports"},
}

// OrchestrationToolAliasDenylist 返回 orchestration 控制面的 canonical 和 legacy 名。
func OrchestrationToolAliasDenylist() []string {
	names := make([]string, 0, len(orchestrationToolAliasRegistry)*2)
	for _, alias := range orchestrationToolAliasRegistry {
		names = append(names, alias.Canonical)
	}
	for _, alias := range orchestrationToolAliasRegistry {
		names = append(names, alias.Legacy)
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

// legacyOrchName 返回 orchestration 短名对应的旧版 orchestration_* 名称。
func legacyOrchName(canonical string) string {
	canonical = strings.TrimSpace(canonical)
	for _, alias := range orchestrationToolAliasRegistry {
		if alias.Canonical == canonical {
			return alias.Legacy
		}
	}
	return ""
}

func canonicalOrchName(name string) string {
	name = strings.TrimSpace(name)
	for _, alias := range orchestrationToolAliasRegistry {
		if alias.Legacy == name {
			return alias.Canonical
		}
	}
	return name
}
