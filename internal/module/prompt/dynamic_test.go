package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterDynamicProviderMakesSlotRenderable(t *testing.T) {
	svc := NewService(&Config{}, nil)
	want := len(StaticSections()) + len(DynamicSlotNames())
	if len(svc.Sections()) != want {
		t.Fatalf("len(Sections()) = %d, want %d", len(svc.Sections()), want)
	}
	provider := DynamicTextProvider{
		Name: DynamicSectionLanguage,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			text := "Always respond in Chinese."
			return &text, nil
		},
	}
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{Language: "Chinese"})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	language, ok := resolvedSectionContent(assembly.ResolvedSections, DynamicSectionLanguage)
	if !ok || !strings.Contains(language, "Always respond in Chinese.") {
		t.Fatalf("ResolvedSections = %#v, want registered dynamic text", assembly.ResolvedSections)
	}
}

func TestUnregisterDynamicProviderRemovesRenderedContent(t *testing.T) {
	svc := NewService(&Config{}, nil)
	provider := DynamicTextProvider{
		Name: DynamicSectionSessionGuidance,
		ResolveFunc: func(context.Context, SectionContext) (*string, error) {
			text := "Use the current skill card."
			return &text, nil
		},
	}
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}
	if !svc.UnregisterDynamicProvider(DynamicSectionSessionGuidance) {
		t.Fatal("UnregisterDynamicProvider() = false, want true")
	}

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(assembly.UserContextText, "Use the current skill card.") {
		t.Fatalf("UserContextText = %q, want provider content removed", assembly.UserContextText)
	}
}

type sessionGuidanceCase struct {
	name         string
	enabledTools []string
	flags        map[string]bool
	want         []string
	absent       []string
	wantNil      bool
}

func TestSessionGuidanceProviderResolveBranchMatrix(t *testing.T) {
	for _, tc := range sessionGuidanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			text := mustSessionGuidanceText(t, tc.enabledTools, tc.flags)
			if tc.wantNil {
				if text != nil {
					t.Fatalf("Resolve() = %q, want nil", *text)
				}
				return
			}
			if text == nil {
				t.Fatal("Resolve() = nil, want content")
			}
			for _, check := range tc.want {
				if !strings.Contains(*text, check) {
					t.Fatalf("Resolve() = %q, want substring %q", *text, check)
				}
			}
			for _, check := range tc.absent {
				if strings.Contains(*text, check) {
					t.Fatalf("Resolve() = %q, want substring %q to be absent", *text, check)
				}
			}
		})
	}
}

func sessionGuidanceCases() []sessionGuidanceCase {
	return []sessionGuidanceCase{
		{name: "headless_without_other_branches_returns_nil", enabledTools: []string{"file"}, flags: map[string]bool{"non_interactive": true}, wantNil: true},
		{name: "interactive_session_shows_login_reminder", enabledTools: []string{"file"}, want: []string{"# Session-specific guidance", "! <command>", "gcloud auth login"}, absent: []string{"Verification protocol", "/<skill-name>"}},
		{
			name:         "traditional_agent_skills_discover_and_verification",
			enabledTools: []string{"request_user_input", "spawn_agent", "grep", "file"},
			flags:        map[string]bool{"explore_agent_enabled": true, "user_invocable_skills": true, "discover_skills_enabled": true, "verification_required": true},
			want:         []string{"request_user_input", "! <command>", "Use `spawn_agent` only for well-scoped parallel subtasks.", "explore-oriented `spawn_agent` subtask", "`grep` and `file`", "/<skill-name>", "discovery flow", "Verification protocol", "3+ file edits", "`PASS`, `FAIL`, or `PARTIAL`"},
		},
		{
			name:         "fork_mode_suppresses_explore_guidance",
			enabledTools: []string{"spawn_agent"},
			flags:        map[string]bool{"fork_subagent": true, "explore_agent_enabled": true},
			want:         []string{"fork-style delegation"},
			absent:       []string{"explore-oriented `spawn_agent` subtask", "Use `spawn_agent` only for well-scoped parallel subtasks."},
		},
		{
			name:         "persistent_child_agents_prefer_managed_launch",
			enabledTools: []string{"spawn_agent", "launch_agent", "get_agent_report", "get_agent_reports", "send_message", "stop_agent", "grep", "file"},
			flags:        map[string]bool{"persistent_subagent_default": true, "explore_agent_enabled": true},
			want: []string{
				"`launch_agent`",
				"`get_agent_report(wait=true)`",
				"`get_agent_reports(wait=true)`",
				"`send_message(wait_report=true)`",
				"`stop_agent`",
				"`context_mode=\"minimal\"`",
				"`context_mode=\"focused\"`",
				"background, confirmed decisions, relevant file paths, forbidden actions, return format, and known risks",
				"Prefer file paths, function names, line numbers, and constraints",
				"Do not paste large code blocks",
				"do not copy the parent conversation history",
				"leaf worker",
				"状态: success | blocked | failed",
				"integrate the report",
			},
			absent: []string{
				"`spawn_agent`",
				"temporary background subtasks",
				"explore-oriented `spawn_agent` subtask",
				"Use `spawn_agent` only for well-scoped parallel subtasks.",
				"orchestration_launch_agent",
				"orchestration_get_agent_report",
				"`files`",
				"`constraints`",
				"`return_format`",
			},
		},
		{
			name:         "persistent_child_agents_with_legacy_surface_still_teaches_short_names",
			enabledTools: []string{"orchestration_launch_agent", "orchestration_get_agent_report"},
			flags:        map[string]bool{"non_interactive": true, "persistent_subagent_default": true},
			want:         []string{"`launch_agent`", "`get_agent_report(wait=true)`", "`context_mode=\"minimal\"`", "`context_mode=\"focused\"`"},
			absent:       []string{"`spawn_agent`", "orchestration_launch_agent", "orchestration_get_agent_report"},
		},
		{
			name:         "managed_agent_only_still_shows_persistent_guidance",
			enabledTools: []string{"launch_agent"},
			flags:        map[string]bool{"non_interactive": true, "persistent_subagent_default": true},
			want:         []string{"`launch_agent`", "`context_mode=\"minimal\"`", "`context_mode=\"focused\"`"},
			absent:       []string{"`spawn_agent`", "orchestration_launch_agent"},
		},
		{name: "skills_without_discovery_show_only_slash_guidance", flags: map[string]bool{"non_interactive": true, "user_invocable_skills": true}, want: []string{"/<skill-name>"}, absent: []string{"discovery flow", "! <command>"}},
		{name: "discover_requires_surfaced_skills", flags: map[string]bool{"non_interactive": true, "discover_skills_enabled": true}, wantNil: true},
	}
}

func TestSessionGuidanceVerificationProtocolIncludesVerifierInputsAndLoop(t *testing.T) {
	text := mustSessionGuidanceText(t, []string{"spawn_agent"}, map[string]bool{
		"non_interactive":       true,
		"verification_required": true,
	})
	if text == nil {
		t.Fatal("Resolve() = nil, want verification guidance")
	}
	for _, check := range []string{
		"original user request",
		"every changed file",
		"implementation approach",
		"plan path",
		"do not preload the verifier with your own test verdicts",
		"`PASS`, `FAIL`, or `PARTIAL`",
		"On `FAIL`, fix the issues and rerun the verifier.",
		"On `PARTIAL`, report only the verified subset",
		"On `PASS`, spot-check the verifier",
		"2-3 commands",
	} {
		if !strings.Contains(*text, check) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, check)
		}
	}
}

func mustSessionGuidanceText(t *testing.T, enabledTools []string, flags map[string]bool) *string {
	t.Helper()
	provider := SessionGuidanceProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		EnabledTools: enabledTools,
		SessionFlags: flags,
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return text
}
