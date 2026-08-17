//go:build windows && e2e

package main

import (
	"slices"
	"strings"
	"testing"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

// TestWindowsIdleQuickCatalogImplementation_E2E 验证 Windows quick workload
// 只通过唯一宿主 light 入口运行一个有界的精确 top-level selector。
func TestWindowsIdleQuickCatalogImplementation_E2E(t *testing.T) {
	document, workload, err := loadRunnerWorkload(repoRootForWindowsWorkloadE2E(t), "mcp-lsp-idle-quick")
	if err != nil {
		t.Fatal(err)
	}
	if document.CatalogDigest == "" {
		t.Fatal("catalog digest is empty")
	}
	if err := validateRunnerWorkload(workload); err != nil {
		t.Fatalf("validate Windows idle-quick workload: %v", err)
	}
	if workload.RunnerTarget != "local-go-test" || workload.ImplementationStatus != "implemented" {
		t.Fatalf("Windows idle-quick implementation = target %q status %q", workload.RunnerTarget, workload.ImplementationStatus)
	}
	if !containsWindowsWorkloadValue(workload.Platforms, "windows") {
		t.Fatalf("Windows idle-quick platforms = %v", workload.Platforms)
	}
	if workload.TimeoutSeconds != 120 {
		t.Fatalf("Windows idle-quick timeout = %ds, want outer bound 120s", workload.TimeoutSeconds)
	}
	wantCommand := []string{
		"bash", "./scripts/test_with_guard.sh", "--host-test", "light",
		"./cmd/mcp-lsp/internal/hiddenexec",
		"-run", "^TestProcessTreeProvidesIdentitySnapshotAndBoundedLifecycle$",
		"-timeout=60s", "-count=1",
	}
	if !slices.Equal(workload.Command, wantCommand) {
		t.Fatalf("Windows idle-quick command = %q, want %q", workload.Command, wantCommand)
	}
}

// TestWindowsDefault15mCatalogImplementation_E2E 验证 Windows source workload
// 解析为专属本地命令；producer/remote authority 仍由后续远程门禁 fail-closed。
func TestWindowsDefault15mCatalogImplementation_E2E(t *testing.T) {
	_, workload, err := loadRunnerWorkload(repoRootForWindowsWorkloadE2E(t), "mcp-lsp-default-15m")
	if err != nil {
		t.Fatal(err)
	}
	if workload.ImplementationStatus != "implemented" || workload.RunnerTarget != "local-go-test" {
		t.Fatalf("Windows default-15m source implementation = target %q status %q", workload.RunnerTarget, workload.ImplementationStatus)
	}
	if workload.ProducerImplementationStatus != "missing" {
		t.Fatalf("Windows default-15m producer status = %q, want fail-closed missing", workload.ProducerImplementationStatus)
	}
	requireWindowsWorkloadSelector(t, workload, "TestMcpLSPBinaryWindowsGoplsDefault15mIdleReclaimsSharedDaemonE2E")
	if err := validateRunnerWorkload(workload); err == nil {
		t.Fatal("Windows default-15m without remote producer authority unexpectedly passed runner validation")
	}
}

// TestWindowsDefault15mSourceCommandCarriesGoTimeout_E2E 锁定 15 分钟 soak 的 Go 测试预算。
func TestWindowsDefault15mSourceCommandCarriesGoTimeout_E2E(t *testing.T) {
	if !slices.Contains(windowsDefault15mSourceCommand(), "-timeout=20m") {
		t.Fatalf("Windows default-15m command = %v, want explicit -timeout=20m", windowsDefault15mSourceCommand())
	}
}

func repoRootForWindowsWorkloadE2E(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func requireWindowsWorkloadSelector(t *testing.T, workload catalog.Workload, testName string) {
	t.Helper()
	if !containsWindowsWorkloadFragment(workload.Command, testName) {
		t.Fatalf("workload %q command = %v, want selector %s", workload.ID, workload.Command, testName)
	}
}

func containsWindowsWorkloadValue(values []string, want string) bool {
	return slices.Contains(values, want)
}

func containsWindowsWorkloadFragment(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
