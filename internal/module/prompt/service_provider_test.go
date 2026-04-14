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
		CWD:          "/repo",
		GitRoot:      "/repo",
		Language:     "Chinese",
		CurrentDate:  "2026-04-14",
		EnabledTools: []string{"lsp_file"},
		MCPSnapshot: MCPSnapshot{
			Servers: []string{"lsp"},
			Tools:   []string{"mcp__lsp__lsp_file"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}

	checks := []string{"# Environment", "# Language", "# MCP Server Instructions"}
	for _, check := range checks {
		if !strings.Contains(assembly.UserContextText, check) {
			t.Fatalf("UserContextText = %q, want substring %q", assembly.UserContextText, check)
		}
	}
}
