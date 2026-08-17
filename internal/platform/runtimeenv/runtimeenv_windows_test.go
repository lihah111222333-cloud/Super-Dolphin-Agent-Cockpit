//go:build windows

package runtimeenv

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// 这些测试依赖 Windows 路径、APPDATA 和 .exe 规则，使用源码 build tag 隔离平台行为。
func TestPackagedResourcesDirDetectsWindowsBinExecutable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Super Dolphin")
	makeDirs(t, root)
	writeRuntimeManifestFixture(t, root, "windows-amd64")

	got, err := packagedResourcesDirForOS("windows", filepath.Join(root, "bin", "agent-terminal.exe"))
	if err != nil {
		t.Fatalf("packagedResourcesDirForOS(windows): %v", err)
	}
	if got != root {
		t.Fatalf("packagedResourcesDirForOS(windows) = %q, want %q", got, root)
	}
}

func TestPackagedAppDataDirUsesWindowsRoamingAppData(t *testing.T) {
	t.Setenv("APPDATA", "")

	got := packagedAppDataDirForOS("windows", `C:\Users\alice`)
	want := filepath.Join(`C:\Users\alice`, "AppData", "Roaming", "Super Dolphin")
	if got != want {
		t.Fatalf("packagedAppDataDirForOS(windows) = %q, want %q", got, want)
	}
}

func TestPackagedAppDataDirPrefersWindowsAppDataEnv(t *testing.T) {
	t.Setenv("APPDATA", `D:\Users\alice\Roaming`)

	got := packagedAppDataDirForOS("windows", `C:\Users\alice`)
	want := filepath.Join(`D:\Users\alice\Roaming`, "Super Dolphin")
	if got != want {
		t.Fatalf("packagedAppDataDirForOS(windows) = %q, want %q", got, want)
	}
}

func TestPackagedPathEntriesForWindowsOmitUnixSystemDirs(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "Super Dolphin")
	rt := packagedRuntimeFromResourcesForOS("windows", resources, `C:\Users\alice`)
	t.Setenv("SystemRoot", `C:\Windows`)

	got := packagedPathEntriesForOS("windows", rt)
	for _, want := range []string{
		filepath.Join(resources, "bin"),
		filepath.Join(resources, "lsp", "bin"),
		filepath.Join(resources, "lsp", "node"),
		filepath.Join(resources, "lsp", "node_modules", ".bin"),
		filepath.Join(`C:\Windows`, "System32"),
		filepath.Join(`C:\Windows`),
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("packagedPathEntriesForOS(windows) = %#v, missing %q", got, want)
		}
	}
	for _, forbidden := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("packagedPathEntriesForOS(windows) = %#v, must not include %q", got, forbidden)
		}
	}
}

func TestRequireExecutableFileForWindowsAcceptsDotExeWithoutUnixExecBit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.exe")
	if err := os.WriteFile(path, []byte("windows binary fixture"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if err := requireExecutableFileForOS("windows", path); err != nil {
		t.Fatalf("requireExecutableFileForOS(windows) error = %v, want nil", err)
	}
}
