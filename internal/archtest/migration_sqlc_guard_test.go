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

func TestMCPOrchSQLCUsesSQLiteBaseline(t *testing.T) {
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
	wantSchema := []string{"internal/platform/db/sqlite/migrations/001_baseline.sql"}
	if !slices.Equal(entry.Schemas, wantSchema) {
		t.Fatalf("cmd/mcp-orch/sqlc.yaml schema = %#v, want canonical SQLite baseline %#v", entry.Schemas, wantSchema)
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
