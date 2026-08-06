package contract

import (
	"errors"
	"slices"
	"testing"
)

var wantOrchestrationCanonicalNames = []string{
	"launch_agent", "send_message", "stop_agent", "recover_agent", "interrupt_agent",
	"list_agents", "get_agent_report", "get_agent_reports",
}

func TestOrchestrationToolRegistryHelpersPreserveOrder(t *testing.T) {
	tests := []struct {
		name string
		got  func() []string
		want []string
	}{
		{name: "canonical", got: OrchestrationToolCanonicalNames, want: wantOrchestrationCanonicalNames},
		{name: "denylist", got: OrchestrationToolDenylist, want: wantOrchestrationCanonicalNames},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(); !slices.Equal(got, tt.want) {
				t.Fatalf("%s helper = %#v, want %#v", tt.name, got, tt.want)
			}
		})
	}
}

func TestReadOnlyAgentDeniedToolsUsesOrchestrationDenylist(t *testing.T) {
	denied := ReadOnlyAgentDeniedTools()
	for _, name := range OrchestrationToolDenylist() {
		if !slices.Contains(denied, name) {
			t.Fatalf("ReadOnlyAgentDeniedTools() missing orchestration tool %q", name)
		}
	}
}

func TestReadOnlyAgentDeniedToolsUsesProductionWritableToolSurface(t *testing.T) {
	denied := ReadOnlyAgentDeniedTools()
	for _, name := range ReadOnlyNonOrchestrationDeniedTools() {
		if !slices.Contains(denied, name) {
			t.Fatalf("ReadOnlyAgentDeniedTools() missing writable tool %q", name)
		}
	}
	for _, legacy := range []string{"edit", "lsp_edit"} {
		if slices.Contains(denied, legacy) {
			t.Fatalf("ReadOnlyAgentDeniedTools() contains retired tool name %q", legacy)
		}
	}
}

func TestOrchestrationToolHelpersReturnCopies(t *testing.T) {
	tests := []struct {
		name string
		get  func() []string
	}{
		{name: "canonical", get: OrchestrationToolCanonicalNames},
		{name: "denylist", get: OrchestrationToolDenylist},
		{name: "read-only non-orchestration denied", get: ReadOnlyNonOrchestrationDeniedTools},
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

func TestIsOrchestrationLaunchToolUsesShortNameOnly(t *testing.T) {
	for _, name := range []string{"launch_agent", " launch_agent ", "LAUNCH_AGENT"} {
		if !IsOrchestrationLaunchTool(name) {
			t.Fatalf("IsOrchestrationLaunchTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "send_message", "orchestration_launch_agent", "mcp__orch__launch_agent"} {
		if IsOrchestrationLaunchTool(name) {
			t.Fatalf("IsOrchestrationLaunchTool(%q) = true, want false", name)
		}
	}
}

func TestOrchestrationLaunchDefaultDisabledTools(t *testing.T) {
	want := []string{
		"launch_agent",
		"mcp__orch__launch_agent",
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
	got, err := orchestrationLaunchDefaultDisabledTools([]string{"send_message"})
	if err == nil {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() error = nil, want %v", ErrOrchestrationToolMissing)
	}
	if !errors.Is(err, ErrOrchestrationToolMissing) {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() error = %v, want %v", err, ErrOrchestrationToolMissing)
	}
	if got != nil {
		t.Fatalf("OrchestrationLaunchDefaultDisabledTools() = %#v, want nil on error", got)
	}
}
