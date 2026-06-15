//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// F6.2 鈥?run 缁堟€佸垽瀹氭祴璇曘€?
//
// 瑙﹀彂閾撅細service.UpdateNodeStatus(缁堟€? 鈫?store.CompleteNodeAndScheduleDownstream
// 瀹屾垚鑺傜偣鏇存柊 + 涓嬫父 promote 鈫?maybeFinalizeRun 妫€鏌ユ墍鏈夎妭鐐规槸鍚﹀叏缁堟€侊紝
// 鑻ユ槸鎸変紭鍏堢骇鎶?task_dag_runs.status 浠?'running' 鎺ㄥ埌 缁堟€併€?
//
// 浼樺厛绾э紙鍚箟锛氫粈涔?status 鍗犱富瀵硷級锛?
//   1. 浠绘剰鑺傜偣 failed                  鈫?run.status = failed
//   2. 鍚﹀垯浠绘剰鑺傜偣 cancelled           鈫?run.status = cancelled
//   3. 鍚﹀垯鍏ㄩ儴 done / skipped          鈫?run.status = succeeded
//   4. 鑻ユ湁闈炵粓鎬?pending/ready/running/retrying/waiting_human) 鈫?涓嶅姩 run锛堜粛 running锛?
//
// 0080 status CHECK锛氭灇涓鹃攣瀹?running|succeeded|failed|cancelled锛屾柊鍐欏叆缁堟€佸繀鍦ㄧ櫧鍚嶅崟銆?

// seedRun 鍦?fake DB 涓互 status='running' 鎻掍竴鏉?run 琛岋紝璁?finalize SQL 鎵惧緱鍒扮洰鏍囥€?
// seedRun seeds a task_dag_runs row in status='running' for the fake DB so the
// finalize statement has a target row to flip into a terminal state.
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

	// failed 浼樺厛绾?> cancelled锛屽嵆浣夸袱鑰呭苟瀛樹篃鍒?failed銆?
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

	// skipped 鏄粓鎬佷絾灞炰簬鎴愬姛璇箟锛坥n_failure=skip 鎯呭喌锛夛紱涓?done 骞跺瓨浠嶈蛋 succeeded銆?
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
		t.Fatalf("run.status = %q, want succeeded (skipped 绠楁垚鍔熻涔?", got)
	}
}

func TestCompleteNode_NotAllTerminal_RunUnchanged(t *testing.T) {
	t.Parallel()

	// 杩樻湁 pending 鑺傜偣 鈫?涓嶅簲鎺ㄨ繘 run.status銆?
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
