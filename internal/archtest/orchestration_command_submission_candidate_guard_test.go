package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

const candidateSubmissionTable = "orchestration_command_submissions"

func TestCommandSubmissionCandidateSQLCArtifactsHaveExplicitOwnership(t *testing.T) {
	root := repoRoot(t)
	owner := orchestratorSQLCOwner(t, root)
	assertCandidateSchemaDeclared(t, owner)
	assertCandidateQuery(t, root)
	assertCandidateGeneratedArtifact(t, root)
}

// TestCommandSubmissionCandidateStaysOutsideStartupAndProductionWiring guards the explicit candidate boundary.
func TestCommandSubmissionCandidateStaysOutsideStartupAndProductionWiring(t *testing.T) {
	root := repoRoot(t)
	assertCandidateSchemaPresent(t, root)
	assertDefaultEmbeddedMigrationsExcludeCandidate(t, root)
	assertProductionWiringExcludesCandidate(t, root)
	assertDefaultMigrationTreeExcludesCandidate(t, root)
}

func orchestratorSQLCOwner(t *testing.T, root string) *sqlcconfig.Unit {
	t.Helper()
	units, err := sqlcconfig.LoadDefault(root)
	if err != nil {
		t.Fatalf("load canonical SQLC configs: %v", err)
	}
	var owner *sqlcconfig.Unit
	for index := range units {
		if units[index].ConfigPath != sqlcconfig.OrchestratorConfigPath {
			continue
		}
		if owner != nil {
			t.Fatal("cmd/mcp-orch/sqlc.yaml has multiple SQLC units")
		}
		owner = &units[index]
	}
	if owner == nil {
		t.Fatal("cmd/mcp-orch/sqlc.yaml has no SQLC unit")
	}
	return owner
}

func assertCandidateSchemaDeclared(t *testing.T, owner *sqlcconfig.Unit) {
	t.Helper()
	candidateSchema := "cmd/mcp-orch/store/orchestrationcommand/candidate_migrations/137_orchestration_command_submissions.sql"
	if !candidateContainsString(owner.Schemas, candidateSchema) {
		t.Fatalf("candidate schema %q is not declared by the orchestrator SQLC owner: %v", candidateSchema, owner.Schemas)
	}
}

func assertCandidateQuery(t *testing.T, root string) {
	t.Helper()
	queryPath := filepath.Join(root, filepath.FromSlash("cmd/mcp-orch/sql/queries/orchestration_command_submission.sql"))
	query, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read candidate SQLC query: %v", err)
	}
	if !strings.Contains(string(query), candidateSubmissionTable) {
		t.Fatalf("candidate SQLC query does not reference %s", candidateSubmissionTable)
	}
}

func assertCandidateGeneratedArtifact(t *testing.T, root string) {
	t.Helper()
	generatedPath := filepath.Join(root, filepath.FromSlash("cmd/mcp-orch/store/sqlc/orchestration_command_submission.sql.go"))
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read candidate SQLC generated artifact: %v", err)
	}
	if !strings.HasPrefix(string(generated), sqlcconfig.GeneratedGoMarker) || !strings.Contains(string(generated), "source: orchestration_command_submission.sql") {
		t.Fatal("candidate SQLC generated artifact is missing canonical ownership markers")
	}
}

func assertCandidateSchemaPresent(t *testing.T, root string) {
	t.Helper()
	candidateRel := filepath.ToSlash(filepath.Join("cmd", "mcp-orch", "store", "orchestrationcommand", "candidate_migrations", "137_orchestration_command_submissions.sql"))
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidateRel))); err != nil {
		t.Fatalf("candidate schema is missing: %v", err)
	}
}

func assertDefaultEmbeddedMigrationsExcludeCandidate(t *testing.T, root string) {
	t.Helper()
	candidateRel := filepath.ToSlash(filepath.Join("cmd", "mcp-orch", "store", "orchestrationcommand", "candidate_migrations", "137_orchestration_command_submissions.sql"))
	embedded, err := os.ReadFile(filepath.Join(root, "internal", "platform", "db", "sqlite", "embedded_migrations.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(embedded), candidateRel) || strings.Contains(string(embedded), "candidate_migrations") {
		t.Fatalf("default sqlite embedded migrations include candidate schema %q", candidateRel)
	}
}

func assertProductionWiringExcludesCandidate(t *testing.T, root string) {
	t.Helper()
	violations := productionCandidateWiringViolations(t, root)
	if len(violations) > 0 {
		t.Fatalf("production command paths call candidate schema installer: %v", violations)
	}
}

func productionCandidateWiringViolations(t *testing.T, root string) []string {
	t.Helper()
	var violations []string
	candidateDir := filepath.Join(root, "cmd", "mcp-orch", "store", "orchestrationcommand")
	walk := func(path string, info os.FileInfo, walkErr error) error {
		violation, err := inspectCandidateProductionFile(path, info, candidateDir, walkErr)
		if violation {
			violations = append(violations, path)
		}
		return err
	}
	if err := filepath.Walk(filepath.Join(root, "cmd", "mcp-orch"), walk); err != nil {
		t.Fatal(err)
	}
	return violations
}

func inspectCandidateProductionFile(path string, info os.FileInfo, candidateDir string, walkErr error) (bool, error) {
	if walkErr != nil || info == nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false, walkErr
	}
	if strings.HasPrefix(path, candidateDir+string(filepath.Separator)) {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	source := string(data)
	return strings.Contains(source, "EnsureCandidateSchema(") || strings.Contains(source, "candidate_migrations/137_orchestration_command_submissions.sql"), nil
}

func assertDefaultMigrationTreeExcludesCandidate(t *testing.T, root string) {
	t.Helper()
	if violations := candidateMigrationDefaultTreeViolations(root); len(violations) > 0 {
		t.Fatalf("candidate migration is present in the default SQLite migration tree: %v", violations)
	}
}

func TestCandidateMigrationDefaultTreeViolationIsRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "137_orchestration_command_submissions.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE orchestration_command_submissions (id TEXT);"), 0o644); err != nil {
		t.Fatal(err)
	}
	violations := candidateMigrationDefaultTreeViolations(root)
	if len(violations) != 1 || violations[0] != filepath.ToSlash(filepath.Join("internal", "platform", "db", "sqlite", "migrations", "137_orchestration_command_submissions.sql")) {
		t.Fatalf("candidate migration violations = %v, want copied candidate migration", violations)
	}
}

func candidateMigrationDefaultTreeViolations(root string) []string {
	paths, err := filepath.Glob(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations", "*.sql"))
	if err != nil {
		return []string{err.Error()}
	}
	var violations []string
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			violations = append(violations, filepath.ToSlash(path))
			continue
		}
		if strings.Contains(strings.ToLower(string(body)), "create table "+candidateSubmissionTable) {
			violations = append(violations, filepath.ToSlash(filepath.Join("internal", "platform", "db", "sqlite", "migrations", filepath.Base(path))))
		}
	}
	return violations
}

func candidateContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
