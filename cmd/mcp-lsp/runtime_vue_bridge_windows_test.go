//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func TestRuntimeServerPrepareVueBridgeUsesLockedCohortAndTSdk(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	prefix := filepath.Join(productRoot, "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", strings.Repeat("a", 64))
	serverBinary := filepath.Join(prefix, "node_modules", ".bin", "vue-language-server.cmd")
	writeRuntimeVueBridgeWindowsFile(t, serverBinary, "@echo off\r\nexit /b 0\r\n")
	writeRuntimeVueBridgeWindowsFile(t, filepath.Join(prefix, "node_modules", ".bin", "typescript-language-server.cmd"), "@echo off\r\nexit /b 0\r\n")
	writeRuntimeVueBridgeWindowsFile(t, filepath.Join(prefix, "node_modules", "typescript", "lib", "tsserver.js"), "module.exports = {};\r\n")
	if err := os.MkdirAll(filepath.Join(prefix, "node_modules", "@vue", "language-server"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prefix, "node_modules", "@vue", "typescript-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRuntimeVueBridgeWindowsFile(t, filepath.Join(prefix, "node_modules", "@vue", "typescript-plugin", "index.js"), "module.exports = {};")
	adapter, ok := multilsp.NewDefaultLanguageAdapterRegistry().AdapterForLanguage("vue")
	if !ok {
		t.Fatal("Vue adapter is not registered")
	}
	args, spec, err := runtimeServerPrepareVueBridge(adapter, serverBinary, []string{"--stdio"})
	if err != nil {
		t.Fatalf("runtimeServerPrepareVueBridge() error = %v", err)
	}
	if spec == nil {
		t.Fatal("runtimeServerPrepareVueBridge() returned nil bridge spec")
	}
	wantTSdk := "--tsdk=" + filepath.Join(prefix, "node_modules", "typescript", "lib")
	if len(args) != 2 || args[1] != wantTSdk {
		t.Fatalf("Vue server args = %#v, want --stdio plus %q", args, wantTSdk)
	}
	if filepath.Clean(spec.typescriptBinary) != filepath.Clean(filepath.Join(prefix, "node_modules", ".bin", "typescript-language-server.cmd")) {
		t.Fatalf("TypeScript companion path = %q, want same cohort path", spec.typescriptBinary)
	}
	if filepath.Clean(spec.typescriptModuleRoot) != filepath.Clean(filepath.Join(prefix, "node_modules", "typescript")) {
		t.Fatalf("TypeScript module root = %q, want same cohort root", spec.typescriptModuleRoot)
	}
	if filepath.Clean(spec.vuePluginLocation) != filepath.Clean(filepath.Join(prefix, "node_modules", "@vue", "language-server")) {
		t.Fatalf("Vue plugin location = %q, want same cohort language-server", spec.vuePluginLocation)
	}

	external := filepath.Join(t.TempDir(), "other", "cache", "lsp-assets", "foreign.exe")
	originalArgs := []string{"--stdio", "--external"}
	preparedExternal, externalSpec, err := runtimeServerPrepareVueBridge(adapter, external, originalArgs)
	if err != nil {
		t.Fatalf("runtimeServerPrepareVueBridge(external) error = %v", err)
	}
	if externalSpec != nil || strings.Join(preparedExternal, "\x00") != strings.Join(originalArgs, "\x00") {
		t.Fatalf("external Vue server changed bridge wiring: args=%#v spec=%#v", preparedExternal, externalSpec)
	}
}

func TestRuntimeServerPrepareVueTSCompanionEnvironmentUsesSecondaryLease(t *testing.T) {
	t.Setenv(agentLSPSharedCacheDirEnv, filepath.Join(t.TempDir(), "cache-root"))
	cacheRoot, err := runtimeServerCacheRoot()
	if err != nil {
		t.Fatalf("create private shared cache root: %v", err)
	}
	cohortID := "repo-vue-companion-test"
	role, primaryLease, err := runtimeServerAcquireResourceLease(cacheRoot, cohortID)
	if err != nil {
		t.Fatalf("acquire Vue primary resource lease: %v", err)
	}
	if role != multilsp.ResourceCohortRolePrimary {
		t.Fatalf("primary resource lease role = %q, want %q", role, multilsp.ResourceCohortRolePrimary)
	}
	primaryEnv := []string{
		multilsp.ResourceCohortDirEnv + "=" + filepath.Join(cacheRoot, "resource-cohorts", "non-gopls"),
		multilsp.ResourceRepositoryCohortIDEnv + "=" + cohortID,
		multilsp.ResourceCohortRoleEnv + "=" + multilsp.ResourceCohortRolePrimary,
		multilsp.ResourceCohortLeaseEnv + "=" + primaryLease,
		multilsp.ResourceProcessRSSLimitMBEnv + "=2560",
		multilsp.ResourceCohortHardLimitMBEnv + "=15360",
	}
	companionEnv, err := runtimeServerPrepareVueTSCompanionEnvironment(primaryEnv)
	if err != nil {
		t.Fatalf("prepare Vue TypeScript companion environment: %v", err)
	}
	if got := runtimeServerEnvValue(companionEnv, multilsp.ResourceCohortRoleEnv); got != multilsp.ResourceCohortRoleSecondary {
		t.Fatalf("companion resource lease role = %q, want %q", got, multilsp.ResourceCohortRoleSecondary)
	}
	if got := runtimeServerEnvValue(companionEnv, multilsp.ResourceCohortLeaseEnv); got == primaryLease || got == "" {
		t.Fatalf("companion lease path = %q, want a distinct non-empty lease", got)
	}
	if got := runtimeServerEnvValue(companionEnv, multilsp.ResourceProcessRSSLimitMBEnv); got != "2560" {
		t.Fatalf("companion RSS limit = %q, want secondary 2560 MiB", got)
	}
	if got := runtimeServerEnvValue(companionEnv, "NODE_OPTIONS"); !strings.Contains(got, "--max-old-space-size=2048") {
		t.Fatalf("companion NODE_OPTIONS = %q, want secondary heap limit", got)
	}
	if err := multilsp.ReleaseResourceCohortLease(companionEnv); err != nil {
		t.Fatalf("release companion resource lease: %v", err)
	}
	if err := multilsp.ReleaseResourceCohortLease(primaryEnv); err != nil {
		t.Fatalf("release primary resource lease: %v", err)
	}
}

func writeRuntimeVueBridgeWindowsFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRuntimeServerWindowsRequireWithinRejectsRootEscape 验证 Vue cohort 解析不会
// 接受产品根外的同名依赖；词法越界必须在任何文件读取前 fail-fast。
func TestRuntimeServerWindowsRequireWithinRejectsRootEscape(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "node_modules")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("create external cohort fixture: %v", err)
	}
	if err := runtimeServerWindowsRequireWithin(root, target, "Vue npm prefix"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "root") {
		t.Fatalf("runtimeServerWindowsRequireWithin() error = %v, want product-root escape rejection", err)
	}
}

// TestRuntimeServerWindowsRequireWithinRejectsParentJunction 验证产品根内的
// junction 不能把同 cohort marker 解析到根外；guard 必须检查每个父路径段。
func TestRuntimeServerWindowsRequireWithinRejectsParentJunction(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.MkdirAll(filepath.Join(external, "node_modules"), 0o700); err != nil {
		t.Fatalf("create external junction fixture: %v", err)
	}
	junction := filepath.Join(root, "node_modules")
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, external).CombinedOutput()
	if err != nil {
		t.Fatalf("create test junction %q -> %q: %v (%s)", junction, external, err, strings.TrimSpace(string(output)))
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove test junction %q: %v", junction, err)
		}
	})
	if err := runtimeServerWindowsRequireWithin(root, filepath.Join(junction, "node_modules"), "Vue npm prefix"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "reparse") {
		t.Fatalf("runtimeServerWindowsRequireWithin() error = %v, want junction/reparse rejection", err)
	}
}

// TestRuntimeServerWindowsRequireWithinRejectsTargetSymlink 验证 target 本身的
// symlink 也不会被 Stat 跟随；若测试身份未获 Windows symlink 权限则明确跳过。
func TestRuntimeServerWindowsRequireWithinRejectsTargetSymlink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	target := filepath.Join(external, "server.js")
	if err := os.WriteFile(target, []byte("module.exports = {};\r\n"), 0o600); err != nil {
		t.Fatalf("write external symlink fixture: %v", err)
	}
	symlink := filepath.Join(root, "server.js")
	if err := os.Symlink(target, symlink); err != nil {
		t.Skipf("Windows symlink fixture unavailable (typed ACL may deny creation): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(symlink); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove test symlink %q: %v", symlink, err)
		}
	})
	if err := runtimeServerWindowsRequireWithin(root, symlink, "Vue server"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("runtimeServerWindowsRequireWithin() error = %v, want symlink rejection", err)
	}
}
