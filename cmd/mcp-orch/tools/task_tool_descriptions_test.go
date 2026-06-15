package tools

import (
	"strings"
	"testing"
)

func TestTaskCreateDAGDescriptionSeparatesTemplateFromExecution(t *testing.T) {
	defs := taskToolDefinitions(nil)
	var desc string
	for _, def := range defs {
		if def.Name == "task_create_dag" {
			desc = def.Description
			break
		}
	}
	if !strings.Contains(desc, "does not start execution") || !strings.Contains(desc, "task_start_dag") {
		t.Fatalf("task_create_dag description = %q, want explicit start guidance", desc)
	}
}

func TestWorkspaceToolDescriptionsDoNotMentionPostgreSQLState(t *testing.T) {
	for _, def := range workspaceToolDefinitions(nil) {
		if strings.Contains(def.Description, "PostgreSQL") {
			t.Fatalf("%s description = %q, want storage-neutral state wording", def.Name, def.Description)
		}
		if strings.Contains(def.Description, "persistent state") {
			continue
		}
		switch def.Name {
		case "workspace_create_run", "workspace_merge_run", "workspace_abort_run":
			t.Fatalf("%s description = %q, want persistent state wording", def.Name, def.Description)
		}
	}
}
