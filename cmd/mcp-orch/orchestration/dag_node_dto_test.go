package orchestration

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// F1.5 / ADR-009: dagNodeDTO 必须把 store/taskdag.Node.SpawningThreadID
// 透出到 contract.DAGNode.SpawningThreadID（不再让 UI 自己解析 result jsonb）。
//
// dagNodeDTO must surface store/taskdag.Node.SpawningThreadID through to
// contract.DAGNode.SpawningThreadID; otherwise UI consumers (T6.1/T8.1) would
// still need to parse result jsonb to recover the child thread id (defeating
// the purpose of the field).

func TestDagNodeDTO_PropagatesSpawningThreadID(t *testing.T) {
	thread := "thread-x"
	in := taskdag.Node{
		ID:               1,
		DagKey:           "dag-a",
		NodeKey:          "n1",
		SpawningThreadID: &thread,
	}
	out := dagNodeDTO(in)
	if out.SpawningThreadID == nil {
		t.Fatalf("SpawningThreadID = nil, want pointer to %q", thread)
	}
	if *out.SpawningThreadID != thread {
		t.Fatalf("SpawningThreadID = %q, want %q", *out.SpawningThreadID, thread)
	}
}

func TestDagNodeDTO_NilSpawningThreadIDStaysNil(t *testing.T) {
	in := taskdag.Node{ID: 2, DagKey: "dag-a", NodeKey: "n2"}
	out := dagNodeDTO(in)
	if out.SpawningThreadID != nil {
		t.Fatalf("SpawningThreadID = %v, want nil when source nil", out.SpawningThreadID)
	}
}

func TestDagNodeDTO_SpawningThreadIDIsCopied(t *testing.T) {
	// cloneString 必须复制而非共享指针——上层修改 out.SpawningThreadID 指向的值
	// 不能反向污染 store-side Node。
	thread := "thread-original"
	in := taskdag.Node{SpawningThreadID: &thread}
	out := dagNodeDTO(in)
	if out.SpawningThreadID == &thread {
		t.Fatalf("SpawningThreadID shares pointer with source; want defensive copy")
	}
	*out.SpawningThreadID = "mutated"
	if thread != "thread-original" {
		t.Fatalf("source thread mutated to %q via DTO pointer alias", thread)
	}
}
