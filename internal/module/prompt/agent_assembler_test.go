package prompt

import (
	"context"
	"strings"
	"testing"
)

// TestAssembleAgent_OverrideSystemPromptTakesPriority mirrors Claude Code's
// override.systemPrompt direct-passthrough branch: when the caller supplies
// an explicit override, neither section resolution nor env-details apply.
func TestAssembleAgent_OverrideSystemPromptTakesPriority(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleAgent(context.Background(), AgentInput{
		StartInput: StartInput{
			Name:                  "Explorer",
			Provider:              "claudecli",
			CWD:                   t.TempDir(),
			DeveloperInstructions: "dev tail",
		},
		AgentType:            AgentTypeExplore,
		OverrideSystemPrompt: "OVERRIDE-ONLY-TEXT",
	})
	if err != nil {
		t.Fatalf("AssembleAgent() error = %v", err)
	}
	if assembly.BaseInstructions != "OVERRIDE-ONLY-TEXT" {
		t.Fatalf("BaseInstructions = %q, want exact override text", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "Subagent runtime guardrails") {
		t.Fatalf("override path must not append env-details: %q", assembly.BaseInstructions)
	}
	if assembly.DeveloperInstructions != "dev tail" {
		t.Fatalf("DeveloperInstructions = %q, want dev tail", assembly.DeveloperInstructions)
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("override path must skip section resolution: %#v", assembly.ResolvedSections)
	}
}

// TestAssembleAgent_ExploreRedactsClaudeMdAndGitStatus verifies the
// Explore/Plan agent-type post-processing: claudeMd is scrubbed from
// UserContext and SystemContext (gitStatus) is nilled so the subagent does
// not inherit the caller's repo context.
func TestAssembleAgent_ExploreRedactsClaudeMdAndGitStatus(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")
	svc := NewService(&Config{}, nil)
	in := StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
		Language: "English",
	}
	base, err := svc.AssembleStart(context.Background(), in)
	if err != nil {
		t.Fatalf("baseline AssembleStart() error = %v", err)
	}
	// Seed claudeMd to simulate a main-thread inheritance we want to scrub.
	base.UserContext["claudeMd"] = "inherited project memory"

	explore, err := svc.AssembleAgent(context.Background(), AgentInput{
		StartInput: in,
		AgentType:  AgentTypeExplore,
	})
	if err != nil {
		t.Fatalf("AssembleAgent() error = %v", err)
	}
	if _, ok := explore.UserContext["claudeMd"]; ok {
		t.Fatalf("Explore agent must not inherit claudeMd: %#v", explore.UserContext)
	}
	if explore.SystemContext != nil {
		t.Fatalf("Explore agent must not inherit SystemContext (gitStatus): %#v", explore.SystemContext)
	}
	if !strings.Contains(explore.BaseInstructions, "Subagent runtime guardrails") {
		t.Fatalf("Explore agent BaseInstructions missing env-details block:\n%s", explore.BaseInstructions)
	}
	if !strings.Contains(explore.BaseInstructions, "absolute paths") {
		t.Fatalf("env-details block missing absolute-path rule:\n%s", explore.BaseInstructions)
	}
}

// TestAssembleAgent_DefaultKeepsContext asserts the non-Explore/Plan default
// path: claudeMd/SystemContext are preserved, but env-details still appended.
func TestAssembleAgent_DefaultKeepsContext(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")
	svc := NewService(&Config{}, nil)
	in := StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
		Language: "English",
	}
	assembly, err := svc.AssembleAgent(context.Background(), AgentInput{
		StartInput: in,
		AgentType:  AgentTypeDefault,
	})
	if err != nil {
		t.Fatalf("AssembleAgent() error = %v", err)
	}
	if !strings.Contains(assembly.BaseInstructions, "Subagent runtime guardrails") {
		t.Fatalf("default agent BaseInstructions missing env-details block:\n%s", assembly.BaseInstructions)
	}
	// UserContext.currentDate is always populated (Phase 3); default agent
	// keeps it so the synthetic meta message includes the date.
	if _, ok := assembly.UserContext["currentDate"]; !ok {
		t.Fatalf("default agent UserContext missing currentDate: %#v", assembly.UserContext)
	}
}
