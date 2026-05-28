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

// F1.5 / ADR-009 — RecordNodeSpawn 写入回路单测。
//
// 用例覆盖：
//   1. 首次 spawn：spawning_thread_id 从空写入 thread-1，不 append events
//      （历史空、无可保留的 prev）。
//   2. 重试 spawn：spawning_thread_id 从 thread-1 覆盖为 thread-2，且同事务内
//      给 dag_key 当前 running run 的 events 数组 append 一条 node_spawn 历史。
//   3. 重试 + 无 running run：覆盖发生但 events append silently miss
//      （AppendedEvent=false），不视为错误（spawn 历史是辅助，缺失不应破坏 spawn 主路）。
//   4. 入参防御：空 thread_id 拒绝；空 dag/node_key 拒绝。

// newSpawnRecorderTestStore 包装 newTaskDAGTestStore 的聚合 Store 返回值，提取 narrow
// port NodeSpawnRecorderStore。F1.5 后续修复：从聚合 Store 中拆出 NodeSpawnRecorderStore
// 以过 archtest TestInterfaceIsolationBudgets，所以测试不能再直接这 Store 接口调。
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
	// Run.events should remain empty since first spawn doesn't write history.
	run := db.runs["run-A"]
	if len(run.Events) != 0 {
		t.Fatalf("run.events = %q, want empty on first spawn", string(run.Events))
	}
}

func TestRecordNodeSpawn_RetryOverwrite_AppendsEvent(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	// 先写一遍 thread-1（首次 spawn）。
	mustRecordNodeSpawn(t, store, runID, "thread-1", "first spawn")

	// 再写 thread-2 —— 重试场景：旧 thread-1 应进 events 历史。
	res := mustRecordNodeSpawn(t, store, runID, "thread-2", "retry spawn")
	assertRetrySpawnResult(t, res, "thread-2", "thread-1", "run-A")

	// 校验 events 中带了一条 node_spawn 历史（prev_thread_id=thread-1, thread_id=thread-2）。
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
	// 故意不 seedRun —— 模拟「DAG 有节点但没有 running run」（M3 之前
	// dispatcher-only 路径常见状态）。

	// First spawn 写入。
	if _, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-1",
	}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}

	// 重试：覆盖会命中缺失 running run；严格 Fail-Fast 下必须返回错误，
	// 不能把 event append 失败伪装成成功。
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
		want string // substring of expected error
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

// TestRecordNodeSpawn_EventsRingTrim_KeepsLastFifty 验证端口收敛 batch 实装的
// task_dag_runs.events 环形截断：append 多于 50 条 node_spawn 事件时，仅保留
// 最近 50 条（按时间序最早的被踢出）。R1 P1 #5 + R2 P0 #2 修复。
//
// 流程：让同一节点重试 N 次（thread-1 → thread-2 → ... → thread-N），每次重试
// （prev != new）都会 append 一条事件。检验 N=60 时 events 长度=50，且最早的
// 10 条已被丢弃，最近 50 条 thread_id 是 thread-11 … thread-60 范围。
//
// 50 阈值是 AppendTaskDagRunEvent SQL 内 CASE 写死的；阈值变更时改 SQL 后本测试
// 跟改 expectedCap 即可。
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
	// 60 spawns: 第 1 次首发不写 events（prev 空），第 2..60 次每次写 1 条 → 共 59 条。
	// 59 > 50 → 截断保留最后 50 条。最早保留的是第 11 次写入的事件（覆盖 thread-10→thread-11）。
	if len(arr) != expectedCap {
		t.Fatalf("events length = %d, want %d (ring trim)", len(arr), expectedCap)
	}
	// 第一条保留的应是 thread_id=thread-11、prev_thread_id=thread-10。
	first := arr[0]
	if first["thread_id"] != "thread-11" {
		t.Fatalf("first kept event thread_id = %v, want thread-11", first["thread_id"])
	}
	if first["prev_thread_id"] != "thread-10" {
		t.Fatalf("first kept event prev_thread_id = %v, want thread-10", first["prev_thread_id"])
	}
	// 最后一条应是最新的：thread-60 / prev thread-59。
	last := arr[expectedCap-1]
	if last["thread_id"] != "thread-60" {
		t.Fatalf("last event thread_id = %v, want thread-60", last["thread_id"])
	}
	if last["prev_thread_id"] != "thread-59" {
		t.Fatalf("last event prev_thread_id = %v, want thread-59", last["prev_thread_id"])
	}
}

// itoa 本地小转字符串，避免引 strconv（保持 import 表干净）。
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
