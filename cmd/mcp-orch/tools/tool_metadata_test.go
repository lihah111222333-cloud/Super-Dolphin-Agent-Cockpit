package tools

import (
	"encoding/json"
	"testing"
)

func TestTaskToolDefinitionsRequireGovernanceMetadata(t *testing.T) {
	defs := taskToolDefinitions(ToolPorts{})
	if len(defs) == 0 {
		t.Fatal("task tool definitions are empty")
	}

	for _, def := range defs {
		t.Run(def.Name, func(t *testing.T) {
			checks := []struct {
				name string
				ok   bool
			}{
				{name: "version", ok: def.Metadata.Version != ""},
				{name: "output schema", ok: len(def.Metadata.OutputSchema) > 0},
				{name: "capabilities", ok: len(def.Metadata.Capabilities) > 0},
				{name: "risk class", ok: def.Metadata.RiskClass != ""},
				{name: "permission", ok: def.Metadata.Permission != ""},
				{name: "workspace scope", ok: def.Metadata.WorkspaceScope != ""},
				{name: "idempotency", ok: def.Metadata.IdempotencyRequirement != ""},
				{name: "audit event type", ok: def.Metadata.AuditEventType != ""},
				{name: "redaction policy", ok: def.Metadata.RedactionPolicy != ""},
			}
			for _, check := range checks {
				if !check.ok {
					t.Fatalf("metadata %s is required", check.name)
				}
			}
			if def.Metadata.ApprovalRequired {
				t.Fatal("task tools must not request approval before approval MVP exists")
			}
		})
	}
}

func TestWorkflowWriteToolsUseHighRiskWritePermission(t *testing.T) {
	writeTools := map[string]bool{
		"task_create_dag":    true,
		"task_dag_apply_ops": true,
		"task_update_node":   true,
		"task_dispatch_node": true,
		"task_start_dag":     true,
		"task_terminate_dag": true,
		"task_delete_dag":    true,
	}

	for _, def := range taskToolDefinitions(ToolPorts{}) {
		if !writeTools[def.Name] {
			continue
		}
		if def.Metadata.RiskClass != ToolRiskHigh {
			t.Fatalf("%s risk class = %q, want %q", def.Name, def.Metadata.RiskClass, ToolRiskHigh)
		}
		if def.Metadata.Permission != ToolPermissionWorkflowWrite {
			t.Fatalf("%s permission = %q, want %q", def.Name, def.Metadata.Permission, ToolPermissionWorkflowWrite)
		}
	}
}

func TestMediaFileWritingToolsRequirePathPolicy(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	for _, name := range []string{"shared_file_write", "tts_generate", "av_merge", "video_with_audio"} {
		t.Run(name, func(t *testing.T) {
			def, ok := registry.Lookup(name)
			if !ok {
				t.Fatalf("registry.Lookup(%q) = false", name)
			}
			raw, err := json.Marshal(def.Metadata)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			var metadata map[string]any
			if err := json.Unmarshal(raw, &metadata); err != nil {
				t.Fatalf("unmarshal metadata: %v", err)
			}
			policy, ok := metadata["path_policy"].(map[string]any)
			if !ok {
				t.Fatalf("%s metadata = %s, missing path_policy", name, raw)
			}
			if policy["path_authority"] == "" {
				t.Fatalf("%s path_policy = %#v, missing path_authority", name, policy)
			}
		})
	}
}
