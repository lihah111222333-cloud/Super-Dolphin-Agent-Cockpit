//go:build windows && e2e

package main

import (
	"go/build"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWindowsLocalGoCacheAcceptsCanonicalGoToolDirE2E 验证 Git Bash 能把
// Windows Go 返回的盘符工具目录纳入本地缓存身份。
func TestWindowsLocalGoCacheAcceptsCanonicalGoToolDirE2E(t *testing.T) {
	repositoryRoot := gitRevParseRequired(t, "--show-toplevel")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("Git Bash is required: %v", err)
	}
	scriptPath := filepath.Join(repositoryRoot, "scripts", "local_go_cache.sh")
	realGo := filepath.Join(build.Default.GOROOT, "bin", "go.exe")
	command := exec.Command(bash, "-c", `
set -euo pipefail
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) ;;
  *) printf 'Git Bash is required, got %s\n' "$(uname -s)" >&2; exit 1 ;;
esac
source "$1"
identity_file="$(mktemp)"
trap 'rm -f -- "$identity_file"' EXIT
local_go_cache_identity "$2" "$3" "$identity_file"
grep -q '^tool:compile=' "$identity_file"
`, "bash", scriptPath, repositoryRoot, realGo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Windows local Go cache identity: %v\n%s", err, output)
	}
}
