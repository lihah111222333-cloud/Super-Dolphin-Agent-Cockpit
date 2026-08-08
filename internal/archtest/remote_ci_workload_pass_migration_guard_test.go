package archtest

import (
	"go/ast"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCINormalRuntimeRejectsImplicitWorkloadPassMigration 锁定 normal
// runtime 与 gate runtime 只能按当前完整 workload identity 查询 PASS；历史
// identity 的 migration readback/alias projection 不得重新接回执行路径。
func TestRemoteCINormalRuntimeRejectsImplicitWorkloadPassMigration(t *testing.T) {
	root := findRepoRoot(t)
	seen := false
	for _, path := range remoteCIProductionFiles(t, root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativize remote CI production path %q: %v", path, err)
		}
		relativeSlash := filepath.ToSlash(relative)
		if !strings.HasPrefix(relativeSlash, "internal/devtools/remoteci/") &&
			!strings.HasPrefix(relativeSlash, "internal/devtools/gate/") {
			continue
		}
		seen = true
		parsed := parseRemoteCIContractGuardFile(t, path)
		for _, violation := range remoteCIImplicitWorkloadPassMigrationViolations(parsed) {
			t.Errorf("%s retains implicit workload PASS migration entry %s", relativeSlash, violation)
		}
	}
	if !seen {
		t.Fatal("remote-CI normal runtime source snapshot did not include internal/devtools/remoteci")
	}
}

// remoteCIImplicitWorkloadPassMigrationViolations 只检查 normal/gate runtime
// 的精确入口符号；不按 migration/alias 等通用词扫描，避免误禁严格 SQLite
// schema migration 与其表名。
func remoteCIImplicitWorkloadPassMigrationViolations(file *ast.File) []string {
	violations := map[string]bool{}
	forbidden := map[string]struct{}{
		"lookupRemoteWorkloadPassesForInput":            {},
		"LookupWorkloadPassEvidenceMigrationCandidates": {},
		"RecordMigratedWorkloadPassEvidence":            {},
		"loadWorkloadPassEvidenceAliasSource":           {},
		"loadWorkloadPassEvidenceAliasRelation":         {},
		"loadWorkloadPassEvidenceOriginContextWithSeen": {},
		"workloadPassEvidenceAliasOriginContext":        {},
		"validateStoredWorkloadPassEvidenceAlias":       {},
		"workloadPassEvidenceOriginCacheKey":            {},
		"validateMigratedWorkloadPassEvidencePair":      {},
	}
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if _, blocked := forbidden[identifier.Name]; blocked {
			violations["identifier "+identifier.Name] = true
		}
		return true
	})
	return remoteCIViolationList(violations)
}

// TestRemoteCIWorkloadPassMigrationGuardCounterexamples 验证精确入口会被
// 拒绝，而正常复用和严格物理 schema migration 不会被通用词误报。
func TestRemoteCIWorkloadPassMigrationGuardCounterexamples(t *testing.T) {
	safe := remoteCIParseGuardFixture(t, `package fixture
func lookupRemoteWorkloadPasses() {}
func migrateDurationLedgerSQLiteV11SchemaOnConnection() {
	_ = "CREATE TABLE IF NOT EXISTS ci_shard_terminal_containers"
	_ = "ci_workload_pass_evidence_aliases"
}
`)
	if got := remoteCIImplicitWorkloadPassMigrationViolations(safe); len(got) != 0 {
		t.Fatalf("safe exact PASS lookup/schema migration fixture has false violations: %v", got)
	}

	legacy := remoteCIParseGuardFixture(t, `package fixture
func lookupRemoteWorkloadPassesForInput() {}
func loadWorkloadPassEvidenceAliasSource() {}
func run(store interface{ LookupWorkloadPassEvidenceMigrationCandidates(); RecordMigratedWorkloadPassEvidence() }) {
	store.LookupWorkloadPassEvidenceMigrationCandidates()
	store.RecordMigratedWorkloadPassEvidence()
	loadWorkloadPassEvidenceAliasSource()
}
`)
	violations := strings.Join(remoteCIImplicitWorkloadPassMigrationViolations(legacy), "\n")
	for _, required := range []string{
		"identifier lookupRemoteWorkloadPassesForInput",
		"identifier LookupWorkloadPassEvidenceMigrationCandidates",
		"identifier RecordMigratedWorkloadPassEvidence",
		"identifier loadWorkloadPassEvidenceAliasSource",
	} {
		if !strings.Contains(violations, required) {
			t.Fatalf("implicit workload PASS migration fixture violations = %q, missing %q", violations, required)
		}
	}
}

// TestRemoteCIWorkloadPassMigrationGuardAllowsStrictSQLiteSchemaMigration
// 保留 v12→v13 的物理 DDL 与 schema identifier；守卫只禁止 runtime API，
// 不把严格 schema migration 当成隐式 workload 证据迁移。
func TestRemoteCIWorkloadPassMigrationGuardAllowsStrictSQLiteSchemaMigration(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"internal/devtools/gate/ledger_store_sqlite_workload_pass_alias_migration.go",
		"internal/devtools/gate/ledger_store_sqlite_schema_terminal_evidence.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		parsed := parseRemoteCIContractGuardFile(t, path)
		if got := remoteCIImplicitWorkloadPassMigrationViolations(parsed); len(got) != 0 {
			t.Fatalf("strict SQLite schema migration %s has false runtime migration violations: %v", relative, got)
		}
	}
}
