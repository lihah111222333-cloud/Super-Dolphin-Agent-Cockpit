package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type knownMigrationDuplicate struct {
	Number int
	Names  []string
}

var knownDeployedDuplicateMigrations = []knownMigrationDuplicate{
	{Number: 1, Names: []string{"0001_initial_schema.sql", "001_baseline.sql"}},
	{Number: 6, Names: []string{"0006_agent_status.sql", "0006_workspace_runs.sql"}},
	{Number: 25, Names: []string{"0025_agent_thread_config_override.sql", "0025_hook_pending_reviews.sql"}},
}

type sqlcDatabaseConfig struct {
	SQL []struct {
		Engine string   `yaml:"engine"`
		Schema []string `yaml:"schema"`
	} `yaml:"sql"`
}

func TestMCPOrchSQLCUsesSQLiteBaseline(t *testing.T) {
	root := repoRoot(t)
	configPath := filepath.Join(root, "cmd", "mcp-orch", "sqlc.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read cmd/mcp-orch/sqlc.yaml: %v", err)
	}
	var config sqlcDatabaseConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse cmd/mcp-orch/sqlc.yaml: %v", err)
	}
	if len(config.SQL) != 1 {
		t.Fatalf("cmd/mcp-orch/sqlc.yaml sql entries = %d, want 1 SQLite owner", len(config.SQL))
	}
	entry := config.SQL[0]
	if entry.Engine != "sqlite" {
		t.Fatalf("cmd/mcp-orch/sqlc.yaml engine = %q, want sqlite", entry.Engine)
	}
	wantSchema := []string{"../../internal/platform/db/sqlite/migrations/001_baseline.sql"}
	if !reflect.DeepEqual(entry.Schema, wantSchema) {
		t.Fatalf("cmd/mcp-orch/sqlc.yaml schema = %#v, want SQLite baseline %#v", entry.Schema, wantSchema)
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
		for _, forbidden := range []string{"TODO(sqlc-migration)", ".Exec(", ".Query(", ".QueryRow(", ".ExecContext(", ".QueryContext(", ".QueryRowContext("} {
			if strings.Contains(content, forbidden) {
				violations = append(violations, fmt.Sprintf("%s contains %s", filepath.ToSlash(filepath.Join("internal", "store", "hookstore", name)), forbidden))
			}
		}
	}
	return violations
}

func missingHookSQLCMethodViolations(t *testing.T, root string) []string {
	t.Helper()
	generatedPath := filepath.Join(root, "internal", "store", "sqlc", "hook_pending_review.sql.go")
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
		"CheckHookReviewIdempotency",
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

func TestMigrationNumberUniqueness(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no migrations/*.sql files found")
	}
	failIfViolations(t, duplicateMigrationNumberViolations(paths, knownDeployedDuplicateMigrations))
}

func TestKnownDeployedDuplicateMigrationsPinned(t *testing.T) {
	want := []knownMigrationDuplicate{
		{Number: 1, Names: []string{"0001_initial_schema.sql", "001_baseline.sql"}},
		{Number: 6, Names: []string{"0006_agent_status.sql", "0006_workspace_runs.sql"}},
		{Number: 25, Names: []string{"0025_agent_thread_config_override.sql", "0025_hook_pending_reviews.sql"}},
	}
	if !reflect.DeepEqual(knownDeployedDuplicateMigrations, want) {
		t.Fatalf("knownDeployedDuplicateMigrations must only contain deployed duplicate numbers 1, 6, and 25; got %#v", knownDeployedDuplicateMigrations)
	}
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
	failIfViolations(t, duplicateMigrationNumberViolations(paths, nil))
}

func duplicateMigrationNumberViolations(paths []string, allowed []knownMigrationDuplicate) []string {
	allowedByNumber := map[int][]string{}
	for _, duplicate := range allowed {
		names := append([]string(nil), duplicate.Names...)
		sort.Strings(names)
		allowedByNumber[duplicate.Number] = names
	}

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
		allowed := allowedByNumber[number]
		if strings.Join(names, "\x00") == strings.Join(allowed, "\x00") {
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
