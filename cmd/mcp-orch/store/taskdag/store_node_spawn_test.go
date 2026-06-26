//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// RecordNodeSpawn 的回归测试覆盖节点派生线程写入和重试覆盖。
// 首次 spawn 只写 spawning_thread_id；重试 spawn 会追加 node_spawn 事件。
// 缺少 running run 或关键入参为空时必须显式失败，避免派生线程历史丢失后仍被当成成功。

// newSpawnRecorderTestStore 从聚合测试 store 中取出 NodeSpawnRecorderStore 窄接口。
// 测试只通过派生线程记录接口调用，防止回归成依赖完整 Store 的路径。
func newSpawnRecorderTestStore() (NodeSpawnRecorderStore, *fakeTaskDAGDB, time.Time) {
	s, db, now := newTaskDAGTestStore()
	return s.(NodeSpawnRecorderStore), db, now
}

func TestRecordNodeSpawn_FirstSpawn_WritesFieldNoEvents(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey:   "dag-1",
		NodeKey:  "n1",
		RunID:    runID,
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("RecordNodeSpawn first error = %v", err)
	}
	if res == nil || res.Node == nil {
		t.Fatalf("RecordNodeSpawn first returned nil node: %+v", res)
	}
	if got := stringValue(res.Node.SpawningThreadID); got != "thread-1" {
		t.Fatalf("Node.SpawningThreadID = %q, want thread-1", got)
	}
	if res.PreviousThreadID != "" {
		t.Fatalf("PreviousThreadID = %q, want empty on first spawn", res.PreviousThreadID)
	}
	if res.AppendedEvent {
		t.Fatalf("AppendedEvent = true, want false on first spawn (no history yet)")
	}
	// 首次 spawn 没有前序线程，run.events 必须保持为空。
	run := db.runs["run-A"]
	if len(run.Events) != 0 {
		t.Fatalf("run.events = %q, want empty on first spawn", string(run.Events))
	}
}

func TestRecordNodeSpawn_RetryOverwrite_AppendsEvent(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	// 先写入 thread-1，模拟首次 spawn。
	mustRecordNodeSpawn(t, store, runID, "thread-1", "first spawn")

	// 再写入 thread-2，模拟同一节点重试；旧线程应进入 run.events。
	res := mustRecordNodeSpawn(t, store, runID, "thread-2", "retry spawn")
	assertRetrySpawnResult(t, res, "thread-2", "thread-1", "run-A")

	// 校验 events 中只追加了一条 node_spawn 覆盖历史。
	assertSingleNodeSpawnEvent(t, db.runs["run-A"].Events, "n1", "thread-1", "thread-2")
}

func mustRecordNodeSpawn(t *testing.T, store NodeSpawnRecorderStore, runID int64, threadID, label string) *RecordNodeSpawnResult {
	t.Helper()
	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: threadID,
	})
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
	return res
}

func assertRetrySpawnResult(t *testing.T, res *RecordNodeSpawnResult, wantThread, wantPrev, wantRun string) {
	t.Helper()
	if got := stringValue(res.Node.SpawningThreadID); got != wantThread {
		t.Fatalf("Node.SpawningThreadID = %q, want %s after retry", got, wantThread)
	}
	if res.PreviousThreadID != wantPrev {
		t.Fatalf("PreviousThreadID = %q, want %s", res.PreviousThreadID, wantPrev)
	}
	if !res.AppendedEvent {
		t.Fatalf("AppendedEvent = false, want true on retry overwrite")
	}
	if res.RunKey != wantRun {
		t.Fatalf("RunKey = %q, want %s", res.RunKey, wantRun)
	}
}

func assertSingleNodeSpawnEvent(t *testing.T, raw json.RawMessage, wantNode, wantPrev, wantThread string) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("run.events empty after retry, expected one node_spawn entry")
	}
	var arr []nodeSpawnEvent
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("unmarshal events %q: %v", string(raw), err)
	}
	if len(arr) != 1 {
		t.Fatalf("events len = %d, want 1; raw = %s", len(arr), string(raw))
	}
	assertNodeSpawnEvent(t, arr[0], wantNode, wantPrev, wantThread)
}

func TestRecordNodeSpawn_RetryWithoutRunningRunFailsFast(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	const runID int64 = 101
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")
	// 不 seed running run，模拟节点存在但运行记录缺失的状态。

	// 首次 spawn 只写节点字段，不需要 run 事件。
	if _, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-1",
	}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}

	// 重试会尝试追加事件；缺少 running run 时必须失败，不能把事件缺失伪装成成功。
	_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-2",
	})
	if err == nil || !strings.Contains(err.Error(), "running run not found") {
		t.Fatalf("retry spawn (no running run) error = %v, want running run failure", err)
	}
}

func TestRecordNodeSpawn_RejectsCancelledRuntimeNode(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "cancelled", "agent-a")

	_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-late",
	})
	if err == nil || !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("RecordNodeSpawn cancelled node error = %v, want not found fence miss", err)
	}
	row := db.nodes[dagRunNodeKey("dag-1", "n1", runID)]
	if got := sqlc.TextPtr(row.SpawningThreadID); got != nil {
		t.Fatalf("cancelled node spawning_thread_id = %q, want nil", *got)
	}
}

func TestRecordNodeSpawn_RunScopedRowsAndEvents(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runA := seedRunID(db, "dag-1", "run-a")
	runB := seedRunID(db, "dag-1", "run-b")
	seedRuntimeNode(t, db, now, runA, "n1", nil, "running", "agent-a")
	seedRuntimeNode(t, db, now, runB, "n1", nil, "running", "agent-a")

	mustRecordNodeSpawn(t, store, runA, "thread-a1", "run A first spawn")
	mustRecordNodeSpawn(t, store, runB, "thread-b1", "run B first spawn")
	res := mustRecordNodeSpawn(t, store, runA, "thread-a2", "run A retry spawn")
	if got := stringValue(res.Node.SpawningThreadID); got != "thread-a2" {
		t.Fatalf("run A returned spawning thread = %q, want thread-a2", got)
	}
	if res.RunKey != "run-a" {
		t.Fatalf("run A retry RunKey = %q, want run-a", res.RunKey)
	}
	assertRunScopedStoredThreads(t, db, runA, runB)
	assertRunScopedEvents(t, db)
}

func assertRunScopedStoredThreads(t *testing.T, db *fakeTaskDAGDB, runA, runB int64) {
	t.Helper()
	assertStoredSpawnThread(t, db.nodes[dagRunNodeKey("dag-1", "n1", runA)], "run A", "thread-a2")
	assertStoredSpawnThread(t, db.nodes[dagRunNodeKey("dag-1", "n1", runB)], "run B", "thread-b1")
}

func assertStoredSpawnThread(t *testing.T, node sqlc.TaskDagNode, label, want string) {
	t.Helper()
	got := sqlc.TextPtr(node.SpawningThreadID)
	if got == nil || *got != want {
		t.Fatalf("%s stored spawning thread = %v, want %s", label, got, want)
	}
}

func assertRunScopedEvents(t *testing.T, db *fakeTaskDAGDB) {
	t.Helper()
	var runAEvents []nodeSpawnEvent
	if err := json.Unmarshal(db.runs["run-a"].Events, &runAEvents); err != nil {
		t.Fatalf("decode run A events: %v", err)
	}
	if len(runAEvents) != 1 {
		t.Fatalf("run A events = %+v, want one a1->a2 event", runAEvents)
	}
	assertNodeSpawnEvent(t, runAEvents[0], "n1", "thread-a1", "thread-a2")
	if len(db.runs["run-b"].Events) != 0 {
		t.Fatalf("run B events = %s, want empty after only first spawn", db.runs["run-b"].Events)
	}
}

func assertNodeSpawnEvent(t *testing.T, event nodeSpawnEvent, wantNode, wantPrev, wantThread string) {
	t.Helper()
	if event.Kind != "node_spawn" || event.NodeKey != wantNode ||
		event.PrevThreadID != wantPrev || event.ThreadID != wantThread {
		t.Fatalf("event = %+v, want kind=node_spawn node_key=%s prev=%s new=%s", event, wantNode, wantPrev, wantThread)
	}
	if event.TS == "" {
		t.Fatalf("event ts empty, want RFC3339Nano timestamp")
	}
}

func TestRecordNodeSpawn_InputValidation(t *testing.T) {
	store, db, _ := newSpawnRecorderTestStore()
	seedDAG(t, db, db.now, []seedNode{
		{key: "n1", deps: nil, status: "running", agent: "agent-a"},
	})

	cases := []struct {
		name string
		in   RecordNodeSpawnInput
		want string // 期望错误片段
	}{
		{"empty_thread_id", RecordNodeSpawnInput{DagKey: "dag-1", NodeKey: "n1"}, "thread_id required"},
		{"whitespace_thread_id", RecordNodeSpawnInput{DagKey: "dag-1", NodeKey: "n1", ThreadID: "   "}, "thread_id required"},
		{"empty_dag_key", RecordNodeSpawnInput{NodeKey: "n1", ThreadID: "t"}, "dag_key and node_key required"},
		{"empty_node_key", RecordNodeSpawnInput{DagKey: "dag-1", ThreadID: "t"}, "dag_key and node_key required"},
		{"missing_run_id", RecordNodeSpawnInput{DagKey: "dag-1", NodeKey: "n1", ThreadID: "t"}, "run_id required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.RecordNodeSpawn(context.Background(), tc.in)
			if err == nil {
				t.Fatalf("RecordNodeSpawn(%+v) error = nil, want %q", tc.in, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RecordNodeSpawn error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TestRecordNodeSpawn_EventsRingTrim_KeepsLastFifty 验证 run.events 只保留最近 50 条 node_spawn 事件。
// 连续 60 次 spawn 中第一次不写事件，后续重试各写一条；最终应保留 thread-11 到 thread-60 的覆盖历史。
// expectedCap 必须与 AppendTaskDagRunEvent SQL 中的截断上限同步。
func TestRecordNodeSpawn_EventsRingTrim_KeepsLastFifty(t *testing.T) {
	const expectedCap = 50
	const totalSpawns = 60

	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	for i := 1; i <= totalSpawns; i++ {
		threadID := "thread-" + itoa(i)
		_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
			DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: threadID,
		})
		if err != nil {
			t.Fatalf("spawn %d error = %v", i, err)
		}
	}

	run := db.runs["run-A"]
	var arr []map[string]any
	if err := json.Unmarshal(run.Events, &arr); err != nil {
		t.Fatalf("decode events: %v; raw=%s", err, run.Events)
	}
	// 60 次 spawn 中第 1 次不写事件，第 2..60 次各写 1 条；59 条超过上限后只保留最后 50 条。
	if len(arr) != expectedCap {
		t.Fatalf("events length = %d, want %d (ring trim)", len(arr), expectedCap)
	}
	// 第一条保留事件应来自 thread-10 覆盖为 thread-11 的那次重试。
	first := arr[0]
	if first["thread_id"] != "thread-11" {
		t.Fatalf("first kept event thread_id = %v, want thread-11", first["thread_id"])
	}
	if first["prev_thread_id"] != "thread-10" {
		t.Fatalf("first kept event prev_thread_id = %v, want thread-10", first["prev_thread_id"])
	}
	// 最后一条应是最新的 thread-59 覆盖为 thread-60。
	last := arr[expectedCap-1]
	if last["thread_id"] != "thread-60" {
		t.Fatalf("last event thread_id = %v, want thread-60", last["thread_id"])
	}
	if last["prev_thread_id"] != "thread-59" {
		t.Fatalf("last event prev_thread_id = %v, want thread-59", last["prev_thread_id"])
	}
}

// itoa 是测试本地的小整数转字符串工具，避免为了断言数据额外引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
