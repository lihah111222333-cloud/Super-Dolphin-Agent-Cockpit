package toolbridge

import (
	"slices"
	"testing"
)

func TestOrchestrationToolAliasDenylistIncludesLegacyAndShortNames(t *testing.T) {
	want := []string{
		"launch_agent", "send_message", "stop_agent", "recover_agent", "interrupt_agent",
		"list_agents", "get_agent_report", "get_agent_reports",
		"orchestration_launch_agent", "orchestration_send_message", "orchestration_stop_agent",
		"orchestration_recover_agent", "orchestration_interrupt_agent", "orchestration_list_agents",
		"orchestration_get_agent_report", "orchestration_get_agent_reports",
	}
	if got := OrchestrationToolAliasDenylist(); !slices.Equal(got, want) {
		t.Fatalf("OrchestrationToolAliasDenylist() = %#v, want %#v", got, want)
	}
}

func TestOrchestrationToolAliasRegistryDrivesCanonicalAndLegacyMapping(t *testing.T) {
	for _, alias := range legacyOrchestrationToolAliases {
		if got := legacyOrchPeerRealName(alias.Canonical); got != alias.LegacyPeerRealName {
			t.Fatalf("legacyOrchPeerRealName(%q) = %q, want %q", alias.Canonical, got, alias.LegacyPeerRealName)
		}
		if got := canonicalOrchSurfaceName(alias.LegacyPeerRealName); got != alias.Canonical {
			t.Fatalf("canonicalOrchSurfaceName(%q) = %q, want %q", alias.LegacyPeerRealName, got, alias.Canonical)
		}
		if !isLegacyOrchPeerRealName(alias.LegacyPeerRealName) {
			t.Fatalf("isLegacyOrchPeerRealName(%q) = false, want true", alias.LegacyPeerRealName)
		}
	}
}
