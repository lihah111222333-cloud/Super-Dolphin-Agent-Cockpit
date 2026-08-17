//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestRuntimeServerWindowsVCLibsEnvironmentUsesReadOnlyShortRuntime 验证生产 cache
// server 在重启后只读取得同身份短路径，且只修改返回给子进程的环境副本。
func TestRuntimeServerWindowsVCLibsEnvironmentUsesReadOnlyShortRuntime(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	serverDir := filepath.Join(productRoot, "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", strings.Repeat("a", 64), "node_modules", ".bin")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	serverBinary := filepath.Join(serverDir, "typescript-language-server.cmd")
	if err := os.WriteFile(serverBinary, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(productRoot, "cache", "lsp-assets", "windows-vclibs-desktop-app-local", "14.0.33321.0", "arm64", strings.Repeat("b", 64), "ready")
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	processRoot, err := installer.WindowsShortProcessPath(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	parentPath := os.Getenv("PATH")
	base := []string{"Path=C:\\caller-bin", runtimeServerWindowsMSVCRuntimeDirEnv + "=C:\\stale", "KEEP=value"}
	var resolvedProductRoot string
	got, err := runtimeServerWindowsVCLibsEnvironmentWithResolver(serverBinary, base, func(root string) (string, error) {
		resolvedProductRoot = root
		return processRoot, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(resolvedProductRoot) != filepath.Clean(productRoot) {
		t.Fatalf("resolved product root = %q, want %q", resolvedProductRoot, productRoot)
	}
	wantPath := processRoot + string(os.PathListSeparator) + "C:\\caller-bin"
	if value := runtimeServerWindowsEnvironmentValue(got, "PATH"); value != wantPath {
		t.Fatalf("child PATH = %q, want %q", value, wantPath)
	}
	if value := runtimeServerWindowsEnvironmentValue(got, runtimeServerWindowsMSVCRuntimeDirEnv); value != processRoot {
		t.Fatalf("child MSVC runtime dir = %q, want %q", value, processRoot)
	}
	if os.Getenv("PATH") != parentPath {
		t.Fatal("Windows VCLibs child environment changed parent PATH")
	}
	processBinary, err := runtimeServerPlatformProcessBinary(serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	canonicalInfo, err := os.Stat(serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	processInfo, err := os.Stat(processBinary)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(canonicalInfo, processInfo) {
		t.Fatal("Windows LSP process path changed server binary identity")
	}
}

// TestRuntimeServerWindowsProductUserDataEnvironmentIsProductScoped 验证受管 Vue 与
// 直连 TypeScript server 的 typingsInstaller 用户态目录都不会落到当前用户的全局 cache。
func TestRuntimeServerWindowsProductUserDataEnvironmentIsProductScoped(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	serverDir := filepath.Join(productRoot, "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", strings.Repeat("a", 64), "node_modules", ".bin")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"localappdata=C:\\outside-local",
		"AppData=C:\\outside-roaming",
		"KEEP=value",
	}
	wantLocalAppData := filepath.Join(productRoot, "runtime-state", "localappdata")
	wantAppData := filepath.Join(productRoot, "runtime-state", "appdata")
	for _, binaryName := range []string{"typescript-language-server.cmd", "vue-language-server.cmd", "vscode-html-language-server.cmd"} {
		binaryName := binaryName
		t.Run(binaryName, func(t *testing.T) {
			serverBinary := filepath.Join(serverDir, binaryName)
			if err := os.WriteFile(serverBinary, []byte("@exit /b 0\r\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := runtimeServerWindowsVCLibsEnvironmentWithResolver(serverBinary, base, func(string) (string, error) {
				return productRoot, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if value := runtimeServerWindowsEnvironmentValue(got, runtimeServerWindowsLocalAppDataEnv); filepath.Clean(value) != filepath.Clean(wantLocalAppData) {
				t.Fatalf("%s LOCALAPPDATA = %q, want %q", binaryName, value, wantLocalAppData)
			}
			if value := runtimeServerWindowsEnvironmentValue(got, runtimeServerWindowsAppDataEnv); filepath.Clean(value) != filepath.Clean(wantAppData) {
				t.Fatalf("%s APPDATA = %q, want %q", binaryName, value, wantAppData)
			}
		})
	}
	for _, path := range []struct {
		label string
		path  string
	}{
		{label: "LOCALAPPDATA", path: wantLocalAppData},
		{label: "APPDATA", path: wantAppData},
	} {
		if err := runtimeServerWindowsRequireWithin(productRoot, path.path, "product-owned "+path.label); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path.path); err != nil || !info.IsDir() {
			t.Fatalf("product-owned %s directory = %q, stat=%v", path.label, path.path, err)
		}
	}
}

// TestRuntimeServerWindowsProductUserDataEnvironmentPreservesTypedACLFailures 验证
// Windows 5/1314 在用户态目录创建边界仍是 typed authorization_required，且错误
// 文本不会泄露产品路径；测试 seam 不改变生产目录创建器。
func TestRuntimeServerWindowsProductUserDataEnvironmentPreservesTypedACLFailures(t *testing.T) {
	productRoot := t.TempDir()
	base := []string{"KEEP=value"}
	for _, code := range []uint32{5, 1314} {
		t.Run(strconv.FormatUint(uint64(code), 10), func(t *testing.T) {
			original := runtimeServerWindowsMkdirAll
			runtimeServerWindowsMkdirAll = func(string, os.FileMode) error { return syscall.Errno(code) }
			t.Cleanup(func() { runtimeServerWindowsMkdirAll = original })
			err := func() error {
				_, err := runtimeServerWindowsProductUserDataEnvironment(productRoot, base)
				return err
			}()
			if err == nil {
				t.Fatal("product-owned user-data directory creation unexpectedly succeeded")
			}
			var permissionErr *securefs.WindowsPermissionError
			if !errors.As(err, &permissionErr) || permissionErr == nil || permissionErr.Win32Code() != code {
				t.Fatalf("product-owned user-data error = %v, typed=%#v; want WindowsPermissionError code %d", err, permissionErr, code)
			}
			if strings.Contains(err.Error(), productRoot) {
				t.Fatalf("product-owned user-data ACL error leaked product path %q: %v", productRoot, err)
			}
		})
	}
}

// TestRuntimeServerWindowsVCLibsEnvironmentPreservesInheritedPathWhenOverridesOmitPath
// 验证调用方只传增量环境时，VCLibs 注入仍显式保留 mcp-lsp 进程的 PATH；
// multilsp 会把该增量环境追加到 os.Environ 后，若不折叠父 PATH，后置 PATH 会令
// Node 的 .cmd 启动器找不到 node.exe。
func TestRuntimeServerWindowsVCLibsEnvironmentPreservesInheritedPathWhenOverridesOmitPath(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	inheritedPath := `C:\locked-node;C:\Windows`
	t.Setenv("PATH", inheritedPath)

	serverDir := filepath.Join(productRoot, "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", strings.Repeat("a", 64), "node_modules", ".bin")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	serverBinary := filepath.Join(serverDir, "typescript-language-server.cmd")
	runtimeRoot := `C:\locked-vclibs`
	got, err := runtimeServerWindowsVCLibsEnvironmentWithResolver(serverBinary, []string{"KEEP=value"}, func(string) (string, error) {
		return runtimeRoot, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := runtimeRoot + string(os.PathListSeparator) + inheritedPath
	if value := runtimeServerWindowsEnvironmentValue(got, "PATH"); value != wantPath {
		t.Fatalf("child PATH = %q, want inherited process PATH %q", value, wantPath)
	}
}

// TestRuntimeServerWindowsVCLibsEnvironmentLeavesExternalBinaryUnchanged 验证
// marker 冲突的外部语言服务器不被产品 VCLibs 策略污染，也不被改写进程路径。
func TestRuntimeServerWindowsVCLibsEnvironmentLeavesExternalBinaryUnchanged(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	external := filepath.Join(t.TempDir(), "cache", "lsp-assets", "foreign.exe")
	base := []string{"Path=C:\\external-bin"}
	resolverCalled := false
	got, err := runtimeServerWindowsVCLibsEnvironmentWithResolver(external, base, func(string) (string, error) {
		resolverCalled = true
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalled {
		t.Fatal("external language server unexpectedly resolved product VCLibs")
	}
	if len(got) != len(base) || got[0] != base[0] {
		t.Fatalf("external language server environment = %#v, want %#v", got, base)
	}
	processBinary, err := runtimeServerPlatformProcessBinary(external)
	if err != nil {
		t.Fatal(err)
	}
	if processBinary != external {
		t.Fatalf("external language server process path = %q, want %q", processBinary, external)
	}
}

// TestRuntimeServerWindowsGoSQLSEnvironmentBindsProductAppData 验证 SQLS 只接收同一
// product-owned cohort 的 APPDATA，避免 os.UserConfigDir 依赖系统用户环境。
func TestRuntimeServerWindowsGoSQLSEnvironmentBindsProductAppData(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	cohortRoot := filepath.Join(productRoot, "cache", "lsp-assets", "go-sqls", "arm64", strings.Repeat("a", 64))
	serverBinary := filepath.Join(cohortRoot, "bin", installer.WindowsGoSQLSBinaryName)
	configRoot := filepath.Join(cohortRoot, "config")
	if err := os.MkdirAll(filepath.Dir(serverBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serverBinary, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"APPDATA=C:\\outside", "KEEP=value"}
	resolved := installer.WindowsRuntimeDependencyProvisionResult{RootPath: cohortRoot, ServerPath: serverBinary}
	got, err := runtimeServerWindowsGoSQLSEnvironmentWithResolver(serverBinary, base, func(root string) (installer.WindowsRuntimeDependencyProvisionResult, error) {
		if filepath.Clean(root) != filepath.Clean(productRoot) {
			t.Fatalf("resolver product root = %q, want %q", root, productRoot)
		}
		return resolved, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if value := runtimeServerWindowsEnvironmentValue(got, runtimeServerWindowsAppDataEnv); filepath.Clean(value) != filepath.Clean(configRoot) {
		t.Fatalf("GoSQLS APPDATA = %q, want %q", value, configRoot)
	}
	if value := runtimeServerWindowsEnvironmentValue(base, runtimeServerWindowsAppDataEnv); value != `C:\outside` {
		t.Fatalf("caller environment was modified: %q", value)
	}
}

// TestRuntimeServerWindowsGoSQLSEnvironmentLeavesExternalBinaryUnchanged 防止同名外部
// sqls.exe 伪装成生产 cohort 并取得产品 APPDATA。
func TestRuntimeServerWindowsGoSQLSEnvironmentLeavesExternalBinaryUnchanged(t *testing.T) {
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	external := filepath.Join(t.TempDir(), installer.WindowsGoSQLSBinaryName)
	base := []string{"APPDATA=C:\\outside", "KEEP=value"}
	called := false
	got, err := runtimeServerWindowsGoSQLSEnvironmentWithResolver(external, base, func(string) (installer.WindowsRuntimeDependencyProvisionResult, error) {
		called = true
		return installer.WindowsRuntimeDependencyProvisionResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("external sqls.exe unexpectedly resolved product cohort")
	}
	if strings.Join(got, "\x00") != strings.Join(base, "\x00") {
		t.Fatalf("external SQLS environment = %#v, want %#v", got, base)
	}
}

func TestRuntimeServerWindowsRustEnvironmentUsesProductRustfmt(t *testing.T) {
	root := `C:\Users\mima0000\AppData\Local\super-dolphin-e2e\lsp-native-arm64-v3`
	t.Setenv("SUPER_DOLPHIN_HOME", root)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("RUNTIME_RESOURCES", "")
	server := filepath.Join(root, `cache\lsp-assets\rust-analyzer\2026-08-10.1\arm64\510ccc383eaeb960f1e1a4b8d3115908d389743383c72f43e4bd17bd1a12b5e5\ready\rust-analyzer.exe`)
	base := []string{"PATH=C:\\outside", "KEEP=value"}
	got, err := runtimeServerWindowsRustEnvironment(server, base)
	if err != nil {
		t.Fatal(err)
	}
	pathValue := runtimeServerWindowsEnvironmentValue(got, "PATH")
	if !strings.Contains(strings.ToLower(pathValue), "rustfmt-preview") || !strings.HasSuffix(strings.ToLower(pathValue), `c:\outside`) {
		t.Fatalf("Rustfmt PATH = %q", pathValue)
	}
	if strings.Join(base, "\x00") != "PATH=C:\\outside\x00KEEP=value" {
		t.Fatalf("caller environment changed: %#v", base)
	}
}
