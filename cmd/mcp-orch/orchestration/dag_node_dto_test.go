package orchestration

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

// TestDagNodeDTO_PropagatesSpawningThreadID 锁定 dagNodeDTO 的子线程字段透传。
// 这个字段是 UI 定位子线程的跨层边界，不能退回到让调用方解析 result JSON。
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
