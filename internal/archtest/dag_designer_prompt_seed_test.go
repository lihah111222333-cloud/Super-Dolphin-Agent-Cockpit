package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDAGDesignerPromptSeed_ZHCoversCoreSurface 守住中文版 DAG 设计师 prompt seed，
// 确保关键工具表面和 schema 约束不在后续重构中被悄悄抽干。
//
// 测试策略：直接读种子 SQL 文件而不是查库 —— seed 一旦合并即代码事实，
// 任何对工具表面 (list_models / task_create_dag / task_dag_apply_ops 等) 或
// node_type schema 关键词的删除都会让本测试红，提醒维护者同步更新设计师 prompt。
func TestDAGDesignerPromptSeed_ZHCoversCoreSurface(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0084_seed_dag_designer_prompt_zh.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0084: %v", err)
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		t.Fatalf("migration 0084 is empty; F7.1 seed must contain a prompt body")
	}

	// 关键字段一：身份位 (prompt_key / agent_key / title 中文)。
	assertDAGDesignerPromptContainsAll(t, content, []string{
		"main/dag_designer_zh",                // prompt_key 唯一锚点
		"'dag_designer'",                      // agent_key
		"AI 流程设计师",                            // title 中文身份
		"ON CONFLICT (prompt_key) DO NOTHING", // 幂等护栏
	}, "migration 0084 missing identity marker %q")

	// 关键字段二：MCP 工具表面 —— 让审阅者一眼能看出设计师能调哪些工具。
	// 任意一个被悄悄删除都视为 prompt 退化。
	assertDAGDesignerPromptContainsAll(t, content, []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_dag_apply_ops",
		"task_get_dag",
		"task_get_run",
		"task_list_runs",
		"task_dispatch_node",
	}, "migration 0084 must reference MCP tool %q in prompt body")

	// 关键字段三：node_type typed schema 三种都要点名，避免 prompt 退化成旧版单形态节点。
	assertDAGDesignerPromptContainsAll(t, content, []string{
		`node_type = "agent"`,
		`node_type = "automation"`,
		`node_type = "hybrid"`,
	}, "migration 0084 must describe %s typed schema")

	// 关键字段四：运行时与输出约束关键词不能丢。
	assertDAGDesignerPromptContainsAll(t, content, []string{
		"base_version", // OCC 乐观锁
		"running",      // 动态可重写约束触发态
		"runtime append",
		"FailureClass", // 失败分类智能重试
		"4KB",          // size_cap / sharedfile 决策
		"scheduled",    // trigger 三态之一 (cron)
		"cron",         // cron 表达式语境
		"CRON_TZ=Asia/Shanghai",
		"裸 cron 默认 UTC",
		"final_node_key",
		"final_output",
	}, "migration 0084 must keep blueprint rule keyword %q")

	assertDAGDesignerPromptContainsAll(t, content, []string{
		"node.config.exec",
		"outputs.to_sharedfile",
		"outputs.to_node_result",
		"first_turn",
		"assigned_to",
		"waiting_for_assignee",
		`{"op":"add_node","node":{"node_key":"...","title":"...","node_type":"agent|automation|hybrid","assigned_to"`,
	}, "migration 0084 must keep executable schema keyword or add_node assigned_to example %q")

	assertDAGDesignerPromptNotContains(t, content, []string{
		`"output_file"`,
		`"config": {"provider"`,
		"task_update_node",
	}, "migration 0084 must not teach unavailable DAG designer field or tool %q")

	// 关键字段五：tags 含中文路由命中词 (router 选这条模板就靠它)。
	assertDAGDesignerPromptContainsAll(t, content, []string{
		"设计 DAG",
		"流程编排",
		"定时任务",
	}, "migration 0084 tags missing routing keyword %q")

	// 体量护栏：prompt 主体 < 200 行属合理；> 600 行就该警惕，可能塞了无关内容。
	// 上限只是软警告 (Logf)，避免无谓阻塞。
	lines := strings.Count(content, "\n")
	if lines < 60 {
		t.Errorf("migration 0084 only %d lines; prompt body looks suspiciously short", lines)
	}
	if lines > 600 {
		t.Logf("migration 0084 has %d lines; consider whether prompt body grew bloat", lines)
	}
}

func assertDAGDesignerPromptContainsAll(t *testing.T, content string, values []string, format string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(content, value) {
			t.Errorf(format, value)
		}
	}
}

func assertDAGDesignerPromptNotContains(t *testing.T, content string, values []string, format string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(content, value) {
			t.Errorf(format, value)
		}
	}
}
