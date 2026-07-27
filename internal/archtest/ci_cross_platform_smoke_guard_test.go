package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCICrossPlatformSmokeGuardsDesktopAndSidecars(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "ci.yml")
	scriptPath := filepath.Join(root, "scripts", "ci_cross_platform_smoke.ps1")
	workflow := readGuardFile(t, workflowPath)
	script := readGuardFile(t, scriptPath)

	requiredWorkflowTokens := []string{
		"cross-platform-smoke:",
		"runs-on: ${{ matrix.os }}",
		"windows-latest",
		"macos-latest",
		"scripts/ci_cross_platform_smoke.ps1",
		"cache-dependency-path: frontend-app/package-lock.json",
		"windows-core-tests:",
		"mapfile -t windows_packages < <(go list ./cmd/... ./internal/... ./pkg/... ./scripts/...)",
		`./scripts/test_with_guard.sh --quick-guard "$package" -count=1 -p 1`,
	}
	for _, want := range requiredWorkflowTokens {
		if !strings.Contains(workflow, want) {
			t.Fatalf(".github/workflows/ci.yml cross-platform smoke missing %q", want)
		}
	}

	requiredScriptTokens := []string{
		"Copy-FrontendAppDistToEmbed",
		"frontend-app/dist",
		"cmd/agent-terminal/web-dist",
		"Invoke-GoBuild -Output $mcpOrch -Package './cmd/mcp-orch'",
		"Invoke-GoBuild -Output $mcpLSP -Package './cmd/mcp-lsp'",
		"Invoke-GoBuild -Output $agentTerminal -Package './cmd/agent-terminal'",
		"Invoke-GuardedGoTest ./cmd/mcp-orch ./cmd/mcp-lsp",
		"Invoke-GuardedGoTest ./internal/provider/codexapp",
		"scripts/test_with_guard.ps1",
		"DiscoverProcessesReturnsBothMaps",
		"CleanOrphanedMCPProcessesSkipsSelf",
	}
	for _, want := range requiredScriptTokens {
		if !strings.Contains(script, want) {
			t.Fatalf("scripts/ci_cross_platform_smoke.ps1 missing %q", want)
		}
	}
}

func readGuardFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func repoRootForCICrossPlatformSmokeGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, ".github", "workflows", "ci.yml")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root not found from %s", dir)
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
