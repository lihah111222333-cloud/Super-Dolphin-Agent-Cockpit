package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
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

// TestRemoteCIGenerationOneDerivesEveryAuthorityHistoryTableFromContract
// 防止首代 bootstrap 重新维护一份会漏根的静态表清单。
func TestRemoteCIGenerationOneDerivesEveryAuthorityHistoryTableFromContract(t *testing.T) {
	root := findRepoRoot(t)
	source := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "gate", "remote_baseline_generation_one.go"))
	for _, required := range []string{
		"cicontract.RetentionRootBindings()",
		"cicontract.GenerationOneAuthoritySupportingTables()",
		"SELECT EXISTS(SELECT 1 FROM %s LIMIT 1)",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("generation-one authority history check lacks %q", required)
		}
	}
	for _, binding := range cicontract.RetentionRootBindings() {
		if strings.Contains(source, `"`+binding.Table+`"`) {
			t.Errorf("generation-one authority history check duplicated retention root %q", binding.Table)
		}
	}
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

// remoteCIRetentionAssertRootGenerationConstraints 锁定七个历史根的 generation 文本约束。
func remoteCIRetentionAssertRootGenerationConstraints(t *testing.T, schema string) {
	t.Helper()
	bindings := cicontract.RetentionRootBindings()
	if len(bindings) != 7 {
		t.Fatalf("retention root count = %d, want 7", len(bindings))
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
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "CreateCalibrationCheckpointIfAbsent"):         false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "CompareAndSwapCalibrationCheckpointScenario"): false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "RecordWorkloadCatalog"):                       false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "RecordProvisionalRemoteCIRun"):                false,
		remoteCIRetentionFunctionKey("", "appendSQLiteDurationSamplesInTransaction"):                        false,
		remoteCIRetentionFunctionKey("", "writeSQLiteShardOverheadCAS"):                                     false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "RecordLiveRemoteCITimingWarning"):             false,
	}
	remoteCIRetentionAssertAcceptedGenerationProofs(t, directory, expectedCallers)
	for caller, guarded := range expectedCallers {
		if !guarded {
			t.Errorf("historical-root writer %s does not prove its accepted generation", caller)
		}
	}
}

type remoteCIRetentionSourceFile struct {
	name string
	file *ast.File
}

type remoteCIRetentionFunctionSymbol struct {
	receiver string
	name     string
}

func remoteCIRetentionFunctionKey(receiver, name string) string {
	if receiver == "" {
		return name
	}
	return receiver + "." + name
}

func (symbol remoteCIRetentionFunctionSymbol) key() string {
	return remoteCIRetentionFunctionKey(symbol.receiver, symbol.name)
}

func remoteCIRetentionFunctionSymbolOf(function *ast.FuncDecl) remoteCIRetentionFunctionSymbol {
	receiver := ""
	if function.Recv != nil && len(function.Recv.List) != 0 {
		receiver = types.ExprString(function.Recv.List[0].Type)
	}
	return remoteCIRetentionFunctionSymbol{receiver: receiver, name: function.Name.Name}
}

func remoteCIRetentionLoadSourceFiles(t *testing.T, directory string) []remoteCIRetentionSourceFile {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]remoteCIRetentionSourceFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		files = append(files, remoteCIRetentionSourceFile{
			name: entry.Name(),
			file: parseRemoteCIContractGuardFile(t, filepath.Join(directory, entry.Name())),
		})
	}
	return files
}

// remoteCIRetentionAssertAcceptedGenerationProofs 检查所有 production writer 的来源证明调用。
func remoteCIRetentionAssertAcceptedGenerationProofs(t *testing.T, directory string, expectedCallers map[string]bool) {
	t.Helper()
	files := remoteCIRetentionLoadSourceFiles(t, directory)
	remoteCIRetentionAssertUniqueExpectedSymbols(t, files, expectedCallers)
	delegation, delegated := remoteCIRetentionResolveHelperDelegation(t, files)
	for _, source := range files {
		remoteCIRetentionAssertFileAcceptedGenerationProofs(t, source.file, delegation, delegated, expectedCallers)
	}
}

// remoteCIRetentionHelperDelegation 记录唯一允许跨函数追踪的 retention helper。
// 只允许 provisional run writer 的精确 receiver、writer 和 helper 三元组；
// 其他 helper 不得借此跳过来源证明或事务尾部检查。
type remoteCIRetentionHelperDelegation struct {
	writer remoteCIRetentionFunctionSymbol
	helper remoteCIRetentionFunctionSymbol
}

type remoteCIRetentionFunctionLocation struct {
	fileName string
	function *ast.FuncDecl
}

func remoteCIRetentionAllowedHelperDelegation() remoteCIRetentionHelperDelegation {
	return remoteCIRetentionHelperDelegation{
		writer: remoteCIRetentionFunctionSymbol{receiver: "*DurationLedgerStore", name: "RecordProvisionalRemoteCIRun"},
		helper: remoteCIRetentionFunctionSymbol{receiver: "*DurationLedgerStore", name: "recordProvisionalRemoteCIRunTransaction"},
	}
}

// remoteCIRetentionHelperDelegationIssues 返回 helper delegation 的结构和调用链违规。
func remoteCIRetentionHelperDelegationIssues(files []remoteCIRetentionSourceFile, delegation remoteCIRetentionHelperDelegation) (writers, helpers []remoteCIRetentionFunctionLocation, issues []string) {
	writers, helpers = remoteCIRetentionCollectHelperLocations(files, delegation)
	if len(writers) == 0 && len(helpers) == 0 {
		return writers, helpers, nil
	}
	if len(writers) != 1 {
		issues = append(issues, fmt.Sprintf("retention writer %s must have one declaration, got %d", delegation.writer.key(), len(writers)))
	}
	if len(helpers) != 1 {
		issues = append(issues, fmt.Sprintf("retention helper %s must have one declaration, got %d", delegation.helper.key(), len(helpers)))
	}
	if len(issues) != 0 {
		return writers, helpers, issues
	}
	writerCalls, totalCalls, callIssues := remoteCIRetentionHelperCallIssues(files, delegation, writers[0])
	issues = append(issues, callIssues...)
	if writerCalls != 1 || totalCalls != 1 {
		issues = append(issues, fmt.Sprintf("retention helper %s must have one unique delegation from %s, writer_calls=%d total_calls=%d", delegation.helper.key(), delegation.writer.key(), writerCalls, totalCalls))
	}
	return writers, helpers, issues
}

func remoteCIRetentionCollectHelperLocations(files []remoteCIRetentionSourceFile, delegation remoteCIRetentionHelperDelegation) (writers, helpers []remoteCIRetentionFunctionLocation) {
	for _, source := range files {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			symbol := remoteCIRetentionFunctionSymbolOf(function)
			if symbol == delegation.writer {
				writers = append(writers, remoteCIRetentionFunctionLocation{fileName: source.name, function: function})
			}
			if symbol == delegation.helper {
				helpers = append(helpers, remoteCIRetentionFunctionLocation{fileName: source.name, function: function})
			}
		}
	}
	return writers, helpers
}

func remoteCIRetentionHelperCallIssues(files []remoteCIRetentionSourceFile, delegation remoteCIRetentionHelperDelegation, writer remoteCIRetentionFunctionLocation) (writerCalls, totalCalls int, issues []string) {
	receiverName := remoteCIRetentionReceiverName(writer.function)
	for _, source := range files {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			functionSymbol := remoteCIRetentionFunctionSymbolOf(function)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || remoteCIRetentionCallName(call) != delegation.helper.name {
					return true
				}
				totalCalls++
				if functionSymbol == delegation.writer && remoteCIRetentionIsExactHelperCall(call, delegation.helper.name, receiverName) {
					writerCalls++
					return true
				}
				issues = append(issues, fmt.Sprintf("retention helper %s has unexpected caller/receiver %s in %s", delegation.helper.key(), functionSymbol.key(), source.name))
				return true
			})
		}
	}
	return writerCalls, totalCalls, issues
}

// remoteCIRetentionResolveHelperDelegation 验证唯一 helper delegation 的 AST 形状与全包调用链。
func remoteCIRetentionResolveHelperDelegation(t *testing.T, files []remoteCIRetentionSourceFile) (remoteCIRetentionHelperDelegation, bool) {
	t.Helper()
	delegation := remoteCIRetentionAllowedHelperDelegation()
	writers, helpers, issues := remoteCIRetentionHelperDelegationIssues(files, delegation)
	if len(writers) == 0 && len(helpers) == 0 {
		return delegation, false
	}
	for _, issue := range issues {
		t.Errorf("%s", issue)
	}
	return delegation, len(issues) == 0
}

func remoteCIRetentionIsExactHelperCall(call *ast.CallExpr, helperName, receiverName string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != helperName {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == receiverName
}

func remoteCIRetentionReceiverName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 || len(function.Recv.List[0].Names) == 0 {
		return ""
	}
	return function.Recv.List[0].Names[0].Name
}

func remoteCIRetentionAssertUniqueExpectedSymbols(t *testing.T, files []remoteCIRetentionSourceFile, expectedCallers map[string]bool) {
	t.Helper()
	locations := remoteCIRetentionExpectedSymbolLocations(files, expectedCallers)
	for symbol, sourceNames := range locations {
		if len(sourceNames) > 1 {
			t.Errorf("retention writer %s has duplicate declarations in %s", symbol, strings.Join(sourceNames, ", "))
		}
	}
}

func remoteCIRetentionExpectedSymbolLocations(files []remoteCIRetentionSourceFile, expectedCallers map[string]bool) map[string][]string {
	locations := make(map[string][]string)
	for _, source := range files {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			symbol := remoteCIRetentionFunctionSymbolOf(function).key()
			if _, expected := expectedCallers[symbol]; expected {
				locations[symbol] = append(locations[symbol], source.name)
			}
		}
	}
	return locations
}

// remoteCIRetentionAssertFileAcceptedGenerationProofs 登记单个 production 文件中的 guard 调用。
func remoteCIRetentionAssertFileAcceptedGenerationProofs(t *testing.T, file *ast.File, delegation remoteCIRetentionHelperDelegation, delegated bool, expectedCallers map[string]bool) {
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
			remoteCIRetentionRegisterAcceptedGenerationProof(t, function, call, delegation, delegated, expectedCallers)
			return true
		})
	}
}

// remoteCIRetentionRegisterAcceptedGenerationProof 保留每个 writer 恰好一次来源证明的 allowlist。
func remoteCIRetentionRegisterAcceptedGenerationProof(t *testing.T, function *ast.FuncDecl, call *ast.CallExpr, delegation remoteCIRetentionHelperDelegation, delegated bool, expectedCallers map[string]bool) {
	t.Helper()
	if remoteCIRetentionCallName(call) != "requireHistoricallyAcceptedGeneration" {
		return
	}
	caller := remoteCIRetentionDelegatedCaller(function, delegation, delegated)
	if _, expected := expectedCallers[caller]; !expected {
		t.Errorf("unexpected accepted-generation proof caller %s", caller)
	} else if expectedCallers[caller] {
		t.Errorf("accepted-generation proof caller %s invokes the guard more than once", caller)
	} else {
		expectedCallers[caller] = true
	}
}

func remoteCIRetentionDelegatedCaller(function *ast.FuncDecl, delegation remoteCIRetentionHelperDelegation, delegated bool) string {
	symbol := remoteCIRetentionFunctionSymbolOf(function)
	if delegated && symbol == delegation.helper {
		return delegation.writer.key()
	}
	return symbol.key()
}

// TestRemoteCIRetentionIsTheLastDatabaseOperationInEveryWriter 用结构守卫锁定
// 同步事务边界，而不是依赖注释约定。
func TestRemoteCIRetentionIsTheLastDatabaseOperationInEveryWriter(t *testing.T) {
	root := findRepoRoot(t)
	directory := filepath.Join(root, "internal", "devtools", "gate")
	expectedCallers := map[string]bool{
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "CreateCalibrationCheckpointIfAbsent"):         false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "CompareAndSwapCalibrationCheckpointScenario"): false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "DeleteCalibrationCheckpoint"):                 false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "RecordWorkloadCatalog"):                       false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "RecordProvisionalRemoteCIRun"):                false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "appendSQLiteSamplesOnce"):                     false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "compareAndSwapSQLiteShardOverhead"):           false,
		remoteCIRetentionFunctionKey("*DurationLedgerStore", "RecordLiveRemoteCITimingWarning"):             false,
		remoteCIRetentionFunctionKey("", "finalizeSQLiteRemoteCIRunAuthority"):                              false,
	}
	remoteCIRetentionAssertFinalDatabaseOperations(t, directory, expectedCallers)
	for caller, called := range expectedCallers {
		if !called {
			t.Errorf("retention authority is not the final operation of required writer %s", caller)
		}
	}
}

// TestRemoteCIRetentionGuardNegativeFixtures 锁定文件移动、重复 symbol、其他 helper caller
// 和 compactor 后数据库访问的 fail-first 行为，避免守卫退化为路径白名单。
func TestRemoteCIRetentionGuardNegativeFixtures(t *testing.T) {
	t.Run("moved file keeps function symbol", func(t *testing.T) {
		file := remoteCIRetentionParseSourceFile(t, "moved_remote_ci_authority_finalize.go", `package gate
func finalizeSQLiteRemoteCIRunAuthority() {}`)
		function := file.file.Decls[0].(*ast.FuncDecl)
		if got, want := remoteCIRetentionFunctionSymbolOf(function).key(), "finalizeSQLiteRemoteCIRunAuthority"; got != want {
			t.Fatalf("moved writer symbol = %q, want %q", got, want)
		}
	})

	t.Run("duplicate writer symbol is visible", func(t *testing.T) {
		expected := map[string]bool{remoteCIRetentionFunctionKey("", "finalizeSQLiteRemoteCIRunAuthority"): false}
		files := []remoteCIRetentionSourceFile{
			remoteCIRetentionParseSourceFile(t, "remote_ci_authority_finalize.go", `package gate
func finalizeSQLiteRemoteCIRunAuthority() {}`),
			remoteCIRetentionParseSourceFile(t, "legacy_workload_pass_evidence.go", `package gate
func finalizeSQLiteRemoteCIRunAuthority() {}`),
		}
		locations := remoteCIRetentionExpectedSymbolLocations(files, expected)
		if got := len(locations["finalizeSQLiteRemoteCIRunAuthority"]); got != 2 {
			t.Fatalf("duplicate writer locations = %d, want 2", got)
		}
	})

	t.Run("other helper caller is rejected", func(t *testing.T) {
		files := []remoteCIRetentionSourceFile{remoteCIRetentionParseSourceFile(t, "ci_query_store.go", `package gate
type DurationLedgerStore struct{}
func (store *DurationLedgerStore) RecordProvisionalRemoteCIRun() { store.recordProvisionalRemoteCIRunTransaction() }
func (store *DurationLedgerStore) recordProvisionalRemoteCIRunTransaction() {}
func (other *DurationLedgerStore) unrelatedWriter() { other.recordProvisionalRemoteCIRunTransaction() }`)}
		delegation := remoteCIRetentionAllowedHelperDelegation()
		_, _, issues := remoteCIRetentionHelperDelegationIssues(files, delegation)
		if len(issues) == 0 || !strings.Contains(strings.Join(issues, "\n"), "unexpected caller/receiver") {
			t.Fatalf("other helper caller was not rejected: %v", issues)
		}
	})

	t.Run("database operation after compactor is rejected", func(t *testing.T) {
		file := remoteCIRetentionParseSourceFile(t, "retention_negative.go", `package gate
type Tx struct{}
func (tx *Tx) Exec(string) {}
func compactDurationLedgerAuthority(*Tx) error { return nil }
func writer(tx *Tx) error {
    if err := compactDurationLedgerAuthority(tx); err != nil { return err }
    tx.Exec("after")
    return nil
}`)
		writer := file.file.Decls[3].(*ast.FuncDecl)
		var compactPosition int
		ast.Inspect(writer.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && remoteCIRetentionCallName(call) == "compactDurationLedgerAuthority" {
				compactPosition = int(call.Pos())
			}
			return true
		})
		operations := remoteCIRetentionDatabaseOperationsAfter(writer, compactPosition)
		if len(operations) != 1 || operations[0] != "Exec" {
			t.Fatalf("database operations after compactor = %v, want [Exec]", operations)
		}
	})
}

func remoteCIRetentionParseSourceFile(t *testing.T, name, source string) remoteCIRetentionSourceFile {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		t.Fatalf("parse retention fixture %s: %v", name, err)
	}
	return remoteCIRetentionSourceFile{name: name, file: file}
}

// remoteCIRetentionAssertFinalDatabaseOperations 检查每个 production writer 的同步 compactor 边界。
func remoteCIRetentionAssertFinalDatabaseOperations(t *testing.T, directory string, expectedCallers map[string]bool) {
	t.Helper()
	files := remoteCIRetentionLoadSourceFiles(t, directory)
	remoteCIRetentionAssertUniqueExpectedSymbols(t, files, expectedCallers)
	delegation, delegated := remoteCIRetentionResolveHelperDelegation(t, files)
	for _, source := range files {
		remoteCIRetentionAssertFileFinalDatabaseOperations(t, source.name, source.file, delegation, delegated, expectedCallers)
	}
}

// remoteCIRetentionAssertFileFinalDatabaseOperations 分别检查调用位置、后续数据库访问和异步调用。
func remoteCIRetentionAssertFileFinalDatabaseOperations(t *testing.T, fileName string, file *ast.File, delegation remoteCIRetentionHelperDelegation, delegated bool, expectedCallers map[string]bool) {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		positions := remoteCIRetentionCompactionPositions(t, fileName, function, delegation, delegated, expectedCallers)
		remoteCIRetentionAssertNoDatabaseAfterCompaction(t, fileName, function, positions)
		remoteCIRetentionAssertNoAsyncCompaction(t, fileName, function)
	}
	remoteCIRetentionAssertNoDatabaseAfterDelegatedHelper(t, fileName, file, delegation, delegated)
}

// remoteCIRetentionCompactionPositions 登记唯一允许的同步 compactor 调用。
func remoteCIRetentionCompactionPositions(t *testing.T, fileName string, function *ast.FuncDecl, delegation remoteCIRetentionHelperDelegation, delegated bool, expectedCallers map[string]bool) []int {
	t.Helper()
	positions := make([]int, 0, 1)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || remoteCIRetentionCallName(call) != "compactDurationLedgerAuthority" {
			return true
		}
		caller := remoteCIRetentionDelegatedCaller(function, delegation, delegated)
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

// remoteCIRetentionAssertNoDatabaseAfterDelegatedHelper 保证 wrapper 委托后没有
// 新的数据库 selector；helper 自身的 compactor 尾部由同一遍 AST 检查锁定。
func remoteCIRetentionAssertNoDatabaseAfterDelegatedHelper(t *testing.T, fileName string, file *ast.File, delegation remoteCIRetentionHelperDelegation, delegated bool) {
	t.Helper()
	if !delegated {
		return
	}
	for _, declaration := range file.Decls {
		writer, ok := declaration.(*ast.FuncDecl)
		if !ok || remoteCIRetentionFunctionSymbolOf(writer) != delegation.writer || writer.Body == nil {
			continue
		}
		ast.Inspect(writer.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !remoteCIRetentionIsExactHelperCall(call, delegation.helper.name, remoteCIRetentionReceiverName(writer)) {
				return true
			}
			delegatedPosition := int(call.Pos())
			ast.Inspect(writer.Body, func(after ast.Node) bool {
				databaseCall, ok := after.(*ast.CallExpr)
				if !ok || int(databaseCall.Pos()) <= delegatedPosition {
					return true
				}
				remoteCIRetentionReportDatabaseOperationAfterCompaction(t, fileName, writer, databaseCall)
				return true
			})
			return true
		})
	}
}

// remoteCIRetentionAssertNoDatabaseAfterCompaction 防止 compactor 之后出现新的数据库操作。
func remoteCIRetentionAssertNoDatabaseAfterCompaction(t *testing.T, fileName string, function *ast.FuncDecl, positions []int) {
	t.Helper()
	for _, compactPosition := range positions {
		for _, databaseOperation := range remoteCIRetentionDatabaseOperationsAfter(function, compactPosition) {
			t.Errorf("%s.%s performs database operation %s after retention compaction", fileName, function.Name.Name, databaseOperation)
		}
	}
}

func remoteCIRetentionDatabaseOperationsAfter(function *ast.FuncDecl, compactPosition int) []string {
	operations := make([]string, 0)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || int(call.Pos()) <= compactPosition {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && remoteCIRetentionIsDatabaseOperation(selector.Sel.Name) {
			operations = append(operations, selector.Sel.Name)
		}
		return true
	})
	return operations
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
