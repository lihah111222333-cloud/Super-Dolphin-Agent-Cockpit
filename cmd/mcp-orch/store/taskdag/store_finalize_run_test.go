package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// F6.2 — run 终态判定测试。
//
// 触发链：service.UpdateNodeStatus(终态) → store.CompleteNodeAndScheduleDownstream
// 完成节点更新 + 下游 promote → maybeFinalizeRun 检查所有节点是否全终态，
// 若是按优先级把 task_dag_runs.status 从 'running' 推到 终态。
//
// 优先级（含义：什么 status 占主导）：
//   1. 任意节点 failed                  → run.status = failed
//   2. 否则任意节点 cancelled           → run.status = cancelled
//   3. 否则全部 done / skipped          → run.status = succeeded
//   4. 若有非终态(pending/ready/running/retrying/waiting_human) → 不动 run（仍 running）
//
// 0080 status CHECK：枚举锁定 running|succeeded|failed|cancelled，新写入终态必在白名单。

// seedRun 在 fake DB 中以 status='running' 插一条 run 行，让 finalize SQL 找得到目标。
// seedRun seeds a task_dag_runs row in status='running' for the fake DB so the
// finalize statement has a target row to flip into a terminal state.
func seedRun(db *fakeTaskDAGDB, dagKey, runKey string) {
	db.runSeq++
	id := db.runSeq
	db.runs[runKey] = sqlc.TaskDagRun{
		ID:                 id,
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: 1,
		TriggerSource:      "manual",
		Status:             "running",
		StartedAt:          timestamptzValue(db.now),
		Metadata:           []byte(`{}`),
		CreatedAt:          timestamptzValue(db.now),
		UpdatedAt:          timestamptzValue(db.now),
	}
}

func runStatusByKey(t *testing.T, db *fakeTaskDAGDB, runKey string) string {
	t.Helper()
	row, ok := db.runs[runKey]
	if !ok {
		t.Fatalf("run %q not found", runKey)
	}
	return row.Status
}

func TestCompleteNode_AllTerminal_AllDone_RunSucceeded(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: nil, status: "done", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	seedRun(db, "dag-1", "run-success")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-success"); got != "succeeded" {
		t.Fatalf("run.status = %q, want succeeded", got)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.Status != "succeeded" {
		t.Fatalf("FinalizedRun = %+v, want {succeeded}", res.FinalizedRun)
	}
}

func TestCompleteNode_AllTerminal_AnyFailed_RunFailed(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "failed", agent: "agent-a"},
		{key: "B", deps: nil, status: "done", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	seedRun(db, "dag-1", "run-fail")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-fail"); got != "failed" {
		t.Fatalf("run.status = %q, want failed", got)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.Status != "failed" {
		t.Fatalf("FinalizedRun = %+v, want {failed}", res.FinalizedRun)
	}
}

func TestCompleteNode_AllTerminal_FailedAndCancelled_RunFailed(t *testing.T) {
	t.Parallel()

	// failed 优先级 > cancelled，即使两者并存也判 failed。
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "failed", agent: "agent-a"},
		{key: "B", deps: nil, status: "cancelled", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	seedRun(db, "dag-1", "run-fail-over-cancel")

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-fail-over-cancel"); got != "failed" {
		t.Fatalf("run.status = %q, want failed (failed beats cancelled)", got)
	}
}

func TestCompleteNode_AllTerminal_CancelledNoFailed_RunCancelled(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "cancelled", agent: "agent-a"},
		{key: "B", deps: nil, status: "done", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	seedRun(db, "dag-1", "run-cancel")

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-cancel"); got != "cancelled" {
		t.Fatalf("run.status = %q, want cancelled", got)
	}
}

func TestCompleteNode_AllTerminal_DoneAndSkipped_RunSucceeded(t *testing.T) {
	t.Parallel()

	// skipped 是终态但属于成功语义（on_failure=skip 情况）；与 done 并存仍走 succeeded。
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "skipped", agent: "agent-a"},
		{key: "B", deps: nil, status: "done", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	seedRun(db, "dag-1", "run-skip-success")

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-skip-success"); got != "succeeded" {
		t.Fatalf("run.status = %q, want succeeded (skipped 算成功语义)", got)
	}
}

func TestCompleteNode_NotAllTerminal_RunUnchanged(t *testing.T) {
	t.Parallel()

	// 还有 pending 节点 → 不应推进 run.status。
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: nil, status: "pending", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	seedRun(db, "dag-1", "run-still-running")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-still-running"); got != "running" {
		t.Fatalf("run.status = %q, want running (still pending nodes left)", got)
	}
	if res.FinalizedRun != nil {
		t.Fatalf("FinalizedRun = %+v, want nil (not all terminal)", res.FinalizedRun)
	}
}
