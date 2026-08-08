package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlitereleasegate"
)

func TestSQLiteReleaseGateDefinitionsAreRunnableFromRepoRoot(t *testing.T) {
	for _, gate := range sqlitereleasegate.Definitions() {
		if gate.CWD != "." {
			t.Fatalf("%s cwd = %q, want repo root", gate.ID, gate.CWD)
		}
		if len(gate.Command) < 2 {
			t.Fatalf("%s command = %#v, want executable plus args", gate.ID, gate.Command)
		}
		if strings.Contains(gate.CommandString(), "go -C backend") {
			t.Fatalf("%s command uses forbidden backend submodule: %s", gate.ID, gate.CommandString())
		}
	}
}

func TestSQLiteReleaseGatePackageSmokeCommands(t *testing.T) {
	root := scriptRepoRoot(t)
	requiredPackageGuards := []string{
		"scripts/package_linux_guard_test.go",
		"scripts/package_macos_release_guard_test.go",
		"scripts/package_windows_guard_test.go",
	}
	for _, rel := range requiredPackageGuards {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing package guard %s: %v", rel, err)
		}
	}

	g12 := findSQLiteGate(t, "G12")
	command := g12.CommandString()
	for _, want := range []string{
		"-v",
		"TestPackageLinux",
		"TestPackageMacOS",
		"TestMacOS",
		"TestPackageWindows",
		"TestSQLiteReleaseGatePackageSmokeRuntime",
		"TestSQLiteReleaseGatePackageSmokeCommands",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("G12 command %q missing package smoke selector %q", command, want)
		}
	}
}

func TestSQLiteReleaseGatePackageSmokeDoesNotUsePlaceholderRuntime(t *testing.T) {
	root := scriptRepoRoot(t)
	body := readRequiredFile(t, filepath.Join(root, "scripts", "sqlite_release_gate_package_smoke_runtime_test.go"))
	for _, forbidden := range []string{
		"packageSmokeBinaryBody",
		"placeholder",
		"exit 0",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("package smoke runtime test still contains fake runtime marker %q", forbidden)
		}
	}
	for _, want := range []string{
		"go build",
		"exec.Command",
		"SUPER_DOLPHIN_PACKAGED_LAUNCHER",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("package smoke runtime test missing real package launch evidence %q", want)
		}
	}
}

func TestSQLiteReleaseGateMCPOrchSmokeFileIsPresent(t *testing.T) {
	root := scriptRepoRoot(t)
	body := readRequiredFile(t, filepath.Join(root, "cmd", "mcp-orch", "sqlite_smoke_test.go"))
	for _, want := range []string{
		"func TestSQLiteMCPOrch",
		"verifyMCPOrchDatabaseReady",
		"DATABASE_URL",
		"POSTGRES_CONNECTION_STRING",
		"SUPER_DOLPHIN_SQLITE_PATH",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("cmd/mcp-orch/sqlite_smoke_test.go missing %q", want)
		}
	}
}

func TestSQLiteReleaseGateDocsAndWorkflowArePresent(t *testing.T) {
	root := scriptRepoRoot(t)
	for _, doc := range []struct {
		path string
		want []string
	}{
		{
			path: "docs/cc/数据库切换/sqlite-backup-restore.md",
			want: []string{
				"PRAGMA integrity_check",
				"PRAGMA foreign_key_check",
				".db/.wal/.shm",
				"wal_checkpoint(TRUNCATE)",
				"thread",
				"prompt",
				"cron",
				"DAG",
			},
		},
		{
			path: "docs/cc/数据库切换/sqlite-release-gate-report.md",
			want: []string{
				"Commit SHA",
				"Gate",
				"G1",
				"G14",
				"sqlite_stress",
				"TestSQLiteLargeFixtureStressExplicitRun",
				"Raw log artifact",
				"Result",
				"PASS",
				"FAIL",
			},
		},
	} {
		body := readRequiredFile(t, filepath.Join(root, filepath.FromSlash(doc.path)))
		for _, want := range doc.want {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %q", doc.path, want)
			}
		}
	}

}

func findSQLiteGate(t *testing.T, id string) sqlitereleasegate.Gate {
	t.Helper()
	for _, gate := range sqlitereleasegate.Definitions() {
		if gate.ID == id {
			return gate
		}
	}
	t.Fatalf("gate %s not found", id)
	return sqlitereleasegate.Gate{}
}

func scriptRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, ".."))
}

func readRequiredFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read required file %s: %v", path, err)
	}
	return string(body)
}
