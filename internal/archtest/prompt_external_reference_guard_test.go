package archtest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/builtinprompts"
)

func TestActiveSystemOwnedPromptSurfacesAvoidExternalIdentityToolAndHostLeaks(t *testing.T) {
	reg, err := builtinprompts.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("load builtin prompt registry: %v", err)
	}

	surfaces := builtinRegistryPromptSurfaces(t, reg)

	failIfViolations(t, promptExternalReferenceViolations(surfaces))
}

func TestPromptExternalReferenceGuardRejectsIdentityClaims(t *testing.T) {
	t.Parallel()

	if got := promptExternalReferenceViolations([]promptSurface{
		{
			Source: "fixture:identity",
			Text:   "You are Claude Code.",
		},
	}); len(got) == 0 {
		t.Fatal("expected external provider identity claim to be rejected")
	}
}

func TestPromptExternalReferenceGuardRejectsExternalToolAndHostAssumptions(t *testing.T) {
	t.Parallel()

	surfaces := []promptSurface{
		{Source: "fixture:tool", Text: "Use WebFetch for current web pages."},
		{Source: "fixture:host", Text: "The IDE sidebar is available for every task."},
	}
	got := promptExternalReferenceViolations(surfaces)
	if len(got) != len(surfaces) {
		t.Fatalf("expected tool and host assumptions to be rejected, got %d: %v", len(got), got)
	}
}

func TestPromptExternalReferenceGuardAllowsNegativeProviderContextAndCleanup(t *testing.T) {
	t.Parallel()

	surfaces := []promptSurface{
		{
			Source: "fixture:negative-boundary",
			Text:   "你不是 Claude / GPT / Codex 或任何底层模型产品。Never say you are Claude.",
		},
		{
			Source: "fixture:provider-context",
			Text:   "不要在 Codex 执行任务时直接 apply 生产/本地 DB migration，除非用户明确要求。",
		},
		{
			Source: "fixture:provider-field",
			Text:   "DAG node config provider values: claude | codex.",
		},
		{
			Source: "fixture:negative-tool-host-boundary",
			Text:   "不要假设 WebFetch、run_command、read_files、IDE sidebar 或浏览器扩展可用。",
		},
		{
			Source:         "fixture:disabled legacy cleanup",
			Text:           "('main/claude-style', 'Legacy Claude-style Prompt (disabled)', 'main')",
			CleanupContext: true,
		},
	}
	if got := promptExternalReferenceViolations(surfaces); len(got) != 0 {
		t.Fatalf("expected negative/provider cleanup contexts to pass, got: %v", got)
	}

	if got := promptExternalReferenceViolations([]promptSurface{
		{
			Source:         "fixture:unsafe legacy cleanup",
			Text:           "('main/claude-style', 'You are Claude Code.', 'main')",
			CleanupContext: true,
		},
	}); len(got) == 0 {
		t.Fatal("expected cleanup context to reject restored active identity claims")
	}
}

func TestPromptExternalReferenceGuardAllowsInternalToolsOnlyWhenGated(t *testing.T) {
	t.Parallel()

	ok := []promptSurface{
		{
			Source:         "main/default:orchestrator_launch_context.body",
			Text:           "可以通过 launch_agent 派生专家子 agent。",
			InternalTools:  []string{"launch_agent"},
			RuntimeGated:   true,
			AllowedToolUse: true,
		},
		{
			Source:         "main/dag_designer_zh:body",
			Text:           "先用 prompt_list、command_list、shared_file_list 和 task_create_dag 建模。",
			InternalTools:  []string{"prompt_list", "command_list", "shared_file_list", "task_create_dag"},
			RuntimeGated:   true,
			AllowedToolUse: true,
		},
	}
	if got := promptExternalReferenceViolations(ok); len(got) != 0 {
		t.Fatalf("expected gated internal tools to pass, got: %v", got)
	}

	bad := []promptSurface{
		{
			Source:        "main/default:static.body",
			Text:          "始终使用 prompt_list 和 command_list 获取上下文。",
			InternalTools: []string{"prompt_list", "command_list"},
			RuntimeGated:  false,
		},
	}
	if got := promptExternalReferenceViolations(bad); len(got) == 0 {
		t.Fatal("expected ungated resident internal tool assumption to be rejected")
	}
}

func TestPromptExternalReferenceGuardRejectsPostCutoverNonExactMigrationToolLiterals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		literal string
	}{
		{
			name:    "dag designer marker",
			literal: "DAG designer should call list_models before writing node config.",
		},
		{
			name:    "orchestrator marker",
			literal: "Orchestrator should call prompt_list before routing work.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			surface := promptSurface{
				Source:         "migration:0106_prompt_template_runtime_metadata.sql.literal[fixture]",
				Text:           tc.literal,
				AllowedToolUse: allowedInternalToolUseInMigrationLiteral("0106_prompt_template_runtime_metadata.sql", tc.literal),
				InternalTools:  internalToolsIn(tc.literal),
			}
			if got := promptExternalReferenceViolations([]promptSurface{surface}); len(got) == 0 {
				t.Fatal("expected non-exact post-cutover migration literal with internal tools to be rejected")
			}
		})
	}
}

func TestPromptExternalReferenceGuardRejectsPostCutoverCleanupMigrationRuntimeToolLiterals(t *testing.T) {
	t.Parallel()

	sql := `
DELETE FROM public.prompt_templates WHERE prompt_key = 'main/retired';
UPDATE public.prompt_templates
SET when_to_use = 'Call prompt_list before routing work.'
WHERE prompt_key = 'main/orchestrator';
`
	surfaces := postRegistryMigrationPromptSurfaces("0108_cleanup_and_runtime.sql", sql)
	if got := promptExternalReferenceViolations(surfaces); len(got) == 0 {
		t.Fatal("expected cleanup migration runtime literal with internal tools to be rejected")
	}
}

type promptSurface struct {
	Source         string
	Text           string
	CleanupContext bool
	RuntimeGated   bool
	AllowedToolUse bool
	InternalTools  []string
}

func builtinRegistryPromptSurfaces(t *testing.T, reg contract.BuiltinPromptRegistry) []promptSurface {
	t.Helper()

	var surfaces []promptSurface
	for _, tmpl := range reg.ListTemplates() {
		if !tmpl.Enabled || !isRuntimeSystemPrompt(tmpl) {
			continue
		}
		templateTexts := map[string]string{
			"prompt_text":   tmpl.PromptText,
			"when_to_use":   tmpl.WhenToUse,
			"description":   tmpl.Description,
			"title":         tmpl.Title,
			"scope":         tmpl.Scope,
			"tags":          strings.Join(tmpl.Tags, " "),
			"match_when":    string(tmpl.MatchWhen),
			"agent_key":     tmpl.AgentKey,
			"tool_name":     tmpl.ToolName,
			"prompt_key":    tmpl.PromptKey,
			"template_kind": tmpl.Kind,
		}
		for field, text := range templateTexts {
			surfaces = appendPromptSurface(surfaces, promptSurface{
				Source: fmt.Sprintf("builtin:%s.%s", tmpl.PromptKey, field),
				Text:   text,
			})
		}

		for _, section := range reg.SectionsByTemplateID(tmpl.ID) {
			if !section.Enabled {
				continue
			}
			runtimeGated := hasEnableWhen(section.EnableWhen)
			allowedInternalTools := runtimeGated && (strings.Contains(section.SectionKey, "orchestrator") ||
				strings.Contains(section.SectionKey, "dag_designer"))
			source := fmt.Sprintf("builtin:%s.section.%s", tmpl.PromptKey, section.SectionKey)
			surfaces = appendPromptSurface(surfaces, promptSurface{
				Source:         source + ".body",
				Text:           section.Body,
				RuntimeGated:   runtimeGated,
				AllowedToolUse: allowedInternalTools,
				InternalTools:  internalToolsIn(section.Body),
			})
			surfaces = appendPromptSurface(surfaces, promptSurface{
				Source:         source + ".enable_when",
				Text:           string(section.EnableWhen),
				RuntimeGated:   runtimeGated,
				AllowedToolUse: true,
				InternalTools:  internalToolsIn(string(section.EnableWhen)),
			})
			surfaces = appendPromptSurface(surfaces, promptSurface{
				Source: source + ".recall_topic",
				Text:   section.RecallTopic,
			})
		}
	}
	return surfaces
}

func postRegistryMigrationPromptSurfaces(name, sql string) []promptSurface {
	var surfaces []promptSurface
	if isPromptCleanupMigration(name, sql) {
		surfaces = appendPromptSurface(surfaces, promptSurface{
			Source:         "migration:" + name,
			Text:           sql,
			CleanupContext: true,
		})
		if !writesPromptRuntimeSurface(sql) {
			return surfaces
		}
	} else if !writesPromptRuntimeSurface(sql) {
		return surfaces
	}

	for i, literal := range sqlStringLiterals(sql) {
		surfaces = appendPromptSurface(surfaces, promptSurface{
			Source:         fmt.Sprintf("migration:%s.literal[%d]", name, i),
			Text:           literal,
			AllowedToolUse: allowedInternalToolUseInMigrationLiteral(name, literal),
			InternalTools:  internalToolsIn(literal),
		})
	}
	return surfaces
}
