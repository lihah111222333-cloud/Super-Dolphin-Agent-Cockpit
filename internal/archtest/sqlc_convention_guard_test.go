package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSqlcConventionGuard(t *testing.T) {
	root := repoRoot(t)
	queriesDir := filepath.Join(root, "sql", "queries")

	var violations []string
	re := regexp.MustCompile(`(?i)\bSELECT\s+\*`)

	err := filepath.Walk(queriesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			if re.MatchString(line) {
				violations = append(violations, fmt.Sprintf("%s:%d uses forbidden SELECT *", filepath.ToSlash(relPath), i+1))
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to walk sql queries: %v", err)
	}

	failIfViolations(t, violations)
}
