package installer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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

	got, err := p.EnsureInstalled(context.Background(), "go")
	if err != nil {
		t.Fatalf("EnsureInstalled() error = %v", err)
	}
	want := filepath.Join(installBin, "gopls")
	if got != want {
		t.Fatalf("EnsureInstalled() = %q, want %q", got, want)
	}
}
