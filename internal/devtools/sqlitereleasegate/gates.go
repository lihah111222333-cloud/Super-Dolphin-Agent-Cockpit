package sqlitereleasegate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status 表示单个 SQLite release gate 的归档结果。
type Status string

// SQLite release gate 的最终状态常量。
const (
	StatusPass    Status = "PASS"
	StatusFail    Status = "FAIL"
	StatusSkipped Status = "SKIPPED"
)

// Gate 描述一个 release gate 的命令、工作目录和失败归属。
type Gate struct {
	ID           string   // 稳定 gate 编号，报告校验依赖它去重和排序。
	Title        string   // 面向发布报告的人类可读标题。
	Priority     string   // 发布优先级，P0 gate 失败时阻断整体报告通过。
	Command      []string // 实际执行的命令及参数，Run 会原样传给 exec.CommandContext。
	CWD          string   // 相对仓库根目录的执行目录。
	BlockerOwner string   // 非 P0 gate 失败时的默认承接人。
	Description  string   // gate 覆盖的发布风险说明。
}

// mcpOrchSQLiteSmokePackages 列出 SQLite 切换后必须保留的 mcp-orch 包烟测范围。
var mcpOrchSQLiteSmokePackages = []string{
	"./cmd/mcp-orch",
	"./cmd/mcp-orch/fxadapter",
	"./cmd/mcp-orch/orchestration",
	"./cmd/mcp-orch/store/commandcard",
	"./cmd/mcp-orch/store/workspace",
	"./cmd/mcp-orch/store/taskdag",
	"./cmd/mcp-orch/tools",
}

// CommandString 将命令切片渲染为报告中的可读命令行。
func (g Gate) CommandString() string {
	return strings.Join(g.Command, " ")
}

// sqliteGateDefinitions 是 SQLite 发布门禁的固定定义。
// 调用方必须通过 Definitions 获取副本，避免测试或运行期修改污染全局定义。
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

// sqliteGoTestCommand 构造标准 go test 命令，保证所有 gate 使用一致的 -run 与 -count 参数。
func sqliteGoTestCommand(runPattern string, packages ...string) []string {
	command := append([]string{"go", "test"}, packages...)
	command = append(command, "-run", runPattern, "-count=1")
	return command
}

// sqliteGoTestCommandWithTags 构造需要 build tags 的 go test 命令。
// tags 为空时不会追加 -tags，避免生成空标签参数影响普通 gate。
func sqliteGoTestCommandWithTags(runPattern string, tags []string, packages ...string) []string {
	command := []string{"go", "test", "-v"}
	if len(tags) > 0 {
		command = append(command, "-tags", strings.Join(tags, ","))
	}
	command = append(command, packages...)
	command = append(command, "-run", runPattern, "-count=1")
	return command
}

// Result 记录单个 release gate 的执行证据。
type Result struct {
	Gate       Gate      // 对应的 gate 定义快照。
	Command    string    // 报告中展示的命令字符串。
	CWD        string    // 实际执行目录。
	StartedAt  time.Time // gate 开始时间，使用 UTC 记录。
	EndedAt    time.Time // gate 结束时间，使用 UTC 记录。
	ExitCode   int       // 进程退出码；未执行成功解析时保持 -1。
	RawLogPath string    // 原始日志文件路径，报告使用 slash 格式保持跨平台稳定。
	Status     Status    // 归档状态，P0 gate 必须为 PASS。
}

// Report 汇总一次 SQLite release gate 运行的环境和全部 gate 结果。
type Report struct {
	CommitSHA string    // 运行时仓库 HEAD，用于回溯发布证据。
	OS        string    // 运行平台 GOOS。
	Arch      string    // 运行平台 GOARCH。
	StartedAt time.Time // 整体报告开始时间。
	EndedAt   time.Time // 整体报告结束时间。
	Results   []Result  // 每个 gate 的执行结果。
}

// Definitions 返回 gate 定义的防御性副本。
// Command 切片也会复制，避免调用方修改返回值后影响后续发布门禁。
func Definitions() []Gate {
	gates := make([]Gate, len(sqliteGateDefinitions))
	for i, gate := range sqliteGateDefinitions {
		gate.Command = append([]string(nil), gate.Command...)
		gates[i] = gate
	}
	return gates
}

// ValidateResults 校验报告结果是否与 gate 定义一致。
// allowPartial 仅放宽缺失 gate 的检查，P0 状态、未知 gate 和重复结果仍然 fail-fast。
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

// mapGateDefinitions 按 gate ID 建立索引，并拒绝重复定义。
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

// validateResultEntries 逐条校验结果并返回已出现的 gate 集合。
func validateResultEntries(gateByID map[string]Gate, results []Result) (map[string]Result, error) {
	seen := make(map[string]Result, len(results))
	for _, result := range results {
		if err := validateResultEntry(gateByID, seen, result); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

// validateResultEntry 校验单条结果的身份、必填证据和 P0 通过状态。
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

// validateP0ResultStatus 保证 P0 gate 不允许被跳过或失败。
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

// requireAllGatesReported 在完整报告模式下确保每个定义的 gate 都有结果。
func requireAllGatesReported(gates []Gate, seen map[string]Result) error {
	for _, gate := range gates {
		if seen[gate.ID].Gate.ID == "" {
			return fmt.Errorf("missing report entry for %s", gate.ID)
		}
	}
	return nil
}

// RenderMarkdown 将 gate 报告渲染为稳定排序的 Markdown 表格。
// 非 P0 失败且未声明 owner 时显式标记 UNASSIGNED，避免发布报告吞掉责任人缺口。
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
	b.WriteString("| Gate | Priority | Title | Command | CWD | Start time | End time | Exit code | Raw log artifact | Result | Blocker owner |\n")
	b.WriteString("|---|---|---|---|---|---|---:|---:|---|---|---|\n")
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
			string(result.Status),
			owner,
		}
		b.WriteString("| " + strings.Join(escapeRow(values), " | ") + " |\n")
	}
	return b.String()
}

// writeRow 写入单行键值表格，并复用统一的 Markdown 单元格转义。
func writeRow(b *strings.Builder, key, value string) {
	b.WriteString("| " + escapeCell(key) + " | " + escapeCell(value) + " |\n")
}

// escapeRow 转义一整行 Markdown 单元格，保持列数和换行展示稳定。
func escapeRow(values []string) []string {
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		escaped = append(escaped, escapeCell(value))
	}
	return escaped
}

// escapeCell 转义 Markdown 表格中的分隔符和换行。
func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}

// formatTime 用 UTC RFC3339 输出时间；零值保持为空，便于报告表示未产生的时间。
func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

// gateSortKey 将 G1/G2 这类编号转成数字排序键，未知格式排在末尾。
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
