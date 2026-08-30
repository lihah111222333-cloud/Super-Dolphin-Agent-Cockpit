package shared

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppManagedProviderHomeUsesSuperDolphinHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)

	got, err := AppManagedProviderHome(ProviderCodex)
	if err != nil {
		t.Fatalf("AppManagedProviderHome() error = %v", err)
	}
	want := filepath.Join(superHome, "providers", "codex")
	if got != want {
		t.Fatalf("AppManagedProviderHome() = %q, want %q", got, want)
	}
}

func TestEnsureAppManagedProviderHomeUsesSuperDolphinHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)

	got, err := EnsureAppManagedProviderHome(ProviderClaude)
	if err != nil {
		t.Fatalf("EnsureAppManagedProviderHome() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "claude"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed home: %v", err)
	}
	if got != want {
		t.Fatalf("EnsureAppManagedProviderHome() = %q, want %q", got, want)
	}
	assertDirMode(t, want, 0o700)
}

func TestEnsureAppManagedProviderHomeDoesNotCreateSkillSubdirectory(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)

	home, err := EnsureAppManagedProviderHome(ProviderCodex)
	if err != nil {
		t.Fatalf("EnsureAppManagedProviderHome() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills")); !os.IsNotExist(err) {
		t.Fatalf("provider home skills subdirectory stat error = %v, want no app-managed skill root", err)
	}
}

func TestEnsureProviderHomeFailsWhenChmodFails(t *testing.T) {
	home := protectedProviderHomeForChmodFailure(t)

	_, err := EnsureProviderHome(ProviderCodex, home)
	if err == nil {
		t.Fatalf("EnsureProviderHome() error = nil, want chmod failure")
	}
	assertProviderStartupGateCode(t, err, ProviderHomePermissionFailedCode)
	if !strings.Contains(err.Error(), ProviderCodex) || !strings.Contains(err.Error(), home) {
		t.Fatalf("EnsureProviderHome() error = %v, want provider and path context", err)
	}
}

func TestEnsureProviderHomeDefaultsToUserCLIHome(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	if err := os.MkdirAll(filepath.Join(userHome, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir claude home: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(userHome, ".codex"), 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}

	claudeHome, err := EnsureProviderHome(ProviderClaude, "")
	if err != nil {
		t.Fatalf("EnsureProviderHome(claude) error = %v", err)
	}
	wantClaudeHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".claude"))
	if err != nil {
		t.Fatalf("EvalSymlinks claude home: %v", err)
	}
	if claudeHome != wantClaudeHome {
		t.Fatalf("claude home = %q, want user CLI home", claudeHome)
	}

	codexHome, err := EnsureProviderHome(ProviderCodex, "")
	if err != nil {
		t.Fatalf("EnsureProviderHome(codex) error = %v", err)
	}
	wantCodexHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks codex home: %v", err)
	}
	if codexHome != wantCodexHome {
		t.Fatalf("codex home = %q, want user CLI home", codexHome)
	}
}

func TestEnsureProviderHomeDefaultCLIHomeIsReadOnlyDiagnostic(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	if err := os.MkdirAll(userHome, 0o700); err != nil {
		t.Fatalf("mkdir user home: %v", err)
	}
	defaultClaudeHome := filepath.Join(userHome, ".claude")

	got, err := EnsureProviderHome(ProviderClaude, "")
	if err == nil {
		t.Fatalf("EnsureProviderHome() error = nil, want missing CLI home diagnostic")
	}
	if got != "" {
		t.Fatalf("EnsureProviderHome() = %q, want empty on diagnostic failure", got)
	}
	if !strings.Contains(err.Error(), "resolve provider home realpath") {
		t.Fatalf("EnsureProviderHome() error = %v, want realpath diagnostic", err)
	}
	if _, statErr := os.Stat(defaultClaudeHome); !os.IsNotExist(statErr) {
		t.Fatalf("default CLI home stat error = %v, want not exist and not auto-created", statErr)
	}
}

func TestEnsureProviderHomeCreatesPrivateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "provider-home")

	got, err := EnsureProviderHome(ProviderClaude, home)
	if err != nil {
		t.Fatalf("EnsureProviderHome() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks home: %v", err)
	}
	if got != want {
		t.Fatalf("EnsureProviderHome() = %q, want %q", got, want)
	}
	assertDirMode(t, home, 0o700)
}

func assertDirMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}

func protectedProviderHomeForChmodFailure(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod failure test requires non-root user")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("chmod failure test uses macOS protected system directories")
	}
	home := "/System"
	info, err := os.Stat(home)
	if err != nil {
		t.Skipf("protected provider home %s unavailable: %v", home, err)
	}
	if !info.IsDir() {
		t.Skipf("protected provider home %s is not a directory", home)
	}
	return home
}

func assertProviderStartupGateCode(t *testing.T, err error, want string) {
	t.Helper()
	var coded interface {
		GateCode() string
	}
	if !errors.As(err, &coded) {
		t.Fatalf("error = %T %v, want provider startup gate code %q", err, err, want)
	}
	if got := coded.GateCode(); got != want {
		t.Fatalf("GateCode() = %q, want %q; error=%v", got, want, err)
	}
}

func TestProviderHomeDevEmptyIgnoresPackagedLeftovers(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv(PackagedCodexEnv, "1")
	t.Setenv(RuntimeModeEnv, RuntimeModeDev)
	if err := os.MkdirAll(filepath.Join(userHome, ".codex"), 0o700); err != nil {
		t.Fatalf("mkdir local codex home: %v", err)
	}

	home, err := EnsureProviderHome(ProviderCodex, "")
	if err != nil {
		t.Fatalf("EnsureProviderHome(codex) error = %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks local codex home: %v", err)
	}
	if home != wantHome {
		t.Fatalf("codex home = %q, want local CLI home %q", home, wantHome)
	}
}

func TestPackagedRuntimeFromEnvRejectsInvalidRuntimeMode(t *testing.T) {
	t.Setenv(RuntimeModeEnv, "bogus")
	if _, err := PackagedRuntimeFromEnv(); err == nil || !strings.Contains(err.Error(), RuntimeModeEnv) {
		t.Fatalf("PackagedRuntimeFromEnv() error = %v, want invalid runtime mode", err)
	}
}

func TestAppManagedProviderHomeRequiresSuperDolphinHome(t *testing.T) {
	t.Setenv(SuperDolphinHomeEnv, "")

	got, err := AppManagedProviderHome(ProviderCodex)
	if err == nil || !strings.Contains(err.Error(), SuperDolphinHomeEnv) {
		t.Fatalf("AppManagedProviderHome() = %q, %v; want missing runtime home error", got, err)
	}
}
