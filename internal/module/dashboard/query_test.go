package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestServiceQueryPassesThroughAndNormalizesArgs(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	svc := NewService(nil, nil, nil, nil, nil, nil, dbquerystore.NewQueryStore(db, time.Second), nil, nil, nil, nil)

	rows, err := svc.Query(context.Background(), "SELECT * FROM agent_threads WHERE thread_id = $1 AND score > $2", float64(7), float64(1.5))
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["thread_id"] != "thread-1" {
		t.Fatalf("Query() rows = %#v", rows)
	}
	if got, ok := db.args[0].(int64); !ok || got != 7 {
		t.Fatalf("arg[0] = %#v", db.args[0])
	}
	if got, ok := db.args[1].(float64); !ok || got != 1.5 {
		t.Fatalf("arg[1] = %#v", db.args[1])
	}
}

func TestServiceQueryRejectsDangerousSQL(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{}
	svc := NewService(nil, nil, nil, nil, nil, nil, dbquerystore.NewQueryStore(db, time.Second), nil, nil, nil, nil)

	_, err := svc.Query(context.Background(), "SELECT version() FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "disallowed function") {
		t.Fatalf("Query() error = %v", err)
	}
	if db.called {
		t.Fatal("Query() reached DBTX.Query for dangerous SQL")
	}
}

func TestServiceQueryRequiresStore(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Query(context.Background(), "SELECT * FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "dbquery store is not configured") {
		t.Fatalf("Query() error = %v", err)
	}
}

func TestDashboardQueryHandlerRegistered(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := NewDashboardHandlers(svc).Handlers
	if _, ok := handlers["dashboard/query"]; !ok {
		t.Fatalf("dashboard/query handler missing from %#v", handlers)
	}
}

func TestDashboardExtraHandlersRegistered(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := NewDashboardHandlers(svc).Handlers
	for _, method := range []string{
		"dashboard/auditLogs",
		"dashboard/busLogs",
		"dashboard/dags",
		"dashboard/dagDetail",
		"dashboard/dagRuns",
		"dashboard/dagRun",
		"dashboard/dagStart",
		"dashboard/dagTerminate",
		"dashboard/dagDelete",
		"dashboard/dagApplyOps",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("%s handler missing from %#v", method, handlers)
		}
	}
}

func TestDashboardTaskTraceHandlerRemoved(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := NewDashboardHandlers(svc).Handlers
	if _, ok := handlers["dashboard/taskTraces"]; ok {
		t.Fatalf("dashboard/taskTraces handler should be removed from %#v", handlers)
	}
}

func TestDashboardQueryHandlerRejectsDangerousSQL(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{}
	server := newDashboardQueryTestServer(t, db)

	_, err := dispatchDashboardQuery(server, `{"query":"SELECT pg_sleep(1) FROM agent_threads"}`)
	if err == nil || !strings.Contains(err.Error(), "disallowed function") {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if db.called {
		t.Fatal("Dispatch() reached DBTX.Query for dangerous SQL")
	}
}

func TestDashboardQueryHandlerNormalArgs(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	server := newDashboardQueryTestServer(t, db)

	rows, err := dispatchDashboardQuery(server, `{"query":"SELECT * FROM agent_threads WHERE thread_id = $1","args":["thread-1"]}`)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["thread_id"] != "thread-1" {
		t.Fatalf("Dispatch() rows = %#v", rows)
	}
	if len(db.args) != 1 || db.args[0] != "thread-1" {
		t.Fatalf("captured args = %#v", db.args)
	}
}

func TestDashboardQueryHandlerFloat64Normalization(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	server := newDashboardQueryTestServer(t, db)

	_, err := dispatchDashboardQuery(server, `{"query":"SELECT * FROM agent_threads WHERE thread_id = $1","args":[7]}`)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(db.args) != 1 {
		t.Fatalf("len(args) = %d, want 1", len(db.args))
	}
	if got, ok := db.args[0].(int64); !ok || got != 7 {
		t.Fatalf("args[0] = %#v", db.args[0])
	}
}

func TestDashboardAuditAndBusLogHandlersReturnLogs(t *testing.T) {
	t.Parallel()

	auditStore := &stubAuditLogStore{
		listResult: []auditlogstore.AuditEvent{{ID: 7, EventType: "tool", Action: "run"}},
	}
	busStore := &stubBusLogStore{
		listResult: []buslogstore.BusExceptionLog{{ID: 9, Category: "rpc", Severity: "error"}},
	}
	server := newDashboardTestServer(t, &service{auditLogs: auditStore, busLogs: busStore})

	assertDashboardAuditLogs(t, server, auditStore)
	assertDashboardBusLogs(t, server, busStore)
}

func assertDashboardAuditLogs(t *testing.T, server *platformrpc.Server, auditStore *stubAuditLogStore) {
	t.Helper()

	var auditResp struct {
		Logs []auditlogstore.AuditEvent `json:"logs"`
	}
	if err := dispatchDashboardInto(server, "dashboard/auditLogs", `{"eventType":"tool","action":"run","actor":"agent-1","limit":7}`, &auditResp); err != nil {
		t.Fatalf("dispatch audit logs error = %v", err)
	}
	if auditStore.listFilter.EventType != "tool" {
		t.Fatalf("audit filter event type = %#v", auditStore.listFilter)
	}
	if auditStore.listFilter.Action != "run" {
		t.Fatalf("audit filter action = %#v", auditStore.listFilter)
	}
	if auditStore.listFilter.Actor != "agent-1" {
		t.Fatalf("audit filter actor = %#v", auditStore.listFilter)
	}
	if auditStore.listFilter.Limit != 7 {
		t.Fatalf("audit filter = %#v", auditStore.listFilter)
	}
	if len(auditResp.Logs) != 1 {
		t.Fatalf("audit response = %#v", auditResp)
	}
	if auditResp.Logs[0].ID != 7 {
		t.Fatalf("audit response = %#v", auditResp)
	}
}

func assertDashboardBusLogs(t *testing.T, server *platformrpc.Server, busStore *stubBusLogStore) {
	t.Helper()

	var busResp struct {
		Logs []buslogstore.BusExceptionLog `json:"logs"`
	}
	if err := dispatchDashboardInto(server, "dashboard/busLogs", `{"category":"rpc","severity":"error","keyword":"timeout","limit":9}`, &busResp); err != nil {
		t.Fatalf("dispatch bus logs error = %v", err)
	}
	if busStore.listFilter.Category != "rpc" {
		t.Fatalf("bus filter category = %#v", busStore.listFilter)
	}
	if busStore.listFilter.Severity != "error" {
		t.Fatalf("bus filter severity = %#v", busStore.listFilter)
	}
	if busStore.listFilter.Keyword != "timeout" {
		t.Fatalf("bus filter keyword = %#v", busStore.listFilter)
	}
	if busStore.listFilter.Limit != 9 {
		t.Fatalf("bus filter = %#v", busStore.listFilter)
	}
	if len(busResp.Logs) != 1 {
		t.Fatalf("bus response = %#v", busResp)
	}
	if busResp.Logs[0].ID != 9 {
		t.Fatalf("bus response = %#v", busResp)
	}
}

func TestDashboardDAGHandlersReturnData(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Dag One", Status: "running"}},
		dagDetail: contract.DAGDetail{
			DAG:   contract.DAGSummary{DagKey: "dag-1", Title: "Dag One"},
			Nodes: []contract.DAGNode{{NodeKey: "node-1", Title: "Node One"}},
		},
		listRunsResult: contract.ListRunsResponse{
			Runs: []contract.Run{{RunKey: "run-1", DagKey: "dag-1", Status: "succeeded"}},
		},
		getRunResult: contract.GetRunResponse{
			Run:   contract.Run{RunKey: "run-1", DagKey: "dag-1", Status: "succeeded"},
			Nodes: []contract.DAGNode{{NodeKey: "node-1", Title: "Runtime Node", Status: "done"}},
		},
		startDAGResult: contract.StartDAGResponse{
			RunID:            88,
			RunKey:           "dag-1#run-ui",
			Version:          9,
			ReadyRootNodes:   1,
			ScheduledWakeups: 0,
			ExecutionState:   "waiting_for_assignee",
			Warning:          "dispatch required",
		},
		dispatchNodeResult: contract.DispatchNodeResponse{
			Node:     contract.DAGNode{DagKey: "dag-1", NodeKey: "draft", AssignedTo: "codex-runner"},
			WakeupID: 99,
			Enqueued: true,
		},
	}
	server := newDashboardTestServer(t, &service{orchestration: orchestration})

	assertDashboardDAGList(t, server, orchestration)
	assertDashboardDAGDetail(t, server, orchestration)
	assertDashboardDAGRuns(t, server, orchestration)
	assertDashboardDAGRun(t, server, orchestration)
	assertDashboardDAGStart(t, server, orchestration)
	assertDashboardDAGDispatchNode(t, server, orchestration)
	assertDashboardDAGTerminate(t, server, orchestration)
	assertDashboardDAGDelete(t, server, orchestration)
	assertDashboardDAGApplyOps(t, server, orchestration)
}

func assertDashboardDAGList(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var dagsResp struct {
		Dags []contract.DAGSummary `json:"dags"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dags", `{"keyword":"build","status":"running","limit":7}`, &dagsResp); err != nil {
		t.Fatalf("dispatch dags error = %v", err)
	}
	if orchestration.listDAGsFilter.Keyword != "build" {
		t.Fatalf("ListDAGs() keyword filter = %#v", orchestration.listDAGsFilter)
	}
	if orchestration.listDAGsFilter.Status != "running" {
		t.Fatalf("ListDAGs() status filter = %#v", orchestration.listDAGsFilter)
	}
	if orchestration.listDAGsFilter.Limit != 7 {
		t.Fatalf("ListDAGs() filter = %#v", orchestration.listDAGsFilter)
	}
	if len(dagsResp.Dags) != 1 {
		t.Fatalf("dags response = %#v", dagsResp)
	}
	if dagsResp.Dags[0].DagKey != "dag-1" {
		t.Fatalf("dags response = %#v", dagsResp)
	}
}

func assertDashboardDAGDetail(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var detailResp struct {
		DAG   contract.DAGSummary `json:"dag"`
		Nodes []contract.DAGNode  `json:"nodes"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagDetail", `{"dagKey":"dag-1"}`, &detailResp); err != nil {
		t.Fatalf("dispatch dag detail error = %v", err)
	}
	if orchestration.getDAGKey != "dag-1" {
		t.Fatalf("GetDAG() key = %q, want dag-1", orchestration.getDAGKey)
	}
	if detailResp.DAG.DagKey != "dag-1" {
		t.Fatalf("dag detail response = %#v", detailResp)
	}
	if len(detailResp.Nodes) != 1 {
		t.Fatalf("dag detail response = %#v", detailResp)
	}
	if detailResp.Nodes[0].NodeKey != "node-1" {
		t.Fatalf("dag detail response = %#v", detailResp)
	}
}

func assertDashboardDAGRuns(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var runsResp struct {
		Runs []contract.Run `json:"runs"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagRuns", `{"dagKey":"dag-1","status":"running","limit":5}`, &runsResp); err != nil {
		t.Fatalf("dispatch dag runs error = %v", err)
	}
	if orchestration.listRunsRequest.DagKey != "dag-1" {
		t.Fatalf("ListRuns() dag key request = %#v", orchestration.listRunsRequest)
	}
	if orchestration.listRunsRequest.Status != "running" || orchestration.listRunsRequest.Limit != 5 {
		t.Fatalf("ListRuns() request = %#v", orchestration.listRunsRequest)
	}
	if len(runsResp.Runs) != 1 {
		t.Fatalf("dag runs response = %#v", runsResp)
	}
	if runsResp.Runs[0].RunKey != "run-1" {
		t.Fatalf("dag runs response = %#v", runsResp)
	}
}

func assertDashboardDAGRun(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var runResp contract.GetRunResponse
	if err := dispatchDashboardInto(server, "dashboard/dagRun", `{"runKey":"run-1"}`, &runResp); err != nil {
		t.Fatalf("dispatch dag run error = %v", err)
	}
	if orchestration.getRunRequest.RunKey != "run-1" {
		t.Fatalf("GetRun() request = %#v", orchestration.getRunRequest)
	}
	if runResp.Run.RunKey != "run-1" {
		t.Fatalf("dag run response = %#v", runResp)
	}
	if len(runResp.Nodes) != 1 || runResp.Nodes[0].Status != "done" {
		t.Fatalf("dag run response nodes = %#v", runResp.Nodes)
	}
}

func assertDashboardDAGStart(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var startResp struct {
		RunID            int64  `json:"runId"`
		RunKey           string `json:"runKey"`
		Version          int64  `json:"version"`
		ReadyRootNodes   int64  `json:"readyRootNodes"`
		ScheduledWakeups int64  `json:"scheduledWakeups"`
		ExecutionState   string `json:"executionState"`
		Warning          string `json:"warning"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagStart", `{"dagKey":"dag-1","triggerSource":"manual","idempotencyKey":"ui-123"}`, &startResp); err != nil {
		t.Fatalf("dispatch dag start error = %v", err)
	}
	if startResp.RunKey != "dag-1#run-ui" || startResp.Version != 9 {
		t.Fatalf("dag start response = %#v", startResp)
	}
	if startResp.RunID != 88 {
		t.Fatalf("dag start runId = %d, want 88", startResp.RunID)
	}
	if startResp.ReadyRootNodes != 1 {
		t.Fatalf("dag start readyRootNodes = %d, want 1", startResp.ReadyRootNodes)
	}
	if startResp.ScheduledWakeups != 0 {
		t.Fatalf("dag start scheduledWakeups = %d, want 0", startResp.ScheduledWakeups)
	}
	if startResp.ExecutionState != "waiting_for_assignee" {
		t.Fatalf("dag start executionState = %q, want waiting_for_assignee", startResp.ExecutionState)
	}
	if startResp.Warning != "dispatch required" {
		t.Fatalf("dag start warning = %q, want dispatch required", startResp.Warning)
	}
	if orchestration.startDAGRequest != (contract.StartDAGRequest{DagKey: "dag-1", TriggerSource: "manual", IdempotencyKey: "ui-123"}) {
		t.Fatalf("StartDAG() request = %#v", orchestration.startDAGRequest)
	}
}

func assertDashboardDAGTerminate(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	if err := dispatchDashboardInto(server, "dashboard/dagTerminate", `{"dagKey":"dag-1","runKey":"run-1","reason":"user_requested"}`, &struct{}{}); err != nil {
		t.Fatalf("dispatch dag terminate error = %v", err)
	}
	if orchestration.terminateDAGRequest != (contract.TerminateDAGRequest{DagKey: "dag-1", RunKey: "run-1", Reason: "user_requested"}) {
		t.Fatalf("TerminateDAG() request = %#v", orchestration.terminateDAGRequest)
	}
}

func assertDashboardDAGDispatchNode(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	var resp contract.DispatchNodeResponse
	if err := dispatchDashboardInto(server, "dashboard/dagDispatchNode", `{"dagKey":"dag-1","runId":88,"nodeKey":"draft","assignedTo":"codex-runner"}`, &resp); err != nil {
		t.Fatalf("dispatch dag node error = %v", err)
	}
	if orchestration.dispatchNodeRequest != (contract.DispatchNodeRequest{DagKey: "dag-1", RunID: 88, NodeKey: "draft", AssignedTo: "codex-runner"}) {
		t.Fatalf("DispatchNode() request = %#v", orchestration.dispatchNodeRequest)
	}
	if !resp.Enqueued || resp.WakeupID != 99 || resp.Node.AssignedTo != "codex-runner" {
		t.Fatalf("dag dispatch node response = %#v", resp)
	}
}

func TestDashboardDAGDispatchNodeRequiresRuntimeRunID(t *testing.T) {
	t.Parallel()

	server := newDashboardTestServer(t, &service{orchestration: &stubDashboardOrchestration{}})

	var resp contract.DispatchNodeResponse
	err := dispatchDashboardInto(server, "dashboard/dagDispatchNode", `{"dagKey":"dag-1","nodeKey":"draft","assignedTo":"codex-runner"}`, &resp)
	if err == nil || !strings.Contains(err.Error(), "runId is required") {
		t.Fatalf("dispatch dag node error = %v, want runId required", err)
	}
}

func TestDashboardDAGDispatchNodeRequiresAssignedTo(t *testing.T) {
	t.Parallel()

	server := newDashboardTestServer(t, &service{orchestration: &stubDashboardOrchestration{}})

	var resp contract.DispatchNodeResponse
	err := dispatchDashboardInto(server, "dashboard/dagDispatchNode", `{"dagKey":"dag-1","runId":88,"nodeKey":"draft"}`, &resp)
	if err == nil || !strings.Contains(err.Error(), "assignedTo is required") {
		t.Fatalf("dispatch dag node error = %v, want assignedTo required", err)
	}
}

func TestDashboardDAGDispatchNodeRejectsUnknownFieldBeforeServiceCall(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{}
	server := newDashboardTestServer(t, &service{orchestration: orchestration})

	var params dagDispatchNodeParams
	if err := json.Unmarshal([]byte(`{"dagKey":"dag-1","runId":88,"nodeKey":"draft","assignedTo":"codex-runner","unexpectedUiOnlyField":"leak"}`), &params); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("dagDispatchNodeParams decode error = %v, want unknown field rejection", err)
	}

	var resp contract.DispatchNodeResponse
	err := dispatchDashboardInto(server, "dashboard/dagDispatchNode", `{"dagKey":"dag-1","runId":88,"nodeKey":"draft","assignedTo":"codex-runner","unexpectedUiOnlyField":"leak"}`, &resp)
	if err == nil || !strings.Contains(err.Error(), "invalid parameters") {
		t.Fatalf("dispatch dag node error = %v, want invalid parameters", err)
	}
	if orchestration.dispatchNodeRequest != (contract.DispatchNodeRequest{}) {
		t.Fatalf("DispatchNode() request = %#v, want decode rejection before service call", orchestration.dispatchNodeRequest)
	}
}

func assertDashboardDAGDelete(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	if err := dispatchDashboardInto(server, "dashboard/dagDelete", `{"dagKey":"dag-1"}`, &struct{}{}); err != nil {
		t.Fatalf("dispatch dag delete error = %v", err)
	}
	if orchestration.deleteDAGRequest != (contract.DeleteDAGRequest{DagKey: "dag-1"}) {
		t.Fatalf("DeleteDAG() request = %#v", orchestration.deleteDAGRequest)
	}
}

func assertDashboardDAGApplyOps(t *testing.T, server *platformrpc.Server, orchestration *stubDashboardOrchestration) {
	t.Helper()

	orchestration.applyOpsResult = contract.ApplyOpsResponse{NewVersion: 12}
	var resp struct {
		NewVersion int64 `json:"newVersion"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagApplyOps", `{"dagKey":"dag-1","baseVersion":11,"ops":[{"op":"update_node","node_key":"draft","patch":{"title":"Draft v2"}}]}`, &resp); err != nil {
		t.Fatalf("dispatch dag apply ops error = %v", err)
	}
	assertDashboardDAGApplyOpsResponse(t, resp.NewVersion)
	assertDashboardDAGApplyOpsRequest(t, orchestration.applyOpsRequest)
}

func assertDashboardDAGApplyOpsResponse(t *testing.T, newVersion int64) {
	t.Helper()

	if newVersion != 12 {
		t.Fatalf("dag apply ops newVersion = %d, want 12", newVersion)
	}
}

func assertDashboardDAGApplyOpsRequest(t *testing.T, req contract.ApplyOpsRequest) {
	t.Helper()

	if req.DagKey != "dag-1" {
		t.Fatalf("ApplyOps() request = %#v", req)
	}
	if req.BaseVersion != 11 {
		t.Fatalf("ApplyOps() request = %#v", req)
	}
	if string(req.Ops) != `[{"op":"update_node","node_key":"draft","patch":{"title":"Draft v2"}}]` {
		t.Fatalf("ApplyOps() ops = %s", req.Ops)
	}
}

func TestDashboardDAGApplyOpsRequiresBaseVersion(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{
		applyOpsResult: contract.ApplyOpsResponse{NewVersion: 1},
	}
	server := newDashboardTestServer(t, &service{orchestration: orchestration})

	if err := dispatchDashboardInto(server, "dashboard/dagApplyOps", `{"dagKey":"dag-1","ops":[{"op":"update_node","node_key":"draft","patch":{"title":"Draft"}}]}`, &struct{}{}); err == nil || !strings.Contains(err.Error(), "baseVersion") {
		t.Fatalf("dispatch missing baseVersion error = %v, want baseVersion required", err)
	}
	if orchestration.applyOpsCalled {
		t.Fatalf("ApplyOps called for request missing baseVersion")
	}

	var resp struct {
		NewVersion int64 `json:"newVersion"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagApplyOps", `{"dagKey":"dag-1","baseVersion":0,"ops":[{"op":"update_node","node_key":"draft","patch":{"title":"Draft"}}]}`, &resp); err != nil {
		t.Fatalf("dispatch explicit zero baseVersion error = %v", err)
	}
	if orchestration.applyOpsRequest.BaseVersion != 0 {
		t.Fatalf("ApplyOps() baseVersion = %d, want 0", orchestration.applyOpsRequest.BaseVersion)
	}
}

func TestDashboardDAGHandlersReturnServiceNotAvailableWithoutOrchestration(t *testing.T) {
	t.Parallel()

	server := newDashboardTestServer(t, &service{})

	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dags", `{"keyword":"build"}`)
	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dagDetail", `{"dagKey":"dag-1"}`)
	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dagRun", `{"runKey":"run-1"}`)
	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dagStart", `{"dagKey":"dag-1"}`)
	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dagTerminate", `{"dagKey":"dag-1","runKey":"run-1"}`)
	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dagDelete", `{"dagKey":"dag-1"}`)
	assertDashboardMethodServiceUnavailable(t, server, "dashboard/dagApplyOps", `{"dagKey":"dag-1","baseVersion":1,"ops":[]}`)
}

func assertDashboardMethodServiceUnavailable(t *testing.T, server *platformrpc.Server, method, payload string) {
	t.Helper()

	if err := dispatchDashboardInto(server, method, payload, &struct{}{}); err == nil || !strings.Contains(err.Error(), "service not available") {
		t.Fatalf("dispatch %s error = %v, want service not available", method, err)
	}
}

func newDashboardQueryTestServer(t *testing.T, db *queryDBTXStub) *platformrpc.Server {
	t.Helper()

	svc := NewService(nil, nil, nil, nil, nil, nil, dbquerystore.NewQueryStore(db, time.Second), nil, nil, nil, nil)
	return newDashboardTestServer(t, svc)
}

func newDashboardTestServer(t *testing.T, svc Service) *platformrpc.Server {
	t.Helper()

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)
	return server
}

func dispatchDashboardQuery(server *platformrpc.Server, raw string) ([]map[string]any, error) {
	var rows []map[string]any
	if err := dispatchDashboardInto(server, "dashboard/query", raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func dispatchDashboardInto(server *platformrpc.Server, method, raw string, out any) error {
	result, err := server.Dispatch(context.Background(), method, json.RawMessage(raw))
	if err != nil {
		return err
	}
	return json.Unmarshal(result, out)
}

type queryDBTXStub struct {
	args   []any
	called bool
	rows   pgx.Rows
	err    error
}

func (s *queryDBTXStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("queryDBTXStub: exec not implemented")
}

func (s *queryDBTXStub) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	s.called = true
	s.args = append([]any(nil), args...)
	if s.err != nil {
		return nil, s.err
	}
	if s.rows != nil {
		return s.rows, nil
	}
	return &queryRowsStub{}, nil
}

func (s *queryDBTXStub) QueryRow(context.Context, string, ...any) pgx.Row {
	return queryRowStub{}
}

type queryRowStub struct{}

func (queryRowStub) Scan(...any) error { return errors.New("queryRowStub: scan not implemented") }

type queryRowsStub struct {
	fields []pgconn.FieldDescription
	values [][]any
	index  int
	err    error
}

func (r *queryRowsStub) Close() { _ = r }

func (r *queryRowsStub) Err() error { return r.err }

func (r *queryRowsStub) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *queryRowsStub) FieldDescriptions() []pgconn.FieldDescription { return r.fields }

func (r *queryRowsStub) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *queryRowsStub) Scan(...any) error { return errors.New("queryRowsStub: scan not implemented") }

func (r *queryRowsStub) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.values) {
		return nil, errors.New("queryRowsStub: invalid cursor")
	}
	return append([]any(nil), r.values[r.index-1]...), nil
}

func (r *queryRowsStub) RawValues() [][]byte { return nil }

func (r *queryRowsStub) Conn() *pgx.Conn { return nil }
