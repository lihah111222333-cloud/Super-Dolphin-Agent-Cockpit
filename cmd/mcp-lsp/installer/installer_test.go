package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		BinaryName:  "gopls",
		InstallCmd:  "go",
		InstallArgs: []string{"install", "golang.org/x/tools/gopls@latest"},
	})

	result, err := p.EnsureInstalledDetailed(context.Background(), "go")
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
		BinaryName:     "slow-lsp",
		InstallCmd:     installerPath,
		InstallTimeout: 50 * time.Millisecond,
	})

	start := time.Now()
	_, err := p.EnsureInstalledDetailed(context.Background(), "slow")
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
