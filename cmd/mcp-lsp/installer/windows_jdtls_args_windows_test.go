//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsJDTLSDataRootPathDigestIsolation 锁定 product-owned data 目录按
// canonical workspace digest 隔离，且不把 JDTLS data 嵌入项目 workspace。
func TestWindowsJDTLSDataRootPathDigestIsolation(t *testing.T) {
	root := t.TempDir()
	assetRoot := filepath.Join(root, "cache", "runtime-dependencies", "jdk-jdtls", "arm64", "cohort")
	workspaceA := filepath.Join(root, "workspace-a")
	workspaceB := filepath.Join(root, "workspace-b")
	if err := os.MkdirAll(workspaceA, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceB, 0o700); err != nil {
		t.Fatal(err)
	}
	dataA, parentA, _, err := windowsJDTLSDataRootPath(assetRoot, workspaceA)
	if err != nil {
		t.Fatal(err)
	}
	dataB, parentB, _, err := windowsJDTLSDataRootPath(assetRoot, workspaceB)
	if err != nil {
		t.Fatal(err)
	}
	if parentA != parentB || !strings.Contains(strings.ToLower(parentA), "jdtls-data") {
		t.Fatalf("data parent = %q/%q, want shared product jdtls-data parent", parentA, parentB)
	}
	if dataA == dataB || filepath.Base(dataA) == "" || len(filepath.Base(dataA)) != 64 {
		t.Fatalf("workspace data roots are not digest-isolated: %q/%q", dataA, dataB)
	}
	for _, workspace := range []string{workspaceA, workspaceB} {
		data, _, _, err := windowsJDTLSDataRootPath(assetRoot, workspace)
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(workspace, data)
		if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			t.Fatalf("data root %q is inside workspace %q", data, workspace)
		}
	}
}

// TestWindowsJDTLSDataRootPathRejectsWorkspaceOverlap 防止 workspace 本身覆盖
// product data 父树；这不是可接受的路径关系，必须 fail-fast。
func TestWindowsJDTLSDataRootPathRejectsWorkspaceOverlap(t *testing.T) {
	root := t.TempDir()
	assetRoot := filepath.Join(root, "cache", "runtime-dependencies", "jdk-jdtls", "arm64", "cohort")
	overlap := filepath.Join(root, "cache", "runtime-workspaces", "jdk-jdtls", "arm64", "jdtls-data")
	if err := os.MkdirAll(overlap, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := windowsJDTLSDataRootPath(assetRoot, overlap); err == nil {
		t.Fatal("workspace overlap was accepted")
	}
}

// TestWindowsJDTLSConfigDirectoryRejectsJunction 锁定 source/mutable config_win
// 均不能通过 junction 越界；mklink 权限失败必须使测试失败，不得跳过成 PASS。
func TestWindowsJDTLSConfigDirectoryRejectsJunction(t *testing.T) {
	root := t.TempDir()
	externalRoot := t.TempDir()
	junction := filepath.Join(root, "config_win")
	createWindowsTestJunction(t, junction, externalRoot)

	err := validateWindowsJDTLSConfigDirectory(root, junction, "JDTLS config")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "reparse") {
		t.Fatalf("validateWindowsJDTLSConfigDirectory() error = %v, want reparse rejection", err)
	}
}

func TestWindowsJDTLSConfigDirectoryDoesNotCreateMissingPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "missing", "config_win")
	if err := validateWindowsJDTLSConfigDirectory(root, target, "JDTLS config"); err == nil {
		t.Fatal("validateWindowsJDTLSConfigDirectory() accepted missing path")
	}
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatalf("validation created missing parent, stat error = %v", err)
	}
}
