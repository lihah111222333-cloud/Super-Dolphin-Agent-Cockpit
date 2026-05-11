package taskdag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
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
	store, db, _ := newSpawnRecorderTestStore()
	seedDAG(t, db, db.now, []seedNode{
		{key: "n1", deps: nil, status: "running", agent: "agent-a"},
	})
	seedRun(db, "dag-1", "run-A")

	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey:   "dag-1",
		NodeKey:  "n1",
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
	store, db, _ := newSpawnRecorderTestStore()
	seedDAG(t, db, db.now, []seedNode{
		{key: "n1", deps: nil, status: "running", agent: "agent-a"},
	})
	seedRun(db, "dag-1", "run-A")

	// 先写一遍 thread-1（首次 spawn）。
	if _, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", ThreadID: "thread-1",
	}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}

	// 再写 thread-2 —— 重试场景：旧 thread-1 应进 events 历史。
	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", ThreadID: "thread-2",
	})
	if err != nil {
		t.Fatalf("retry spawn error = %v", err)
	}
	if got := stringValue(res.Node.SpawningThreadID); got != "thread-2" {
		t.Fatalf("Node.SpawningThreadID = %q, want thread-2 after retry", got)
	}
	if res.PreviousThreadID != "thread-1" {
		t.Fatalf("PreviousThreadID = %q, want thread-1", res.PreviousThreadID)
	}
	if !res.AppendedEvent {
		t.Fatalf("AppendedEvent = false, want true on retry overwrite")
	}
	if res.RunKey != "run-A" {
		t.Fatalf("RunKey = %q, want run-A", res.RunKey)
	}

	// 校验 events 中带了一条 node_spawn 历史（prev_thread_id=thread-1, thread_id=thread-2）。
	run := db.runs["run-A"]
	if len(run.Events) == 0 {
		t.Fatalf("run.events empty after retry, expected one node_spawn entry")
	}
	var arr []nodeSpawnEvent
	if err := json.Unmarshal(run.Events, &arr); err != nil {
		t.Fatalf("unmarshal events %q: %v", string(run.Events), err)
	}
	if len(arr) != 1 {
		t.Fatalf("events len = %d, want 1; raw = %s", len(arr), string(run.Events))
	}
	if arr[0].Kind != "node_spawn" || arr[0].NodeKey != "n1" ||
		arr[0].PrevThreadID != "thread-1" || arr[0].ThreadID != "thread-2" {
		t.Fatalf("event = %+v, want kind=node_spawn node_key=n1 prev=thread-1 new=thread-2", arr[0])
	}
	if arr[0].TS == "" {
		t.Fatalf("event ts empty, want RFC3339Nano timestamp")
	}
}

func TestRecordNodeSpawn_RetryWithoutRunningRun_SoftMiss(t *testing.T) {
	store, db, _ := newSpawnRecorderTestStore()
	seedDAG(t, db, db.now, []seedNode{
		{key: "n1", deps: nil, status: "running", agent: "agent-a"},
	})
	// 故意不 seedRun —— 模拟「DAG 有节点但没有 running run」（M3 之前
	// dispatcher-only 路径常见状态）。

	// First spawn 写入。
	if _, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", ThreadID: "thread-1",
	}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}

	// 重试：覆盖应成功，但 append events 命中 0 行；store 把这种 case 视为
	// 软失败，不返回 error。
	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", ThreadID: "thread-2",
	})
	if err != nil {
		t.Fatalf("retry spawn (no running run) error = %v, want nil", err)
	}
	if got := stringValue(res.Node.SpawningThreadID); got != "thread-2" {
		t.Fatalf("Node.SpawningThreadID = %q, want thread-2", got)
	}
	if res.PreviousThreadID != "thread-1" {
		t.Fatalf("PreviousThreadID = %q, want thread-1", res.PreviousThreadID)
	}
	if res.AppendedEvent {
		t.Fatalf("AppendedEvent = true, want false when no running run exists")
	}
	if res.RunKey != "" {
		t.Fatalf("RunKey = %q, want empty when append silently missed", res.RunKey)
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

	store, db, _ := newSpawnRecorderTestStore()
	seedDAG(t, db, db.now, []seedNode{
		{key: "n1", deps: nil, status: "running", agent: "agent-a"},
	})
	seedRun(db, "dag-1", "run-A")

	for i := 1; i <= totalSpawns; i++ {
		threadID := "thread-" + itoa(i)
		_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
			DagKey: "dag-1", NodeKey: "n1", ThreadID: threadID,
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
