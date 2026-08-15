package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnsureInstalledFindsGoInstallTargetOutsidePATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake go binary")
	}
	fakeBin := t.TempDir()
	installBin := t.TempDir()
	gopath := filepath.Join(t.TempDir(), "gopath")
	fakeGo := filepath.Join(fakeBin, "go")
	script := `#!/bin/sh
set -eu
if [ "$1" = "install" ]; then
  /bin/mkdir -p "$GOBIN"
  printf '#!/bin/sh\nexit 0\n' > "$GOBIN/gopls"
  /bin/chmod +x "$GOBIN/gopls"

  exit 0
fi
if [ "$1" = "env" ]; then
  shift
  for name in "$@"; do
    eval "printf '%s\n' \"\${$name:-}\""
  done
  exit 0
fi
echo "unexpected fake go args: $*" >&2
exit 1
`
	if err := os.WriteFile(fakeGo, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake go): %v", err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("GOBIN", installBin)
	t.Setenv("GOPATH", gopath)

	p := NewProvider()
	p.Register("go", InstallerConfig{
		BinaryName:          "gopls",
		InstallCmd:          "go",
		InstallArgs:         []string{"install", "golang.org/x/tools/gopls@latest"},
		AllowInstallCommand: true,
	})

	result, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "go")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed() error = %v", err)
	}
	want := filepath.Join(installBin, "gopls")
	if result.Path != want {
		t.Fatalf("EnsureInstalledDetailed().Path = %q, want %q", result.Path, want)
	}
	if result.Status != InstallStatusInstalledFallback {
		t.Fatalf("EnsureInstalledDetailed().Status = %q, want %q", result.Status, InstallStatusInstalledFallback)
	}
}

func TestEnsureInstalledFindsCargoInstallTargetOutsidePATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake cargo binary")
	}
	fakeBin := t.TempDir()
	cargoHome := filepath.Join(t.TempDir(), "cargo-home")
	fakeCargo := filepath.Join(fakeBin, "cargo")
	script := `#!/bin/sh
set -eu
if [ "$1" != "install" ] || [ "$2" != "sqruff" ]; then
  echo "unexpected fake cargo args: $*" >&2
  exit 1
fi
/bin/mkdir -p "$CARGO_HOME/bin"
/bin/cat > "$CARGO_HOME/bin/sqruff" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  echo "sqruff 0.29.3"
  exit 0
fi
exit 0
EOF
/bin/chmod +x "$CARGO_HOME/bin/sqruff"
`
	if err := os.WriteFile(fakeCargo, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake cargo): %v", err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("CARGO_HOME", cargoHome)

	p := NewProvider()
	p.Register("sql", InstallerConfig{
		BinaryName:          "sqruff",
		BinaryCheckArgs:     []string{"--version"},
		InstallCmd:          "cargo",
		InstallArgs:         []string{"install", "sqruff"},
		AllowInstallCommand: true,
	})

	result, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "sql")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	want := filepath.Join(cargoHome, "bin", "sqruff")
	if result.Path != want {
		t.Fatalf("EnsureInstalledDetailed(sql).Path = %q, want %q", result.Path, want)
	}
	if result.Status != InstallStatusInstalledFallback {
		t.Fatalf("EnsureInstalledDetailed(sql).Status = %q, want %q", result.Status, InstallStatusInstalledFallback)
	}
}

func TestEnsureInstalledSerializesConcurrentFirstInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake installer")
	}
	binDir := t.TempDir()
	installerPath := filepath.Join(binDir, "install-sqruff")
	script := `#!/bin/sh
set -eu
if ! /bin/mkdir "$INSTALL_LOCK" 2>/dev/null; then
  echo "concurrent installer invocation" >&2
  exit 42
fi
printf 'install\n' >> "$INSTALL_MARKER"
/bin/sleep 1
/bin/cat > "$FAKE_BIN_DIR/sqruff" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  echo "sqruff 0.38.0"
fi
exit 0
EOF
/bin/chmod +x "$FAKE_BIN_DIR/sqruff"
/bin/rmdir "$INSTALL_LOCK"
`
	if err := os.WriteFile(installerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake installer: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "install-count")
	lockDir := filepath.Join(t.TempDir(), "active-install")
	t.Setenv("PATH", binDir)
	t.Setenv("FAKE_BIN_DIR", binDir)
	t.Setenv("INSTALL_MARKER", marker)
	t.Setenv("INSTALL_LOCK", lockDir)

	p := NewProvider()
	p.Register("sql", InstallerConfig{
		BinaryName:          "sqruff",
		BinaryCheckArgs:     []string{"--version"},
		InstallCmd:          installerPath,
		InstallArgs:         []string{"install", "sqruff"},
		AllowInstallCommand: true,
	})

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "sql")
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent EnsureInstalledDetailed: %v", err)
		}
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read install marker: %v", err)
	}
	if got := strings.Count(string(raw), "install\n"); got != 1 {
		t.Fatalf("installer invocation count = %d, want 1", got)
	}
}

func TestEnsureInstalledReusesCargoFallbackOutsidePATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX fake cargo script")
	}
	binDir := t.TempDir()
	cargoHome := filepath.Join(t.TempDir(), "cargo-home")
	marker := filepath.Join(t.TempDir(), "install-count")
	fakeCargo := filepath.Join(binDir, "cargo")
	script := `#!/bin/sh
set -eu
printf 'install\n' >> "$INSTALL_MARKER"
/bin/mkdir -p "$CARGO_HOME/bin"
/bin/cat > "$CARGO_HOME/bin/sqruff" <<'EOF'
#!/bin/sh
echo "sqruff 0.38.0"
EOF
/bin/chmod +x "$CARGO_HOME/bin/sqruff"
`
	if err := os.WriteFile(fakeCargo, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake cargo: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("INSTALL_MARKER", marker)

	p := NewProvider()
	p.Register("sql", InstallerConfig{
		BinaryName: "sqruff", BinaryCheckArgs: []string{"--version"},
		InstallCmd: fakeCargo, InstallArgs: []string{"install", "sqruff"}, AllowInstallCommand: true,
	})
	ctx := WithInstallCommandCapability(context.Background())
	for range 2 {
		if _, err := p.EnsureInstalledDetailed(ctx, "sql"); err != nil {
			t.Fatalf("EnsureInstalledDetailed: %v", err)
		}
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read install marker: %v", err)
	}
	if got := strings.Count(string(raw), "install\n"); got != 1 {
		t.Fatalf("cargo invocation count = %d, want 1", got)
	}
}

func TestExecutableInDirPrefersWindowsExeSuffix(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only path resolution behavior")
	}
	dir := t.TempDir()
	want := filepath.Join(dir, "gopls.exe")
	if err := os.WriteFile(want, []byte("binary"), 0o644); err != nil {
		t.Fatalf("WriteFile(gopls.exe): %v", err)
	}
	got, ok := executableInDir(dir, "gopls")
	if !ok {
		t.Fatal("executableInDir() ok = false, want true")
	}
	if got != want {
		t.Fatalf("executableInDir() = %q, want %q", got, want)
	}
}

func TestEnsureInstalledUsesInstallTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake installer")
	}
	fakeBin := t.TempDir()
	installerPath := filepath.Join(fakeBin, "slow-install")
	if err := os.WriteFile(installerPath, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake installer: %v", err)
	}
	t.Setenv("PATH", fakeBin)

	p := NewProvider()
	p.Register("slow", InstallerConfig{
		BinaryName:          "slow-lsp",
		InstallCmd:          installerPath,
		InstallTimeout:      50 * time.Millisecond,
		AllowInstallCommand: true,
	})

	start := time.Now()
	_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "slow")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("EnsureInstalledDetailed() error = nil, want install timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("EnsureInstalledDetailed() error = %v, want context deadline exceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("EnsureInstalledDetailed() elapsed = %v, want local install timeout to stop slow installer", elapsed)
	}
}

func TestEnsureInstalledRejectsBrokenPathBinaryAndRunsInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell scripts as fake binaries")
	}
	binDir := t.TempDir()
	broken := filepath.Join(binDir, "fake-language-server")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho broken sql language server >&2\nexit 42\n"), 0o755); err != nil {
		t.Fatalf("write broken fake-language-server: %v", err)
	}
	installerPath := filepath.Join(binDir, "install-sql-lsp")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$INSTALL_MARKER"
/bin/cat > "$FAKE_BIN_DIR/fake-language-server" <<'EOF'
#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "1.7.1"
  exit 0
fi
exit 0
EOF
/bin/chmod +x "$FAKE_BIN_DIR/fake-language-server"
`
	if err := os.WriteFile(installerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sql installer: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "installer-args")
	t.Setenv("PATH", binDir)
	t.Setenv("FAKE_BIN_DIR", binDir)
	t.Setenv("INSTALL_MARKER", marker)

	p := NewProvider()
	p.Register("sql", InstallerConfig{
		BinaryName:          "fake-language-server",
		BinaryCheckArgs:     []string{"--version"},
		InstallCmd:          installerPath,
		InstallArgs:         []string{"fake-language-server"},
		AllowInstallCommand: true,
	})

	result, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "sql")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	if result.Path != broken {
		t.Fatalf("EnsureInstalledDetailed(sql).Path = %q, want repaired PATH binary %q", result.Path, broken)
	}
	if result.Status != InstallStatusInstalledPath {
		t.Fatalf("EnsureInstalledDetailed(sql).Status = %q, want %q", result.Status, InstallStatusInstalledPath)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("installer did not run for broken PATH binary: %v", err)
	}
}

func TestEnsureInstalledUsesManagedAbsoluteLauncherWithoutPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX managed launcher")
	}
	t.Setenv("PATH", t.TempDir())
	launcher := filepath.Join(t.TempDir(), "managed", "native-lsp")
	installCalls := 0
	p := NewProvider()
	p.Register("native", InstallerConfig{
		BinaryName:          "native-lsp",
		BinaryCheckArgs:     []string{"--version"},
		AllowInstallCommand: true,
		ManagedBinaryPath:   launcher,
		ManagedInstall: func(context.Context) (string, error) {
			installCalls++
			return writeTestManagedLauncher(launcher)
		},
	})

	result, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "native")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(managed): %v", err)
	}
	if result.Path != launcher || result.Status != InstallStatusInstalledPath || installCalls != 1 {
		t.Fatalf("managed result = %#v calls=%d, want path=%q installed_path once", result, installCalls, launcher)
	}
	second, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "native")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(managed cached path): %v", err)
	}
	if second.Path != launcher || second.Status != InstallStatusPathFound || installCalls != 1 {
		t.Fatalf("managed cached result = %#v calls=%d, want path_found without reinstall", second, installCalls)
	}
}

func writeTestManagedLauncher(launcher string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\necho managed-native-lsp\n"), 0o700); err != nil {
		return "", err
	}
	return launcher, nil
}

func TestEnsureInstalledManagedInstallerHonorsCheckOnlyAndConfigExclusivity(t *testing.T) {
	called := false
	p := NewProvider()
	p.Register("managed", InstallerConfig{
		BinaryName:          "missing-managed-lsp",
		AllowInstallCommand: true,
		ManagedBinaryPath:   filepath.Join(t.TempDir(), "missing-managed-lsp"),
		ManagedInstall: func(context.Context) (string, error) {
			called = true
			return "", errors.New("must not run")
		},
	})
	ctx := WithToolCallInstallCheckOnly(WithInstallCommandCapability(context.Background()))
	if _, err := p.EnsureInstalledDetailed(ctx, "managed"); err == nil {
		t.Fatal("check-only managed install returned nil error")
	}
	if called {
		t.Fatal("check-only tool call executed managed installer")
	}

	p.Register("invalid", InstallerConfig{
		BinaryName:          "invalid-lsp",
		InstallCmd:          "global-installer",
		AllowInstallCommand: true,
		ManagedBinaryPath:   filepath.Join(t.TempDir(), "invalid-lsp"),
		ManagedInstall: func(context.Context) (string, error) {
			return "", nil
		},
	})
	if _, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "invalid"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("invalid dual installer error = %v, want mutual-exclusion failure", err)
	}
}

func TestConfigForLanguageReturnsDetachedSlices(t *testing.T) {
	p := NewProvider()
	p.Register("detached", InstallerConfig{
		BinaryName:      "detached-lsp",
		BinaryCheckArgs: []string{"--version"},
		InstallCmd:      "npm",
		InstallArgs:     []string{"install", "-g", "detached-lsp"},
		RequiredBinaries: []RequiredBinary{{
			Name:      "companion",
			CheckArgs: []string{"--check"},
		}},
	})

	first, ok := p.ConfigForLanguage("detached")
	if !ok {
		t.Fatal("ConfigForLanguage returned no registered configuration")
	}
	first.BinaryCheckArgs[0] = "mutated"
	first.InstallArgs[0] = "mutated"
	first.RequiredBinaries[0].CheckArgs[0] = "mutated"

	second, ok := p.ConfigForLanguage("detached")
	if !ok {
		t.Fatal("ConfigForLanguage lost registered configuration")
	}
	if second.BinaryCheckArgs[0] != "--version" || second.InstallArgs[0] != "install" || second.RequiredBinaries[0].CheckArgs[0] != "--check" {
		t.Fatalf("ConfigForLanguage leaked mutable slices: %#v", second)
	}
}
