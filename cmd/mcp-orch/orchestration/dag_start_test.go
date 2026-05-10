package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
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

// stubRunStore 实现 RunStore，支持 happy / 错误 / WithRunTx 行为定制。
type stubRunStore struct {
	taskdag.RunStore // nil 嵌入：未覆盖方法 panic

	activeCount int64
	activeErr   error

	createReply *taskdag.Run
	createErr   error
	promoteRows int64
	promoteErr  error

	withTxErr error // 模拟事务整体失败

	// 调用观测
	countCalls   []string
	createCalls  []taskdag.CreateRunInput
	promoteCalls []string
}

func (s *stubRunStore) CountActiveRunsByDagKey(_ context.Context, dagKey string) (int64, error) {
	s.countCalls = append(s.countCalls, dagKey)
	return s.activeCount, s.activeErr
}

func (s *stubRunStore) CreateRun(_ context.Context, input taskdag.CreateRunInput) (*taskdag.Run, error) {
	s.createCalls = append(s.createCalls, input)
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createReply != nil {
		return s.createReply, nil
	}
	return &taskdag.Run{
		RunKey:             input.RunKey,
		DagKey:             input.DagKey,
		DagVersionSnapshot: input.DagVersionSnapshot,
		TriggerSource:      input.TriggerSource,
		Status:             "running",
	}, nil
}

func (s *stubRunStore) PromoteRootNodesToReady(_ context.Context, dagKey string) (int64, error) {
	s.promoteCalls = append(s.promoteCalls, dagKey)
	return s.promoteRows, s.promoteErr
}

func (s *stubRunStore) WithRunTx(ctx context.Context, fn func(taskdag.RunStore) error) error {
	if s.withTxErr != nil {
		return s.withTxErr
	}
	return fn(s) // mock: 同一实例当作 tx-bound 实例
}

// makeStartDAGService 构造测试用 service：仅注入 dagStore + runStore。
func makeStartDAGService(dagStore taskdag.OrchestrationStore, runStore taskdag.RunStore) *service {
	return &service{dagStore: dagStore, runStore: runStore}
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
	if resp.RunKey == "" {
		t.Fatalf("resp.RunKey empty, want generated key")
	}
	if !strings.HasPrefix(resp.RunKey, "dag-1#run-") {
		t.Errorf("resp.RunKey = %q, want prefix dag-1#run-", resp.RunKey)
	}
	if got := len(runStore.countCalls); got != 1 {
		t.Errorf("CountActiveRunsByDagKey calls = %d, want 1", got)
	}
	if got := len(runStore.createCalls); got != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", got)
	}
	if runStore.createCalls[0].TriggerSource != "manual" {
		t.Errorf("CreateRun TriggerSource = %q, want 'manual'", runStore.createCalls[0].TriggerSource)
	}
	if got := len(runStore.promoteCalls); got != 1 {
		t.Errorf("PromoteRootNodesToReady calls = %d, want 1", got)
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

// ---- active run > 0 → ErrDAGAlreadyRunning ----

func TestStartDAG_RejectMultiRunConcurrency(t *testing.T) {
	dagStore := &stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}
	runStore := &stubRunStore{activeCount: 1}
	svc := makeStartDAGService(dagStore, runStore)

	_, err := svc.StartDAG(context.Background(), StartDAGRequest{DagKey: "dag-1"})
	if !errors.Is(err, ErrDAGAlreadyRunning) {
		t.Fatalf("StartDAG() error = %v, want errors.Is(ErrDAGAlreadyRunning)", err)
	}
	if got := len(runStore.createCalls); got != 0 {
		t.Errorf("CreateRun called %d times, want 0 (rejected before tx)", got)
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
