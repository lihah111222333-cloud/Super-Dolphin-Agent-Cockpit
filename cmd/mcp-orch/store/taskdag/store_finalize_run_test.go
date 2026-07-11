//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// run 终态判定测试覆盖 CompleteNodeAndScheduleDownstream 的收尾边界。
//
// 触发链路从节点终态更新进入 store，完成节点写入和下游 promote 后，
// maybeFinalizeRun 只在 run 内所有节点都进入终态时推进 task_dag_runs.status。
//
// 终态优先级决定哪个节点状态主导 run 结果：
//   1. 任意节点 failed 时，run.status = failed。
//   2. 否则任意节点 cancelled 时，run.status = cancelled。
//   3. 否则全部 done/skipped 时，run.status = succeeded。
//   4. 仍有 pending/ready/running/retrying/waiting_human 时，run 保持 running。
//
// DB status CHECK 只允许 running/succeeded/failed/cancelled，新增终态必须同步更新白名单。

// seedRun 在 fake DB 中插入 running 状态的 run，供 finalize SQL 推进为终态。
func seedRun(db *fakeTaskDAGDB, dagKey, runKey string) {
	seedRunWithMetadata(db, dagKey, runKey, json.RawMessage(`{}`))
}

func seedRunWithMetadata(db *fakeTaskDAGDB, dagKey, runKey string, metadata json.RawMessage) {
	db.runSeq++
	id := db.runSeq
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	db.runs[runKey] = sqlc.TaskDagRun{
		ID:                 id,
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: 1,
		TriggerSource:      "manual",
		Status:             "running",
		StartedAt:          timestamptzValue(db.now),
		Metadata:           append([]byte(nil), metadata...),
		CreatedAt:          timestamptzValue(db.now),
		UpdatedAt:          timestamptzValue(db.now),
	}
}

func seedRunWithTemplateNodes(t *testing.T, db *fakeTaskDAGDB, dagKey, runKey string) int64 {
	t.Helper()
	seedRun(db, dagKey, runKey)
	return cloneTemplateNodesToRun(t, db, dagKey, runKey)
}

func seedRunWithMetadataAndTemplateNodes(t *testing.T, db *fakeTaskDAGDB, dagKey, runKey string, metadata json.RawMessage) int64 {
	t.Helper()
	seedRunWithMetadata(db, dagKey, runKey, metadata)
	return cloneTemplateNodesToRun(t, db, dagKey, runKey)
}

func cloneTemplateNodesToRun(t *testing.T, db *fakeTaskDAGDB, dagKey, runKey string) int64 {
	t.Helper()
	run, ok := db.runs[runKey]
	if !ok {
		t.Fatalf("run %q not found", runKey)
	}
	for _, row := range db.nodes {
		if row.DagKey != dagKey || row.RunID.Valid {
			continue
		}
		copy := cloneTaskDagNode(row)
		copy.ID = int64(len(db.nodes) + 1)
		copy.RunID = sqlc.Int8ValuePtr(&run.ID)
		db.nodes[dagRunNodeKey(dagKey, row.NodeKey, run.ID)] = copy
	}
	return run.ID
}

func seedDAGMetadata(db *fakeTaskDAGDB, dagKey string, metadata json.RawMessage) {
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	db.dags[dagKey] = sqlc.TaskDag{
		ID:        1,
		DagKey:    dagKey,
		Title:     dagKey,
		Status:    "active",
		Metadata:  append([]byte(nil), metadata...),
		CreatedAt: timestamptzValue(db.now),
		UpdatedAt: timestamptzValue(db.now),
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

func runMetadataByKey(t *testing.T, db *fakeTaskDAGDB, runKey string) map[string]any {
	t.Helper()
	row, ok := db.runs[runKey]
	if !ok {
		t.Fatalf("run %q not found", runKey)
	}
	var metadata map[string]any
	if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal run metadata: %v (raw=%s)", err, string(row.Metadata))
	}
	return metadata
}

func finalOutputByRunKey(t *testing.T, db *fakeTaskDAGDB, runKey string) map[string]any {
	t.Helper()
	metadata := runMetadataByKey(t, db, runKey)
	raw, ok := metadata["final_output"]
	if !ok {
		t.Fatalf("metadata.final_output missing in %v", metadata)
	}
	out, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("metadata.final_output = %T, want object", raw)
	}
	return out
}

func withNodeResult(row sqlc.TaskDagNode, result json.RawMessage) sqlc.TaskDagNode {
	row.Result = append([]byte(nil), result...)
	return row
}

func TestCompleteNode_AllTerminal_AllDone_RunSucceeded(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: nil, status: "done", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-success")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: runID, Status: "done", Result: json.RawMessage(`{}`),
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

func TestCompleteNode_FinalNodeSharedfileRefPromotesRunFinalOutput(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "collect", deps: nil, status: "done", agent: "agent-a"},
		{key: "final-report", deps: nil, status: "running", agent: "agent-b"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-report"}`))
	runID := seedRunWithMetadataAndTemplateNodes(t, db, "dag-1", "run-final-file", json.RawMessage(`{"request_id":"req-1"}`))

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "final-report",
		RunID:   runID,
		Status:  "done",
		Result:  json.RawMessage(`{"sharedfile":{"path":"reports/daily/final_report.pptx"}}`),
	})
	if err != nil {
		t.Fatalf("complete final-report error = %v", err)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.Status != "succeeded" {
		t.Fatalf("FinalizedRun = %+v, want succeeded", res.FinalizedRun)
	}

	metadata := runMetadataByKey(t, db, "run-final-file")
	if metadata["request_id"] != "req-1" {
		t.Fatalf("metadata.request_id = %v, want req-1", metadata["request_id"])
	}
	out := finalOutputByRunKey(t, db, "run-final-file")
	if out["kind"] != "file" || out["role"] != "final_output" || out["source_node_key"] != "final-report" {
		t.Fatalf("final_output identity = %v, want file final-report", out)
	}
	if out["path"] != "reports/daily/final_report.pptx" {
		t.Fatalf("final_output.path = %v", out["path"])
	}
	if _, ok := out["result"]; ok {
		t.Fatalf("file final_output must not duplicate node result: %v", out)
	}
}

func TestCompleteNode_FinalNodeConfiguredSharedfilePromotesRunFinalOutput(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "final-report", deps: nil, status: "running", agent: "agent-b"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-report"}`))

	node := db.nodes[dagNodeKey("dag-1", "final-report")]
	node.Config = json.RawMessage(`{
		"exec":{"agent_key":"paper_summarizer"},
		"outputs":{
			"to_sharedfile":{"path":"reports/daily/final_report.pptx","lock_mode":"exclusive"},
			"to_node_result":true
		}
	}`)
	db.nodes[dagNodeKey("dag-1", "final-report")] = node
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-final-configured-file")

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "final-report",
		RunID:   runID,
		Status:  "done",
		Result:  json.RawMessage(`{"summary":"short node result"}`),
	})
	if err != nil {
		t.Fatalf("complete final-report error = %v", err)
	}

	out := finalOutputByRunKey(t, db, "run-final-configured-file")
	if out["kind"] != "file" || out["role"] != "final_output" || out["source_node_key"] != "final-report" {
		t.Fatalf("final_output identity = %v, want file final-report", out)
	}
	if out["path"] != "reports/daily/final_report.pptx" {
		t.Fatalf("final_output.path = %v", out["path"])
	}
	if _, ok := out["result"]; ok {
		t.Fatalf("file final_output must not duplicate raw node result: %v", out)
	}
}

func TestCompleteNode_FinalNodeConfiguredSharedfileWithEmptyResultPromotesRunFinalOutput(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "final-report", deps: nil, status: "running", agent: "agent-b"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-report"}`))

	node := db.nodes[dagNodeKey("dag-1", "final-report")]
	node.Config = json.RawMessage(`{
		"exec":{"agent_key":"paper_summarizer"},
		"outputs":{
			"to_sharedfile":{"path":"reports/daily/final_report.pptx","lock_mode":"exclusive"},
			"to_node_result":false
		}
	}`)
	db.nodes[dagNodeKey("dag-1", "final-report")] = node
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-final-configured-empty")

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "final-report",
		RunID:   runID,
		Status:  "done",
		Result:  nil,
	})
	if err != nil {
		t.Fatalf("complete final-report error = %v", err)
	}

	out := finalOutputByRunKey(t, db, "run-final-configured-empty")
	if out["kind"] != "file" || out["role"] != "final_output" || out["source_node_key"] != "final-report" {
		t.Fatalf("final_output identity = %v, want file final-report", out)
	}
	if out["path"] != "reports/daily/final_report.pptx" {
		t.Fatalf("final_output.path = %v", out["path"])
	}
	if _, ok := out["result"]; ok {
		t.Fatalf("file final_output must not duplicate empty node result: %v", out)
	}
}

func TestCompleteNode_FinalNodeJSONPromotesRunFinalOutput(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "final-json", deps: nil, status: "running", agent: "agent-a"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-json"}`))
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-final-json")

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "final-json",
		RunID:   runID,
		Status:  "done",
		Result:  json.RawMessage(`{"summary":"ok","count":2}`),
	})
	if err != nil {
		t.Fatalf("complete final-json error = %v", err)
	}

	out := finalOutputByRunKey(t, db, "run-final-json")
	if out["kind"] != "json" || out["source_node_key"] != "final-json" {
		t.Fatalf("final_output = %v, want json final-json", out)
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("final_output.result = %T, want object", out["result"])
	}
	if result["summary"] != "ok" || result["count"] != float64(2) {
		t.Fatalf("final_output.result = %v", result)
	}
}

func TestCompleteNode_FinalNodeTextPromotesRunFinalOutput(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "final-text", deps: nil, status: "running", agent: "agent-a"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-text"}`))
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-final-text")

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "final-text",
		RunID:   runID,
		Status:  "done",
		Result:  json.RawMessage(`"daily summary ready"`),
	})
	if err != nil {
		t.Fatalf("complete final-text error = %v", err)
	}

	out := finalOutputByRunKey(t, db, "run-final-text")
	if out["kind"] != "text" || out["text"] != "daily summary ready" || out["source_node_key"] != "final-text" {
		t.Fatalf("final_output = %v, want text daily summary ready", out)
	}
}

func TestCompleteNode_NoFinalNodeKeyLeavesRunMetadataUnchanged(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"owner":"ops"}`))
	runID := seedRunWithMetadataAndTemplateNodes(t, db, "dag-1", "run-no-final-key", json.RawMessage(`{"request_id":"req-2"}`))

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: runID, Status: "done", Result: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}

	metadata := runMetadataByKey(t, db, "run-no-final-key")
	if len(metadata) != 1 || metadata["request_id"] != "req-2" {
		t.Fatalf("run metadata = %v, want only original request_id", metadata)
	}
}

func TestCompleteNode_MissingFinalNodeLeavesRunMetadataUnchanged(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
	})
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"missing-final"}`))
	runID := seedRunWithMetadataAndTemplateNodes(t, db, "dag-1", "run-missing-final-node", json.RawMessage(`{"request_id":"req-3"}`))

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: runID, Status: "done", Result: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}

	metadata := runMetadataByKey(t, db, "run-missing-final-node")
	if len(metadata) != 1 || metadata["request_id"] != "req-3" {
		t.Fatalf("run metadata = %v, want only original request_id", metadata)
	}
}

func TestCompleteNode_NonObjectRunMetadataStillPromotesFinalOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		metadata json.RawMessage
	}{
		{name: "null", metadata: json.RawMessage(`null`)},
		{name: "string", metadata: json.RawMessage(`"legacy"`)},
		{name: "array", metadata: json.RawMessage(`["legacy"]`)},
		{name: "number", metadata: json.RawMessage(`7`)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store, db, now := newTaskDAGTestStore()
			seedDAG(t, db, now, []seedNode{
				{key: "final-report", deps: nil, status: "running", agent: "agent-a"},
			})
			seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-report"}`))
			runKey := "run-non-object-" + tt.name
			runID := seedRunWithMetadataAndTemplateNodes(t, db, "dag-1", runKey, tt.metadata)

			_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
				DagKey:  "dag-1",
				NodeKey: "final-report",
				RunID:   runID,
				Status:  "done",
				Result:  json.RawMessage(`{"sharedfile":{"path":"reports/daily/final_report.pptx"}}`),
			})
			if err != nil {
				t.Fatalf("complete final-report error = %v", err)
			}

			metadata := runMetadataByKey(t, db, runKey)
			if len(metadata) != 1 {
				t.Fatalf("run metadata = %v, want only final_output object", metadata)
			}
			out := finalOutputByRunKey(t, db, runKey)
			if out["kind"] != "file" || out["path"] != "reports/daily/final_report.pptx" {
				t.Fatalf("final_output = %v, want file path", out)
			}
		})
	}
}

func TestCompleteNode_FailedRunDoesNotPromoteFinalOutput(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "final-report", deps: nil, status: "done", agent: "agent-a"},
		{key: "failed-node", deps: nil, status: "running", agent: "agent-b"},
	})
	db.nodes[dagNodeKey("dag-1", "final-report")] = withNodeResult(
		db.nodes[dagNodeKey("dag-1", "final-report")],
		json.RawMessage(`{"sharedfile":{"path":"reports/daily/final_report.pptx"}}`),
	)
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"final-report"}`))
	runID := seedRunWithMetadataAndTemplateNodes(t, db, "dag-1", "run-failed-no-final-output", json.RawMessage(`{"request_id":"req-4"}`))

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "failed-node", RunID: runID, Status: "failed", Result: json.RawMessage(`{"error":"boom"}`),
	})
	if err != nil {
		t.Fatalf("complete failed-node error = %v", err)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.Status != "failed" {
		t.Fatalf("FinalizedRun = %+v, want failed", res.FinalizedRun)
	}

	metadata := runMetadataByKey(t, db, "run-failed-no-final-output")
	if len(metadata) != 1 || metadata["request_id"] != "req-4" {
		t.Fatalf("run metadata = %v, want only original request_id", metadata)
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
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-fail")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: runID, Status: "done", Result: json.RawMessage(`{}`),
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

	// failed 优先级高于 cancelled，即使两者并存也必须落到 failed。
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "failed", agent: "agent-a"},
		{key: "B", deps: nil, status: "cancelled", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-fail-over-cancel")

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: runID, Status: "done", Result: json.RawMessage(`{}`),
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
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-cancel")

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: runID, Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-cancel"); got != "cancelled" {
		t.Fatalf("run.status = %q, want cancelled", got)
	}
}

func TestCompleteNode_AllTerminal_DoneAndSkipped_RunSucceeded(t *testing.T) {
	t.Parallel()

	// skipped 是终态但属于成功结果，和 done 并存时仍推进为 succeeded。
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "skipped", agent: "agent-a"},
		{key: "B", deps: nil, status: "done", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-skip-success")

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: runID, Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := runStatusByKey(t, db, "run-skip-success"); got != "succeeded" {
		t.Fatalf("run.status = %q, want succeeded (skipped counts as success)", got)
	}
}

func TestCompleteNode_NotAllTerminal_RunUnchanged(t *testing.T) {
	t.Parallel()

	// 仍有 pending 节点时不能推进 run.status，避免提前关闭后续调度。
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: nil, status: "pending", agent: "agent-b"},
		{key: "C", deps: nil, status: "running", agent: "agent-c"},
	})
	runID := seedRunWithTemplateNodes(t, db, "dag-1", "run-still-running")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: runID, Status: "done", Result: json.RawMessage(`{}`),
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
