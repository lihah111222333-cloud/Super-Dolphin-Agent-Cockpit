package tools

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestReportToolGuidanceDistinguishesSingleAndBatchReports(t *testing.T) {
	defs := orchestrationToolDefinitions(&golden.OrchestrationStub{})
	def := requireToolDefinition(t, defs, "list_agents")

	requireContains(t, def.Description, "get_agent_report")
	requireContains(t, def.Description, "get_agent_reports")
	requireNotContains(t, def.Description, "use get_agent_report for full reports")

	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("list_agents properties type = %T, want map[string]any", def.InputSchema["properties"])
	}
	includeReports, ok := props["include_reports"].(map[string]any)
	if !ok {
		t.Fatalf("include_reports schema type = %T, want map[string]any", props["include_reports"])
	}
	description, _ := includeReports["description"].(string)
	requireContains(t, description, "get_agent_report")
	requireContains(t, description, "get_agent_reports")
	requireNotContains(t, description, "prefer get_agent_report")
}

func requireToolDefinition(t *testing.T, defs []ToolDefinition, name string) ToolDefinition {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("tool definition %q not found", name)
	return ToolDefinition{}
}

func requireContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("text %q does not contain %q", got, want)
	}
}

func requireNotContains(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("text %q unexpectedly contains %q", got, unwanted)
	}
}
