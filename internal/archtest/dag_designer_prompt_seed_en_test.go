package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDAGDesignerPromptSeed_ENCoversCoreSurface 保护英文 DAG designer seed 的核心工具面。
// 测试直接读取迁移 SQL 文件，因为 seed 文件是当前事实来源；删除工具、章节锚点
// 或 node_type schema 关键字时必须显式更新断言。
func TestDAGDesignerPromptSeed_ENCoversCoreSurface(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0085_seed_dag_designer_prompt_en.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0085: %v", err)
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		t.Fatalf("migration 0085 is empty; designer seed must contain a prompt body")
	}

	assertDAGDesignerPromptIdentity(t, content)
	assertDAGDesignerPromptToolSurface(t, content)
	assertDAGDesignerPromptSections(t, content)
	assertDAGDesignerPromptTypedSchemas(t, content)
	assertDAGDesignerPromptBlueprintRules(t, content)
	assertDAGDesignerPromptRoutingTags(t, content)
	assertDAGDesignerPromptSize(t, content)
}

func assertDAGDesignerPromptIdentity(t *testing.T, content string) {
	t.Helper()
	// 身份标记覆盖 prompt_key、agent_key、英文标题和幂等写入保护。
	assertContainsAll(t, content, "missing identity marker", []string{
		"main/dag_designer_en",
		"'dag_designer'",
		"AI Flow Designer (English)",
		"ON CONFLICT (prompt_key) DO NOTHING",
	})
}

func assertDAGDesignerPromptToolSurface(t *testing.T, content string) {
	t.Helper()
	// MCP 工具面必须显式列出，避免重构时静默削弱 designer 可调用能力。
	assertContainsAll(t, content, "must reference MCP tool", []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_dag_apply_ops",
		"task_update_node",
		"task_get_dag",
		"task_get_run",
		"task_list_runs",
	})
}

func assertDAGDesignerPromptSections(t *testing.T, content string) {
	t.Helper()
	// 英文章节锚点保证 prompt 仍保留可执行的工作结构。
	assertContainsAll(t, content, "must keep English section anchor", []string{
		"# Your Work Loop",
		"# Available MCP Tools (mcp-orch)",
		"## Resource Discovery",
		"## DAG Writes",
		"## DAG Reads",
		"# Node Typed Schema",
		"# Blueprint v2 Guardrails",
		"# Example Conversation",
		"# Style",
	})
}

func assertDAGDesignerPromptTypedSchemas(t *testing.T, content string) {
	t.Helper()
	// 三类 node_type schema 是 DAG 节点建模入口，缺一类都会破坏生成边界。
	assertContainsAll(t, content, "must describe typed schema", []string{
		`node_type = "agent"`,
		`node_type = "automation"`,
		`node_type = "hybrid"`,
	})
}

func assertDAGDesignerPromptBlueprintRules(t *testing.T, content string) {
	t.Helper()
	// 这些约束让英文 prompt 保留并发写入、重试分类和调度输出边界。
	assertContainsAll(t, content, "must keep blueprint rule keyword", []string{
		"base_version", // 乐观锁字段
		"running",      // 动态改写受限状态
		"FailureClass", // 重试分类字段
		"ErrVersionConflict",
		"4KB",       // 小内容与 sharedfile 分流阈值
		"scheduled", // 触发模式
		"cron",      // cron 表达式上下文
		"inputs.summarization",
		"final_node_key",
		"final_output",
	})
}

func assertDAGDesignerPromptRoutingTags(t *testing.T, content string) {
	t.Helper()
	// 路由 tags 需要和 seed 字段保持一致，便于按中文意图发现。
	assertContainsAll(t, content, "tags missing routing keyword", []string{
		"设计 DAG",
		"流程编排",
		"定时任务",
	})

	// 英文路由 tags 保证英文 seed 也能被直接检索到。
	assertContainsAll(t, content, "tags missing English routing keyword", []string{
		"AI design flow",
		"schedule task",
		"cron expression",
	})
}

func assertDAGDesignerPromptSize(t *testing.T, content string) {
	t.Helper()
	// 大小保护用于发现 seed 被误删；超过 600 行只记日志，避免阻断有意扩展。
	lines := strings.Count(content, "\n")
	if lines < 60 {
		t.Errorf("migration 0085 only %d lines; prompt body looks suspiciously short", lines)
	}
	if lines > 600 {
		t.Logf("migration 0085 has %d lines; consider whether prompt body grew bloat", lines)
	}
}

func assertContainsAll(t *testing.T, content, failure string, values []string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(content, value) {
			t.Errorf("migration 0085 %s %q", failure, value)
		}
	}
}
