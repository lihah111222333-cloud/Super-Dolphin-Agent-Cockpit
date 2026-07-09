package builtinprompts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var mainGeneralZhSectionKeys = []string{
	"identity",
	"worktree_hint",
	"system_constraints",
	"engineering",
	"actions",
	"tool_preferences",
	"lsp_basics",
	"style",
	"output_efficiency",
	"lsp_advanced",
	"recall_lsp_basics",
	"recall_lsp_advanced",
	"recall_sqlc_workflow",
	"recall_prompt_template_editing",
	"recall_frontend_react",
	"recall_migration_rules",
	"recall_guard_rules",
}

var mainGeneralZhSectionShapes = []sectionShape{
	{key: "identity", region: "static", ordinal: 0, triggerType: "always"},
	{key: "worktree_hint", region: "dynamic", ordinal: 10, triggerType: "always"},
	{key: "system_constraints", region: "static", ordinal: 20, triggerType: "always"},
	{key: "engineering", region: "static", ordinal: 30, triggerType: "always"},
	{key: "actions", region: "static", ordinal: 40, triggerType: "always"},
	{key: "tool_preferences", region: "static", ordinal: 50, triggerType: "always"},
	{key: "lsp_basics", region: "static", ordinal: 55, triggerType: "always"},
	{key: "style", region: "static", ordinal: 60, triggerType: "always"},
	{key: "output_efficiency", region: "static", ordinal: 70, triggerType: "always"},
	{key: "lsp_advanced", region: "dynamic", ordinal: 80, triggerType: "always"},
	{key: "recall_lsp_basics", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "lsp-basics"},
	{key: "recall_lsp_advanced", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "lsp-advanced"},
	{key: "recall_sqlc_workflow", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "sqlc-workflow"},
	{key: "recall_prompt_template_editing", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "prompt-template-editing"},
	{key: "recall_frontend_react", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "frontend-react"},
	{key: "recall_migration_rules", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "migration-rules"},
	{key: "recall_guard_rules", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "guard-rules"},
}

func TestDefaultRegistryLoadsEmbeddedMainDefault(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	template, ok := reg.GetTemplate("main/default")
	require.True(t, ok)
	require.Equal(t, int64(-100000), template.ID)
	require.Equal(t, "main/default", template.PromptKey)
	require.Equal(t, "base", template.Kind)
	require.Equal(t, "global", template.Scope)
	require.Contains(t, template.Tags, "builtin:system")

	sections := reg.SectionsByTemplateID(template.ID)
	requireSectionKeys(t, sections, "identity", "engineering_principles", "risky_actions", "tone_style", "orchestrator_launch_context", "orchestrator_report_context", "worktree_reminder", "zh_courtesy")
	for key, want := range map[string]string{"identity": "Super-Dolphin", "engineering_principles": "完成前必须验证", "risky_actions": "force push"} {
		require.Contains(t, sectionBodyByKey(sections, key), want)
	}
	launchBody := sectionBodyByKey(sections, "orchestrator_launch_context")
	reportBody := sectionBodyByKey(sections, "orchestrator_report_context")
	for _, want := range []string{"launch_agent", `context_mode="minimal"`, `context_mode="focused"`, "leaf worker"} {
		require.Contains(t, launchBody, want)
	}
	for _, want := range []string{"get_agent_report", "wait=true", "状态: success | blocked | failed"} {
		require.Contains(t, reportBody, want)
	}
	for _, oldName := range []string{"orchestration_launch_agent", "orchestration_get_agent_report"} {
		require.NotContains(t, launchBody, oldName)
		require.NotContains(t, reportBody, oldName)
	}

	orchestratorLaunch := requireSection(t, sections, "orchestrator_launch_context")
	require.Equal(t, "dynamic", orchestratorLaunch.Region)
	require.JSONEq(t, `{"enabled_tools_has":"launch_agent"}`, string(orchestratorLaunch.EnableWhen))
	orchestratorReport := requireSection(t, sections, "orchestrator_report_context")
	require.Equal(t, "dynamic", orchestratorReport.Region)
	require.JSONEq(t, `{"enabled_tools_has":"get_agent_report"}`, string(orchestratorReport.EnableWhen))

	for i, section := range sections {
		require.NotEqual(t, template.ID, section.ID)
		require.Equal(t, int64(-200000-i), section.ID)
		require.Equal(t, template.ID, section.TemplateID)
	}
}

func TestRegistryLoadsMainGeneralZhWithProductionParity(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	template, ok := reg.GetTemplate("main/general-zh")
	require.True(t, ok)
	require.Equal(t, "main/general-zh", template.PromptKey)
	require.Equal(t, "base", template.Kind)
	require.Equal(t, "main", template.AgentKey)
	require.Equal(t, "global", template.Scope)
	require.Equal(t, 160, template.Priority)
	require.Contains(t, template.Tags, "builtin:system")
	require.Contains(t, template.Tags, "zh")
	require.JSONEq(t, `{}`, string(template.MatchWhen))

	sections := reg.SectionsByTemplateID(template.ID)
	requireSectionKeys(t, sections, mainGeneralZhSectionKeys...)
	requireSectionShapes(t, sections, mainGeneralZhSectionShapes)

	body := strings.Join(sectionBodies(sections), "\n")
	require.Contains(t, body, "Super-Dolphin")
	require.Contains(t, body, "工具偏好")
	require.Contains(t, body, "LSP")
	require.Contains(t, body, "SQLC")
	require.Contains(t, body, "你不是 Claude / GPT / Codex 或任何底层模型产品")
	require.NotContains(t, body, "我是 Claude")
	require.NotContains(t, body, "You are Claude")
	require.NotContains(t, body, "You are Claude Code")
	require.Contains(t, body, "把用户原始要求拆成可核对清单")
	require.Contains(t, body, "Done / Deferred / Not covered")
	require.Contains(t, body, "未验证、测试失败或需求未覆盖")
	require.Contains(t, body, "定时任务")
	require.Contains(t, body, "main/dag_designer_zh")
	require.Contains(t, body, "agent_key")
	require.Contains(t, body, "command_ref")

	lspBasics := requireSection(t, sections, "lsp_basics")
	require.Contains(t, string(lspBasics.EnableWhen), "enabled_tools_has")
	require.NotContains(t, string(lspBasics.EnableWhen), "language")

	lspAdvanced := requireSection(t, sections, "lsp_advanced")
	require.Contains(t, string(lspAdvanced.EnableWhen), "enabled_tools_has")
	require.Contains(t, string(lspAdvanced.EnableWhen), "tags_has")
	require.NotContains(t, string(lspAdvanced.EnableWhen), "language")

	worktreeHint := requireSection(t, sections, "worktree_hint")
	require.JSONEq(t, `{"isWorktree": true}`, string(worktreeHint.EnableWhen))
}

func TestDefaultRegistryLoadsDeveloperExpertCards(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	cases := []struct {
		promptKey  string
		agentKey   string
		title      string
		workflow   string
		promptText string
	}{
		{
			promptKey: "main/git-ops",
			agentKey:  "git-ops",
			title:     "Git 操作专家",
			workflow:  "workflow:git",
			promptText: "Git 操作专家：基于 diff、log、冲突或提交上下文，" +
				"产出可验证的 git 操作建议；危险历史改写必须要求用户确认。",
		},
		{
			promptKey: "main/docs",
			agentKey:  "docs-writer",
			title:     "文档专家",
			workflow:  "workflow:documentation",
			promptText: "技术文档专家：基于代码、接口、变更和目标读者，" +
				"产出结构清楚、可维护的 README、API 文档、注释或 changelog 草稿。",
		},
	}
	for _, tc := range cases {
		template, ok := reg.GetTemplate(tc.promptKey)
		require.True(t, ok, tc.promptKey)
		require.Equal(t, "expert", template.Kind)
		require.Equal(t, tc.title, template.Title)
		require.Equal(t, tc.agentKey, template.AgentKey)
		require.Equal(t, "global", template.Scope)
		require.Equal(t, 20, template.Priority)
		require.Equal(t, tc.promptText, template.PromptText)
		require.NotEmpty(t, template.WhenToUse)
		require.NotEmpty(t, template.Description)
		require.Contains(t, template.Tags, "builtin:system")
		require.Contains(t, template.Tags, "intent:expert")
		require.Contains(t, template.Tags, "domain:developer")
		require.Contains(t, template.Tags, tc.workflow)

		sections := reg.SectionsByTemplateID(template.ID)
		requireSectionKeys(t, sections, "identity")
		require.Equal(t, tc.promptText, sectionBodyByKey(sections, "identity"))
		requireNoExternalIdentityClaims(t, tc.promptKey, tc.promptText)
		requireNoExternalToolProtocols(t, tc.promptKey, tc.promptText)
		requireNoHostAssumptions(t, tc.promptKey, tc.promptText)
	}
}

func TestRegistryLoadsDAGDesignerPrompts(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	zh, ok := reg.GetTemplate("main/dag_designer_zh")
	require.True(t, ok)
	require.Equal(t, int64(-100002), zh.ID)
	require.Equal(t, "dag_designer", zh.AgentKey)
	require.True(t, zh.Enabled)
	require.Equal(t, "global", zh.Scope)
	require.Contains(t, zh.Tags, "builtin:system")
	require.Contains(t, zh.Tags, "intent:dag_designer")

	en, ok := reg.GetTemplate("main/dag_designer_en")
	require.True(t, ok)
	require.Equal(t, int64(-100003), en.ID)
	require.Equal(t, "dag_designer", en.AgentKey)
	require.False(t, en.Enabled)

	sections := reg.SectionsByTemplateID(zh.ID)
	requireSectionKeys(t, sections, "dag_designer_runtime_tools")
	body := sectionBodyByKey(sections, "dag_designer_runtime_tools")
	for _, want := range []string{
		"list_models()",
		"prompt_list(keyword?)",
		"command_list(keyword?)", "shared_file_list(prefix?)",
		"workflow_template_list", "workflow_template_get", "workflow_template_render_dag", "task_create_dag", "task_get_dag",
		"node.config.exec", "assigned_to", "waiting_for_assignee", "final_output",
		"provider-native Skill",
	} {
		require.Contains(t, body, want)
	}
	require.JSONEq(t, `{"enabled_tools_all":["list_models","prompt_list","command_list","shared_file_list","workflow_template_list","workflow_template_get","workflow_template_render_dag","task_create_dag","task_get_dag","task_get_run","task_list_runs","task_dag_apply_ops","task_dispatch_node","task_start_dag"]}`, string(requireSection(t, sections, "dag_designer_runtime_tools").EnableWhen))

	enSections := reg.SectionsByTemplateID(en.ID)
	enBody := sectionBodyByKey(enSections, "dag_designer_runtime_tools")
	require.Contains(t, enBody, "provider-native Skill")
}

func TestRegistryCorePromptsAvoidExternalIdentityToolAndHostLeaks(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	for _, promptKey := range []string{"main/default", "main/general-zh"} {
		template, ok := reg.GetTemplate(promptKey)
		require.True(t, ok)
		body := strings.Join(sectionBodies(reg.SectionsByTemplateID(template.ID)), "\n")
		requireNoExternalIdentityClaims(t, promptKey, body)
		requireNoExternalToolProtocols(t, promptKey, body)
		requireNoHostAssumptions(t, promptKey, body)
		require.Contains(t, body, "高危", promptKey)
		require.Contains(t, body, "验证", promptKey)
	}
}

func TestMainGeneralZhLSPSectionsUseLatestShortContract(t *testing.T) {
	t.Parallel()

	reg, err := NewDefaultRegistry()
	require.NoError(t, err)

	template, ok := reg.GetTemplate("main/general-zh")
	require.True(t, ok)
	sections := reg.SectionsByTemplateID(template.ID)

	lspBasics := sectionBodyByKey(sections, "lsp_basics")
	recallLSPBasics := sectionBodyByKey(sections, "recall_lsp_basics")
	lspAdvanced := sectionBodyByKey(sections, "lsp_advanced")
	recallLSPAdvanced := sectionBodyByKey(sections, "recall_lsp_advanced")
	require.Contains(t, string(requireSection(t, sections, "lsp_advanced").EnableWhen), "tags_has")

	lspBodies := map[string]string{
		"lsp_basics":          lspBasics,
		"lsp_advanced":        lspAdvanced,
		"recall_lsp_basics":   recallLSPBasics,
		"recall_lsp_advanced": recallLSPAdvanced,
	}
	for name, body := range lspBodies {
		require.LessOrEqual(t, nonBlankLineCount(body), 42, name)
		require.Contains(t, body, "当前契约", name)
		require.Contains(t, body, "每个任务至少组合 4 种 LSP 工具", name)
		require.Contains(t, body, "不要只用 `grep + file`", name)
		require.Contains(t, body, "`grep`、`file`、`structure`、`inspect`、`xref`、`patch_edit`、`completion`", name)
		require.Contains(t, body, "`grep(ast_search)`", name)
		require.Contains(t, body, "`file(read_file)`", name)
		require.Contains(t, body, "file(action=read_file, pos=<file>:<func_start>", name)
		require.Contains(t, body, "`scope=lines`", name)
		require.Contains(t, body, "`work_dir`", name)
		require.Contains(t, body, "`max_results`", name)
		require.Contains(t, body, "`exec_command`", name)
	}

	body := strings.Join([]string{
		lspBasics,
		lspAdvanced,
		recallLSPBasics,
		recallLSPAdvanced,
	}, "\n")
	for _, stale := range []string{
		"11 个仓库感知工具",
		"7 个仓库感知 LSP 工具",
		"verbosity",
		"offset=",
		"line/end_line",
		"new_text",
		"persist_to_disk",
		"force",
		"replace_range",
		"code_action",
		"folding_range",
		"semantic_tokens",
		"`patch_edit(rename)",
		"`patch_edit(format)",
	} {
		require.NotContains(t, body, stale)
	}
}

func TestExternalToolAndHostLeakGuardsAllowNegativeBoundaryText(t *testing.T) {
	t.Parallel()

	requireNoExternalToolProtocols(t, "negative-fixture", "不要假设 WebFetch、run_command 或 read_files 可用。")
	requireNoHostAssumptions(t, "negative-fixture", "不要假设可见屏幕、terminal-only、no-browser、IDE sidebar 或浏览器扩展可用。")
}

func TestLoadRegistryAssignsStableNegativeTemplateIDsByPromptKey(t *testing.T) {
	t.Parallel()

	reg, err := LoadRegistryFromFS(testFS{
		"manifest.json":    `{"version":1,"templates":["templates/z.json","templates/a.json"]}`,
		"templates/z.json": minimalTemplateJSON("main/z", "sections/z.md"),
		"templates/a.json": minimalTemplateJSON("main/a", "sections/a.md"),
		"sections/z.md":    `z body`,
		"sections/a.md":    `a body`,
	})
	require.NoError(t, err)

	a, ok := reg.GetTemplate("main/a")
	require.True(t, ok)
	require.Equal(t, int64(-100000), a.ID)
	z, ok := reg.GetTemplate("main/z")
	require.True(t, ok)
	require.Equal(t, int64(-100001), z.ID)
}

func TestLoadRegistryRejectsDuplicateResolvedTemplateIDs(t *testing.T) {
	t.Parallel()

	_, err := LoadRegistryFromFS(testFS{
		"manifest.json":    `{"version":1,"templates":["templates/a.json","templates/b.json"]}`,
		"templates/a.json": minimalTemplateJSONWithID("main/a", -100010, "sections/a.md"),
		"templates/b.json": minimalTemplateJSONWithID("main/b", -100010, "sections/b.md"),
		"sections/a.md":    `a body`,
		"sections/b.md":    `b body`,
	})
	require.ErrorContains(t, err, `duplicate resolved template id -100010`)
}

func TestRegistryRejectsExternalProviderIdentity(t *testing.T) {
	t.Parallel()

	_, err := LoadRegistryFromFS(testFS{
		"manifest.json": `{"version":1,"templates":["templates/bad.json"]}`,
		"templates/bad.json": `{
			"prompt_key":"main/bad",
			"kind":"base",
			"title":"Bad",
			"agent_key":"main",
			"enabled":true,
			"scope":"global",
			"tags":["builtin:system"],
			"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":"sections/bad.md"}]
		}`,
		"sections/bad.md": `You are Claude Code.`,
	})
	require.ErrorContains(t, err, "external provider identity")
}

func TestRegistryRejectsExternalProviderIdentityAfterUnrelatedDoNot(t *testing.T) {
	t.Parallel()

	_, err := LoadRegistryFromFS(testFS{
		"manifest.json": `{"version":1,"templates":["templates/bad.json"]}`,
		"templates/bad.json": `{
			"prompt_key":"main/bad",
			"kind":"base",
			"title":"Bad",
			"agent_key":"main",
			"enabled":true,
			"scope":"global",
			"tags":["builtin:system"],
			"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":"sections/bad.md"}]
		}`,
		"sections/bad.md": `Do not leak. You are Claude Code.`,
	})
	require.ErrorContains(t, err, "external provider identity")
}

func TestRegistryRejectsExternalProviderIdentityAfterDistantUnrelatedDoNot(t *testing.T) {
	t.Parallel()

	_, err := LoadRegistryFromFS(testFS{
		"manifest.json": `{"version":1,"templates":["templates/bad.json"]}`,
		"templates/bad.json": `{
			"prompt_key":"main/bad",
			"kind":"base",
			"title":"Bad",
			"agent_key":"main",
			"enabled":true,
			"scope":"global",
			"tags":["builtin:system"],
			"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":"sections/bad.md"}]
		}`,
		"sections/bad.md": `Do not mention internal details. You are Claude Code.`,
	})
	require.ErrorContains(t, err, "external provider identity")
}

func TestRegistryAllowsNegativeExternalProviderIdentityWarning(t *testing.T) {
	t.Parallel()

	_, err := LoadRegistryFromFS(testFS{
		"manifest.json": `{"version":1,"templates":["templates/ok.json"]}`,
		"templates/ok.json": `{
			"prompt_key":"main/ok",
			"kind":"base",
			"title":"OK",
			"agent_key":"main",
			"enabled":true,
			"scope":"global",
			"tags":["builtin:system"],
			"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":"sections/ok.md"}]
		}`,
		"sections/ok.md": `你不是 Claude、Claude Code 或任何 Anthropic 产品。你是 Super-Dolphin。`,
	})
	require.NoError(t, err)
}

func TestRegistryAllowsDirectEnglishExternalProviderIdentityWarning(t *testing.T) {
	t.Parallel()

	_, err := LoadRegistryFromFS(testFS{
		"manifest.json": `{"version":1,"templates":["templates/ok.json"]}`,
		"templates/ok.json": `{
			"prompt_key":"main/ok",
			"kind":"base",
			"title":"OK",
			"agent_key":"main",
			"enabled":true,
			"scope":"global",
			"tags":["builtin:system"],
			"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":"sections/ok.md"}]
		}`,
		"sections/ok.md": `Never say you are Claude Code.`,
	})
	require.NoError(t, err)
}

type testFS map[string]string

func (fs testFS) ReadFile(name string) ([]byte, error) {
	body, ok := fs[name]
	if !ok {
		return nil, fmt.Errorf("missing file %s", name)
	}
	return []byte(body), nil
}

func minimalTemplateJSON(promptKey, bodyFile string) string {
	return fmt.Sprintf(`{
		"prompt_key":%q,
		"kind":"base",
		"title":"Test",
		"agent_key":"main",
		"enabled":true,
		"scope":"global",
		"tags":["builtin:system"],
		"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":%q}]
	}`, promptKey, bodyFile)
}

func minimalTemplateJSONWithID(promptKey string, id int64, bodyFile string) string {
	return fmt.Sprintf(`{
		"prompt_key":%q,
		"id":%d,
		"kind":"base",
		"title":"Test",
		"agent_key":"main",
		"enabled":true,
		"scope":"global",
		"tags":["builtin:system"],
		"sections":[{"section_key":"identity","region":"static","ordinal":0,"trigger_type":"always","body_file":%q}]
	}`, promptKey, id, bodyFile)
}

func requireSectionKeys(t *testing.T, sections []contract.BuiltinPromptSection, keys ...string) {
	t.Helper()

	got := make([]string, 0, len(sections))
	for _, section := range sections {
		got = append(got, section.SectionKey)
	}
	require.Equal(t, keys, got)
}

type sectionShape struct {
	key         string
	region      string
	ordinal     int
	triggerType string
	recallTopic string
}

func requireSectionShapes(t *testing.T, sections []contract.BuiltinPromptSection, expected []sectionShape) {
	t.Helper()

	for _, shape := range expected {
		section := requireSection(t, sections, shape.key)
		require.Equal(t, shape.region, section.Region, "section %s region", shape.key)
		require.Equal(t, shape.ordinal, section.Ordinal, "section %s ordinal", shape.key)
		require.Equal(t, shape.triggerType, section.TriggerType, "section %s trigger_type", shape.key)
		require.Equal(t, shape.recallTopic, section.RecallTopic, "section %s recall_topic", shape.key)
	}
}

func sectionBodyByKey(sections []contract.BuiltinPromptSection, key string) string {
	for _, section := range sections {
		if section.SectionKey == key {
			return section.Body
		}
	}
	return strings.Join(sectionBodies(sections), "\n")
}

func requireSection(t *testing.T, sections []contract.BuiltinPromptSection, key string) contract.BuiltinPromptSection {
	t.Helper()

	for _, section := range sections {
		if section.SectionKey == key {
			return section
		}
	}
	require.Failf(t, "missing section", "missing section %q", key)
	return contract.BuiltinPromptSection{}
}

func sectionBodies(sections []contract.BuiltinPromptSection) []string {
	bodies := make([]string, 0, len(sections))
	for _, section := range sections {
		bodies = append(bodies, section.Body)
	}
	return bodies
}

func requireNoExternalIdentityClaims(t *testing.T, promptKey, body string) {
	t.Helper()

	for _, phrase := range []string{
		"我是 Claude",
		"我是 Codex",
		"我是 GPT",
		"我是 Cursor",
		"我是 Kiro",
		"我是 Warp",
		"我是 GitHub Copilot",
		"我是 Traycer",
		"我是 Cluely",
		"You are Claude.",
		"You are Claude Code.",
		"You are Codex.",
		"You are GPT.",
		"You are Cursor.",
		"You are Kiro.",
		"You are Warp.",
		"You are GitHub Copilot.",
		"You are Traycer.",
		"You are Cluely.",
		"I am Claude",
		"I am Codex",
		"provided by Anthropic",
		"provided by OpenAI",
		"由 Anthropic",
		"由 OpenAI",
	} {
		require.NotContains(t, body, phrase, promptKey)
	}
}

func requireNoExternalToolProtocols(t *testing.T, promptKey, body string) {
	t.Helper()

	for _, phrase := range []string{
		"可以通过 WebFetch",
		"调用 WebFetch",
		"使用 WebFetch",
		"WebFetch 工具可用",
		"WebFetch tool is available",
		"run_command(",
		"read_files(",
		"run_command tool is available",
		"read_files tool is available",
		"IDE-only approval",
		"external approval schema",
	} {
		require.NotContains(t, body, phrase, promptKey)
	}
}

func requireNoHostAssumptions(t *testing.T, promptKey, body string) {
	t.Helper()

	for _, phrase := range []string{
		"屏幕可见",
		"可以看到屏幕",
		"可以听到音频",
		"音频可见",
		"must be terminal-only",
		"must use no-browser",
		"IDE sidebar is available",
		"browser extension is available",
		"浏览器扩展已启用",
		"浏览器扩展已经可用",
		"可以使用浏览器扩展",
	} {
		require.NotContains(t, body, phrase, promptKey)
	}
}

func nonBlankLineCount(body string) int {
	count := 0
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
