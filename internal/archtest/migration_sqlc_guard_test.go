package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestSqlcQueryParameterLimitPinned(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "sqlc.yaml"))
	if err != nil {
		t.Fatalf("read sqlc.yaml: %v", err)
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.Contains(content, "\n        query_parameter_limit: 0\n") {
		t.Fatalf("sqlc.yaml must pin gen.go.query_parameter_limit: 0 to always generate param structs per convention")
	}
}

func TestSqlcStrictOrderByAtSQLBlockLevel(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "sqlc.yaml"))
	if err != nil {
		t.Fatalf("read sqlc.yaml: %v", err)
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.Contains(content, "\n        strict_order_by: true\n") {
		t.Fatalf("sqlc.yaml must not place strict_order_by under gen.go options; sqlc v1.30.0 rejects that location")
	}
	if !strings.Contains(content, "\n    strict_order_by: true\n") {
		t.Fatalf("sqlc.yaml must pin strict_order_by at the top-level sql block so make sqlc-verify can parse it")
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
		for _, forbidden := range []string{"TODO(sqlc-migration)", ".Exec(", ".Query(", ".QueryRow("} {
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

	knownDeployedDuplicates := map[int][]string{
		1:  {"0001_initial_schema.sql", "001_baseline.sql"},
		6:  {"0006_agent_status.sql", "0006_workspace_runs.sql"},
		25: {"0025_agent_thread_config_override.sql", "0025_hook_pending_reviews.sql"},
	}
	for number, names := range byNumber {
		sort.Strings(names)
		if len(names) <= 1 {
			continue
		}
		allowed := append([]string(nil), knownDeployedDuplicates[number]...)
		sort.Strings(allowed)
		if strings.Join(names, "\x00") == strings.Join(allowed, "\x00") {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"migration number %04d is reused by %s; add a new higher-numbered migration instead of renaming deployed files",
			number,
			strings.Join(names, ", "),
		))
	}
	failIfViolations(t, violations)
}
