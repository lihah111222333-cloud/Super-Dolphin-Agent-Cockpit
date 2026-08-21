package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestInstallPortableGoWorkspaceWritesCanonicalWorkspace 锁定 descriptor 到 go.work 的原子投影。
func TestInstallPortableGoWorkspaceWritesCanonicalWorkspace(t *testing.T) {
	root := t.TempDir()
	descriptor := remoteci.PortableGoWorkspaceDescriptor{
		SchemaVersion: remoteci.PortableGoWorkspaceSchema,
		EngineTree:    strings.Repeat("a", 40),
		TargetTree:    strings.Repeat("b", 40),
		TargetPath:    "portable-target",
		Modules:       []string{".", "portable-target/remote-ci"},
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	descriptorPath := filepath.Join(root, filepath.FromSlash(remoteci.PortableGoWorkspaceDescriptorPath))
	if err := os.MkdirAll(filepath.Dir(descriptorPath), 0o700); err != nil {
		t.Fatalf("create descriptor root: %v", err)
	}
	if err := os.WriteFile(descriptorPath, data, 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	if err := remoteci.InstallPortableGoWorkspace(root); err != nil {
		t.Fatalf("InstallPortableGoWorkspace: %v", err)
	}
	workspace, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatalf("read go.work: %v", err)
	}
	for _, material := range [][]byte{[]byte("go 1.26.5"), []byte("\t."), []byte("\t./portable-target/remote-ci")} {
		if !bytes.Contains(workspace, material) {
			t.Fatalf("go.work misses %q: %q", material, workspace)
		}
	}
	if err := remoteci.InstallPortableGoWorkspace(root); err == nil || !strings.Contains(err.Error(), "existing go.work") {
		t.Fatalf("second InstallPortableGoWorkspace error = %v", err)
	}
}
