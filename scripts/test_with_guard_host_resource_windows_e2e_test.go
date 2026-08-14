//go:build windows && e2e

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsHostTestResourceSnapshotE2E 验证 Git Bash 能读取 Windows 实际 CPU busy、
// 逻辑 CPU 和可用内存证据，从而让轻量本机 RED/GREEN 继续经过唯一守卫入口。
func TestWindowsHostTestResourceSnapshotE2E(t *testing.T) {
	repositoryRoot := gitRevParseRequired(t, "--show-toplevel")
	scriptPath := filepath.Join(repositoryRoot, "scripts", "test_with_guard.sh")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("Git Bash is required: %v", err)
	}
	command := exec.Command(bash, "-c", `
set -euo pipefail
source "$1"
snapshot="$(host_test_resource_snapshot)"
read -r tier cpu_busy logical_cpus memory_free <<<"$snapshot"
[[ "$tier" =~ ^(low|medium|high)$ ]]
[[ "$cpu_busy" =~ ^[0-9]+([.][0-9]+)?$ ]]
[[ "$logical_cpus" =~ ^[0-9]+$ && "$logical_cpus" -gt 0 ]]
[[ "$memory_free" =~ ^[0-9]+([.][0-9]+)?$ ]]
printf '%s\n' "$snapshot"
`, "bash", scriptPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Windows host resource snapshot: %v\n%s", err, output)
	}
	if len(strings.Fields(string(output))) != 4 {
		t.Fatalf("Windows host resource snapshot = %q, want four fields", output)
	}
}
