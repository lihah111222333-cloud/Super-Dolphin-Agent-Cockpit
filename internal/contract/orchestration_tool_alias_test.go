package contract

import (
	"errors"
	"slices"
	"testing"
)

var (
	wantOrchestrationCanonicalNames = []string{
		"launch_agent", "send_message", "stop_agent", "recover_agent", "interrupt_agent",
		"list_agents", "get_agent_report", "get_agent_reports",
	}
	wantOrchestrationLegacyPeerRealNames = []string{
		"orchestration_launch_agent", "orchestration_send_message", "orchestration_stop_agent",
		"orchestration_recover_agent", "orchestration_interrupt_agent", "orchestration_list_agents",
		"orchestration_get_agent_report", "orchestration_get_agent_reports",
	}
	wantOrchestrationHiddenAliases = []string{
		"mcp__orch__orchestration_launch_agent", "mcp__orch__orchestration_send_message",
		"mcp__orch__orchestration_stop_agent", "mcp__orch__orchestration_recover_agent",
		"mcp__orch__orchestration_interrupt_agent", "mcp__orch__orchestration_list_agents",
		"mcp__orch__orchestration_get_agent_report", "mcp__orch__orchestration_get_agent_reports",
	}
)

func TestOrchestrationToolRegistryHelpersPreserveOrder(t *testing.T) {
	tests := []struct {
		name string
		got  func() []string
		want []string
	}{
		{name: "canonical", got: OrchestrationToolCanonicalNames, want: wantOrchestrationCanonicalNames},
		{name: "legacy", got: OrchestrationToolLegacyPeerRealNames, want: wantOrchestrationLegacyPeerRealNames},
		{name: "hidden", got: OrchestrationToolHiddenAliases, want: wantOrchestrationHiddenAliases},
		{name: "denylist", got: OrchestrationToolAliasDenylist, want: wantOrchestrationDenylist()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(); !slices.Equal(got, tt.want) {
				t.Fatalf("%s helper = %#v, want %#v", tt.name, got, tt.want)
			}
		})
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

func TestOrchestrationToolHelpersReturnCopies(t *testing.T) {
	got := OrchestrationToolAliases()
	got[0].Canonical = "mutated"
	if aliases := OrchestrationToolAliases(); aliases[0].Canonical == "mutated" {
		t.Fatal("OrchestrationToolAliases() returned mutable shared backing storage")
	}

	tests := []struct {
		name string
		get  func() []string
	}{
		{name: "canonical", get: OrchestrationToolCanonicalNames},
		{name: "legacy", get: OrchestrationToolLegacyPeerRealNames},
		{name: "hidden", get: OrchestrationToolHiddenAliases},
		{name: "denylist", get: OrchestrationToolAliasDenylist},
		{name: "launch default disabled", get: func() []string {
			tools, err := OrchestrationLaunchDefaultDisabledTools()
			if err != nil {
				t.Fatalf("OrchestrationLaunchDefaultDisabledTools() error = %v", err)
			}
			return tools
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.get()
			got[0] = "mutated"
			if again := tt.get(); again[0] == "mutated" {
				t.Fatalf("%s helper returned mutable shared backing storage", tt.name)
			}
		})
	}
}

func TestOrchestrationToolAliasLookups(t *testing.T) {
	legacy, ok := OrchestrationLegacyPeerRealName(" launch_agent ")
	if !ok || legacy != "orchestration_launch_agent" {
		t.Fatalf("OrchestrationLegacyPeerRealName() = %q, %v", legacy, ok)
	}

	legacy, ok = OrchestrationLaunchLegacyPeerRealName()
	if !ok || legacy != "orchestration_launch_agent" {
		t.Fatalf("OrchestrationLaunchLegacyPeerRealName() = %q, %v", legacy, ok)
	}

	canonical, ok := OrchestrationCanonicalToolName(" orchestration_send_message ")
	if !ok || canonical != "send_message" {
		t.Fatalf("OrchestrationCanonicalToolName() = %q, %v", canonical, ok)
	}
}

func TestOrchestrationToolAliasLookupsReturnFalseForUnknownNames(t *testing.T) {
	for _, canonical := range []string{"", "unknown", "orchestration_launch_agent"} {
		if legacy, ok := OrchestrationLegacyPeerRealName(canonical); ok || legacy != "" {
			t.Fatalf("OrchestrationLegacyPeerRealName(%q) = %q, %v; want empty, false", canonical, legacy, ok)
		}
	}

	for _, legacy := range []string{"", "unknown", "launch_agent", "mcp__orch__orchestration_launch_agent"} {
		if canonical, ok := OrchestrationCanonicalToolName(legacy); ok || canonical != "" {
			t.Fatalf("OrchestrationCanonicalToolName(%q) = %q, %v; want empty, false", legacy, canonical, ok)
		}
	}
}

func TestIsOrchestrationLaunchToolUsesRegistry(t *testing.T) {
	for _, name := range []string{"launch_agent", " launch_agent ", "orchestration_launch_agent", "ORCHESTRATION_LAUNCH_AGENT"} {
		if !IsOrchestrationLaunchTool(name) {
			t.Fatalf("IsOrchestrationLaunchTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "send_message", "orchestration_send_message", "mcp__orch__orchestration_launch_agent"} {
		if IsOrchestrationLaunchTool(name) {
			t.Fatalf("IsOrchestrationLaunchTool(%q) = true, want false", name)
		}
	}
}

func TestOrchestrationLaunchDefaultDisabledTools(t *testing.T) {
	want := []string{
		"launch_agent",
		"mcp__orch__launch_agent",
		"orchestration_launch_agent",
		"mcp__orch__orchestration_launch_agent",
	}
	got, err := OrchestrationLaunchDefaultDisabledTools()
	if err != nil {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() error = %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() = %#v, want %#v", got, want)
	}
}

func TestOrchestrationLaunchDefaultDisabledToolsFailsFastWhenRegistryMissing(t *testing.T) {
	old := orchestrationToolAliases
	orchestrationToolAliases = []OrchestrationToolAlias{
		{Canonical: "send_message", LegacyPeerRealName: "orchestration_send_message"},
	}
	t.Cleanup(func() { orchestrationToolAliases = old })

	got, err := OrchestrationLaunchDefaultDisabledTools()
	if err == nil {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() error = nil, want %v", ErrOrchestrationToolAliasMissing)
	}
	if !errors.Is(err, ErrOrchestrationToolAliasMissing) {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() error = %v, want %v", err, ErrOrchestrationToolAliasMissing)
	}
	if got != nil {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() = %#v, want nil on error", got)
	}
}

func wantOrchestrationDenylist() []string {
	want := append([]string(nil), wantOrchestrationCanonicalNames...)
	want = append(want, wantOrchestrationLegacyPeerRealNames...)
	return want
}
