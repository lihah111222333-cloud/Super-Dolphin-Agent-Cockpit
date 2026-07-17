package uistate

import (
	"encoding/json"
	"testing"
	"time"
)

// requireNonNilEmptySlice 断言 clone 结果为非 nil 空 slice。
func requireNonNilEmptySlice[T any](t *testing.T, name string, got []T) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s is nil, want non-nil empty slice", name)
	}
	if len(got) != 0 {
		t.Fatalf("%s has %d elements, want empty", name, len(got))
	}
}

// requireJSONKeyIsEmptyArray 断言序列化结果中指定键存在且为 []。
func requireJSONKeyIsEmptyArray(t *testing.T, raw map[string]json.RawMessage, key string, full []byte) {
	t.Helper()
	value, ok := raw[key]
	if !ok {
		t.Fatalf("key %q missing from sidebar JSON: %s", key, full)
	}
	if string(value) != "[]" {
		t.Fatalf("key %q = %s, want []", key, value)
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func mustUnmarshalJSONObject(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return raw
}

// TestCloneSidebarSerializesEmptyListsAsArrays 锁定 sidebar wire 契约：
// threads/agents/recent_turns 在空状态下必须序列化为 []，不得为 null 或缺失。
func TestCloneSidebarSerializesEmptyListsAsArrays(t *testing.T) {
	t.Parallel()

	cloned := cloneSidebar(Sidebar{})
	requireNonNilEmptySlice(t, "threads", cloned.Threads)
	requireNonNilEmptySlice(t, "agents", cloned.Agents)
	requireNonNilEmptySlice(t, "recent_turns", cloned.RecentTurns)

	data := mustMarshalJSON(t, cloned)
	raw := mustUnmarshalJSONObject(t, data)
	for _, key := range []string{"threads", "agents", "recent_turns"} {
		requireJSONKeyIsEmptyArray(t, raw, key, data)
	}
}

// TestCloneSliceFunctionsKeepEmptyNonNil 验证 nil 输入经 clone 后得到非 nil 空 slice。
func TestCloneSliceFunctionsKeepEmptyNonNil(t *testing.T) {
	t.Parallel()

	requireNonNilEmptySlice(t, "cloneThreads(nil)", cloneThreads(nil))
	requireNonNilEmptySlice(t, "cloneAgents(nil)", cloneAgents(nil))
	requireNonNilEmptySlice(t, "cloneTurns(nil)", cloneTurns(nil))
}

// TestCloneThreadsPreservesContent 验证非空 threads 克隆后内容保持不变。
func TestCloneThreadsPreservesContent(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	threads := []ThreadSummary{{ID: "thread-1", Name: "keep me", CreatedAt: &stamp}}

	cloned := cloneThreads(threads)
	if len(cloned) != 1 || cloned[0].ID != "thread-1" || cloned[0].Name != "keep me" {
		t.Fatalf("cloneThreads content drift: %#v", cloned)
	}
	if cloned[0].CreatedAt == nil || !cloned[0].CreatedAt.Equal(stamp) {
		t.Fatalf("cloneThreads lost CreatedAt: %#v", cloned[0].CreatedAt)
	}
}

// TestCloneAgentsPreservesContent 验证非空 agents 克隆后内容保持不变。
func TestCloneAgentsPreservesContent(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	agents := []AgentSummary{{ID: "agent-1", CreatedAt: &stamp}}

	cloned := cloneAgents(agents)
	if len(cloned) != 1 || cloned[0].ID != "agent-1" {
		t.Fatalf("cloneAgents content drift: %#v", cloned)
	}
	if cloned[0].CreatedAt == nil || !cloned[0].CreatedAt.Equal(stamp) {
		t.Fatalf("cloneAgents lost CreatedAt: %#v", cloned[0].CreatedAt)
	}
}

// TestCloneThreadsDoesNotMutateSource 验证 clone 不修改原始数据、不共享元素存储。
func TestCloneThreadsDoesNotMutateSource(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	threads := []ThreadSummary{{ID: "thread-1", Name: "keep me", CreatedAt: &stamp}}

	cloned := cloneThreads(threads)
	cloned[0].Name = "changed"
	if threads[0].Name != "keep me" {
		t.Fatalf("cloneThreads mutated or shares storage with source: %#v", threads[0])
	}
}
