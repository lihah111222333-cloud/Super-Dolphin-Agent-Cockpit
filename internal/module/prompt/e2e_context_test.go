package prompt_test

import (
	"context"
	"path/filepath"
	"testing"

	promptpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
)

func TestStartAssemblyFullContext(t *testing.T) {
	h := newFxHarness(t)
	worktree := filepath.Join(h.projectRoot, "worktree")
	extraDir := filepath.Join(h.projectRoot, "extra")
	start, err := h.assembly.AssembleStart(context.Background(), promptpkg.StartInput{
		Provider:                     "codex",
		CWD:                          worktree,
		GitRoot:                      h.projectRoot,
		Language:                     "Chinese",
		EnabledTools:                 []string{"file", "request_user_input", "spawn_agent"},
		AdditionalWorkingDirectories: []string{extraDir},
		MCPSnapshot: promptpkg.MCPSnapshot{
			Servers: []string{"orch"},
			Tools:   []string{"mcp__orch__task_get_dag"},
			Instructions: map[string]string{
				"orch": "Use DAG tools for orchestration state.",
			},
		},
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}

	env := sectionContent(start.ResolvedSections, promptpkg.DynamicSectionEnvInfoSimple)
	mustContain(t, env, worktree)
	mustContain(t, env, h.projectRoot)
	mustContain(t, env, "enabled (file)")
	mustContain(t, env, extraDir)

	language := sectionContent(start.ResolvedSections, promptpkg.DynamicSectionLanguage)
	mustContain(t, language, "Always respond in Chinese")

	guidance := sectionContent(start.ResolvedSections, promptpkg.DynamicSectionSessionGuidance)
	mustContain(t, guidance, "request_user_input")
	mustContain(t, guidance, "spawn_agent")

	mcp := sectionContent(start.ResolvedSections, promptpkg.DynamicSectionMCPInstructions)
	mustContain(t, mcp, "## orch")
	mustContain(t, mcp, "Use DAG tools for orchestration state.")
}
