package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestEnvInfoProviderResolveBuildsEnvironmentDetails(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	provider := EnvInfoProvider{}

	text, err := provider.Resolve(context.Background(), SectionContext{
		BuildCtx: BuildCtx{
			CWD:                          "/repo/worktree",
			GitRoot:                      "/repo",
			IsWorktree:                   true,
			Provider:                     "codex",
			Model:                        "gpt-5.5",
			EnabledTools:                 []string{"exec_command", "file", "grep", "file"},
			AdditionalWorkingDirectories: []string{"/repo/extra", " /repo/extra-two ", "/repo/extra"},
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want content")
	}

	checks := []string{
		"# Environment",
		"- Primary working directory: /repo/worktree",
		"- Is a git repository: yes",
		"- Git root: /repo",
		"- Git worktree: yes",
		"- Worktree note: run all commands from this directory and do not cd to the original repository root",
		"- Platform: ",
		"- Shell: zsh",
		"- OS version: ",
		"- Language server status: enabled (file, grep)",
		"- Additional working directory: /repo/extra",
		"- Additional working directory: /repo/extra-two",
		"- Provider: codex",
		"- Model metadata: GPT-5.5 (model ID: gpt-5.5)",
		"- Knowledge cutoff: not published by the provider",
	}
	for _, check := range checks {
		if !strings.Contains(*text, check) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, check)
		}
	}
}

func TestEnvInfoProviderResolveWithoutLSPToolsMarksUnavailable(t *testing.T) {
	provider := EnvInfoProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{EnabledTools: []string{"exec_command"}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "- Language server status: not enabled in this session") {
		t.Fatalf("Resolve() = %v, want missing-LSP notice", text)
	}
}
