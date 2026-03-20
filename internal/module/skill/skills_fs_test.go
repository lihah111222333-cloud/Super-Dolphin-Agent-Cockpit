package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadConfigReturnsExplicitStubBindingState(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	out, err := svc.ReadConfig(context.Background(), " agent-1 ")
	if err != nil {
		t.Fatalf("ReadConfig returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadConfig result type mismatch: %T", out)
	}
	if got, _ := result["agent_id"].(string); got != "agent-1" {
		t.Fatalf("agent_id mismatch: got %q", got)
	}
	if got, _ := result["configured"].(bool); got {
		t.Fatal("configured mismatch: got true want false")
	}
	if got, _ := result["binding_count"].(int); got != 0 {
		t.Fatalf("binding_count mismatch: got %d", got)
	}
	if got, _ := result["binding_source"].(string); got != "stub" {
		t.Fatalf("binding_source mismatch: got %q", got)
	}
}

func TestWriteSkillContentWritesNamedSkillContent(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	out, err := svc.WriteSkillContent(context.Background(), "demo-skill", "# demo")
	if err != nil {
		t.Fatalf("WriteSkillContent returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("WriteSkillContent result type mismatch: %T", out)
	}
	path, _ := result["path"].(string)
	if path == "" {
		t.Fatal("WriteSkillContent path is empty")
	}
	if want := filepath.Join(svc.root, "demo-skill", skillMainFile); path != want {
		t.Fatalf("WriteSkillContent path mismatch: got %q want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "# demo" {
		t.Fatalf("WriteSkillContent content mismatch: got %q", string(data))
	}
}
