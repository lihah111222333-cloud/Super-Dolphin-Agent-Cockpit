package main

import (
	"context"
	"io"
	"testing"
)

func TestMcpSkillMode_DoesNotStartFullApp(t *testing.T) {
	oldDesktop := runDesktopApp
	oldSkill := runSkillMCPMode
	defer func() {
		runDesktopApp = oldDesktop
		runSkillMCPMode = oldSkill
	}()

	desktopCalls := 0
	skillCalls := 0
	runDesktopApp = func() error {
		desktopCalls++
		return nil
	}
	runSkillMCPMode = func(context.Context, io.Reader, io.Writer) error {
		skillCalls++
		return nil
	}

	if err := runMain([]string{"--mcp-skill-mode"}); err != nil {
		t.Fatalf("runMain(--mcp-skill-mode) error = %v", err)
	}
	if skillCalls != 1 {
		t.Fatalf("skill mode calls = %d, want 1", skillCalls)
	}
	if desktopCalls != 0 {
		t.Fatalf("desktop calls = %d, want 0", desktopCalls)
	}
}
