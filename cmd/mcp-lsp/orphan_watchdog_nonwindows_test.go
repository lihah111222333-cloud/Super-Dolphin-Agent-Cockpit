//go:build !windows

// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"os"
	"testing"
)

func TestNonWindowsIsParentOrphaned(t *testing.T) {
	t.Parallel()

	t.Run("ppid <= 1 is orphaned", func(t *testing.T) {
		t.Parallel()
		if !isParentOrphaned(1) {
			t.Fatal("expected isParentOrphaned(1) == true")
		}
		if !isParentOrphaned(0) {
			t.Fatal("expected isParentOrphaned(0) == true")
		}
		if !isParentOrphaned(-1) {
			t.Fatal("expected isParentOrphaned(-1) == true")
		}
	})

	t.Run("current process pid is alive", func(t *testing.T) {
		t.Parallel()
		currentPid := os.Getpid()
		if isParentOrphaned(currentPid) {
			t.Fatalf("expected current pid %d to not be orphaned", currentPid)
		}
	})

	t.Run("non existent pid is orphaned", func(t *testing.T) {
		t.Parallel()
		// Large PID unlikely to exist
		nonExistentPid := 99999999
		if !isParentOrphaned(nonExistentPid) {
			t.Fatalf("expected non existent pid %d to be orphaned", nonExistentPid)
		}
	})
}
