package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestHostTestResourceTierUsesActualCPUPercent 锁定 CPU busy 80% 边界与独立内存阻断。
func TestHostTestResourceTierUsesActualCPUPercent(t *testing.T) {
	repositoryRoot := gitRevParseRequired(t, "--show-toplevel")
	scriptPath := filepath.Join(repositoryRoot, "scripts", "test_with_guard.sh")
	command := exec.Command("bash", "-c", `
set -euo pipefail
source "$1"
host_test_resource_tier 49.9 25
host_test_resource_tier 79.9 15
host_test_resource_tier 80.0 90
host_test_resource_tier 10.0 14.9
`, "bash", scriptPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run host resource tier contract: %v\n%s", err, output)
	}
	if got, want := strings.Fields(string(output)), []string{"low", "medium", "high", "high"}; !slices.Equal(got, want) {
		t.Fatalf("host resource tiers = %v, want %v", got, want)
	}
}

// TestHostTestResourceSnapshotRejectsLoadAverageMetric 防止重新用 load average 冒充 CPU 占用。
func TestHostTestResourceSnapshotRejectsLoadAverageMetric(t *testing.T) {
	repositoryRoot := gitRevParseRequired(t, "--show-toplevel")
	payload, err := os.ReadFile(filepath.Join(repositoryRoot, "scripts", "test_with_guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(payload)
	for _, retired := range []string{"vm.loadavg", "/proc/loadavg", "load / cpus", "load_per_cpu="} {
		if strings.Contains(source, retired) {
			t.Fatalf("host admission still contains retired load metric %q", retired)
		}
	}
	for _, required := range []string{"CPU usage:", "/proc/stat", "cpu_busy_percent", "top -l 2 -n 0 -s 5", "sleep 5", "cpu < 80"} {
		if !strings.Contains(source, required) {
			t.Fatalf("host admission is missing CPU busy evidence %q", required)
		}
	}
}
