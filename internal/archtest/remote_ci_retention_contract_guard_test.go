package archtest

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIRetentionHasOneSynchronousAuthorityBoundary 防止 retention
// 漂移到 timer/worker 或出现第二个生产 compactor。
func TestRemoteCIRetentionHasOneSynchronousAuthorityBoundary(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(cicontract.DocumentPath)))
	retention, retentionSources := remoteCIRetentionAuthoritySources(t, root)
	if got := remoteCIRetentionContractBlock(t, contract); got != cicontract.CanonicalRetentionMarkdown() {
		t.Fatal("accepted retention prose and cicontract retention policy are not 1:1")
	}
	remoteCIRetentionAssertNoBackgroundGC(t, retention)
	remoteCIRetentionAssertContractConstants(t, retentionSources)
}

// remoteCIRetentionAuthoritySources 汇总唯一 compactor 与各历史根写入侧源码。
func remoteCIRetentionAuthoritySources(t *testing.T, root string) (string, string) {
	t.Helper()
	retention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_retention.go"))
	sampleRetention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_write.go"))
	checkpointRetention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/calibration_checkpoint_store.go"))
	return retention, retention + sampleRetention + checkpointRetention
}

// remoteCIRetentionAssertNoBackgroundGC 保持 retention 只能同步发生在写事务中的约束。
func remoteCIRetentionAssertNoBackgroundGC(t *testing.T, retention string) {
	t.Helper()
	if strings.Count(retention, "func compactDurationLedgerAuthority(") != 1 {
		t.Fatal("duration ledger retention must expose exactly one authority compactor")
	}
	for _, forbidden := range []string{"go compactDurationLedgerAuthority", "time.NewTicker", "time.AfterFunc"} {
		if strings.Contains(retention, forbidden) {
			t.Fatalf("retention authority must not use background GC: %q", forbidden)
		}
	}
}

// remoteCIRetentionAssertContractConstants 确认所有写入侧消费唯一契约 owner。
func remoteCIRetentionAssertContractConstants(t *testing.T, retentionSources string) {
	t.Helper()
	if err := cicontract.ValidateRetentionGenerations(); err != nil {
		t.Fatalf("retention constants: %v", err)
	}
	for _, name := range []string{"RetentionGenerations", "RetentionRootBindings"} {
		if !strings.Contains(retentionSources, "cicontract."+name) {
			t.Errorf("retention authority must consume cicontract.%s", name)
		}
	}
	for _, name := range []string{"RemoteRunsTable", "WorkloadCatalogsTable", "CatalogObservationsTable"} {
		if !strings.Contains(retentionSources, "cicontract."+name) {
			t.Errorf("retention authority must consume cicontract.%s", name)
		}
	}
}

// TestRemoteCIRetentionRootsShareOneAcceptedGenerationWindow 将所有历史根锁到
// 同一 generation 列，并拒绝旧的固定行数上限。
func TestRemoteCIRetentionRootsShareOneAcceptedGenerationWindow(t *testing.T) {
	root := findRepoRoot(t)
	schema := remoteCIRetentionSchemaSources(t, root)
	remoteCIRetentionAssertRootGenerationConstraints(t, schema)
	remoteCIRetentionAssertRetiredCapsAbsent(t, root)
}

// TestRemoteCITimingWarningLifecycleIsTransientAndAtomic 锁定 live 事实不成为第六根，
// 且只在最终 run 写事务中移动到 cascade 子表。
func TestRemoteCITimingWarningLifecycleIsTransientAndAtomic(t *testing.T) {
	root := findRepoRoot(t)
	for _, binding := range cicontract.RetentionRootBindings() {
		if binding.Table == cicontract.LiveTimingWarningsTable || binding.Table == cicontract.RunTimingWarningsTable {
			t.Fatalf("timing warning lifecycle table %q must not be a retention root", binding.Table)
		}
	}
	warningSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/remote_ci_timing_warning.go"))
	retentionSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_retention.go"))
	for _, required := range []string{
		"func finalizeSQLiteRemoteCITimingWarnings(",
		"INSERT INTO ci_run_timing_warnings",
		"DELETE FROM ci_live_timing_warnings",
		"func compactLiveRemoteCITimingWarnings(",
		"currentAcceptedBaselineGeneration",
	} {
		if !strings.Contains(warningSource+retentionSource, required) {
			t.Errorf("timing warning lifecycle lacks %q", required)
		}
	}
}

// remoteCIRetentionSchemaSources 仅合并 ledger schema 分卷，避免非 schema 文件干扰断言。
func remoteCIRetentionSchemaSources(t *testing.T, root string) string {
	t.Helper()
	schemaDirectory := filepath.Join(root, "internal", "devtools", "gate")
	entries, err := os.ReadDir(schemaDirectory)
	if err != nil {
		t.Fatal(err)
	}
	var schema strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "ledger_store_sqlite_schema") || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		schema.WriteString(readRemoteCIContractGuardFile(t, filepath.Join(schemaDirectory, entry.Name())))
	}
	return schema.String()
}

// remoteCIRetentionAssertRootGenerationConstraints 锁定五个历史根的 generation 文本约束。
func remoteCIRetentionAssertRootGenerationConstraints(t *testing.T, schema string) {
	t.Helper()
	bindings := cicontract.RetentionRootBindings()
	if len(bindings) != 5 {
		t.Fatalf("retention root count = %d, want 5", len(bindings))
	}
	for _, binding := range bindings {
		block := remoteCIRetentionCreateTableBlock(t, schema, binding.Table)
		declaration := binding.GenerationColumn + " TEXT NOT NULL CHECK ("
		if !strings.Contains(block, declaration) ||
			!strings.Contains(block, "NOT GLOB '*[^0-9]*'") ||
			!strings.Contains(block, "18446744073709551615") {
			t.Errorf("retention root %q does not lock %q to canonical positive uint64 text", binding.Table, binding.GenerationColumn)
		}
	}
}

// remoteCIRetentionAssertRetiredCapsAbsent 拒绝各 production owner 中已废弃的行数上限。
func remoteCIRetentionAssertRetiredCapsAbsent(t *testing.T, root string) {
	t.Helper()
	forbidden := []string{
		"DurationSuccessSamplesPerBucket",
		"DurationFailureSamplesPerBucket",
		"DurationExecutionVariantsPerWorkload",
		"DurationSamplesTotal",
		"CompletedRemoteCIRuns",
		"CatalogObservationsPerEntrypointProfile",
		"CompletedCalibrationCheckpointsPerIdentity",
		"IncompleteCalibrationCheckpointsPerIdentity",
		"CalibrationScenariosPerCheckpoint",
	}
	for _, relativePath := range []string{
		"internal/devtools/cicontract",
		"internal/devtools/gate",
		cicontract.DocumentPath,
	} {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			remoteCIRejectRetiredRetentionCaps(t, path, forbidden)
			continue
		}
		remoteCIRejectRetiredRetentionCapsInDirectory(t, path, forbidden)
	}
}

// remoteCIRejectRetiredRetentionCapsInDirectory 检查目录中的 production Go 文件。
func remoteCIRejectRetiredRetentionCapsInDirectory(t *testing.T, path string, forbidden []string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		remoteCIRejectRetiredRetentionCaps(t, filepath.Join(path, entry.Name()), forbidden)
	}
}

// TestRemoteCIRetentionRootWritersProveAcceptedGeneration 将来源证明锁入每个
// 历史根创建或更新事务。
func TestRemoteCIRetentionRootWritersProveAcceptedGeneration(t *testing.T) {
	root := findRepoRoot(t)
	directory := filepath.Join(root, "internal", "devtools", "gate")
	expectedCallers := map[string]bool{
		"calibration_checkpoint_store.go.CreateCalibrationCheckpointIfAbsent":         false,
		"calibration_checkpoint_store.go.CompareAndSwapCalibrationCheckpointScenario": false,
		"ci_catalog_store.go.RecordWorkloadCatalog":                                   false,
		"ci_query_store.go.RecordProvisionalRemoteCIRun":                              false,
		"ledger_store_sqlite.go.appendSQLiteDurationSamplesInTransaction":             false,
		"remote_ci_timing_warning.go.RecordLiveRemoteCITimingWarning":                 false,
	}
	remoteCIRetentionAssertAcceptedGenerationProofs(t, directory, expectedCallers)
	for caller, guarded := range expectedCallers {
		if !guarded {
			t.Errorf("historical-root writer %s does not prove its accepted generation", caller)
		}
	}
}

// remoteCIRetentionAssertAcceptedGenerationProofs 检查所有 production writer 的来源证明调用。
func remoteCIRetentionAssertAcceptedGenerationProofs(t *testing.T, directory string, expectedCallers map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file := parseRemoteCIContractGuardFile(t, filepath.Join(directory, entry.Name()))
		remoteCIRetentionAssertFileAcceptedGenerationProofs(t, entry.Name(), file, expectedCallers)
	}
}

// remoteCIRetentionAssertFileAcceptedGenerationProofs 登记单个 production 文件中的 guard 调用。
func remoteCIRetentionAssertFileAcceptedGenerationProofs(t *testing.T, fileName string, file *ast.File, expectedCallers map[string]bool) {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			remoteCIRetentionRegisterAcceptedGenerationProof(t, fileName, function, call, expectedCallers)
			return true
		})
	}
}

// remoteCIRetentionRegisterAcceptedGenerationProof 保留每个 writer 恰好一次来源证明的 allowlist。
func remoteCIRetentionRegisterAcceptedGenerationProof(t *testing.T, fileName string, function *ast.FuncDecl, call *ast.CallExpr, expectedCallers map[string]bool) {
	t.Helper()
	if remoteCIRetentionCallName(call) != "requireHistoricallyAcceptedGeneration" {
		return
	}
	caller := fileName + "." + function.Name.Name
	if _, expected := expectedCallers[caller]; !expected {
		t.Errorf("unexpected accepted-generation proof caller %s", caller)
	} else if expectedCallers[caller] {
		t.Errorf("accepted-generation proof caller %s invokes the guard more than once", caller)
	} else {
		expectedCallers[caller] = true
	}
}

// TestRemoteCIRetentionIsTheLastDatabaseOperationInEveryWriter 用结构守卫锁定
// 同步事务边界，而不是依赖注释约定。
func TestRemoteCIRetentionIsTheLastDatabaseOperationInEveryWriter(t *testing.T) {
	root := findRepoRoot(t)
	directory := filepath.Join(root, "internal", "devtools", "gate")
	expectedCallers := map[string]bool{
		"calibration_checkpoint_store.go.CreateCalibrationCheckpointIfAbsent":         false,
		"calibration_checkpoint_store.go.CompareAndSwapCalibrationCheckpointScenario": false,
		"calibration_checkpoint_store.go.DeleteCalibrationCheckpoint":                 false,
		"ci_catalog_store.go.RecordWorkloadCatalog":                                   false,
		"ci_query_store.go.RecordProvisionalRemoteCIRun":                              false,
		"ledger_store_sqlite.go.appendSQLiteSamplesOnce":                              false,
		"remote_ci_timing_warning.go.RecordLiveRemoteCITimingWarning":                 false,
		"workload_pass_evidence.go.finalizeSQLiteRemoteCIRunAuthority":                false,
	}
	remoteCIRetentionAssertFinalDatabaseOperations(t, directory, expectedCallers)
	for caller, called := range expectedCallers {
		if !called {
			t.Errorf("retention authority is not the final operation of required writer %s", caller)
		}
	}
}

// remoteCIRetentionAssertFinalDatabaseOperations 检查每个 production writer 的同步 compactor 边界。
func remoteCIRetentionAssertFinalDatabaseOperations(t *testing.T, directory string, expectedCallers map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file := parseRemoteCIContractGuardFile(t, filepath.Join(directory, entry.Name()))
		remoteCIRetentionAssertFileFinalDatabaseOperations(t, entry.Name(), file, expectedCallers)
	}
}

// remoteCIRetentionAssertFileFinalDatabaseOperations 分别检查调用位置、后续数据库访问和异步调用。
func remoteCIRetentionAssertFileFinalDatabaseOperations(t *testing.T, fileName string, file *ast.File, expectedCallers map[string]bool) {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		positions := remoteCIRetentionCompactionPositions(t, fileName, function, expectedCallers)
		remoteCIRetentionAssertNoDatabaseAfterCompaction(t, fileName, function, positions)
		remoteCIRetentionAssertNoAsyncCompaction(t, fileName, function)
	}
}

// remoteCIRetentionCompactionPositions 登记唯一允许的同步 compactor 调用。
func remoteCIRetentionCompactionPositions(t *testing.T, fileName string, function *ast.FuncDecl, expectedCallers map[string]bool) []int {
	t.Helper()
	positions := make([]int, 0, 1)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || remoteCIRetentionCallName(call) != "compactDurationLedgerAuthority" {
			return true
		}
		caller := fileName + "." + function.Name.Name
		if _, expected := expectedCallers[caller]; !expected {
			t.Errorf("unexpected retention authority caller %s", caller)
		} else if expectedCallers[caller] {
			t.Errorf("retention authority caller %s invokes compaction more than once", caller)
		} else {
			expectedCallers[caller] = true
		}
		positions = append(positions, int(call.Pos()))
		return true
	})
	return positions
}

// remoteCIRetentionAssertNoDatabaseAfterCompaction 防止 compactor 之后出现新的数据库操作。
func remoteCIRetentionAssertNoDatabaseAfterCompaction(t *testing.T, fileName string, function *ast.FuncDecl, positions []int) {
	t.Helper()
	for _, compactPosition := range positions {
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || int(call.Pos()) <= compactPosition {
				return true
			}
			remoteCIRetentionReportDatabaseOperationAfterCompaction(t, fileName, function, call)
			return true
		})
	}
}

// remoteCIRetentionReportDatabaseOperationAfterCompaction 只报告 compactor 后的数据库 selector。
func remoteCIRetentionReportDatabaseOperationAfterCompaction(t *testing.T, fileName string, function *ast.FuncDecl, call *ast.CallExpr) {
	t.Helper()
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !remoteCIRetentionIsDatabaseOperation(selector.Sel.Name) {
		return
	}
	t.Errorf("%s.%s performs database operation %s after retention compaction", fileName, function.Name.Name, selector.Sel.Name)
}

// remoteCIRetentionIsDatabaseOperation 列出事务边界后不允许出现的数据库调用。
func remoteCIRetentionIsDatabaseOperation(name string) bool {
	return name == "Exec" || name == "Query" || name == "QueryRow" || name == "Prepare"
}

// remoteCIRetentionAssertNoAsyncCompaction 拒绝将唯一 compactor 放入 goroutine。
func remoteCIRetentionAssertNoAsyncCompaction(t *testing.T, fileName string, function *ast.FuncDecl) {
	t.Helper()
	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.GoStmt)
		if !ok || remoteCIRetentionCallName(statement.Call) != "compactDurationLedgerAuthority" {
			return true
		}
		t.Errorf("%s.%s starts retention asynchronously", fileName, function.Name.Name)
		return true
	})
}

func remoteCIRetentionCallName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	default:
		return ""
	}
}

func remoteCIRetentionCreateTableBlock(t *testing.T, schema, table string) string {
	t.Helper()
	for _, prefix := range []string{"CREATE TABLE IF NOT EXISTS ", "CREATE TABLE "} {
		start := strings.Index(schema, prefix+table+" (")
		if start < 0 {
			continue
		}
		relativeEnd := strings.Index(schema[start:], "\n);")
		if relativeEnd < 0 {
			t.Fatalf("retention root %q CREATE TABLE block is unterminated", table)
		}
		return schema[start : start+relativeEnd+len("\n);")]
	}
	t.Fatalf("retention root %q has no canonical CREATE TABLE block", table)
	return ""
}

func remoteCIRejectRetiredRetentionCaps(t *testing.T, path string, forbidden []string) {
	t.Helper()
	source := readRemoteCIContractGuardFile(t, path)
	for _, identifier := range forbidden {
		if strings.Contains(source, identifier) {
			t.Errorf("%s retains retired row-count cap %q", filepath.Base(path), identifier)
		}
	}
}

func remoteCIRetentionContractBlock(t *testing.T, document string) string {
	t.Helper()
	const begin, end = "<!-- cicontract:retention:begin -->", "<!-- cicontract:retention:end -->"
	start := strings.Index(document, begin)
	if start < 0 {
		t.Fatal("accepted remote CI document is missing the retention begin marker")
	}
	relativeEnd := strings.Index(document[start:], end)
	if relativeEnd < 0 {
		t.Fatal("accepted remote CI document is missing the retention end marker")
	}
	finish := start + relativeEnd + len(end)
	if strings.Contains(document[finish:], begin) || strings.Contains(document[finish:], end) {
		t.Fatal("accepted remote CI document contains multiple retention blocks")
	}
	return document[start:finish]
}
