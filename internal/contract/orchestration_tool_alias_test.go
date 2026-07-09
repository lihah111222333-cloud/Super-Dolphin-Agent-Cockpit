package contract

import (
	"slices"
	"testing"
)

func TestOrchestrationToolAliasDenylistIncludesCanonicalAndLegacyNames(t *testing.T) {
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

func TestReadOnlyAgentDeniedToolsUsesOrchestrationAliasDenylist(t *testing.T) {
	denied := ReadOnlyAgentDeniedTools()
	for _, name := range OrchestrationToolAliasDenylist() {
		if !slices.Contains(denied, name) {
			t.Fatalf("ReadOnlyAgentDeniedTools() missing orchestration alias %q", name)
		}
	}
}

func TestOrchestrationToolAliasesReturnsCopy(t *testing.T) {
	got := OrchestrationToolAliases()
	got[0].Canonical = "mutated"
	if aliases := OrchestrationToolAliases(); aliases[0].Canonical == "mutated" {
		t.Fatal("OrchestrationToolAliases() returned mutable shared backing storage")
	}
}
