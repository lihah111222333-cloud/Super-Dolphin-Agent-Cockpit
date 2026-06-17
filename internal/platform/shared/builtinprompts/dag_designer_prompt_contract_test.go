package builtinprompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGDesignerPromptContractCoversRuntimeRecoveryAndSchedule(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	template, ok := reg.GetTemplate("main/dag_designer_zh")
	require.True(t, ok)

	sections := reg.SectionsByTemplateID(template.ID)
	section := requireSection(t, sections, "dag_designer_runtime_tools")
	body := sectionBodyByKey(sections, "dag_designer_runtime_tools")

	for _, want := range []string{
		"task_dispatch_node",
		"task_get_run",
		"task_list_runs",
		"node.config.exec",
		"outputs.to_sharedfile",
		"outputs.to_node_result",
		"first_turn",
		"assigned_to",
		`{"op":"add_node","node":{"node_key":"final","title":"最终输出","node_type":"agent","assigned_to"`,
		"waiting_for_assignee",
		"CRON_TZ=Asia/Shanghai",
		"裸 cron 默认 UTC",
		"runtime append",
		"政企工作流模板库",
		"阶段评估",
		"template_id",
		"template_version",
		"ui_schema",
		"dag_template",
		"workflow_template_list",
		"workflow_template_get",
		"workflow_template_render_dag",
		"宣传视频",
		"日报/周报",
		"项目汇报",
		"会议纪要",
		"数据分析简报",
		"审批材料",
		"reports/workflows/{{dag_key}}/{{run_id}}/",
		"dag/{{dag_key}}/{{run_id}}/",
		"config.ui",
		"operation_summary",
		"execution_mode",
		"expected_outputs",
		"md、json、pdf、docx、xlsx、pptx、mp4",
		"command_card",
		"复核节点只生成审批/审稿/口径复核材料",
		"review_node",
		"final_node_key",
		"video_with_audio",
		"outputs.to_artifact",
	} {
		require.Contains(t, body, want)
	}

	require.JSONEq(t, `{"enabled_tools_all":["list_models","prompt_list","command_list","shared_file_list","workflow_template_list","workflow_template_get","workflow_template_render_dag","task_create_dag","task_get_dag","task_get_run","task_list_runs","task_dag_apply_ops","task_dispatch_node","task_start_dag"]}`, string(section.EnableWhen))
	for _, legacy := range []string{`"output_file"`, `"config":{"provider"`, `"config": {"provider"`, "task_update_node"} {
		if strings.Contains(body, legacy) {
			t.Fatalf("DAG designer prompt must not teach legacy node config field or unavailable tool %s", legacy)
		}
	}
}
