package sqlitereleasegate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status describes the outcome of one SQLite release gate.
type Status string

const (
	// StatusPass means the gate command exited successfully.
	StatusPass Status = "PASS"
	// StatusFail means the gate command, gate setup, or result persistence failed.
	StatusFail Status = "FAIL"
	// StatusSkipped means a non-required gate was intentionally not executed.
	StatusSkipped Status = "SKIPPED"
)

// Gate defines one SQLite release verification command and its release priority.
type Gate struct {
	ID           string
	Title        string
	Priority     string
	Command      []string
	CWD          string
	BlockerOwner string
	Description  string
}

var mcpOrchSQLiteSmokePackages = []string{
	"./cmd/mcp-orch",
	"./cmd/mcp-orch/fxadapter",
	"./cmd/mcp-orch/orchestration",
	"./cmd/mcp-orch/store/commandcard",
	"./cmd/mcp-orch/store/workspace",
	"./cmd/mcp-orch/store/taskdag",
	"./cmd/mcp-orch/tools",
}

// CommandString returns the shell-style command text used in release reports.
func (g Gate) CommandString() string {
	return strings.Join(g.Command, " ")
}

var sqliteGateDefinitions = []Gate{
	{
		ID:          "G1",
		Title:       "SQLite runtime startup",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/platform/db/...", "-run", "TestSQLiteRuntimeStartupSmoke|TestNewDBCreatesSQLiteWithPragmasAndRestrictiveFiles", "-count=1"},
		CWD:         ".",
		Description: "Open a SQLite runtime database, apply migrations, verify schema floor, and reject broken startup.",
	},
	{
		ID:          "G2",
		Title:       "Postgres runtime not used",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/platform/db/sqlite", "-run", "TestSQLiteRuntimeIgnoresPostgresEnvironment", "-count=1"},
		CWD:         ".",
		Description: "Prove PostgreSQL DATABASE_URL-style environment does not drive the SQLite runtime.",
	},
	{
		ID:          "G3",
		Title:       "SQLite schema baseline and version floor",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/platform/db/sqlite", "-run", "TestSQLiteBaseline|TestSQLiteRuntimeStartupSmoke", "-count=1"},
		CWD:         ".",
		Description: "Verify baseline schema tables, indexes, constraints, and the minimum schema version gate.",
	},
	{
		ID:          "G4",
		Title:       "Main store SQLite regression",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/store/...", "-run", "TestSQLite", "-count=1"},
		CWD:         ".",
		Description: "Exercise main-store thread, prompt, binding, status, system log, session insight, and cron paths.",
	},
	{
		ID:          "G5",
		Title:       "mcp-orch SQLite regression",
		Priority:    "P0",
		Command:     sqliteGoTestCommand("TestSQLite", mcpOrchSQLiteSmokePackages...),
		CWD:         ".",
		Description: "Exercise mcp-orch startup readiness and DAG SQLite store paths.",
	},
	{
		ID:          "G6",
		Title:       "Cron claim concurrency",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/store/cron", "-run", "TestSQLiteClaimDueJobsConcurrentGoroutinesAndProcesses", "-count=1"},
		CWD:         ".",
		Description: "Detect duplicate or missing cron claims with concurrent SQLite writers.",
	},
	{
		ID:          "G7",
		Title:       "DAG wakeup claim concurrency",
		Priority:    "P0",
		Command:     []string{"go", "test", "./cmd/mcp-orch/store/taskdag", "-run", "TestSQLiteWakeupClaimConcurrentGoroutinesAndProcesses", "-count=1"},
		CWD:         ".",
		Description: "Detect duplicate or missing DAG wakeup claims across goroutines and OS processes.",
	},
	{
		ID:          "G8",
		Title:       "SQLite runtime lock replacement",
		Priority:    "P0",
		Command:     []string{"go", "test", "./cmd/mcp-orch/store/taskdag", "-run", "TestSQLiteRuntimeLock", "-count=1"},
		CWD:         ".",
		Description: "Verify SQLite runtime lock/CAS behavior that replaces PostgreSQL advisory locks.",
	},
	{
		ID:          "G9",
		Title:       "Prompt recall lock replacement",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/store/prompt", "-run", "TestRecallTopicLockSerializesSameCWDTopicAcrossDBHandles|TestRecallTopicLockRetriesBusyUntilConcurrentWriterCommits", "-count=1"},
		CWD:         ".",
		Description: "Verify prompt recall topic locking and target updates on SQLite.",
	},
	{
		ID:          "G10",
		Title:       "DAG JSON event golden",
		Priority:    "P0",
		Command:     []string{"go", "test", "./cmd/mcp-orch/store/taskdag", "-run", "TestSQLiteRunEventAppendGoldenPayloads|TestSQLiteRunEventAppendConcurrentWritersDoNotOverwrite", "-count=1"},
		CWD:         ".",
		Description: "Verify JSON event append/truncate semantics and concurrent append integrity.",
	},
	{
		ID:          "G11",
		Title:       "Mixed main-app and mcp-orch write pressure",
		Priority:    "P0",
		Command:     []string{"go", "test", "./internal/platform/db/sqlite", "-run", "TestSQLiteMixedWritePressure", "-count=1"},
		CWD:         ".",
		Description: "Run two OS processes against one SQLite file and fail on unrecoverable SQLITE_BUSY/LOCKED or lost events.",
	},
	{
		ID:          "G12",
		Title:       "Packaging smoke without PostgreSQL runtime",
		Priority:    "P0",
		Command:     []string{"go", "test", "-v", "./scripts", "-run", "TestPackageLinux|TestPackageMacOS|TestMacOS|TestPackageWindows|TestSQLiteReleaseGatePackageSmokeRuntime|TestSQLiteReleaseGatePackageSmokeCommands", "-count=1"},
		CWD:         ".",
		Description: "Verify release package guards keep PostgreSQL runtime artifacts out and keep mcp-orch bundled.",
	},
	{
		ID:           "G13",
		Title:        "Old PostgreSQL data ignored",
		Priority:     "P1",
		Command:      []string{"go", "test", "./internal/platform/config", "./internal/app", "./internal/provider/...", "-run", "TestNew_PostgresEnvAndOldDataDirDoNotOverrideSQLitePath|TestNewUIDesktopScriptDefaultsSQLiteUnderHomeAndIgnoresOldPostgresDataDir|Test.*ScrubsDatabaseEnv|TestBuildManifest_StripsDatabaseEnvironmentFromMCPBinaries|TestPeerProcessEnvPassesExplicitSQLitePathToTrustedOrchOnly", "-count=1"},
		CWD:          ".",
		BlockerOwner: "sqlite-switch release owner",
		Description:  "Verify legacy PostgreSQL data/env is not used as SQLite runtime state.",
	},
	{
		ID:           "G14",
		Title:        "Regression, performance, backup restore",
		Priority:     "P1",
		Command:      sqliteGoTestCommandWithTags("TestSQLiteBackupRestoreSmoke|TestSQLiteQueryPlanSmoke|TestSQLiteMediumFixtureDistribution|TestSQLiteLargeFixtureStressExplicitRun|TestDashboardDAGSnapshotListQueryCountDoesNotScaleWithPageSize|TestSQLite", []string{"sqlite_stress"}, append([]string{"./internal/platform/db/sqlite", "./internal/module/dashboard", "./internal/store/..."}, mcpOrchSQLiteSmokePackages...)...),
		CWD:          ".",
		BlockerOwner: "sqlite-switch release owner",
		Description:  "Run backup restore, query-plan, medium fixture, explicit large fixture stress, and regression smoke coverage.",
	},
}

func sqliteGoTestCommand(runPattern string, packages ...string) []string {
	command := append([]string{"go", "test"}, packages...)
	command = append(command, "-run", runPattern, "-count=1")
	return command
}

func sqliteGoTestCommandWithTags(runPattern string, tags []string, packages ...string) []string {
	command := []string{"go", "test", "-v"}
	if len(tags) > 0 {
		command = append(command, "-tags", strings.Join(tags, ","))
	}
	command = append(command, packages...)
	command = append(command, "-run", runPattern, "-count=1")
	return command
}

// Result captures the command output metadata and status for one executed gate.
type Result struct {
	Gate       Gate
	Command    string
	CWD        string
	StartedAt  time.Time
	EndedAt    time.Time
	ExitCode   int
	RawLogPath string
	Status     Status
	LogError   string
}

// Report is the full release gate audit artifact for a commit and platform.
type Report struct {
	CommitSHA string
	OS        string
	Arch      string
	StartedAt time.Time
	EndedAt   time.Time
	Results   []Result
}

// Definitions returns a defensive copy of the configured SQLite release gates.
func Definitions() []Gate {
	gates := make([]Gate, len(sqliteGateDefinitions))
	for i, gate := range sqliteGateDefinitions {
		gate.Command = append([]string(nil), gate.Command...)
		gates[i] = gate
	}
	return gates
}

// ValidateResults verifies that a report contains known gates and passing P0 outcomes.
func ValidateResults(gates []Gate, results []Result, allowPartial bool) error {
	gateByID, err := mapGateDefinitions(gates)
	if err != nil {
		return err
	}
	seen, err := validateResultEntries(gateByID, results)
	if err != nil {
		return err
	}
	if allowPartial {
		return nil
	}
	return requireAllGatesReported(gates, seen)
}

func mapGateDefinitions(gates []Gate) (map[string]Gate, error) {
	gateByID := make(map[string]Gate, len(gates))
	for _, gate := range gates {
		if gateByID[gate.ID].ID != "" {
			return nil, fmt.Errorf("duplicate gate definition %s", gate.ID)
		}
		gateByID[gate.ID] = gate
	}
	return gateByID, nil
}

func validateResultEntries(gateByID map[string]Gate, results []Result) (map[string]Result, error) {
	seen := make(map[string]Result, len(results))
	for _, result := range results {
		if err := validateResultEntry(gateByID, seen, result); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

func validateResultEntry(gateByID map[string]Gate, seen map[string]Result, result Result) error {
	gateID := result.Gate.ID
	if gateID == "" {
		return fmt.Errorf("result has empty gate id")
	}
	gate, ok := gateByID[gateID]
	if !ok {
		return fmt.Errorf("report entry for unknown gate %s", gateID)
	}
	if seen[gateID].Gate.ID != "" {
		return fmt.Errorf("duplicate report entry for %s", gateID)
	}
	if err := validateP0ResultStatus(gate, result); err != nil {
		return err
	}
	if result.Command == "" {
		return fmt.Errorf("%s report command is empty", gateID)
	}
	if result.CWD == "" {
		return fmt.Errorf("%s report cwd is empty", gateID)
	}
	if result.RawLogPath == "" {
		return fmt.Errorf("%s report raw log artifact path is empty", gateID)
	}
	seen[gateID] = result
	return nil
}

func validateP0ResultStatus(gate Gate, result Result) error {
	if gate.Priority != "P0" {
		return nil
	}
	if result.Status == StatusSkipped {
		return fmt.Errorf("P0 gate %s cannot be SKIPPED", gate.ID)
	}
	if result.Status != StatusPass {
		return fmt.Errorf("P0 gate %s result = %s, want PASS", gate.ID, result.Status)
	}
	return nil
}

func requireAllGatesReported(gates []Gate, seen map[string]Result) error {
	for _, gate := range gates {
		if seen[gate.ID].Gate.ID == "" {
			return fmt.Errorf("missing report entry for %s", gate.ID)
		}
	}
	return nil
}

// RenderMarkdown renders a release gate report as a stable Markdown table.
func RenderMarkdown(report Report) string {
	var b strings.Builder
	b.WriteString("# SQLite Release Gate Report\n\n")
	b.WriteString("| Field | Value |\n")
	b.WriteString("|---|---|\n")
	writeRow(&b, "Commit SHA", report.CommitSHA)
	writeRow(&b, "OS/arch", report.OS+"/"+report.Arch)
	writeRow(&b, "Start time", formatTime(report.StartedAt))
	writeRow(&b, "End time", formatTime(report.EndedAt))
	b.WriteString("\n")
	b.WriteString("| Gate | Priority | Title | Command | CWD | Start time | End time | Exit code | Raw log artifact | Log error | Result | Blocker owner |\n")
	b.WriteString("|---|---|---|---|---|---|---:|---:|---|---|---|---|\n")
	results := append([]Result(nil), report.Results...)
	sort.Slice(results, func(i, j int) bool {
		return gateSortKey(results[i].Gate.ID) < gateSortKey(results[j].Gate.ID)
	})
	for _, result := range results {
		owner := result.Gate.BlockerOwner
		if owner == "" && result.Gate.Priority != "P0" && result.Status != StatusPass {
			owner = "UNASSIGNED"
		}
		values := []string{
			result.Gate.ID,
			result.Gate.Priority,
			result.Gate.Title,
			result.Command,
			result.CWD,
			formatTime(result.StartedAt),
			formatTime(result.EndedAt),
			fmt.Sprintf("%d", result.ExitCode),
			result.RawLogPath,
			result.LogError,
			string(result.Status),
			owner,
		}
		b.WriteString("| " + strings.Join(escapeRow(values), " | ") + " |\n")
	}
	return b.String()
}

func writeRow(b *strings.Builder, key, value string) {
	b.WriteString("| " + escapeCell(key) + " | " + escapeCell(value) + " |\n")
}

func escapeRow(values []string) []string {
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		escaped = append(escaped, escapeCell(value))
	}
	return escaped
}

func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

func gateSortKey(id string) int {
	if !strings.HasPrefix(id, "G") {
		return 1000
	}
	var n int
	if _, err := fmt.Sscanf(id, "G%d", &n); err != nil {
		return 1000
	}
	return n
}
