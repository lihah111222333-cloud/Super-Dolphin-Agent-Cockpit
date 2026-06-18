package builtinprompts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultRegistryDocumentsFocusedContextGuidance(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)
	template, ok := reg.GetTemplate("main/default")
	require.True(t, ok)

	sections := reg.SectionsByTemplateID(template.ID)
	launchBody := sectionBodyByKey(sections, "orchestrator_launch_context")
	reportBody := sectionBodyByKey(sections, "orchestrator_report_context")

	for _, want := range []string{
		"背景", "已确认决策", "相关文件", "禁止事项", "返回格式", "已知风险",
		"文件路径、函数名、行号和约束",
		"不要大段粘贴代码",
		"不要复制整段父历史",
	} {
		require.Contains(t, launchBody, want)
	}
	for _, want := range []string{"launch_agent", `context_mode="focused"`, "get_agent_reports", "send_message(wait_report=true)"} {
		require.Contains(t, launchBody+"\n"+reportBody, want)
	}
	for _, field := range []string{"`files`", "`constraints`", "`return_format`"} {
		require.NotContains(t, launchBody, field)
		require.NotContains(t, reportBody, field)
	}
}
