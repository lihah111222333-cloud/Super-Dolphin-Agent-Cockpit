package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// stubStartDAGStore 实现 service 调到的 OrchestrationStore 子集（GetDAG）。
// 嵌入 nil OrchestrationStore：未覆盖方法 panic 暴露漏覆盖；本测试只用 GetDAG。
type stubStartDAGStore struct {
	taskdag.OrchestrationStore

	dag    *taskdag.DAG
	getErr error
}

func (s *stubStartDAGStore) GetDAG(_ context.Context, _ string) (*taskdag.DAG, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.dag, nil
}

type stubRunStore struct {
	taskdag.RunStore // nil 嵌入：未覆盖方法 panic

	activeCount int64
	activeErr   error

	createReply *taskdag.Run
	createErr   error
	cloneRows   int64
	cloneErr    error
	promoteRows int64
	promoteErr  error

	withTxErr error // 模拟事务整体失败，可传 unique violation 错误驱动幂等 fallback 路径。

	getRunReply *taskdag.Run
	getRunErr   error

	listRunNodesReply []taskdag.Node
	listRunNodesErr   error
	listRunNodesCalls []runNodeCall
	lockedDAG         *taskdag.DAG

	scheduleRootWakeupsRows  int64
	scheduleRootWakeupsErr   error
	scheduleRootWakeupsCalls []runNodeCall
	updateNextRunRows        int64
	updateNextRunErr         error
	updateNextRunCalls       []scheduledNextRunCall
	terminateRunErr          error
	terminateRunResult       taskdag.TerminateRunResult
	terminateRunCalls        []taskdag.TerminateRunInput

	listRunsReply      []taskdag.Run
	listRunsErr        error
	listRunsCalls      []taskdag.ListRunsFilter
	listRunsLastFilter taskdag.ListRunsFilter

	// 调用观测
	countCalls    []string
	lockCalls     []string
	createCalls   []taskdag.CreateRunInput
	cloneCalls    []runNodeCall
	promoteCalls  []string
	promoteRunIDs []int64
	getRunCalls   []string
	callOrder     []string
}

// receiver alias 按 run fixture 角色拆分导出方法，同时保留旧字段和 composite literal 写法。
type stubRunStoreDAGLockFixture = stubRunStore
type stubRunStoreWriteFixture = stubRunStore
type stubRunStoreTxFixture = stubRunStore
type stubRunStoreReadFixture = stubRunStore
type stubRunStoreScheduleFixture = stubRunStore

type runNodeCall struct {
	DagKey string
	RunID  int64
}

type scheduledNextRunCall struct {
	DagKey    string
	DueAt     time.Time
	NextRunAt time.Time
}

func (s *stubRunStoreDAGLockFixture) CountActiveRunsByDagKey(_ context.Context, dagKey string) (int64, error) {
	s.countCalls = append(s.countCalls, dagKey)
	return s.activeCount, s.activeErr
}

func (s *stubRunStoreDAGLockFixture) GetDAGForUpdate(_ context.Context, dagKey string) (*taskdag.DAG, error) {
	s.lockCalls = append(s.lockCalls, dagKey)
	s.callOrder = append(s.callOrder, "lock:"+dagKey)
	if s.lockedDAG != nil {
		return s.lockedDAG, nil
	}
	return &taskdag.DAG{DagKey: dagKey}, nil
}

func (s *stubRunStoreWriteFixture) CreateRun(_ context.Context, input taskdag.CreateRunInput) (*taskdag.Run, error) {
	s.createCalls = append(s.createCalls, input)
	s.callOrder = append(s.callOrder, "create:"+input.DagKey)
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createReply != nil {
		return s.createReply, nil
	}
	return &taskdag.Run{
		ID:                 99,
		RunKey:             input.RunKey,
		DagKey:             input.DagKey,
		DagVersionSnapshot: input.DagVersionSnapshot,
		TriggerSource:      input.TriggerSource,
		Status:             "running",
	}, nil
}

func (s *stubRunStoreWriteFixture) CloneNodesForRun(_ context.Context, dagKey string, runID int64) (int64, error) {
	s.cloneCalls = append(s.cloneCalls, runNodeCall{DagKey: dagKey, RunID: runID})
	s.callOrder = append(s.callOrder, "clone:"+dagKey)
	return s.cloneRows, s.cloneErr
}

func (s *stubRunStoreWriteFixture) PromoteRootNodesToReady(_ context.Context, dagKey string, runID int64) (int64, error) {
	s.promoteCalls = append(s.promoteCalls, dagKey)
	s.promoteRunIDs = append(s.promoteRunIDs, runID)
	s.callOrder = append(s.callOrder, "promote:"+dagKey)
	return s.promoteRows, s.promoteErr
}

func (s *stubRunStoreTxFixture) WithRunTx(ctx context.Context, fn func(taskdag.RunStore) error) error {
	if s.withTxErr != nil {
		return s.withTxErr
	}
	return fn(s) // mock: 同一实例当作 tx-bound 实例
}

func (s *stubRunStoreTxFixture) WithScheduledStartTx(ctx context.Context, fn func(taskdag.ScheduledStartTxStore) error) error {
	if s.withTxErr != nil {
		return s.withTxErr
	}
	return fn(s)
}

func (s *stubRunStoreReadFixture) GetRun(_ context.Context, runKey string) (*taskdag.Run, error) {
	s.getRunCalls = append(s.getRunCalls, runKey)
	if s.getRunErr != nil {
		return nil, s.getRunErr
	}
	return s.getRunReply, nil
}

func (s *stubRunStoreReadFixture) ListRunNodes(_ context.Context, dagKey string, runID int64) ([]taskdag.Node, error) {
	s.listRunNodesCalls = append(s.listRunNodesCalls, runNodeCall{DagKey: dagKey, RunID: runID})
	if s.listRunNodesErr != nil {
		return nil, s.listRunNodesErr
	}
	return s.listRunNodesReply, nil
}

func (s *stubRunStoreWriteFixture) ScheduleRootWakeups(_ context.Context, dagKey string, runID int64) (int64, error) {
	s.scheduleRootWakeupsCalls = append(s.scheduleRootWakeupsCalls, runNodeCall{DagKey: dagKey, RunID: runID})
	s.callOrder = append(s.callOrder, "schedule_roots:"+dagKey)
	if s.scheduleRootWakeupsErr != nil {
		return 0, s.scheduleRootWakeupsErr
	}
	return s.scheduleRootWakeupsRows, nil
}

func (s *stubRunStoreScheduleFixture) UpdateScheduledDAGNextRun(_ context.Context, dagKey string, dueAt, nextRunAt time.Time) (int64, error) {
	s.updateNextRunCalls = append(s.updateNextRunCalls, scheduledNextRunCall{DagKey: dagKey, DueAt: dueAt, NextRunAt: nextRunAt})
	s.callOrder = append(s.callOrder, "update_next_run:"+dagKey)
	if s.updateNextRunErr != nil {
		return 0, s.updateNextRunErr
	}
	if s.updateNextRunRows != 0 {
		return s.updateNextRunRows, nil
	}
	return 1, nil
}

func (s *stubRunStoreWriteFixture) TerminateRun(_ context.Context, input taskdag.TerminateRunInput) (taskdag.TerminateRunResult, error) {
	s.terminateRunCalls = append(s.terminateRunCalls, input)
	s.callOrder = append(s.callOrder, "terminate:"+input.DagKey)
	if platformdb.IsNotFound(s.terminateRunErr) && s.getRunReply != nil {
		s.getRunReply.Status = "cancelled"
	}
	if s.terminateRunErr != nil {
		return taskdag.TerminateRunResult{}, s.terminateRunErr
	}
	return s.terminateRunResult, nil
}

// uniqueViolationErr 生成真实 SQLite UNIQUE 错误，驱动 service 走 GetRun-first fallback 路径。
func uniqueViolationErr(t *testing.T) error {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite unique fixture: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec("CREATE TABLE fixture (value TEXT UNIQUE)"); err != nil {
		t.Fatalf("create sqlite unique fixture: %v", err)
	}
	if _, err := database.Exec("INSERT INTO fixture (value) VALUES ('duplicate')"); err != nil {
		t.Fatalf("seed sqlite unique fixture: %v", err)
	}
	_, err = database.Exec("INSERT INTO fixture (value) VALUES ('duplicate')")
	if err == nil || !platformdb.IsUniqueViolation(err) {
		t.Fatalf("sqlite unique fixture error = %v", err)
	}
	return err
}

// makeStartDAGService 构造测试用 service：仅注入 dagStore + runStore。
func makeStartDAGService(dagStore taskdag.OrchestrationStore, runStore taskdag.RunStore) *service {
	params := dagControllerParams{DAGStore: dagStore, RunStore: runStore}
	if scheduled, ok := runStore.(taskdag.ScheduledStartStore); ok {
		params.ScheduledStartStore = scheduled
	}
	return newDAGTestService(params)
}

// ---- happy path ----

func TestStartDAG_HappyPath(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey:        "dag-1",
		TriggerSource: "manual",
	})
	if err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}
	assertHappyStartDAGResponse(t, resp)
	assertHappyStartDAGStoreCalls(t, runStore)
}

func TestStartDAG_SchedulesRootWakeupsAfterPromote(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{promoteRows: 1, scheduleRootWakeupsRows: 1}
	svc := makeStartDAGService(dagStore, runStore)

	if _, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey:        "dag-1",
		TriggerSource: "manual",
	}); err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}

	if len(runStore.scheduleRootWakeupsCalls) != 1 {
		t.Fatalf("ScheduleRootWakeups calls = %d, want 1", len(runStore.scheduleRootWakeupsCalls))
	}
	if got := runStore.scheduleRootWakeupsCalls[0]; got.DagKey != "dag-1" || got.RunID != 99 {
		t.Fatalf("ScheduleRootWakeups call = %+v, want dag-1 run_id=99", got)
	}
	wantOrder := []string{"lock:dag-1", "create:dag-1", "clone:dag-1", "promote:dag-1", "schedule_roots:dag-1"}
	if strings.Join(runStore.callOrder, "|") != strings.Join(wantOrder, "|") {
		t.Fatalf("callOrder = %v, want %v", runStore.callOrder, wantOrder)
	}
}

func TestStartDAG_UsesLockedDAGVersionSnapshot(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1", Version: 3}}
	runStore := &stubRunStore{lockedDAG: &taskdag.DAG{DagKey: "dag-1", Version: 7}}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey:        "dag-1",
		TriggerSource: "manual",
	})
	if err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}
	if resp.Version != 7 {
		t.Fatalf("resp.Version = %d, want locked dag version 7", resp.Version)
	}
	if len(runStore.createCalls) != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", len(runStore.createCalls))
	}
	if got := runStore.createCalls[0].DagVersionSnapshot; got != 7 {
		t.Fatalf("CreateRun DagVersionSnapshot = %d, want 7", got)
	}
}

func assertHappyStartDAGResponse(t *testing.T, resp StartDAGResponse) {
	t.Helper()
	if resp.RunKey == "" {
		t.Fatalf("resp.RunKey empty, want generated key")
	}
	if !strings.HasPrefix(resp.RunKey, "dag-1#run-") {
		t.Errorf("resp.RunKey = %q, want prefix dag-1#run-", resp.RunKey)
	}
}

func assertHappyStartDAGStoreCalls(t *testing.T, runStore *stubRunStore) {
	t.Helper()
	// service 不再调 CountActiveRunsByDagKey；同 DAG 可并发多 run。
	if got := len(runStore.countCalls); got != 0 {
		t.Errorf("CountActiveRunsByDagKey calls = %d, want 0 (L3: DB-side guard)", got)
	}
	if got := len(runStore.createCalls); got != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", got)
	}
	if runStore.createCalls[0].TriggerSource != "manual" {
		t.Errorf("CreateRun TriggerSource = %q, want 'manual'", runStore.createCalls[0].TriggerSource)
	}
	if got := len(runStore.cloneCalls); got != 1 {
		t.Errorf("CloneNodesForRun calls = %d, want 1", got)
	}
	if len(runStore.cloneCalls) == 1 && runStore.cloneCalls[0].RunID != 99 {
		t.Errorf("CloneNodesForRun run_id = %d, want 99", runStore.cloneCalls[0].RunID)
	}
	if got := len(runStore.promoteCalls); got != 1 {
		t.Errorf("PromoteRootNodesToReady calls = %d, want 1", got)
	}
	if len(runStore.promoteRunIDs) == 1 && runStore.promoteRunIDs[0] != 99 {
		t.Errorf("PromoteRootNodesToReady run_id = %d, want 99", runStore.promoteRunIDs[0])
	}
}

func TestStartDAG_LocksDAGBeforeCreateRun(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}
	want := []string{"lock:dag-1", "create:dag-1", "clone:dag-1", "promote:dag-1", "schedule_roots:dag-1"}
	if strings.Join(runStore.callOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", runStore.callOrder, want)
	}
}

// ---- IdempotencyKey 落到 run_key ----

func TestStartDAG_IdempotencyKey_FlowsIntoRunKey(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey:         "dag-1",
		IdempotencyKey: "abc123",
	})
	if err != nil {
		t.Fatalf("StartDAG() error = %v", err)
	}
	want := "dag-1#run-abc123"
	if resp.RunKey != want {
		t.Errorf("resp.RunKey = %q, want %q", resp.RunKey, want)
	}
}

// 区隔 IdempotencyKey 与 nanos 生成路径：同一 dagKey + 同一 IdempotencyKey 调用
// 两次返回同一 run_key，这是幂等兑底的实际入口（UNIQUE 冲突 → INSERT fail
// → service 包错上传，T1.2-mid 不会跳过）。
func TestGenerateRunKey_IdempotencyKey_DeterministicAcrossCalls(t *testing.T) {
	k1 := generateRunKey("dag-x", "abc")
	k2 := generateRunKey("dag-x", "abc")
	if k1 != k2 {
		t.Errorf("generateRunKey with same idempotency key produced different values: %q vs %q", k1, k2)
	}
}

// ---- DAG 不存在 → ErrDAGNotFound ----

func TestStartDAG_DAGNotFound(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: nil}
	runStore := &stubRunStore{}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "missing"})
	if !errors.Is(err, ErrDAGNotFound) {
		t.Fatalf("StartDAG() error = %v, want errors.Is(ErrDAGNotFound)", err)
	}
	// CountActiveRunsByDagKey / CreateRun 都不应被调
	if got := len(runStore.countCalls); got != 0 {
		t.Errorf("CountActiveRunsByDagKey called %d times, want 0 (short-circuit on dag-not-found)", got)
	}
	if got := len(runStore.createCalls); got != 0 {
		t.Errorf("CreateRun called %d times, want 0", got)
	}
}

// ---- idempotency 命中返已有 run（running 状态继续复用） ----
//
// 同 IdempotencyKey 重入时先遇到 run_key 唯一冲突，再在事务外 GetRun(run_key) 命中已有 running run。
func TestStartDAG_IdempotencyKeyReplay_ReturnsExistingRun(t *testing.T) {
	existing := &taskdag.Run{ID: 123, RunKey: "dag-1#run-abc", DagKey: "dag-1", DagVersionSnapshot: 7, Status: "running"}
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr:               uniqueViolationErr(t),
		getRunReply:             existing, // GetRun 命中 running 后走幂等复用路径。
		scheduleRootWakeupsRows: 1,
	}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey:         "dag-1",
		IdempotencyKey: "abc",
	})
	if err != nil {
		t.Fatalf("StartDAG() idempotent replay error = %v, want nil", err)
	}
	if resp.RunKey != existing.RunKey {
		t.Errorf("resp.RunKey = %q, want existing %q (幂等返已有 run)", resp.RunKey, existing.RunKey)
	}
	if resp.Version != existing.DagVersionSnapshot {
		t.Errorf("resp.Version = %d, want existing %d", resp.Version, existing.DagVersionSnapshot)
	}
	if got := len(runStore.getRunCalls); got != 1 {
		t.Errorf("GetRun fallback calls = %d, want 1", got)
	}
	if len(runStore.scheduleRootWakeupsCalls) != 1 {
		t.Fatalf("ScheduleRootWakeups fallback calls = %d, want 1", len(runStore.scheduleRootWakeupsCalls))
	}
	if got := runStore.scheduleRootWakeupsCalls[0]; got.DagKey != "dag-1" || got.RunID != 123 {
		t.Fatalf("ScheduleRootWakeups fallback call = %+v, want dag-1 run_id=123", got)
	}
}

func TestStartDAG_IdempotencyKeyReplay_RootWakeupFailurePropagates(t *testing.T) {
	existing := &taskdag.Run{ID: 123, RunKey: "dag-1#run-abc", DagKey: "dag-1", DagVersionSnapshot: 7, Status: "running"}
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr:              uniqueViolationErr(t),
		getRunReply:            existing,
		scheduleRootWakeupsErr: errors.New("wakeup store down"),
	}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey:         "dag-1",
		IdempotencyKey: "abc",
	})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want root wakeup scheduling failure")
	}
	if !strings.Contains(err.Error(), "ScheduleRootWakeups existing run") {
		t.Fatalf("StartDAG() error = %q, want ScheduleRootWakeups existing run context", err.Error())
	}
}

// ---- status=succeeded 同样复用 ----
func TestStartDAG_IdempotencyKey_ExistingRunSucceeded_ReturnsExisting(t *testing.T) {
	existing := &taskdag.Run{RunKey: "dag-1#run-abc", DagKey: "dag-1", DagVersionSnapshot: 9, Status: "succeeded"}
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr:   uniqueViolationErr(t),
		getRunReply: existing,
	}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey: "dag-1", IdempotencyKey: "abc",
	})
	if err != nil {
		t.Fatalf("StartDAG() succeeded replay error = %v, want nil (幂等复用成功结果)", err)
	}
	if resp.RunKey != existing.RunKey {
		t.Errorf("resp.RunKey = %q, want existing %q", resp.RunKey, existing.RunKey)
	}
	if resp.Version != existing.DagVersionSnapshot {
		t.Errorf("resp.Version = %d, want %d", resp.Version, existing.DagVersionSnapshot)
	}
}

// ---- status=failed 返 ErrIdempotencyKeyExhausted ----
func TestStartDAG_IdempotencyKey_ExistingRunFailed_ReturnsExhausted(t *testing.T) {
	existing := &taskdag.Run{RunKey: "dag-1#run-abc", DagKey: "dag-1", DagVersionSnapshot: 3, Status: "failed"}
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr:   uniqueViolationErr(t),
		getRunReply: existing,
	}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey: "dag-1", IdempotencyKey: "abc",
	})
	if !errors.Is(err, ErrIdempotencyKeyExhausted) {
		t.Fatalf("StartDAG() error = %v, want errors.Is(ErrIdempotencyKeyExhausted)", err)
	}
	if resp.RunKey != "" {
		t.Errorf("resp.RunKey = %q, want empty when exhausted", resp.RunKey)
	}
	// 富错误详情应携带旧 RunKey + status
	var exhausted *IdempotencyKeyExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("errors.As(*IdempotencyKeyExhaustedError) failed for err=%v", err)
	}
	if exhausted.RunKey != existing.RunKey || exhausted.Status != "failed" {
		t.Errorf("exhausted = {RunKey:%q Status:%q}, want {RunKey:%q Status:\"failed\"}", exhausted.RunKey, exhausted.Status, existing.RunKey)
	}
}

// ---- status=cancelled 返 ErrIdempotencyKeyExhausted ----
func TestStartDAG_IdempotencyKey_ExistingRunCancelled_ReturnsExhausted(t *testing.T) {
	existing := &taskdag.Run{RunKey: "dag-1#run-abc", DagKey: "dag-1", DagVersionSnapshot: 3, Status: "cancelled"}
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr:   uniqueViolationErr(t),
		getRunReply: existing,
	}
	svc := makeStartDAGService(dagStore, runStore)

	resp, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey: "dag-1", IdempotencyKey: "abc",
	})
	if !errors.Is(err, ErrIdempotencyKeyExhausted) {
		t.Fatalf("StartDAG() error = %v, want errors.Is(ErrIdempotencyKeyExhausted)", err)
	}
	if resp.RunKey != "" {
		t.Errorf("resp.RunKey = %q, want empty when exhausted", resp.RunKey)
	}
	var exhausted *IdempotencyKeyExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("errors.As(*IdempotencyKeyExhaustedError) failed for err=%v", err)
	}
	if exhausted.RunKey != existing.RunKey || exhausted.Status != "cancelled" {
		t.Errorf("exhausted = {RunKey:%q Status:%q}, want {RunKey:%q Status:\"cancelled\"}", exhausted.RunKey, exhausted.Status, existing.RunKey)
	}
}

// ---- 未知 status 返防御错误，不静默复用也不当作幂等耗尽 ----
func TestStartDAG_IdempotencyKey_UnknownStatus_ReturnsError(t *testing.T) {
	existing := &taskdag.Run{RunKey: "dag-1#run-abc", DagKey: "dag-1", Status: "weird-state"}
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr:   uniqueViolationErr(t),
		getRunReply: existing,
	}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{
		DagKey: "dag-1", IdempotencyKey: "abc",
	})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want defensive error on unknown status")
	}
	if errors.Is(err, ErrIdempotencyKeyExhausted) {
		t.Errorf("err = %v should NOT match ErrIdempotencyKeyExhausted on unknown status", err)
	}
	if errors.Is(err, ErrDAGAlreadyRunning) {
		t.Errorf("err = %v should NOT match ErrDAGAlreadyRunning on unknown status", err)
	}
	if !strings.Contains(err.Error(), "unexpected run status") {
		t.Errorf("err = %q, want contains 'unexpected run status'", err.Error())
	}
}

// ---- GetRun fallback 本身返非-NotFound 错误 → 包错上传 ----
func TestStartDAG_GetRunFallbackError_PropagatesWithContext(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr: uniqueViolationErr(t),
		getRunErr: errors.New("connection lost during fallback"), // 非 IsNotFound
	}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want fallback error propagation")
	}
	if !strings.Contains(err.Error(), "GetRun fallback") {
		t.Errorf("StartDAG() error = %q, want contains 'GetRun fallback'", err.Error())
	}
	if !strings.Contains(err.Error(), "connection lost during fallback") {
		t.Errorf("StartDAG() error = %q, want contains GetRun 错误原文", err.Error())
	}
}

// ---- GetDAG 返错误（不是 nil dag）→ 包错上传 ----
func TestStartDAG_GetDAGError_PropagatesWithContext(t *testing.T) {
	dagStore := &stubStartDAGStore{getErr: errors.New("db down")}
	runStore := &stubRunStore{}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want GetDAG error propagation")
	}
	if !strings.Contains(err.Error(), "GetDAG") {
		t.Errorf("StartDAG() error = %q, want contains 'GetDAG'", err.Error())
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("StartDAG() error = %q, want contains GetDAG 错误原文", err.Error())
	}
	// CreateRun / GetRun fallback 都不应被调
	if got := len(runStore.createCalls); got != 0 {
		t.Errorf("CreateRun called %d times, want 0 (short-circuit on GetDAG err)", got)
	}
}

// ---- CreateRun 返非-unique-violation 错误 → 事务包错上传不走 fallback ----
func TestStartDAG_CreateRunGenericError_NoFallback(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		createErr: errors.New("disk full"), // 非 PG unique violation
	}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want CreateRun error propagation")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("StartDAG() error = %q, want contains 'disk full'", err.Error())
	}
	// GetRun fallback 不应被调（仅 unique violation 走 fallback）
	if got := len(runStore.getRunCalls); got != 0 {
		t.Errorf("GetRun fallback called %d times, want 0 (only on unique violation)", got)
	}
}

// ---- DB unique 冲突 + GetRun 未命中 → 原始 unique error 透出 ----
//
// run_key 唯一冲突只用于幂等复用；GetRun miss 说明冲突不属于当前 run_key，必须透出原始错误。
func TestStartDAG_DBUniqueViolation_GetRunMissPropagatesOriginal(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		withTxErr: uniqueViolationErr(t),
		getRunErr: platformdb.ErrNotFound, // GetRun 未命中
	}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want unresolved unique violation")
	}
	if errors.Is(err, ErrDAGAlreadyRunning) {
		t.Fatalf("StartDAG() error = %v, should not match legacy ErrDAGAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), "unresolved unique violation") {
		t.Fatalf("StartDAG() error = %v, want unresolved unique violation context", err)
	}
	if got := len(runStore.getRunCalls); got != 1 {
		t.Errorf("GetRun fallback calls = %d, want 1", got)
	}
}

// ---- runStore=nil → ErrRunStoreUnset ----

func TestStartDAG_RunStoreUnsetReturnsSentinel(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	svc := makeStartDAGService(dagStore, nil) // runStore 注入缺失

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if !errors.Is(err, ErrRunStoreUnset) {
		t.Fatalf("StartDAG() error = %v, want errors.Is(ErrRunStoreUnset)", err)
	}
}

// ---- dagKey 空 → 显式错误 ----

func TestStartDAG_EmptyDagKeyRejected(t *testing.T) {
	dagStore := &stubStartDAGStore{}
	runStore := &stubRunStore{}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "  "})
	if err == nil {
		t.Fatalf("StartDAG(empty dag_key) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "dag_key required") {
		t.Errorf("StartDAG(empty dag_key) error = %q, want contains 'dag_key required'", err.Error())
	}
}

// ---- WithRunTx 内 PromoteRootNodesToReady 失败 → 错误传播 ----

func TestStartDAG_PromoteFailureRollsBack(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{
		promoteErr: errors.New("simulated promote failure"),
	}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want propagation of promote failure")
	}
	if !strings.Contains(err.Error(), "PromoteRootNodesToReady") {
		t.Errorf("StartDAG() error = %q, want contains 'PromoteRootNodesToReady'", err.Error())
	}
	// CreateRun 一定被调（Promote 在它后面）
	if got := len(runStore.createCalls); got != 1 {
		t.Errorf("CreateRun calls = %d, want 1", got)
	}
}

// ---- WithRunTx 整体失败 → 错误传播 ----

func TestStartDAG_WithRunTxOuterFailure(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{withTxErr: errors.New("connection lost")}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if err == nil {
		t.Fatalf("StartDAG() error = nil, want propagation of WithRunTx failure")
	}
	if !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("StartDAG() error = %q, want contains 'connection lost'", err.Error())
	}
}

// ---- generateRunKey 单测 ----

func TestGenerateRunKey_WithIdempotency(t *testing.T) {
	got := generateRunKey("dag-x", "abc")
	want := "dag-x#run-abc"
	if got != want {
		t.Errorf("generateRunKey(dag-x, abc) = %q, want %q", got, want)
	}
}

func TestGenerateRunKey_WithoutIdempotency(t *testing.T) {
	got := generateRunKey("dag-x", "")
	if !strings.HasPrefix(got, "dag-x#run-") {
		t.Errorf("generateRunKey(dag-x, '') = %q, want prefix dag-x#run-", got)
	}
	// 生产路径依赖 task_dag_runs.run_key UNIQUE 约束兑底双发冲突，本生成函数
	// 不保证 nanos 唯一。不断言 "两次连续调用不同"——同一纳秒下可能重复。
}
