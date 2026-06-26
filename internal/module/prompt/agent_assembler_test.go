package prompt

import (
	"context"
	"strings"
	"testing"
)

// TestAssembleAgent_OverrideSystemPromptTakesPriority 锁定 override.systemPrompt 的直通路径。
// 调用方显式覆盖系统提示词时，不再解析 section，也不追加子代理环境说明。
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

func TestAssembleAgent_OverrideSystemPromptDoesNotUsePromptAsDisplayName(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleAgent(context.Background(), AgentInput{
		StartInput: StartInput{
			Prompt:   "user task must not become a name",
			Provider: "claudecli",
			CWD:      t.TempDir(),
		},
		AgentType:            AgentTypeExplore,
		OverrideSystemPrompt: "OVERRIDE-ONLY-TEXT",
	})
	if err != nil {
		t.Fatalf("AssembleAgent() error = %v", err)
	}
	if assembly.DisplayName != "" {
		t.Fatalf("DisplayName = %q, want empty", assembly.DisplayName)
	}
}

// TestAssembleAgent_ExploreRedactsClaudeMdAndGitStatus 验证探索/计划类子代理会清理继承上下文。
// claudeMd 和 gitStatus 不应透传给子代理，避免把主线程仓库状态当作子任务事实。
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

// TestAssembleAgent_DefaultKeepsContext 验证默认子代理保留上下文，同时仍追加环境说明。
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
	// 默认子代理保留 currentDate，确保 synthetic meta message 仍包含日期。
	if _, ok := assembly.UserContext["currentDate"]; !ok {
		t.Fatalf("default agent UserContext missing currentDate: %#v", assembly.UserContext)
	}
}
