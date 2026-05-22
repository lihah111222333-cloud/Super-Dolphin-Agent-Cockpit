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
	"recall_frontend_vue3",
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
	{key: "recall_frontend_vue3", region: "dynamic", ordinal: 0, triggerType: "recall", recallTopic: "frontend-vue3"},
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
	requireSectionKeys(t, sections,
		"identity",
		"engineering_principles",
		"risky_actions",
		"tone_style",
		"orchestrator_context",
		"worktree_reminder",
		"zh_courtesy",
	)
	require.Contains(t, sectionBodyByKey(sections, "identity"), "Super-Dolphin")
	require.Contains(t, sectionBodyByKey(sections, "engineering_principles"), "完成前必须验证")
	require.Contains(t, sectionBodyByKey(sections, "risky_actions"), "force push")
	require.Contains(t, sectionBodyByKey(sections, "orchestrator_context"), "orchestration_launch_agent")

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
