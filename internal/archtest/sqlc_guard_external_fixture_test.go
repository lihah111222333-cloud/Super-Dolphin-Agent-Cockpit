package archtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeCanonicalSQLCGuardFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"sqlc.yaml": `version: "2"
sql:
  - engine: sqlite
    queries: sql/queries
    schema: [schema/main.sql]
    strict_order_by: true
    gen:
      go:
        package: sqlc
        out: internal/store/sqlc
        emit_interface: true
        query_parameter_limit: 0
`,
		"cmd/mcp-orch/sqlc.yaml": `version: "2"
sql:
  - engine: sqlite
    queries: sql/queries
    schema: [../../schema/orch.sql]
    strict_order_by: true
    gen:
      go:
        package: sqlc
        out: store/sqlc
        query_parameter_limit: 0
`,
		"sql/queries/query.sql":              "-- fixture\n",
		"cmd/mcp-orch/sql/queries/query.sql": "-- fixture\n",
		"schema/main.sql":                    "-- fixture\n",
		"schema/orch.sql":                    "-- fixture\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create SQLC fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write SQLC fixture file: %v", err)
		}
	}
	command := exec.Command("git", "-C", root, "init", "-q")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize SQLC fixture Git repository: %v\n%s", err, output)
	}
}
