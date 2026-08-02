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
	retention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_retention.go"))
	sampleRetention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_write.go"))
	checkpointRetention := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/calibration_checkpoint_store.go"))
	retentionSources := retention + sampleRetention + checkpointRetention
	if got := remoteCIRetentionContractBlock(t, contract); got != cicontract.CanonicalRetentionMarkdown() {
		t.Fatal("accepted retention prose and cicontract retention policy are not 1:1")
	}
	if strings.Count(retention, "func compactDurationLedgerAuthority(") != 1 {
		t.Fatal("duration ledger retention must expose exactly one authority compactor")
	}
	for _, forbidden := range []string{"go compactDurationLedgerAuthority", "time.NewTicker", "time.AfterFunc"} {
		if strings.Contains(retention, forbidden) {
			t.Fatalf("retention authority must not use background GC: %q", forbidden)
		}
	}
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
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema.go"))
	bindings := cicontract.RetentionRootBindings()
	if len(bindings) != 4 {
		t.Fatalf("retention root count = %d, want 4", len(bindings))
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
		"ci_query_store.go.RecordRemoteCIRun":                                         false,
		"ledger_store_sqlite.go.appendSQLiteSamplesOnce":                              false,
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file := parseRemoteCIContractGuardFile(t, filepath.Join(directory, entry.Name()))
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || remoteCIRetentionCallName(call) != "requireHistoricallyAcceptedGeneration" {
					return true
				}
				caller := entry.Name() + "." + function.Name.Name
				if _, expected := expectedCallers[caller]; !expected {
					t.Errorf("unexpected accepted-generation proof caller %s", caller)
				} else if expectedCallers[caller] {
					t.Errorf("accepted-generation proof caller %s invokes the guard more than once", caller)
				} else {
					expectedCallers[caller] = true
				}
				return true
			})
		}
	}
	for caller, guarded := range expectedCallers {
		if !guarded {
			t.Errorf("historical-root writer %s does not prove its accepted generation", caller)
		}
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
		"ci_query_store.go.RecordRemoteCIRun":                                         false,
		"ledger_store_sqlite.go.appendSQLiteSamplesOnce":                              false,
		"remote_ci_authority_store.go.AppendRemoteRefreshDelta":                       false,
		"remote_ci_authority_store.go.AppendCheckReceipts":                            false,
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file := parseRemoteCIContractGuardFile(t, filepath.Join(directory, entry.Name()))
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			compactPositions := make([]int, 0, 1)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := remoteCIRetentionCallName(call)
				if name == "compactDurationLedgerAuthority" {
					caller := entry.Name() + "." + function.Name.Name
					if _, expected := expectedCallers[caller]; !expected {
						t.Errorf("unexpected retention authority caller %s", caller)
					} else if expectedCallers[caller] {
						t.Errorf("retention authority caller %s invokes compaction more than once", caller)
					} else {
						expectedCallers[caller] = true
					}
					compactPositions = append(compactPositions, int(call.Pos()))
				}
				return true
			})
			for _, compactPosition := range compactPositions {
				ast.Inspect(function.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || int(call.Pos()) <= compactPosition {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if ok && (selector.Sel.Name == "Exec" || selector.Sel.Name == "Query" || selector.Sel.Name == "QueryRow" || selector.Sel.Name == "Prepare") {
						t.Errorf("%s.%s performs database operation %s after retention compaction", entry.Name(), function.Name.Name, selector.Sel.Name)
					}
					return true
				})
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				statement, ok := node.(*ast.GoStmt)
				if !ok {
					return true
				}
				if name := remoteCIRetentionCallName(statement.Call); name == "compactDurationLedgerAuthority" {
					t.Errorf("%s.%s starts retention asynchronously", entry.Name(), function.Name.Name)
				}
				return true
			})
		}
	}
	for caller, called := range expectedCallers {
		if !called {
			t.Errorf("retention authority is not the final operation of required writer %s", caller)
		}
	}
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
