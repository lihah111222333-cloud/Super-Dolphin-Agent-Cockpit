package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

// TestMCPOrchSQLCUsesSQLiteMigrationManifest 校验编排器 SQLC 使用唯一 SQLite 迁移清单。
func TestMCPOrchSQLCUsesSQLiteMigrationManifest(t *testing.T) {
	root := repoRoot(t)
	units, err := sqlcconfig.LoadDefault(root)
	if err != nil {
		t.Fatalf("load canonical SQLC configs: %v", err)
	}
	var entry *sqlcconfig.Unit
	for index := range units {
		if units[index].ConfigPath == "cmd/mcp-orch/sqlc.yaml" {
			if entry != nil {
				t.Fatal("cmd/mcp-orch/sqlc.yaml has multiple SQLC units; want one SQLite owner")
			}
			entry = &units[index]
		}
	}
	if entry == nil {
		t.Fatal("cmd/mcp-orch/sqlc.yaml has no SQLC unit")
	}
	wantSchema := []string{
		"internal/platform/db/sqlite/migrations/001_baseline.sql",
		"internal/platform/db/sqlite/migrations/124_orchestration_direct_peer.sql",
		"internal/platform/db/sqlite/migrations/125_orchestration_direct_peer_events.sql",
		"internal/platform/db/sqlite/migrations/126_orchestration_command_intents.sql",
		"internal/platform/db/sqlite/migrations/127_orchestration_command_journal.sql",
		"internal/platform/db/sqlite/migrations/128_orchestration_identity_owner.sql",
		"internal/platform/db/sqlite/migrations/129_orchestration_event_store.sql",
		"internal/platform/db/sqlite/migrations/130_orchestration_access_wait.sql",
		"internal/platform/db/sqlite/migrations/131_orchestration_command_intent_profiles.sql",
		"internal/platform/db/sqlite/migrations/132_orchestration_content_single.sql",
		"internal/platform/db/sqlite/migrations/133_orchestration_resource_reserved.sql",
		"internal/platform/db/sqlite/migrations/134_orchestration_wakeup_payload_version.sql",
		"internal/platform/db/sqlite/migrations/135_orchestration_command_attempts.sql",
		"internal/platform/db/sqlite/migrations/136_orchestration_wait_operation.sql",
		"cmd/mcp-orch/store/orchestrationcommand/candidate_migrations/137_orchestration_command_submissions.sql",
	}
	if !slices.Equal(entry.Schemas, wantSchema) {
		t.Fatalf("cmd/mcp-orch/sqlc.yaml schema = %#v, want canonical SQLite migration manifest %#v", entry.Schemas, wantSchema)
	}
	for _, schema := range entry.Schemas {
		if strings.Contains(schema, "candidate_migrations/") && strings.HasPrefix(schema, "internal/platform/db/sqlite/migrations/") {
			t.Fatalf("candidate schema must not be placed in the default embedded migration directory: %s", schema)
		}
	}
}

// TestMCPOrchWaitOperationSQLCArtifactsAreManifestOwned 校验 wait_operation 产物归属清单声明的 SQLC 单元。
func TestMCPOrchWaitOperationSQLCArtifactsAreManifestOwned(t *testing.T) {
	root := repoRoot(t)
	owner := migrationSQLCOwner(t, root)
	assertWaitOperationOwnerPaths(t, owner)
	assertWaitOperationQuery(t, root, owner)
	assertWaitOperationGenerated(t, root, owner)
}

// migrationSQLCOwner 从规范 SQLC 配置中解析唯一的编排器所有者。
func migrationSQLCOwner(t *testing.T, root string) *sqlcconfig.Unit {
	t.Helper()
	units, err := sqlcconfig.LoadDefault(root)
	if err != nil {
		t.Fatalf("load canonical SQLC configs: %v", err)
	}
	var owner *sqlcconfig.Unit
	for index := range units {
		unit := &units[index]
		if unit.ConfigPath != sqlcconfig.OrchestratorConfigPath {
			continue
		}
		if owner != nil {
			t.Fatal("cmd/mcp-orch/sqlc.yaml has multiple SQLC units; wait_operation ownership is ambiguous")
		}
		owner = unit
	}
	if owner == nil {
		t.Fatal("cmd/mcp-orch/sqlc.yaml has no SQLC unit owning wait_operation artifacts")
	}
	return owner
}

// assertWaitOperationOwnerPaths 固定 wait_operation 查询与生成物的 SQLC 路径。
func assertWaitOperationOwnerPaths(t *testing.T, owner *sqlcconfig.Unit) {
	t.Helper()
	if owner.Queries != "cmd/mcp-orch/sql/queries" || owner.Output != "cmd/mcp-orch/store/sqlc" {
		t.Fatalf("wait_operation SQLC owner paths = queries:%q output:%q", owner.Queries, owner.Output)
	}
}

// assertWaitOperationQuery 确认查询同时引用 migration 136 的两张表。
func assertWaitOperationQuery(t *testing.T, root string, owner *sqlcconfig.Unit) {
	t.Helper()
	queryPath := filepath.Join(root, filepath.FromSlash(owner.Queries), "wait_operation.sql")
	query, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read manifest-owned wait_operation query: %v", err)
	}
	if !strings.Contains(string(query), "orchestration_wait_operations") || !strings.Contains(string(query), "orchestration_wait_operation_target_journal") {
		t.Fatal("manifest-owned wait_operation query does not reference both migration 136 tables")
	}
}

// assertWaitOperationGenerated 确认 wait_operation.sql.go 是该清单生成的 SQLC 文件。
func assertWaitOperationGenerated(t *testing.T, root string, owner *sqlcconfig.Unit) {
	t.Helper()
	generatedPath := filepath.Join(root, filepath.FromSlash(owner.Output), "wait_operation.sql.go")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read manifest-owned wait_operation generated file: %v", err)
	}
	if !strings.HasPrefix(string(generated), sqlcconfig.GeneratedGoMarker) || !strings.Contains(string(generated), "source: wait_operation.sql") {
		t.Fatal("wait_operation.sql.go is not the generated output owned by the orchestrator SQLC manifest")
	}
}

func TestSQLiteTaskDAGTestsRejectPostgreSQLDrift(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "cmd", "mcp-orch", "store", "taskdag", "*_test.go"))
	if err != nil {
		t.Fatalf("glob taskdag tests: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no taskdag tests found")
	}
	var violations []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := strings.ToLower(string(data))
		for _, forbidden := range []string{
			"taskdag_integration",
			"testtaskdagpgintegration",
			"testcontainers",
			"requires pg",
			"select for update",
		} {
			if strings.Contains(content, forbidden) {
				violations = append(violations, fmt.Sprintf("%s contains PostgreSQL-only marker %q", filepath.Base(path), forbidden))
			}
		}
	}
	failIfViolations(t, violations)
}

func TestSqlcQueryParameterLimitPinned(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"sqlc.yaml", "cmd/mcp-orch/sqlc.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := strings.ReplaceAll(string(data), "\r\n", "\n")
		if !strings.Contains(content, "\n        query_parameter_limit: 0\n") {
			t.Fatalf("%s must pin gen.go.query_parameter_limit: 0 to always generate param structs per convention", rel)
		}
	}
}

func TestSqlcStrictOrderByAtSQLBlockLevel(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"sqlc.yaml", "cmd/mcp-orch/sqlc.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := strings.ReplaceAll(string(data), "\r\n", "\n")
		if strings.Contains(content, "\n        strict_order_by: true\n") {
			t.Fatalf("%s must not place strict_order_by under gen.go options; sqlc v1.30.0 rejects that location", rel)
		}
		if !strings.Contains(content, "\n    strict_order_by: true\n") {
			t.Fatalf("%s must pin strict_order_by at the top-level sql block so make sqlc-verify can parse it", rel)
		}
	}
}

func TestHookstoreUsesGeneratedSQLC(t *testing.T) {
	root := repoRoot(t)
	violations := collectHookstoreSQLCBypassViolations(t, root)
	violations = append(violations, missingHookSQLCMethodViolations(t, root)...)
	failIfViolations(t, violations)
}

func collectHookstoreSQLCBypassViolations(t *testing.T, root string) []string {
	t.Helper()
	storeDir := filepath.Join(root, "internal", "store", "hookstore")
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		t.Fatalf("read hookstore dir: %v", err)
	}
	var violations []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(storeDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)
		for _, forbidden := range []string{"TODO(sqlc-migration)", "CheckHookReviewIdempotency", ".Exec(", ".Query(", ".QueryRow(", ".ExecContext(", ".QueryContext(", ".QueryRowContext("} {
			if strings.Contains(content, forbidden) {
				violations = append(violations, fmt.Sprintf("%s contains %s", filepath.ToSlash(filepath.Join("internal", "store", "hookstore", name)), forbidden))
			}
		}
	}
	return violations
}

func missingHookSQLCMethodViolations(t *testing.T, root string) []string {
	t.Helper()
	generatedDir := sqlcGeneratedDirForConfig(t, root, "sqlc.yaml")
	generatedPath := filepath.Join(generatedDir, "hook_pending_review.sql.go")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated hook sqlc file: %v", err)
	}
	generatedContent := string(generated)
	var violations []string
	for _, method := range []string{
		"SaveHookPendingReview",
		"GetHookPendingReview",
		"ListHookPendingReviewsByAgent",
		"ResolveHookPendingReview",
		"GetHookResolvedReview",
		"CancelHookPendingReviewsByLease",
		"CancelHookPendingReviewsByAgent",
		"CancelExpiredHookReviews",
		"RecoverHookPendingReviews",
	} {
		if !strings.Contains(generatedContent, "func (q *Queries) "+method+"(") {
			violations = append(violations, fmt.Sprintf("generated hook_pending_review sqlc file missing %s", method))
		}
	}
	return violations
}

func sqlcGeneratedDirForConfig(t *testing.T, root, configPath string) string {
	t.Helper()
	units, err := sqlcconfig.LoadDefault(root)
	if err != nil {
		t.Fatalf("load canonical SQLC output owner: %v", err)
	}
	var matches []sqlcconfig.Unit
	for _, unit := range units {
		if unit.ConfigPath == configPath {
			matches = append(matches, unit)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("SQLC config %s units = %d, want exactly one", configPath, len(matches))
	}
	return filepath.Join(root, filepath.FromSlash(matches[0].Output))
}

func TestSQLiteRuntimeMigrationNumberUniqueness(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("glob SQLite runtime migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no internal/platform/db/sqlite/migrations/*.sql files found")
	}
	failIfViolations(t, duplicateMigrationNumberViolations(paths))
}

func duplicateMigrationNumberViolations(paths []string) []string {
	byNumber := map[int][]string{}
	var violations []string
	prefixRE := regexp.MustCompile(`^(\d+)_.*\.sql$`)
	for _, path := range paths {
		name := filepath.Base(path)
		match := prefixRE.FindStringSubmatch(name)
		if match == nil {
			violations = append(violations, fmt.Sprintf("%s does not start with a numeric migration prefix", name))
			continue
		}
		n, err := strconv.Atoi(match[1])
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s has invalid migration prefix %q: %v", name, match[1], err))
			continue
		}
		byNumber[n] = append(byNumber[n], name)
	}

	numbers := make([]int, 0, len(byNumber))
	for number := range byNumber {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		names := byNumber[number]
		sort.Strings(names)
		if len(names) <= 1 {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"migration number %04d is reused by %s; add a new higher-numbered migration instead of renaming deployed files",
			number,
			strings.Join(names, ", "),
		))
	}
	return violations
}
