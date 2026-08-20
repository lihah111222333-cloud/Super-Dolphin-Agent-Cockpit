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

// TestWindowsJDTLSLaunchArgumentsProvisionProductConfiguration 锁定启动层会把只读
// asset config_win 自动复制到产品私有可写目录，而不是要求用户项目预建 config_win。
func TestWindowsJDTLSLaunchArgumentsProvisionProductConfiguration(t *testing.T) {
	root := t.TempDir()
	assetRoot := filepath.Join(root, "cache", "runtime-dependencies", "jdk-jdtls", "arm64", "cohort")
	javaExecutable := filepath.Join(assetRoot, "jdk-21.0.12+8", "bin", "java.exe")
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductJDKJDTLS)
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(assetRoot, filepath.FromSlash(entry.Install.ServerPath))
	for path, contents := range map[string]string{
		javaExecutable: "java",
		launcher:       "launcher",
		filepath.Join(assetRoot, "config_win", "config.ini"): "eclipse.application=org.eclipse.jdt.ls.core.id1",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspaceRoot := filepath.Join(root, "user-workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	args, err := WindowsJDTLSLaunchArguments(javaExecutable, workspaceRoot)
	if err != nil {
		t.Fatalf("WindowsJDTLSLaunchArguments() error = %v", err)
	}
	configurationPath := argumentValueAfter(t, args, "-configuration")
	if relative, relErr := filepath.Rel(workspaceRoot, configurationPath); relErr == nil &&
		(relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))) {
		t.Fatalf("JDTLS mutable configuration %q is inside user workspace %q", configurationPath, workspaceRoot)
	}
	if contents, readErr := os.ReadFile(filepath.Join(configurationPath, "config.ini")); readErr != nil || len(contents) == 0 {
		t.Fatalf("product-owned JDTLS config.ini was not copied: contents=%q error=%v", contents, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(workspaceRoot, "config_win")); !os.IsNotExist(statErr) {
		t.Fatalf("JDTLS launch wrote config_win into user workspace: %v", statErr)
	}
}

func argumentValueAfter(t *testing.T, args []string, key string) string {
	t.Helper()
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key {
			return args[index+1]
		}
	}
	t.Fatalf("argument %q missing from %#v", key, args)
	return ""
}
