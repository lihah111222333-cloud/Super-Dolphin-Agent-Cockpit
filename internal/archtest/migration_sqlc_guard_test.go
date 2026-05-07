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
	if !strings.Contains(string(data), "\n        query_parameter_limit: 0\n") {
		t.Fatalf("sqlc.yaml must pin gen.go.query_parameter_limit: 0 to always generate param structs per convention")
	}
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
