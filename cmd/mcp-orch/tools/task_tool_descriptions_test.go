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
