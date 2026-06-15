package binding

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// TestUpsertForwardsCodexIdentity asserts P21 P1a persistence: when the
// caller supplies codex identity in UpsertParams, all three fields reach the
// underlying sqlc param struct unchanged.
func TestUpsertForwardsCodexIdentity(t *testing.T) {
	t.Parallel()

	params := UpsertParams{
		AgentID:            "agent-identity",
		Provider:           "codex",
		ProviderThreadID:   "pt-identity",
		CodexThreadID:      "ct-identity",
		Cwd:                "/repo",
		CreatedAt:          1,
		UpdatedAt:          2,
		CodexHome:          "/realpath/.codex-providers/glm",
		CodexInstanceKey:   "glm",
		CodexModelProvider: "glm-compat",
	}
	var got sqlc.UpsertAgentProviderBindingParams
	s := &store{q: &bindingQuerierStub{
		upsertAgentProviderBindingFn: func(_ context.Context, arg sqlc.UpsertAgentProviderBindingParams) error {
			got = arg
			return nil
		},
	}}
	if err := s.Upsert(context.Background(), params); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.CodexHome != params.CodexHome ||
		got.CodexInstanceKey != params.CodexInstanceKey ||
		got.CodexModelProvider != params.CodexModelProvider {
		t.Fatalf("identity forwarded wrong: home=%q key=%q provider=%q",
			got.CodexHome, got.CodexInstanceKey, got.CodexModelProvider)
	}
}

// TestGetByAgentIDSurfacesCodexIdentity asserts the read path maps the three
// new sqlc columns into the Binding struct consumers see.
func TestGetByAgentIDSurfacesCodexIdentity(t *testing.T) {
	t.Parallel()

	row := sqlc.AgentProviderBinding{
		AgentID:            "agent-read",
		Provider:           "codex",
		ProviderThreadID:   "pt-read",
		CodexThreadID:      "ct-read",
		RolloutPath:        "/tmp/r",
		CWD:                "/repo",
		Archived:           0,
		CreatedAt:          5,
		UpdatedAt:          6,
		SessionUUID:        "sess",
		CodexHome:          "/realpath/.codex-providers/glm",
		CodexInstanceKey:   "glm",
		CodexModelProvider: "glm-compat",
	}
	s := &store{q: &bindingQuerierStub{
		getAgentProviderBindingByAgentIDFn: func(_ context.Context, _ string) (sqlc.AgentProviderBinding, error) {
			return row, nil
		},
	}}

	b, err := s.GetByAgentID(context.Background(), "agent-read")
	if err != nil {
		t.Fatalf("GetByAgentID() error = %v", err)
	}
	if b.CodexHome != row.CodexHome ||
		b.CodexInstanceKey != row.CodexInstanceKey ||
		b.CodexModelProvider != row.CodexModelProvider {
		t.Fatalf("identity not surfaced: %+v", b)
	}
}

// TestGetByProviderThreadSurfacesCodexIdentity covers the second read path.
func TestGetByProviderThreadSurfacesCodexIdentity(t *testing.T) {
	t.Parallel()

	row := sqlc.AgentProviderBinding{
		AgentID:            "agent-pt",
		Provider:           "codex",
		ProviderThreadID:   "pt",
		CodexHome:          "/realpath/.codex-providers/qwen",
		CodexInstanceKey:   "qwen",
		CodexModelProvider: "qwen-compat",
	}
	s := &store{q: &bindingQuerierStub{
		getByProviderThreadFn: func(_ context.Context, _ sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error) {
			return row, nil
		},
	}}
	b, err := s.GetByProviderThread(context.Background(), "codex", "pt")
	if err != nil {
		t.Fatalf("GetByProviderThread() error = %v", err)
	}
	if b.CodexHome != row.CodexHome ||
		b.CodexInstanceKey != row.CodexInstanceKey ||
		b.CodexModelProvider != row.CodexModelProvider {
		t.Fatalf("identity not surfaced: %+v", b)
	}
}

// TestListAgentThreadBindingsSurfacesCodexIdentity covers the list read path.
func TestListAgentThreadBindingsSurfacesCodexIdentity(t *testing.T) {
	t.Parallel()

	s := &store{q: &bindingQuerierStub{
		listAgentThreadBindingsFn: func(context.Context) ([]sqlc.AgentProviderBinding, error) {
			return []sqlc.AgentProviderBinding{{
				AgentID:            "agent-list",
				Provider:           "codex",
				ProviderThreadID:   "pt-list",
				CodexHome:          "/realpath/.codex-providers/glm",
				CodexInstanceKey:   "glm",
				CodexModelProvider: "glm-compat",
				CreatedAt:          1,
				UpdatedAt:          2,
			}}, nil
		},
	}}

	bindings, err := s.ListAgentThreadBindings(context.Background())
	if err != nil {
		t.Fatalf("ListAgentThreadBindings() error = %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("len = %d, want 1", len(bindings))
	}
	b := bindings[0]
	if b.CodexHome != "/realpath/.codex-providers/glm" ||
		b.CodexInstanceKey != "glm" ||
		b.CodexModelProvider != "glm-compat" {
		t.Fatalf("identity not surfaced: %+v", b)
	}
}

// TestUpsertSQLPreservesCodexIdentityOnEmpty asserts the migration 0048
// discipline: the ON CONFLICT DO UPDATE SET clause for each codex identity
// column uses the "” preserves existing value" CASE. A caller passing ”
// must not clobber an already-populated identity. The immutable trigger then
// catches any attempt to rewrite a non-empty value with a different
// non-empty one; that side is covered by the migration SQL lint below.
func TestUpsertSQLPreservesCodexIdentityOnEmpty(t *testing.T) {
	t.Parallel()

	sql := readRepoFile(t, filepath.Join("sql", "queries", "agent_provider_binding.sql"))
	cols := []string{"codex_home", "codex_instance_key", "codex_model_provider"}
	for _, col := range cols {
		want := "CASE WHEN EXCLUDED." + col + " = '' THEN agent_provider_binding." + col +
			" ELSE EXCLUDED." + col + " END"
		if !strings.Contains(sql, want) {
			t.Fatalf("UPSERT must guard %q with empty-preserves-existing CASE; got:\n%s", col, sql)
		}
	}
}

// TestMigration0048ExtendsImmutableTrigger asserts the migration declares
// the three new identity columns as immutable once set (non-empty -> different
// non-empty). This is the schema-level counterpart to the CASE guard above.
func TestMigration0048ExtendsImmutableTrigger(t *testing.T) {
	t.Parallel()

	sql := readRepoFile(t, filepath.Join("migrations", "0048_binding_codex_identity.sql"))
	for _, col := range []string{"codex_home", "codex_instance_key", "codex_model_provider"} {
		// Match "OLD.<col> <> '' AND NEW.<col> IS DISTINCT FROM OLD.<col>"
		pattern := regexp.MustCompile(
			`OLD\.` + regexp.QuoteMeta(col) + `\s*<>\s*''\s+AND\s+NEW\.` +
				regexp.QuoteMeta(col) + `\s+IS\s+DISTINCT\s+FROM\s+OLD\.` + regexp.QuoteMeta(col))
		if !pattern.MatchString(sql) {
			t.Fatalf("migration 0048 must guard %q with once-set-immutable clause", col)
		}
		if !strings.Contains(sql, "ALTER TABLE agent_provider_binding") ||
			!strings.Contains(sql, "ADD COLUMN IF NOT EXISTS "+col) {
			t.Fatalf("migration 0048 must add column %q", col)
		}
	}
}

// readRepoFile walks up from the current test file location to the repository
// root (where go.mod lives) and reads `rel`. Using this helper keeps tests
// independent of the working directory used by go test.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found walking up from %s", file)
	return ""
}
