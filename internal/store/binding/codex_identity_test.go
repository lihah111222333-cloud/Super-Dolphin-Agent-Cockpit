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

// TestUpsertForwardsCodexIdentity 锁住 Codex identity 写入路径。
// 调用方显式传入三段 identity 时，store 必须原样传给 sqlc 参数，不能在中间层丢字段。
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

// TestGetByAgentIDSurfacesCodexIdentity 锁住按 agent id 读取时的 identity 映射。
// sqlc 行里的三段字段必须进入 Binding，供上层恢复 Codex 实例上下文。
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

// TestGetByProviderThreadSurfacesCodexIdentity 覆盖按 provider thread 读取时的同一映射边界。
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

// TestListAgentThreadBindingsSurfacesCodexIdentity 覆盖列表读取路径，避免批量查询遗漏 identity 字段。
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

// TestUpsertSQLPreservesCodexIdentityOnEmpty 校验 SQL upsert 的空值保留规则。
// 调用方传空字符串时不能覆盖已有 identity；非空改写由下方迁移 SQL 检查覆盖。
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

// 校验数据库迁移声明 identity 一经设置不可改写。
// 这是数据库层对 upsert CASE 保护的补充，防止非空值被另一个非空值替换。
func TestMigration0048ExtendsImmutableTrigger(t *testing.T) {
	t.Parallel()

	sql := readRepoFile(t, filepath.Join("migrations", "0048_binding_codex_identity.sql"))
	for _, col := range []string{"codex_home", "codex_instance_key", "codex_model_provider"} {
		// 匹配迁移 SQL 中“旧值非空且新旧不同”的不可变触发条件。
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

// readRepoFile 从当前测试文件向上找到 go.mod 后读取相对路径。
// 这样测试不依赖 go test 的启动目录，也不会静默读错仓库外文件。
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
