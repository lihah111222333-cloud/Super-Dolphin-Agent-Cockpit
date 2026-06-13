package sqlitereleasegate

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGateDefinitionsCoverG1ThroughG14(t *testing.T) {
	gates := Definitions()
	if len(gates) != 14 {
		t.Fatalf("gate count = %d, want 14", len(gates))
	}
	for i, gate := range gates {
		wantID := "G" + strconv.Itoa(i+1)
		if gate.ID != wantID {
			t.Fatalf("gate[%d].ID = %q, want %q", i, gate.ID, wantID)
		}
		if gate.Priority == "" {
			t.Fatalf("%s priority is empty", gate.ID)
		}
		if len(gate.Command) == 0 {
			t.Fatalf("%s command is empty", gate.ID)
		}
	}
}

func TestGateDefinitionsDoNotReferenceStaleSelectors(t *testing.T) {
	staleSelectors := []string{
		"TestNewDBCreatesSQLiteFileAndRunsMigrations",
		"TestSQLiteStoreRegression",
		"TestMCPOrchSQLiteSmoke",
		"TestSQLitePromptRecall",
		"TestSQLiteTaskDagRunEvent",
		"TestSQLiteConcurrentRunEvent",
		"TestSQLiteIgnoresOldPostgres",
	}
	for _, gate := range Definitions() {
		command := gate.CommandString()
		for _, selector := range staleSelectors {
			if strings.Contains(command, selector) {
				t.Fatalf("%s command references stale test selector %q: %s", gate.ID, selector, command)
			}
		}
	}
}

func TestMCPOrchSQLiteGateCommandsUseFocusedRunnablePackages(t *testing.T) {
	focusedPackages := []string{
		"./cmd/mcp-orch",
		"./cmd/mcp-orch/fxadapter",
		"./cmd/mcp-orch/orchestration",
		"./cmd/mcp-orch/store/commandcard",
		"./cmd/mcp-orch/store/workspace",
		"./cmd/mcp-orch/store/taskdag",
		"./cmd/mcp-orch/tools",
	}
	for _, gateID := range []string{"G5", "G14"} {
		gate := findGateForTest(t, gateID)
		command := gate.CommandString()
		if strings.Contains(command, "./cmd/mcp-orch/...") {
			t.Fatalf("%s command compiles all mcp-orch packages instead of the focused SQLite smoke set: %s", gateID, command)
		}
		for _, excluded := range []string{
			"./cmd/mcp-orch/store/prompt",
			"./cmd/mcp-orch/store/sharedfile",
		} {
			if commandHasArg(gate.Command, excluded) {
				t.Fatalf("%s command includes package with unrelated legacy test compile debt %s: %s", gateID, excluded, command)
			}
		}
		for _, want := range focusedPackages {
			if !commandHasArg(gate.Command, want) {
				t.Fatalf("%s command missing focused mcp-orch SQLite package %s: %s", gateID, want, command)
			}
		}
	}
}

func TestRegressionBackupRestoreGateKeepsRequiredSQLiteCoverage(t *testing.T) {
	g14 := findGateForTest(t, "G14")
	for _, want := range []string{
		"./internal/platform/db/sqlite",
		"./internal/module/dashboard",
		"./internal/store/...",
	} {
		if !commandHasArg(g14.Command, want) {
			t.Fatalf("G14 command missing required package %s: %s", want, g14.CommandString())
		}
	}
	for _, want := range []string{
		"TestSQLiteBackupRestoreSmoke",
		"TestSQLiteQueryPlanSmoke",
		"TestSQLiteMediumFixtureDistribution",
		"TestDashboardDAGSnapshotListQueryCountDoesNotScaleWithPageSize",
		"TestSQLite",
	} {
		if !strings.Contains(g14.CommandString(), want) {
			t.Fatalf("G14 command missing required selector %s: %s", want, g14.CommandString())
		}
	}
}

func TestRegressionBackupRestoreGateRunsLargeFixtureStressExplicitly(t *testing.T) {
	g14 := findGateForTest(t, "G14")
	command := g14.CommandString()
	for _, want := range []string{
		"-tags",
		"sqlite_stress",
		"TestSQLiteLargeFixtureStressExplicitRun",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("G14 command missing large fixture stress coverage %q: %s", want, command)
		}
	}
}

func TestPackagingSmokeGateRunsRuntimePackageSmoke(t *testing.T) {
	g12 := findGateForTest(t, "G12")
	command := g12.CommandString()
	for _, want := range []string{
		"-v",
		"./scripts",
		"TestPackageLinux",
		"TestPackageMacOS",
		"TestMacOS",
		"TestPackageWindows",
		"TestSQLiteReleaseGatePackageSmokeRuntime",
		"TestSQLiteReleaseGatePackageSmokeCommands",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("G12 command missing runtime package smoke coverage %q: %s", want, command)
		}
	}
}

func commandHasArg(command []string, want string) bool {
	for _, arg := range command {
		if arg == want {
			return true
		}
	}
	return false
}

func findGateForTest(t *testing.T, id string) Gate {
	t.Helper()
	for _, gate := range Definitions() {
		if gate.ID == id {
			return gate
		}
	}
	t.Fatalf("gate %s not found", id)
	return Gate{}
}

func TestValidateResultsFailsMissingGateAndP0Skip(t *testing.T) {
	gates := Definitions()
	results := make([]Result, 0, len(gates)-1)
	for _, gate := range gates[1:] {
		results = append(results, passingResult(gate))
	}
	if err := ValidateResults(gates, results, false); err == nil || !strings.Contains(err.Error(), "missing report entry for G1") {
		t.Fatalf("ValidateResults missing G1 error = %v", err)
	}

	results = make([]Result, 0, len(gates))
	for _, gate := range gates {
		result := passingResult(gate)
		if gate.ID == "G1" {
			result.Status = StatusSkipped
		}
		results = append(results, result)
	}
	if err := ValidateResults(gates, results, false); err == nil || !strings.Contains(err.Error(), "P0 gate G1") {
		t.Fatalf("ValidateResults P0 skip error = %v", err)
	}
}

func TestRenderMarkdownReportIncludesRequiredFields(t *testing.T) {
	gates := Definitions()
	results := make([]Result, 0, len(gates))
	for _, gate := range gates {
		results = append(results, passingResult(gate))
	}
	report := RenderMarkdown(Report{
		CommitSHA: "1569c76f",
		OS:        "linux",
		Arch:      "amd64",
		StartedAt: time.Unix(1700000000, 0).UTC(),
		EndedAt:   time.Unix(1700000060, 0).UTC(),
		Results:   results,
	})
	for _, want := range []string{
		"Commit SHA",
		"OS/arch",
		"Gate",
		"Command",
		"CWD",
		"Start time",
		"End time",
		"Exit code",
		"Raw log artifact",
		"Result",
		"Blocker owner",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func passingResult(gate Gate) Result {
	start := time.Unix(1700000000, 0).UTC()
	return Result{
		Gate:       gate,
		Command:    gate.CommandString(),
		CWD:        ".",
		StartedAt:  start,
		EndedAt:    start.Add(time.Second),
		ExitCode:   0,
		RawLogPath: ".sqlite-release-gate-logs/" + gate.ID + ".log",
		Status:     StatusPass,
	}
}
