package claudecli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
)

// TestSetupWorkspaceSkillsBeforeLaunch verifies that the cliadapter helper
// works correctly when invoked with claudecli's expected workspace + cache
// directory pattern. End-to-end Claude CLI subprocess testing is in Task 9.
func TestSetupWorkspaceSkillsBeforeLaunch(t *testing.T) {
	workspace := t.TempDir()
	cache := t.TempDir()
	sentinel := filepath.Join(cache, "marker")
	if err := os.WriteFile(sentinel, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cliadapter.SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	via := filepath.Join(workspace, ".claude", "skills", "marker")
	b, err := os.ReadFile(via)
	if err != nil {
		t.Fatalf("read via workspace symlink: %v", err)
	}
	if string(b) != "seen" {
		t.Errorf("read = %q, want seen", string(b))
	}
}
