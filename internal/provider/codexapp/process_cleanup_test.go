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

func TestBuildProcessAncestry(t *testing.T) {
	allProcs := map[int]int{
		10:  1,
		100: 10,
		101: 100,
		20:  1,
		300: 300,
	}

	ancestry := buildProcessAncestry(101, allProcs)
	for _, want := range []int{101, 100, 10} {
		if _, ok := ancestry[want]; !ok {
			t.Errorf("ancestry rooted at 101 should contain PID %d", want)
		}
	}
	for _, notWant := range []int{1, 20, 300} {
		if _, ok := ancestry[notWant]; ok {
			t.Errorf("ancestry rooted at 101 should NOT contain PID %d", notWant)
		}
	}

	cycle := buildProcessAncestry(300, allProcs)
	if len(cycle) != 1 {
		t.Fatalf("cycle ancestry should contain only one PID, got %d", len(cycle))
	}
	if _, ok := cycle[300]; !ok {
		t.Fatal("cycle ancestry should contain PID 300")
	}
}

func TestBuildRuntimeProtectionSetCombinesTreeAndAncestry(t *testing.T) {
	allProcs := map[int]int{
		10:  1,
		100: 10,
		101: 100,
		102: 101,
		20:  1,
	}

	protected := buildRuntimeProtectionSet(101, allProcs)
	for _, want := range []int{10, 100, 101, 102} {
		if _, ok := protected[want]; !ok {
			t.Fatalf("protection set should contain PID %d; got %#v", want, protected)
		}
	}
	if _, ok := protected[20]; ok {
		t.Fatalf("protection set should not contain unrelated PID 20; got %#v", protected)
	}
}

func TestFilterOrphanAppServersSkipsCurrentAncestry(t *testing.T) {
	allProcs := map[int]int{
		10:  1,  // existing app-server ancestor
		100: 10, // shell/tool runner
		101: 100,
		20:  1, // real orphan app-server
		99:  1, // another live application
		30:  99,
	}
	protected := buildProcessTree(101, allProcs)
	for pid := range buildProcessAncestry(101, allProcs) {
		protected[pid] = struct{}{}
	}

	orphans := filterOrphanAppServers([]appServerProcessInfo{
		{pid: 10, ppid: 1},
		{pid: 20, ppid: 1},
		{pid: 30, ppid: 99},
		{pid: 101, ppid: 100},
	}, protected)
	if len(orphans) != 1 || orphans[0].pid != 20 {
		t.Fatalf("filterOrphanAppServers() = %#v, want only PID 20", orphans)
	}
}

func TestFilterOrphanMCPProcessesSkipsCurrentAncestry(t *testing.T) {
	allProcs := map[int]int{
		10:  1,  // existing mcp ancestor
		100: 10, // shell/tool runner
		101: 100,
		20:  1, // real orphan mcp
	}
	protected := buildRuntimeProtectionSet(101, allProcs)

	orphans := filterOrphanMCPProcesses([]mcpProcessInfo{
		{pid: 10, ppid: 1, binary: "mcp-orch"},
		{pid: 20, ppid: 1, binary: "mcp-lsp"},
		{pid: 101, ppid: 100, binary: "mcp-orch"},
	}, protected)
	if len(orphans) != 1 || orphans[0].pid != 20 {
		t.Fatalf("filterOrphanMCPProcesses() = %#v, want only PID 20", orphans)
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
