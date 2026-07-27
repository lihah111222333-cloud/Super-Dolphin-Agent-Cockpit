package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlitereleasegate"
	"gopkg.in/yaml.v3"
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

	workflow := readRequiredFile(t, filepath.Join(root, ".github", "workflows", "sqlite-release-gates.yml"))
	for _, want := range []string{
		"ubuntu-latest",
		"windows-latest",
		"macos-latest",
		"go run ./scripts/sqlite_release_gates",
		"sqlite-release-gate-report.md",
		".sqlite-release-gate-logs",
		"actions/upload-artifact",
		"G12",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("sqlite release gate workflow missing %q", want)
		}
	}
}

func TestSQLiteReleaseGateWorkflowArtifactsAreUploadableAndUnique(t *testing.T) {
	root := scriptRepoRoot(t)
	workflow := readRequiredFile(t, filepath.Join(root, ".github", "workflows", "sqlite-release-gates.yml"))
	for _, want := range []string{
		"name: sqlite-release-gate-report-full-ubuntu-latest",
		"name: sqlite-release-gate-raw-logs-full-ubuntu-latest",
		"name: sqlite-release-gate-report-g12-${{ matrix.os }}",
		"name: sqlite-release-gate-raw-logs-g12-${{ matrix.os }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("sqlite release gate workflow missing unique artifact name %q", want)
		}
	}
	for _, old := range []string{
		"name: sqlite-release-gate-report-ubuntu-latest",
		"name: sqlite-release-gate-raw-logs-ubuntu-latest",
		"name: sqlite-release-gate-report-${{ matrix.os }}",
		"name: sqlite-release-gate-raw-logs-${{ matrix.os }}",
	} {
		if strings.Contains(workflow, old) {
			t.Fatalf("sqlite release gate workflow still has colliding artifact name %q", old)
		}
	}
	if got := strings.Count(workflow, "include-hidden-files: true"); got < 2 {
		t.Fatalf("workflow has include-hidden-files: true count = %d, want at least raw log uploads", got)
	}
}

func TestSQLiteReleaseGateWorkflowProvidesLinuxDesktopRuntime(t *testing.T) {
	root := scriptRepoRoot(t)
	workflow := parseSQLiteReleaseGateWorkflow(t, filepath.Join(root, ".github", "workflows", "sqlite-release-gates.yml"))
	fullJob := requireSQLiteReleaseGateWorkflowJob(t, workflow, "sqlite-release-gates")
	matrixJob := requireSQLiteReleaseGateWorkflowJob(t, workflow, "sqlite-packaging-smoke")

	const setupNode = "actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020"
	assertSQLiteWorkflowUse(t, fullJob, setupNode)
	assertSQLiteWorkflowUse(t, matrixJob, setupNode)
	assertSQLiteWorkflowWorkingRun(t, fullJob, "frontend-app", "npm ci", "npm run build")
	assertSQLiteWorkflowWorkingRun(t, matrixJob, "frontend-app", "npm ci", "npm run build")
	assertSQLiteWorkflowRun(t, fullJob, "", "sudo apt-get install -y pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev xvfb")
	assertSQLiteWorkflowRun(t, fullJob, "", "xvfb-run -a go run ./scripts/sqlite_release_gates")
	assertSQLiteWorkflowRun(t, matrixJob, "runner.os == 'Linux'", "sudo apt-get install -y pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev xvfb")
	assertSQLiteWorkflowRun(t, matrixJob, "runner.os == 'Linux'", "xvfb-run -a go run ./scripts/sqlite_release_gates -only G12")
	assertSQLiteWorkflowRun(t, matrixJob, "runner.os != 'Linux'", "go run ./scripts/sqlite_release_gates -only G12")
}

type sqliteReleaseGateWorkflow struct {
	Jobs map[string]sqliteReleaseGateWorkflowJob `yaml:"jobs"`
}

type sqliteReleaseGateWorkflowJob struct {
	Steps []sqliteReleaseGateWorkflowStep `yaml:"steps"`
}

type sqliteReleaseGateWorkflowStep struct {
	If               string `yaml:"if"`
	Run              string `yaml:"run"`
	Uses             string `yaml:"uses"`
	WorkingDirectory string `yaml:"working-directory"`
}

func parseSQLiteReleaseGateWorkflow(t *testing.T, path string) sqliteReleaseGateWorkflow {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SQLite release workflow: %v", err)
	}
	var workflow sqliteReleaseGateWorkflow
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("parse SQLite release workflow: %v", err)
	}
	return workflow
}

func requireSQLiteReleaseGateWorkflowJob(t *testing.T, workflow sqliteReleaseGateWorkflow, name string) sqliteReleaseGateWorkflowJob {
	t.Helper()
	job, ok := workflow.Jobs[name]
	if !ok {
		t.Fatalf("SQLite release workflow missing job %q", name)
	}
	return job
}

func assertSQLiteWorkflowRun(t *testing.T, job sqliteReleaseGateWorkflowJob, condition, command string) {
	t.Helper()
	for _, step := range job.Steps {
		if strings.TrimSpace(step.If) == condition && strings.Contains(step.Run, command) {
			return
		}
	}
	t.Fatalf("SQLite release workflow job missing run %q with if=%q", command, condition)
}

func assertSQLiteWorkflowUse(t *testing.T, job sqliteReleaseGateWorkflowJob, action string) {
	t.Helper()
	for _, step := range job.Steps {
		if step.Uses == action {
			return
		}
	}
	t.Fatalf("SQLite release workflow job missing action %q", action)
}

func assertSQLiteWorkflowWorkingRun(t *testing.T, job sqliteReleaseGateWorkflowJob, directory string, commands ...string) {
	t.Helper()
	for _, step := range job.Steps {
		if step.WorkingDirectory != directory {
			continue
		}
		matches := true
		for _, command := range commands {
			matches = matches && strings.Contains(step.Run, command)
		}
		if matches {
			return
		}
	}
	t.Fatalf("SQLite release workflow job missing commands %v under %q", commands, directory)
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
