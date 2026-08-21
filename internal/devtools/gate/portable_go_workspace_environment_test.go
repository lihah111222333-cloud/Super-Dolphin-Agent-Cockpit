package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecutorEnvironmentUsesValidatedPortableWorkspace 锁定 worker 只为精确 descriptor 派生的 go.work 覆盖 GOWORK。
func TestExecutorEnvironmentUsesValidatedPortableWorkspace(t *testing.T) {
	layout := newExecutorLayout(t.TempDir())
	if err := os.MkdirAll(filepath.Join(layout.sourceCopy, ".super-dolphin"), 0o700); err != nil {
		t.Fatalf("create source copy: %v", err)
	}
	descriptor := PortableGoWorkspaceDescriptor{
		SchemaVersion: PortableGoWorkspaceSchema,
		EngineTree:    strings.Repeat("a", 40), TargetTree: strings.Repeat("b", 40),
		TargetPath: "portable-target", Modules: []string{".", "portable-target/remote-ci"},
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.sourceCopy, filepath.FromSlash(PortableGoWorkspaceDescriptorPath)), data, 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	if err := InstallPortableGoWorkspace(layout.sourceCopy); err != nil {
		t.Fatalf("InstallPortableGoWorkspace: %v", err)
	}
	environment := executorEnvironment(layout, "/usr/bin", layout.goModCache, "/usr/local/go", "", "")
	want := "GOWORK=" + filepath.Join(layout.sourceCopy, "go.work")
	count := 0
	for _, assignment := range environment {
		if strings.HasPrefix(assignment, "GOWORK=") {
			count++
			if assignment != want {
				t.Fatalf("GOWORK = %q, want %q", assignment, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("GOWORK assignment count = %d, want 1", count)
	}
}
