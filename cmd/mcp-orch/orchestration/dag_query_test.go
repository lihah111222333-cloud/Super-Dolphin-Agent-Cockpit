package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// makeGetRunService 构造测试用 service：仅注入 runStore（GetRun 只用 runStore）�?
// makeGetRunService builds the service under test for GetRun, wiring only the
// runStore (GetRun does not touch dagStore).
func makeGetRunService(runStore taskdag.RunStore) *service {
	// dagStore 也注入是为了�?service.dagStore != nil 通过初期防御检查，
	// �?makeStartDAGService 保持一致�?
	// dagStore is also wired so that service.dagStore != nil passes any future
	// defensive checks; mirrors makeStartDAGService.
	return &service{dagStore: &stubStartDAGStore{}, runStore: runStore}
}

// ---- happy path ----
func TestGetRun_HappyPath(t *testing.T) {
	fixture := newGetRunHappyFixture()
	stub := &stubRunStore{getRunReply: fixture.srcRun}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if err != nil {
		t.Fatalf("GetRun() error = %v, want nil", err)
	}
	assertGetRunStoreCall(t, stub, "dag-1#run-abc")
	assertGetRunHappyDTO(t, resp.Run, fixture)
	assertBudgetLimitDefensiveCopy(t, &resp.Run, fixture.srcRun)
}

type getRunHappyFixture struct {
	now      time.Time
	finished time.Time
	srcRun   *taskdag.Run
}

func newGetRunHappyFixture() getRunHappyFixture {
	now := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	finished := now.Add(2 * time.Minute)
	budgetLimit := int64(100)
	return getRunHappyFixture{
		now:      now,
		finished: finished,
		srcRun: &taskdag.Run{
			ID:                 42,
			RunKey:             "dag-1#run-abc",
			DagKey:             "dag-1",
			DagVersionSnapshot: 7,
			TriggerSource:      "manual",
			Status:             "running",
			StartedAt:          now,
			FinishedAt:         &finished,
			Events:             json.RawMessage(`[]`),
			BudgetUsed:         3,
			BudgetLimit:        &budgetLimit,
			Metadata:           json.RawMessage(`{"x":1}`),
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
}

func assertGetRunStoreCall(t *testing.T, stub *stubRunStore, want string) {
	t.Helper()
	if got := len(stub.getRunCalls); got != 1 {
		t.Fatalf("getRunCalls = %v, want one call", stub.getRunCalls)
	}
	if stub.getRunCalls[0] != want {
		t.Fatalf("getRunCalls = %v, want [%q]", stub.getRunCalls, want)
	}
}

func assertGetRunHappyDTO(t *testing.T, run contract.Run, fixture getRunHappyFixture) {
	t.Helper()
	assertGetRunIdentity(t, run)
	assertGetRunScalars(t, run)
	if !run.StartedAt.Equal(fixture.now) {
		t.Errorf("resp.Run.StartedAt = %v, want %v", run.StartedAt, fixture.now)
	}
	if run.FinishedAt == nil || !run.FinishedAt.Equal(fixture.finished) {
		t.Errorf("resp.Run.FinishedAt = %v, want %v", run.FinishedAt, fixture.finished)
	}
}

func assertGetRunIdentity(t *testing.T, run contract.Run) {
	t.Helper()
	if run.ID != 42 {
		t.Errorf("resp.Run.ID = %d, want 42", run.ID)
	}
	if run.RunKey != "dag-1#run-abc" {
		t.Errorf("resp.Run.RunKey = %q, want dag-1#run-abc", run.RunKey)
	}
	if run.DagKey != "dag-1" {
		t.Errorf("resp.Run.DagKey = %q, want dag-1", run.DagKey)
	}
}

func assertGetRunScalars(t *testing.T, run contract.Run) {
	t.Helper()
	if run.DagVersionSnapshot != 7 {
		t.Errorf("resp.Run.DagVersionSnapshot = %d, want 7", run.DagVersionSnapshot)
	}
	if run.TriggerSource != "manual" {
		t.Errorf("resp.Run.TriggerSource = %q, want manual", run.TriggerSource)
	}
	if run.Status != "running" {
		t.Errorf("resp.Run.Status = %q, want running", run.Status)
	}
}

func assertBudgetLimitDefensiveCopy(t *testing.T, run *contract.Run, srcRun *taskdag.Run) {
	t.Helper()
	// BudgetLimit cloneInt64 独立性断言：修改源 row 指向�?int64，dto 拷贝不应变�?
	// BudgetLimit defensive-copy assertion: mutating the source pointer's value
	// must not leak into the DTO.
	if run.BudgetLimit == nil || *run.BudgetLimit != 100 {
		t.Fatalf("resp.Run.BudgetLimit = %v, want *=100", run.BudgetLimit)
	}
	*srcRun.BudgetLimit = 999
	if *run.BudgetLimit != 100 {
		t.Errorf("BudgetLimit shared pointer with store row: dto=%d after src mutated to 999", *run.BudgetLimit)
	}
}

func TestGetRun_IncludesRuntimeNodesForRun(t *testing.T) {
	now := time.Date(2026, 5, 14, 8, 0, 0, 0, time.UTC)
	runID := int64(9001)
	stub := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:                 runID,
			RunKey:             "dag-1#run-9001",
			DagKey:             "dag-1",
			DagVersionSnapshot: 12,
			Status:             "running",
			StartedAt:          now,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		listRunNodesReply: []taskdag.Node{
			{
				ID:        1,
				DagKey:    "dag-1",
				NodeKey:   "n1",
				RunID:     &runID,
				Title:     "N1",
				Status:    "ready",
				DependsOn: testRawConfig(t, `[]`),
				Config:    testRawConfig(t, `{"exec":{"agent_key":"coder","cwd":"/tmp/node-cwd"}}`),
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-9001"})
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got := stub.listRunNodesCalls; len(got) != 1 || got[0].DagKey != "dag-1" || got[0].RunID != runID {
		t.Fatalf("ListRunNodes calls = %+v, want dag-1 run_id=%d", got, runID)
	}
	if len(resp.Nodes) != 1 {
		t.Fatalf("resp.Nodes len = %d, want 1 runtime node", len(resp.Nodes))
	}
	if got := resp.Nodes[0]; got.NodeKey != "n1" || got.Status != "ready" {
		t.Fatalf("resp.Nodes[0] = %+v, want n1 ready", got)
	}
}

func TestGetDAG_IncludesCurrentVersionForApplyOps(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 7,
		nodes: []taskdag.Node{{
			DagKey:    "dag-1",
			NodeKey:   "draft",
			Title:     "Draft",
			NodeType:  "agent",
			Status:    "pending",
			DependsOn: testRawConfig(t, `[]`),
			Config:    testRawConfig(t, `{"exec":{"agent_key":"writer"}}`),
		}},
	}
	stub.dagStatus = "ready"
	svc := &service{dagStore: stub}

	resp, err := svc.GetDAG(context.Background(), "dag-1")
	if err != nil {
		t.Fatalf("GetDAG() error = %v", err)
	}
	if resp.DAG.Version != 7 {
		t.Fatalf("GetDAG().DAG.Version = %d, want 7", resp.DAG.Version)
	}
	if stub.getVersionReadCalls != 2 {
		t.Fatalf("GetDAGVersion calls = %d, want 2", stub.getVersionReadCalls)
	}
}

func TestGetDAG_RejectsVersionChangedDuringDetailLoad(t *testing.T) {
	stub := &stubDAGOpsStore{
		versionReads: []int64{7, 8},
		nodes: []taskdag.Node{{
			DagKey:    "dag-1",
			NodeKey:   "draft",
			Title:     "Draft",
			NodeType:  "agent",
			Status:    "pending",
			DependsOn: testRawConfig(t, `[]`),
			Config:    testRawConfig(t, `{"exec":{"agent_key":"writer"}}`),
		}},
	}
	stub.dagStatus = "ready"
	svc := &service{dagStore: stub}

	_, err := svc.GetDAG(context.Background(), "dag-1")
	if err == nil {
		t.Fatalf("GetDAG() error = nil, want version change error")
	}
	if !strings.Contains(err.Error(), "dag detail version changed") {
		t.Fatalf("GetDAG() error = %v, want detail version changed", err)
	}
	if stub.getVersionReadCalls != 2 {
		t.Fatalf("GetDAGVersion calls = %d, want 2", stub.getVersionReadCalls)
	}
}

func TestDAGSummaryDTO_ExposesExplicitScheduleEnabled(t *testing.T) {
	nextRunAt := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)

	active := dagSummaryDTO(taskdag.DAG{
		DagKey:    "daily-ai-essay",
		Trigger:   "scheduled",
		CronExpr:  "CRON_TZ=Asia/Shanghai 0 8 * * *",
		NextRunAt: &nextRunAt,
	})
	if !active.ScheduleEnabled {
		t.Fatalf("active.ScheduleEnabled = false, want true")
	}

	paused := dagSummaryDTO(taskdag.DAG{
		DagKey:   "daily-ai-essay",
		Trigger:  "scheduled",
		CronExpr: "CRON_TZ=Asia/Shanghai 0 8 * * *",
	})
	if paused.ScheduleEnabled {
		t.Fatalf("paused.ScheduleEnabled = true, want false")
	}
	wire, err := json.Marshal(paused)
	if err != nil {
		t.Fatalf("json.Marshal(paused) error = %v", err)
	}
	if !strings.Contains(string(wire), `"schedule_enabled":false`) {
		t.Fatalf("paused DAG JSON = %s, want explicit schedule_enabled=false", wire)
	}
	if strings.Contains(string(wire), `"next_run_at"`) {
		t.Fatalf("paused DAG JSON = %s, want next_run_at omitted when nil", wire)
	}
}

func TestUpdateNodeParams_UnmarshalLegacyRunIDAlias(t *testing.T) {
	var params updateNodeParams
	if err := json.Unmarshal(testRawConfig(t, `{"dagKey":"dag-1","nodeKey":"n1","runId":77,"status":"done"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal(updateNodeParams) error = %v", err)
	}
	req := updateNodeRequestFromParams(params)
	if req.DagKey != "dag-1" || req.NodeKey != "n1" || req.RunID != 77 || req.Status != "done" {
		t.Fatalf("updateNodeRequestFromParams = %+v, want dag-1/n1 run_id=77 status=done", req)
	}
}

// ---- run_key 缺失 �?required 校验 ----
func TestGetRun_BlankRunKey_Rejected(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "   "})
	if err == nil {
		t.Fatalf("GetRun() error = nil, want required error")
	}
	if got := len(stub.getRunCalls); got != 0 {
		t.Errorf("GetRun should short-circuit before runStore call, got %d calls", got)
	}
	if errors.Is(err, ErrRunNotFound) {
		t.Errorf("err = %v should NOT match ErrRunNotFound for blank input", err)
	}
	if resp.Run.RunKey != "" {
		t.Errorf("resp.Run.RunKey = %q, want empty on validation failure", resp.Run.RunKey)
	}
}

// ---- runStore 未注�?�?ErrRunStoreUnset ----
func TestGetRun_RunStoreUnset(t *testing.T) {
	svc := &service{} // �?service，runStore == nil
	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if !errors.Is(err, ErrRunStoreUnset) {
		t.Fatalf("GetRun() error = %v, want errors.Is(ErrRunStoreUnset)", err)
	}
}

// ---- service 本身�?nil �?ErrRunStoreUnset（与 ListRuns / StartDAG 一致） ----
// ---- nil service �?ErrRunStoreUnset (matches ListRuns / StartDAG defense) ----

func TestGetRun_NilService_ReturnsErrRunStoreUnset(t *testing.T) {
	_, err := (*service)(nil).GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if !errors.Is(err, ErrRunStoreUnset) {
		t.Fatalf("(*service)(nil).GetRun() error = %v, want errors.Is(ErrRunStoreUnset)", err)
	}
}

// ---- IsNotFound 域错�?�?ErrRunNotFound ----
// pgx.ErrNoRows �?platformdb.IsNotFound 命中路径，service 应包�?ErrRunNotFound�?
// pgx.ErrNoRows is IsNotFound-matched; service must wrap it into
// ErrRunNotFound so the tool layer can translate to bilingual MCP error.
func TestGetRun_NotFound_WrapsErrRunNotFound(t *testing.T) {
	stub := &stubRunStore{getRunErr: pgx.ErrNoRows}
	svc := makeGetRunService(stub)

	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-missing"})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun() error = %v, want errors.Is(ErrRunNotFound)", err)
	}
}

// ---- runStore (nil run, nil err) 防御兜底 �?ErrRunNotFound ----
// 实际 store 实现不会�?(nil, nil)，但 service 的防御分支必须可达�?
// Real store impls never return (nil, nil), but the service's defensive
// branch must remain reachable for future regressions.
func TestGetRun_NilRunNilErr_DefensiveNotFound(t *testing.T) {
	stub := &stubRunStore{getRunReply: nil, getRunErr: nil}
	svc := makeGetRunService(stub)

	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun() error = %v, want errors.Is(ErrRunNotFound) for defensive (nil,nil) path", err)
	}
}

// ---- �?NotFound 错误透传，不误转�?ErrRunNotFound ----
// Generic IO error must NOT be silently mapped to ErrRunNotFound.
func TestGetRun_OtherError_PassThrough(t *testing.T) {
	boom := errors.New("connection lost")
	stub := &stubRunStore{getRunErr: boom}
	svc := makeGetRunService(stub)

	_, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if err == nil {
		t.Fatalf("GetRun() error = nil, want pass-through error")
	}
	if errors.Is(err, ErrRunNotFound) {
		t.Errorf("err = %v should NOT match ErrRunNotFound for non-NotFound error", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want errors.Is(connection lost)", err)
	}
}

// ---- DTO 转换：Events / Metadata defensive copy ----
// service �?dagRunDTO 应做 RawMessage 防御拷贝，避�?caller 修改 events
// 渗回 store 缓存�?
// dagRunDTO must defensively copy RawMessage fields so callers cannot mutate
// store-backed slices.
func TestGetRun_DTO_DefensiveCopiesRawMessages(t *testing.T) {
	events := testRawConfig(t, `[{"k":"v"}]`)
	metadata := testRawConfig(t, `{"a":1}`)
	stub := &stubRunStore{
		getRunReply: &taskdag.Run{
			RunKey:   "dag-1#run-abc",
			DagKey:   "dag-1",
			Status:   "running",
			Events:   events,
			Metadata: metadata,
		},
	}
	svc := makeGetRunService(stub)

	resp, err := svc.GetRun(context.Background(), contract.GetRunRequest{RunKey: "dag-1#run-abc"})
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if string(resp.Run.Events) != string(events) || string(resp.Run.Metadata) != string(metadata) {
		t.Fatalf("resp.Run events/metadata content mismatch")
	}
	// �?caller 拿到�?byte：原 store 行不应被影响�?
	if len(resp.Run.Events) > 0 {
		resp.Run.Events[0] = 'X'
		if events[0] == 'X' {
			t.Errorf("dagRunDTO did not defensively copy Events; mutation leaked back to store row")
		}
	}
	if len(resp.Run.Metadata) > 0 {
		resp.Run.Metadata[0] = 'X'
		if metadata[0] == 'X' {
			t.Errorf("dagRunDTO did not defensively copy Metadata; mutation leaked back to store row")
		}
	}
}

// stubRunStore.ListRuns �?dag_start_test.go 之外补上，覆�?T3.2 service.ListRuns
// 单元测试需要的行为定制。listRunsReply / listRunsErr / listRunsCalls /
// listRunsLastFilter 字段定义�?dag_start_test.go �?stubRunStore struct
// 上，本文件只提供�?ListRuns 方法实现，避免包�?var 造成的串话并发隐患�?
//
// stubRunStore.ListRuns is implemented in this file (instead of
// dag_start_test.go) so this file owns the T3.2 list_runs tests; the
// behavioural state itself (listRunsReply / listRunsErr / listRunsCalls /
// listRunsLastFilter) lives on the stub struct so tests stay parallel-safe.
func (s *stubRunStore) ListRuns(_ context.Context, filter taskdag.ListRunsFilter) ([]taskdag.Run, error) {
	s.listRunsCalls = append(s.listRunsCalls, filter)
	s.listRunsLastFilter = filter
	if s.listRunsErr != nil {
		return nil, s.listRunsErr
	}
	return s.listRunsReply, nil
}

// ---- happy path：返�?run ----
// ---- happy path: returns multiple runs ----

func TestListRuns_HappyPath(t *testing.T) {
	now := time.Now()
	stub := &stubRunStore{
		listRunsReply: []taskdag.Run{
			{RunKey: "dag-1#run-a", DagKey: "dag-1", Status: "running", StartedAt: now},
			{RunKey: "dag-1#run-b", DagKey: "dag-1", Status: "succeeded", StartedAt: now.Add(-time.Hour)},
		},
	}
	svc := makeStartDAGService(nil, stub)

	resp, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1"})
	if err != nil {
		t.Fatalf("ListRuns() error = %v, want nil", err)
	}
	if got := len(resp.Runs); got != 2 {
		t.Fatalf("ListRuns() runs = %d, want 2", got)
	}
	if resp.Runs[0].RunKey != "dag-1#run-a" {
		t.Errorf("Runs[0].RunKey = %q, want dag-1#run-a", resp.Runs[0].RunKey)
	}
	if got := len(stub.listRunsCalls); got != 1 {
		t.Fatalf("ListRuns calls = %d, want 1", got)
	}
	if stub.listRunsLastFilter.DagKey != "dag-1" {
		t.Errorf("filter.DagKey = %q, want dag-1", stub.listRunsLastFilter.DagKey)
	}
}

// ---- 空结果（无匹配） ----
// ---- empty result (no match) ----

func TestListRuns_EmptyResult(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	resp, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-empty"})
	if err != nil {
		t.Fatalf("ListRuns() error = %v, want nil", err)
	}
	if got := len(resp.Runs); got != 0 {
		t.Errorf("Runs = %d, want 0", got)
	}
}

// ---- status filter 透传 ----
// ---- status filter passthrough ----

func TestListRuns_StatusFilterPassthrough(t *testing.T) {
	stub := &stubRunStore{
		listRunsReply: []taskdag.Run{
			{RunKey: "dag-1#run-x", DagKey: "dag-1", Status: "failed"},
		},
	}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{
		DagKey: "dag-1",
		Status: " failed ", // 验证 service �?strings.TrimSpace
	})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if got := stub.listRunsLastFilter.Status; got != "failed" {
		t.Errorf("filter.Status = %q, want %q (trimmed)", got, "failed")
	}
}

// ---- limit 默认 50（不�?limit�?----
// ---- limit defaults to 50 when omitted ----

func TestListRuns_DefaultLimit(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1"})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if got := stub.listRunsLastFilter.Limit; got != 50 {
		t.Errorf("filter.Limit = %d, want 50 (default)", got)
	}
}

// ---- limit 传值透传（在 max=200 上限内） ----
// ---- limit passthrough within max=200 cap ----

func TestListRuns_ExplicitLimitPassthrough(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1", Limit: 7})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if got := stub.listRunsLastFilter.Limit; got != 7 {
		t.Errorf("filter.Limit = %d, want 7", got)
	}
}

// ---- limit=0 �?ClampLimit 默认�?50 ----
// ---- limit=0 routed through ClampLimit default (50) ----

func TestListRuns_LimitZero_DefaultsToFifty(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1", Limit: 0})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if got := stub.listRunsLastFilter.Limit; got != 50 {
		t.Errorf("filter.Limit = %d, want 50 (ClampLimit 默认)", got)
	}
}

// ---- limit<0 �?ClampLimit 默认�?50 ----
// ---- limit<0 routed through ClampLimit default (50) ----

func TestListRuns_LimitNegative_DefaultsToFifty(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1", Limit: -1})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if got := stub.listRunsLastFilter.Limit; got != 50 {
		t.Errorf("filter.Limit = %d, want 50 (ClampLimit val<min �?default)", got)
	}
}

// ---- limit 超大�?�?service cap �?200 ----
// ---- very large limit �?capped by service to 200 ----
//
// service �?ClampLimit(val, 1, 200, 50) 会把 >200 的调用手推回 200�?
// 避免调用方传 99999999 后透到 SQL 层�?
// service-side ClampLimit(val, 1, 200, 50) caps anything above 200 so a
// 99999999 caller cannot push that limit down to SQL.
func TestListRuns_LimitVeryLarge_CappedToTwoHundred(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1", Limit: 99999999})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if got := stub.listRunsLastFilter.Limit; got != 200 {
		t.Errorf("filter.Limit = %d, want 200 (service-side cap)", got)
	}
}

// ---- runStore == nil 防御�?StartDAG 一�?----
// ---- runStore == nil defense (matches StartDAG) ----

func TestListRuns_RunStoreUnset(t *testing.T) {
	svc := makeStartDAGService(nil, nil)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1"})
	if !errors.Is(err, ErrRunStoreUnset) {
		t.Fatalf("ListRuns() error = %v, want errors.Is(ErrRunStoreUnset)", err)
	}
}

// ---- dag_key 必填 ----
// ---- dag_key required ----

func TestListRuns_DagKeyRequired(t *testing.T) {
	stub := &stubRunStore{}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "  "})
	if err == nil {
		t.Fatalf("ListRuns() error = nil, want non-nil for blank dag_key")
	}
}

// ---- store 错误透传（包装信息含 dag_key�?----
// ---- store error propagated (wrapped with dag_key) ----

func TestListRuns_StoreErrorPropagated(t *testing.T) {
	boom := errors.New("connection lost")
	stub := &stubRunStore{listRunsErr: boom}
	svc := makeStartDAGService(nil, stub)

	_, err := svc.ListRuns(context.Background(), contract.ListRunsRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("ListRuns() error = nil, want non-nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want errors.Is(boom)", err)
	}
}
