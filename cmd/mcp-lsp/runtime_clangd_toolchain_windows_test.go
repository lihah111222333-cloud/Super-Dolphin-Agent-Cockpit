//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func TestRuntimeServerWindowsClangdArgumentsInjectsProductLLVMDrivers(t *testing.T) {
	serverBinary, binDir := newWindowsClangdToolchainFixture(t)
	command := multilsp.ServerCommand{Executable: "clangd"}

	args, err := runtimeServerWindowsClangdArguments(command, serverBinary)
	if err != nil {
		t.Fatalf("runtimeServerWindowsClangdArguments() error = %v", err)
	}
	if len(args) != 1 || !strings.HasPrefix(args[0], "--query-driver=") {
		t.Fatalf("clangd args = %#v, want one query-driver argument", args)
	}
	for _, name := range []string{"clang++.exe", "clang-cl.exe", "clang.exe"} {
		want := filepath.Join(binDir, name)
		if !strings.Contains(strings.ToLower(args[0]), strings.ToLower(want)) {
			t.Fatalf("clangd query-driver = %q, missing product driver %q", args[0], want)
		}
	}
}

func TestRuntimeServerWindowsClangdEnvironmentPrependsProductLLVMDrivers(t *testing.T) {
	serverBinary, binDir := newWindowsClangdToolchainFixture(t)
	t.Setenv("PATH", `C:\system\path`)

	got, err := runtimeServerWindowsClangdEnvironment(serverBinary, []string{"PATH=C:\\caller\\path", "LANG=C"})
	if err != nil {
		t.Fatalf("runtimeServerWindowsClangdEnvironment() error = %v", err)
	}
	wantPath := binDir + string(os.PathListSeparator) + `C:\caller\path`
	if gotPath := runtimeServerWindowsEnvironmentValue(got, "PATH"); gotPath != wantPath {
		t.Fatalf("clangd PATH = %q, want %q", gotPath, wantPath)
	}
	if gotLang := runtimeServerWindowsEnvironmentValue(got, "LANG"); gotLang != "C" {
		t.Fatalf("clangd LANG = %q, want C", gotLang)
	}
}

func TestRuntimeServerWindowsClangdDriversRejectsTamperedRegularFile(t *testing.T) {
	serverBinary, binDir := newWindowsClangdToolchainFixture(t)
	if err := os.WriteFile(filepath.Join(binDir, "clang.exe"), []byte("tampered regular file"), 0o700); err != nil {
		t.Fatalf("tamper product LLVM driver: %v", err)
	}

	_, err := runtimeServerWindowsClangdArguments(multilsp.ServerCommand{Executable: "clangd"}, serverBinary)
	if err == nil {
		t.Fatal("runtimeServerWindowsClangdArguments() error = nil, want tampered driver rejection")
	}
}

func TestRuntimeServerWindowsClangdDriversRejectsWrongPEArchitecture(t *testing.T) {
	serverBinary, binDir := newWindowsClangdToolchainFixture(t)
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	wrongMachine := installer.WindowsImageFileMachineARM64
	if platform.NativeArch == installer.WindowsHostArchARM64 {
		wrongMachine = installer.WindowsImageFileMachineAMD64
	}
	if err := rewriteWindowsPEMachine(filepath.Join(binDir, "clang-cl.exe"), wrongMachine); err != nil {
		t.Fatalf("rewrite product LLVM driver PE machine: %v", err)
	}

	_, err = runtimeServerWindowsClangdArguments(multilsp.ServerCommand{Executable: "clangd"}, serverBinary)
	if err == nil {
		t.Fatal("runtimeServerWindowsClangdArguments() error = nil, want wrong PE architecture rejection")
	}
}

func TestRuntimeServerWindowsClangdExternalBinaryKeepsArgumentsAndEnvironment(t *testing.T) {
	root := t.TempDir()
	serverBinary := filepath.Join(root, "clangd.exe")
	if err := os.WriteFile(serverBinary, []byte("clangd executable marker"), 0o700); err != nil {
		t.Fatalf("write external clangd marker: %v", err)
	}
	wantArgs := []string{"--compile-commands-dir=C:\\workspace"}
	command := multilsp.ServerCommand{Executable: "clangd", Args: wantArgs}
	gotArgs, err := runtimeServerWindowsClangdArguments(command, serverBinary)
	if err != nil {
		t.Fatalf("runtimeServerWindowsClangdArguments() external error = %v", err)
	}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("external clangd args = %#v, want %#v", gotArgs, wantArgs)
	}
	wantEnv := []string{"PATH=C:\\caller\\path", "LANG=C"}
	gotEnv, err := runtimeServerWindowsClangdEnvironment(serverBinary, wantEnv)
	if err != nil {
		t.Fatalf("runtimeServerWindowsClangdEnvironment() external error = %v", err)
	}
	if !slices.Equal(gotEnv, wantEnv) {
		t.Fatalf("external clangd env = %#v, want %#v", gotEnv, wantEnv)
	}
}

func newWindowsClangdToolchainFixture(t *testing.T) (string, string) {
	t.Helper()
	productRoot := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	asset, err := installer.WindowsLSPAssetForPlatform(installer.WindowsLSPProductClangd, platform)
	if err != nil {
		t.Fatalf("resolve clangd asset for %q: %v", platform.NativeArch, err)
	}
	readyRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, string(installer.WindowsLSPProductClangd), asset.Version, asset.Architecture, strings.ToLower(asset.SHA256), "ready")
	binDir := filepath.Dir(filepath.Join(readyRoot, filepath.FromSlash(asset.BinaryPath)))
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("create product LLVM bin directory: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	executableBytes, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	for _, name := range []string{"clangd.exe", "clang++.exe", "clang-cl.exe", "clang.exe"} {
		if err := os.WriteFile(filepath.Join(binDir, name), executableBytes, 0o700); err != nil {
			t.Fatalf("write product LLVM driver %q: %v", name, err)
		}
	}
	return filepath.Join(binDir, "clangd.exe"), binDir
}

func rewriteWindowsPEMachine(path string, machine uint16) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(contents) < 0x40 {
		return fmt.Errorf("PE image is shorter than DOS header")
	}
	peOffset := int(binary.LittleEndian.Uint32(contents[0x3c:0x40]))
	if peOffset < 0 || peOffset+6 > len(contents) || string(contents[peOffset:peOffset+4]) != "PE\x00\x00" {
		return fmt.Errorf("PE signature is invalid")
	}
	binary.LittleEndian.PutUint16(contents[peOffset+4:peOffset+6], machine)
	return os.WriteFile(path, contents, 0o700)
}
