//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestRuntimeServerWindowsSwiftOwnedLaunchUsesReceiptSDKAndResource(t *testing.T) {
	productRoot := `C:\Program Files\Super Dolphin`
	result := runtimeServerWindowsSwiftTestReceipt(productRoot)
	args, err := runtimeServerWindowsSwiftLaunchArgsWithResolver(
		result.ServerPath,
		[]string{"--caller-arg"},
		productRoot,
		func(string) (installer.WindowsRuntimeDependencyProvisionResult, error) { return result, nil },
		runtimeServerWindowsSwiftTestShortPathResolver,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeServerWindowsSwiftHasSDKArgument(args, runtimeServerWindowsSwiftTestShortPath(productRoot, installer.WindowsSwiftSourceKitLSPPlatformSDK(result.RootPath))) {
		t.Fatalf("Swift launch args lost pinned SDK: %v", args)
	}
	if !runtimeServerWindowsSwiftHasResourceArgument(args, runtimeServerWindowsSwiftTestShortPath(productRoot, installer.WindowsSwiftSourceKitLSPToolchainResource(result.RootPath))) {
		t.Fatalf("Swift launch args lost pinned resource-dir: %v", args)
	}
	if !runtimeServerWindowsSwiftHasFlag(args, "-resource-dir") {
		t.Fatalf("Swift launch args did not add resource-dir: %v", args)
	}
}

func TestRuntimeServerWindowsSwiftOwnedEnvironmentAllowlistsVCLibsAndSystem32(t *testing.T) {
	productRoot := `C:\Program Files\Super Dolphin`
	result := runtimeServerWindowsSwiftTestReceipt(productRoot)
	env, err := runtimeServerWindowsSwiftEnvironmentWithResolver(
		result.ServerPath,
		[]string{
			// C:\evil may contain swift.exe/sourcekit-lsp.exe; its name intentionally
			// does not mention Swift so directory-name filtering cannot protect us.
			"PATH=C:\\evil;C:\\VCLibs;C:\\Windows\\System32;C:\\Parent\\bin",
			"SUPER_DOLPHIN_MSVC_RUNTIME_DIR=C:\\VCLibs",
			"SystemRoot=C:\\Windows",
			"KEEP=1",
		},
		productRoot,
		func(string) (installer.WindowsRuntimeDependencyProvisionResult, error) { return result, nil },
		runtimeServerWindowsSwiftTestShortPathResolver,
	)
	if err != nil {
		t.Fatal(err)
	}
	pathValue := runtimeServerWindowsEnvironmentValue(env, "PATH")
	shortRoot := runtimeServerWindowsSwiftTestShortPath(productRoot, result.RootPath)
	shortBin := runtimeServerWindowsSwiftTestShortPath(productRoot, installer.WindowsSwiftSourceKitLSPToolchainBin(result.RootPath))
	shortRuntime := runtimeServerWindowsSwiftTestShortPath(productRoot, installer.WindowsSwiftSourceKitLSPRuntimeRoot(result.RootPath))
	if !strings.HasPrefix(pathValue, shortRuntime+string(os.PathListSeparator)+shortBin+string(os.PathListSeparator)+shortRoot) {
		t.Fatalf("Swift PATH does not begin with short cohort/toolchain roots: %q", pathValue)
	}
	for _, blocked := range []string{`C:\evil`, `C:\Parent\bin`} {
		if strings.Contains(strings.ToLower(pathValue), strings.ToLower(blocked)) {
			t.Fatalf("Swift PATH retained an untrusted inherited directory %q: %q", blocked, pathValue)
		}
	}
	if !strings.Contains(pathValue, `C:\VCLibs;C:\Windows\System32`) {
		t.Fatalf("Swift PATH dropped allowlisted VCLibs/System32 entries: %q", pathValue)
	}
	if got := runtimeServerWindowsEnvironmentValue(env, "SOURCEKIT_TOOLCHAIN_PATH"); got != runtimeServerWindowsSwiftTestShortPath(productRoot, installer.WindowsSwiftSourceKitLSPToolchainRoot(result.RootPath)) {
		t.Fatalf("SOURCEKIT_TOOLCHAIN_PATH = %q", got)
	}
	if got := runtimeServerWindowsEnvironmentValue(env, "SDKROOT"); got != runtimeServerWindowsSwiftTestShortPath(productRoot, installer.WindowsSwiftSourceKitLSPPlatformSDK(result.RootPath)) {
		t.Fatalf("SDKROOT = %q", got)
	}
}

func TestRuntimeServerWindowsSwiftExternalSourceKitIsUnchanged(t *testing.T) {
	external := `C:\External\sourcekit-lsp.exe`
	wantArgs := []string{"--external"}
	args, err := runtimeServerWindowsSwiftLaunchArgs(external, wantArgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("external Swift args changed: got=%v want=%v", args, wantArgs)
	}
	wantEnv := []string{"PATH=C:\\External"}
	env, err := runtimeServerWindowsSwiftEnvironment(external, wantEnv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(env, "\x00") != strings.Join(wantEnv, "\x00") {
		t.Fatalf("external Swift environment changed: got=%v want=%v", env, wantEnv)
	}
}

func TestRuntimeServerWindowsSwiftRejectsReceiptRootEscape(t *testing.T) {
	productRoot := `C:\Program Files\Super Dolphin`
	result := runtimeServerWindowsSwiftTestReceipt(`C:\Outside\cohort`)
	_, err := runtimeServerWindowsSwiftLaunchArgsWithResolver(
		result.ServerPath,
		nil,
		productRoot,
		func(string) (installer.WindowsRuntimeDependencyProvisionResult, error) { return result, nil },
		runtimeServerWindowsSwiftTestShortPathResolver,
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "escapes product root") {
		t.Fatalf("root escape error = %v", err)
	}
}

func TestRuntimeServerWindowsSwiftPreservesACLAuthorizationRequired(t *testing.T) {
	productRoot := `C:\Program Files\Super Dolphin`
	result := runtimeServerWindowsSwiftTestReceipt(productRoot)
	aclErr := fmt.Errorf("authorization_required win32=5: %w", syscall.Errno(5))
	_, err := runtimeServerWindowsSwiftEnvironmentWithResolver(
		result.ServerPath,
		nil,
		productRoot,
		func(string) (installer.WindowsRuntimeDependencyProvisionResult, error) { return result, nil },
		func(string, string) (string, error) { return "", aclErr },
	)
	if err == nil || !strings.Contains(err.Error(), "authorization_required") || !errors.Is(err, syscall.Errno(5)) {
		t.Fatalf("ACL authorization_required was not preserved: %v", err)
	}
}

func runtimeServerWindowsSwiftTestReceipt(productRoot string) installer.WindowsRuntimeDependencyProvisionResult {
	root := filepath.Join(productRoot, "cache", "lsp-assets", "swift-sourcekit-lsp", "arm64", "swift-toolchain-6.3.3")
	server := filepath.Join(installer.WindowsSwiftSourceKitLSPToolchainBin(root), "sourcekit-lsp.exe")
	return installer.WindowsRuntimeDependencyProvisionResult{
		Product:    installer.WindowsRuntimeDependencyProductSwiftSourceKitLS,
		RootPath:   root,
		ServerPath: server,
		Args:       installer.WindowsSwiftSourceKitLSPLaunchArgs(root),
		Env:        []string{"SDKROOT=" + installer.WindowsSwiftSourceKitLSPPlatformSDK(root)},
	}
}

func runtimeServerWindowsSwiftTestShortPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(`S:\`, relative)
}

func runtimeServerWindowsSwiftTestShortPathResolver(root, path string) (string, error) {
	return runtimeServerWindowsSwiftTestShortPath(root, path), nil
}
