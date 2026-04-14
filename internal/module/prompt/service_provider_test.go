package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestNewServiceRegistersBuiltInDynamicProviders(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	svc := NewService(&Config{}, nil)

	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{
		CWD:      "/repo",
		GitRoot:  "/repo",
		Language: "Chinese",
		EnabledTools: []string{
			"lsp_file",
			"request_user_input",
			"spawn_agent",
		},
		MCPSnapshot: MCPSnapshot{
			Servers:      []string{"lsp"},
			Instructions: map[string]string{"lsp": "Use the LSP MCP first."},
		},
		SessionFlags: map[string]bool{"verification_required": true},
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}

	checks := []string{
		"# Session-specific guidance",
		"request_user_input",
		"spawn_agent",
		"# Environment",
		"# Language",
		"# MCP Server Instructions",
	}
	for _, check := range checks {
		if !resolvedSectionsContain(assembly.ResolvedSections, check) {
			t.Fatalf("ResolvedSections = %#v, want substring %q", assembly.ResolvedSections, check)
		}
	}
}

func resolvedSectionsContain(sections []ResolvedPromptSection, want string) bool {
	for _, section := range sections {
		if strings.Contains(section.Content, want) {
			return true
		}
	}
	return false
}
