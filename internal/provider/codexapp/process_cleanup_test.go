package codexapp

import (
	"os"
	"testing"
)

func TestDiscoverProcessesReturnsBothMaps(t *testing.T) {
	allProcs, mcpProcs := discoverProcesses()
	// allProcs should have at least the current process.
	if len(allProcs) == 0 {
		t.Fatal("discoverProcesses() returned empty allProcs")
	}
	myPID := os.Getpid()
	if _, ok := allProcs[myPID]; !ok {
		t.Errorf("allProcs does not contain current PID %d", myPID)
	}
	// mcpProcs may be empty (no MCP processes running), but should not
	// contain invalid entries.
	for _, proc := range mcpProcs {
		if proc.pid <= 0 {
			t.Errorf("invalid pid %d for binary %s", proc.pid, proc.binary)
		}
		if proc.binary != "mcp-orch" && proc.binary != "mcp-lsp" {
			t.Errorf("unexpected binary name %q", proc.binary)
		}
	}
}

func TestBuildProcessTree(t *testing.T) {
	// Synthetic process table:
	//   1 → 10 → 100
	//            101
	//   1 → 20 → 200
	//   1 → 30
	allProcs := map[int]int{
		10:  1,
		20:  1,
		30:  1,
		100: 10,
		101: 10,
		200: 20,
	}

	// Tree rooted at PID 10: should include 10, 100, 101.
	tree := buildProcessTree(10, allProcs)
	for _, want := range []int{10, 100, 101} {
		if _, ok := tree[want]; !ok {
			t.Errorf("tree rooted at 10 should contain PID %d", want)
		}
	}
	for _, notWant := range []int{1, 20, 30, 200} {
		if _, ok := tree[notWant]; ok {
			t.Errorf("tree rooted at 10 should NOT contain PID %d", notWant)
		}
	}

	// Tree rooted at PID 1: should include everything.
	tree1 := buildProcessTree(1, allProcs)
	for _, want := range []int{1, 10, 20, 30, 100, 101, 200} {
		if _, ok := tree1[want]; !ok {
			t.Errorf("tree rooted at 1 should contain PID %d", want)
		}
	}

	// Tree rooted at PID 200 (leaf): should be {200} only.
	tree200 := buildProcessTree(200, allProcs)
	if len(tree200) != 1 {
		t.Errorf("tree rooted at 200 should have 1 entry, got %d", len(tree200))
	}
	if _, ok := tree200[200]; !ok {
		t.Error("tree rooted at 200 should contain PID 200")
	}
}

func TestCleanOrphanedMCPProcessesSkipsSelf(t *testing.T) {
	myPID := os.Getpid()
	// Ensure our own PID is never killed.
	skip := map[int]struct{}{myPID: {}}
	// This should not panic or kill our own process.
	_ = cleanOrphanedMCPProcesses(skip)
}

func TestCleanOrphanedMCPProcessesNilSkip(t *testing.T) {
	// nil skipPIDs should not panic.
	_ = cleanOrphanedMCPProcesses(nil)
}

func TestKillMCPProcessRefusesPID1(t *testing.T) {
	if err := killMCPProcess(1); err == nil {
		t.Fatal("killMCPProcess(1) should return error")
	}
	if err := killMCPProcess(0); err == nil {
		t.Fatal("killMCPProcess(0) should return error")
	}
}
