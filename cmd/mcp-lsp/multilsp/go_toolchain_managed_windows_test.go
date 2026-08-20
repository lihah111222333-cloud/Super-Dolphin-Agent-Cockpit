//go:build windows

package multilsp

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestManagedWindowsLSPProductRootUsesProductionPrecedence 锁定 managed Go 与 runtimeenv 相同的产品根优先级。
func TestManagedWindowsLSPProductRootUsesProductionPrecedence(t *testing.T) {
	productHome := filepath.Join(t.TempDir(), "product-home")
	projectRoot := filepath.Join(t.TempDir(), "project-root")
	resourcesRoot := filepath.Join(t.TempDir(), "resources-root")

	got, ok := managedWindowsLSPProductRoot([]string{
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=" + resourcesRoot,
		"PROJECT_ROOT=" + projectRoot,
		"SUPER_DOLPHIN_HOME=" + productHome,
	})
	if !ok {
		t.Fatal("managedWindowsLSPProductRoot() did not resolve explicit product home")
	}
	if got != filepath.Clean(productHome) {
		t.Fatalf("managedWindowsLSPProductRoot() = %q, want %q", got, filepath.Clean(productHome))
	}

	got, ok = managedWindowsLSPProductRoot([]string{"PROJECT_ROOT=" + projectRoot})
	if !ok || got != filepath.Join(filepath.Clean(projectRoot), ".super-dolphin") {
		t.Fatalf("managedWindowsLSPProductRoot(PROJECT_ROOT) = %q, %v", got, ok)
	}

	got, ok = managedWindowsLSPProductRoot([]string{
		"SUPER_DOLPHIN_HOME=",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=" + resourcesRoot,
	})
	if !ok || got != filepath.Join(filepath.Clean(resourcesRoot), ".super-dolphin") {
		t.Fatalf("managedWindowsLSPProductRoot(runtime resources) = %q, %v", got, ok)
	}
}

// TestManagedWindowsLSPProductRootDoesNotInheritHostForEmptyRequest 锁定显式空环境不会偷读宿主产品根。
func TestManagedWindowsLSPProductRootDoesNotInheritHostForEmptyRequest(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "host-product")
	t.Setenv("SUPER_DOLPHIN_HOME", hostRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")

	if got, ok := managedWindowsLSPProductRoot([]string{}); ok || got != "" {
		t.Fatalf("managedWindowsLSPProductRoot(empty env) = %q, %v, want no host-derived root", got, ok)
	}
}

// TestManagedGoToolchainSelectionInjectsPathForManagedCandidate 锁定 managed Go 不在 PATH 时仍注入其 bin 目录。
func TestManagedGoToolchainSelectionInjectsPathForManagedCandidate(t *testing.T) {
	repo := normalizedTempDir(t)
	managedDir := writeFakeGoVersion(t, repo, "managed-go", "go version go1.26.5 windows/arm64")
	candidate := goToolchainCandidate{
		binDir: filepath.Clean(managedDir),
		path:   filepath.Join(managedDir, "go.exe"),
	}
	required, err := parseGoVersion("1.26.4")
	if err != nil {
		t.Fatalf("parse required Go version: %v", err)
	}

	selected, err := selectGoToolchainCandidate(required, "", repo, []string{"PATH="}, []goToolchainCandidate{candidate})
	if err != nil {
		t.Fatalf("select managed Go candidate: %v", err)
	}
	if selected.BinDir != filepath.Clean(managedDir) {
		t.Fatalf("selected managed Go bin dir = %q, want %q", selected.BinDir, filepath.Clean(managedDir))
	}
	if !strings.HasPrefix(selected.PathEnv, filepath.Clean(managedDir)) {
		t.Fatalf("selected managed Go PATH = %q, want prefix %q", selected.PathEnv, filepath.Clean(managedDir))
	}
}
