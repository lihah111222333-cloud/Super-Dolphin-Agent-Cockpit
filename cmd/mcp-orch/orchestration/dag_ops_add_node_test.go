package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// add_node 真实业务实现单测覆盖以下契约边界：
//   - happy: add 单节点 / add 带 depends_on 指向现有节点
//   - happy: raw ops 透传（MCP handler 形状 → ApplyOps → 实际 add_node）
//   - cycle: A→B + B→A 二节点对环；A→B→C→A 三节点链环
//   - cycle: 跨现有节点引入环（add 节点的依赖回指自己上游）
//   - OCC: base_version stale 返 ErrVersionConflict
//   - node_key 重名（与现有节点 / 与同批 ops 内）
//   - depends_on 指向不存在节点 → 拒
//   - 自依赖 → 拒

// stubDAGOpsStore 实现 DAGOpsStore + DAGOpsTxRunner + OrchestrationStore，
// 嵌入 nil OrchestrationStore：未覆盖方法 panic（暴露遗漏的调用面）。
// 测试通过观测 currentVersion / nodes 字段及 calls 链验证业务路径。
type stubDAGOpsStore struct {
	taskdag.OrchestrationStore // nil 嵌入：service.applyTypedOps 不应触发其它方法

	currentVersion int64
	versionErr     error
	versionReads   []int64
	dagStatus      string // 当前 DAG 状态；空串表示测试默认 draft。
	getDagErr      error  // 模拟 GetDAG 错误，用于覆盖状态读取失败。
	dagTrigger     string
	dagCronExpr    string

	nodes   []taskdag.Node // 当前 DAG 已存的节点
	listErr error

	upsertErr error
	bumpErr   error
	// activeRuns 模拟当前 DAG 下 task_dag_runs.status='running' 的行数。
	activeRuns    int64
	activeRunsErr error
	deleteRows    *int64

	// 调用观测
	getVersionCalls     int // 调用 GetDAGVersionForUpdate 次数（事务内、带 FOR UPDATE）
	getVersionLockCalls int // = getVersionCalls 别名，错误消息里表达「锁」意图
	getVersionReadCalls int // 调用 GetDAGVersion 次数（事务外、不加锁）
	listCalls           int
	dagPatchCalls       []taskdag.UpdateDAGPatchInput
	upsertCalls         []taskdag.Node
	deleteCalls         []string
	bumpCalls           []int64 // 调用 BumpDAGVersion 时传入的 expectedVersion
}

// GetDAGVersion (DAGVersionReader): 事务外只读路径，独立计数。
func (s *stubDAGOpsStore) GetDAGVersion(_ context.Context, _ string) (int64, error) {
	s.getVersionReadCalls++
	if s.versionErr != nil {
		return 0, s.versionErr
	}
	if idx := s.getVersionReadCalls - 1; idx >= 0 && idx < len(s.versionReads) {
		return s.versionReads[idx], nil
	}
	return s.currentVersion, nil
}

// GetDAG 返回一个带当前 dagStatus 的 *taskdag.DAG。
// runOpsBatch 在事务内读取 DAG 状态；默认 draft 覆盖可编辑路径，需要验证拒绝路径时显式设置 running。
func (s *stubDAGOpsStore) GetDAG(_ context.Context, _ string) (*taskdag.DAG, error) {
	if s.getDagErr != nil {
		return nil, s.getDagErr
	}
	status := s.dagStatus
	if status == "" {
		status = "draft"
	}
	return &taskdag.DAG{Status: status}, nil
}

func (s *stubDAGOpsStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	// 返回 copy 避免测试代码后续修改污染 stub。
	out := make([]taskdag.Node, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

func (s *stubDAGOpsStore) GetDAGVersionForUpdate(_ context.Context, _ string) (int64, error) {
	s.getVersionCalls++
	if s.versionErr != nil {
		return 0, s.versionErr
	}
	return s.currentVersion, nil
}

func (s *stubDAGOpsStore) BumpDAGVersion(_ context.Context, _ string, expectedVersion int64) (int64, error) {
	s.bumpCalls = append(s.bumpCalls, expectedVersion)
	if s.bumpErr != nil {
		return 0, s.bumpErr
	}
	if s.currentVersion != expectedVersion {
		// 模拟 WHERE version=expected 不匹配 → store 返 IsNotFound。
		// 但 happy path stub 不应进入这分支 —— Get 与 Bump 之间无外
		// 部冲突。
		return 0, errors.New("stubDAGOpsStore: bump version mismatch")
	}
	s.currentVersion++
	return s.currentVersion, nil
}

func (s *stubDAGOpsStore) CountRunningRunsByDagKey(_ context.Context, _ string) (int64, error) {
	if s.activeRunsErr != nil {
		return 0, s.activeRunsErr
	}
	return s.activeRuns, nil
}

func (s *stubDAGOpsStore) GetDAGSchedule(_ context.Context, _ string) (taskdag.DAGSchedule, error) {
	return taskdag.DAGSchedule{
		Trigger:  s.dagTrigger,
		CronExpr: s.dagCronExpr,
	}, nil
}

func (s *stubDAGOpsStore) UpdateDAGPatch(_ context.Context, input taskdag.UpdateDAGPatchInput) (int64, error) {
	s.dagPatchCalls = append(s.dagPatchCalls, input)
	return 1, nil
}

func (s *stubDAGOpsStore) UpsertNode(_ context.Context, node taskdag.Node) (*taskdag.Node, error) {
	s.upsertCalls = append(s.upsertCalls, node)
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	// 复用 stubDAGOpsStore.nodes 体现持久化（让二次 ListNodes 看见）。
	saved := node
	saved.Status = "pending"
	s.nodes = append(s.nodes, saved)
	return &saved, nil
}

func (s *stubDAGOpsStore) DeleteNode(_ context.Context, dagKey, nodeKey string) (int64, error) {
	s.deleteCalls = append(s.deleteCalls, nodeKey)
	if s.deleteRows != nil {
		return *s.deleteRows, nil
	}
	for i, node := range s.nodes {
		if node.DagKey != "" && node.DagKey != dagKey {
			continue
		}
		if node.NodeKey == nodeKey {
			s.nodes = append(s.nodes[:i], s.nodes[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (s *stubDAGOpsStore) WithDAGOpsTx(ctx context.Context, fn func(taskdag.DAGOpsStore) error) error {
	// stub: 同一实例当作 tx-bound 实例（与 stubRunStore.WithRunTx 同款）。
	return fn(s)
}

// makeApplyOpsService 构造测试用 service，仅注入 dagStore。
func makeApplyOpsService(store taskdag.OrchestrationStore) *service {
	return &service{dagStore: store}
}

// ---- happy path ----

func TestApplyOps_AddSingleNode_Happy(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"hello","node_type":"agent"}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Fatalf("NewVersion = %d, want 2", resp.NewVersion)
	}
	if len(stub.upsertCalls) != 1 {
		t.Fatalf("upsertCalls = %d, want 1", len(stub.upsertCalls))
	}
	if stub.upsertCalls[0].NodeKey != "n1" {
		t.Fatalf("upsertCalls[0].NodeKey = %q, want n1", stub.upsertCalls[0].NodeKey)
	}
	if len(stub.bumpCalls) != 1 || stub.bumpCalls[0] != 1 {
		t.Fatalf("bumpCalls = %v, want [1]", stub.bumpCalls)
	}
}

func TestApplyOps_AddNode_PersistsAssignedTo(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"hello","node_type":"agent","assigned_to":"agent-root"}}
		]`),
	}

	if _, err := s.ApplyOps(context.Background(), req); err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if len(stub.upsertCalls) != 1 {
		t.Fatalf("upsertCalls = %d, want 1", len(stub.upsertCalls))
	}
	if got := stub.upsertCalls[0].AssignedTo; got != "agent-root" {
		t.Fatalf("upsert AssignedTo = %q, want agent-root", got)
	}
}

func TestApplyOps_AddNodeWithDeps_Happy(t *testing.T) {
	t.Parallel()
	// 现有节点 n0，新增 n1 depends on n0。
	stub := &stubDAGOpsStore{
		currentVersion: 3,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "n0", Status: "done", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 3,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"t","node_type":"agent","depends_on":["n0"]}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 4 {
		t.Fatalf("NewVersion = %d, want 4", resp.NewVersion)
	}
}

// TestApplyOps_RawOpsPassthrough_PT4 验证 MCP handler 原样透传的 json.RawMessage 能完整进入 add_node 业务。
// 该用例覆盖 wire payload、ApplyOps 解码和 typed op 执行之间的边界。
func TestApplyOps_RawOpsPassthrough_PT4(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 0}
	s := makeApplyOpsService(stub)
	// 模拟 MCP handler 接收的 raw payload（"ops" 是数组，内含 typed payload）。
	raw := json.RawMessage(`[
		{"op":"add_node","node":{"node_key":"a","title":"A","node_type":"agent"}},
		{"op":"add_node","node":{"node_key":"b","title":"B","node_type":"automation","depends_on":["a"]}}
	]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-pt4", BaseVersion: 0, Ops: raw}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 1 {
		t.Fatalf("NewVersion = %d, want 1", resp.NewVersion)
	}
	if len(stub.upsertCalls) != 2 {
		t.Fatalf("upsertCalls = %d, want 2", len(stub.upsertCalls))
	}
	gotKeys := []string{stub.upsertCalls[0].NodeKey, stub.upsertCalls[1].NodeKey}
	wantKeys := []string{"a", "b"}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("upsertCalls[%d].NodeKey = %q, want %q", i, gotKeys[i], wantKeys[i])
		}
	}
}

// ---- cycle ----

func TestApplyOps_AddNode_CycleSelfLoop(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 0}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"t","node_type":"agent","depends_on":["n1"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("self loop: want error, got nil")
	}
	// 自依赖在 buildAddNodePlan 提前拒（更精确错误信息），命中 ErrApplyOpsInvalid。
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("self loop: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "depends on itself") {
		t.Fatalf("self loop: err should mention self-dependency, got %v", err)
	}
}

func TestApplyOps_AddNode_CycleTwoNodes(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 0}
	s := makeApplyOpsService(stub)
	// A 依赖 B、B 依赖 A，二者皆在同一 ops 批内 → 环。
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"a","title":"A","node_type":"agent","depends_on":["b"]}},
			{"op":"add_node","node":{"node_key":"b","title":"B","node_type":"agent","depends_on":["a"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("A<->B cycle: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("A<->B cycle: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !errors.Is(err, nodeexec.ErrDAGCyclic) {
		t.Fatalf("A<->B cycle: err = %v, want errors.Is nodeexec.ErrDAGCyclic", err)
	}
}

func TestApplyOps_AddNode_CycleThreeNodes(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 0}
	s := makeApplyOpsService(stub)
	// a→b→c→a
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"a","title":"A","node_type":"agent","depends_on":["c"]}},
			{"op":"add_node","node":{"node_key":"b","title":"B","node_type":"agent","depends_on":["a"]}},
			{"op":"add_node","node":{"node_key":"c","title":"C","node_type":"agent","depends_on":["b"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("3-node cycle: want error, got nil")
	}
	if !errors.Is(err, nodeexec.ErrDAGCyclic) {
		t.Fatalf("3-node cycle: err = %v, want errors.Is ErrDAGCyclic", err)
	}
}

// 与现有节点联动的环：现有节点 x 依赖待加节点 y，
// 同时 y 又依赖现有 x → 环。但 add_node 不允许改现有节点 depends，
// 所以这里造成环只能是「现有节点 x，新增 y 把 x 加到 y 的 depends，
// 同时 x 的 depends 已含 y」—— ApplyOps 阶段假设现有节点是合法的，
// 此场景在 ApplyOps add_node 期间不应发生。退而验证：现有图含 a→b，
// 新增 c 依赖 b、再有 a 现有依赖 c 是不可能（因为 a 在 c 之前就 fixed）。
// 该场景属于 update_node 修改现有边的责任，不在 add_node 测试中重复覆盖。
//
// 本测试覆盖：现有图链 a→b 上加 c 依赖 a，且 a 的 depends_on 含 c
// （buildAddNodePlan 用 existing.DependsOn 把 a→c 这条边带进 adjacency）。
func TestApplyOps_AddNode_CycleAgainstExisting(t *testing.T) {
	t.Parallel()
	// 现有图 a→b（b depends a）。a 的 DependsOn 已含 "c"（外部依赖），
	// 现在 add c depends b → 环 a→b→c→a。
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "a", DependsOn: json.RawMessage(`["c"]`), Status: "pending"},
			{NodeKey: "b", DependsOn: json.RawMessage(`["a"]`), Status: "pending"},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"c","title":"C","node_type":"agent","depends_on":["b"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("cross-existing cycle: want error, got nil")
	}
	if !errors.Is(err, nodeexec.ErrDAGCyclic) {
		t.Fatalf("cross-existing cycle: err = %v, want errors.Is ErrDAGCyclic", err)
	}
}

// ---- OCC stale ----

func TestApplyOps_OCCConflict(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 5}
	s := makeApplyOpsService(stub)
	// base_version=2，但 store currentVersion=5 → 应返 OCC 冲突。
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 2,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"x","title":"x","node_type":"agent"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("OCC conflict: want error, got nil")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("OCC conflict: err = %v, want errors.Is ErrVersionConflict", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Fatalf("OCC conflict: upsert should NOT have been called, got %d calls", len(stub.upsertCalls))
	}
	if len(stub.bumpCalls) != 0 {
		t.Fatalf("OCC conflict: bump should NOT have been called, got %d calls", len(stub.bumpCalls))
	}
}

// ---- duplicate node_key ----

func TestApplyOps_AddNode_DuplicateKey_AgainstExisting(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"dup","node_type":"agent"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("duplicate vs existing: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("duplicate vs existing: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate vs existing: err should mention 'already exists', got %v", err)
	}
}

func TestApplyOps_AddNode_DuplicateKey_WithinBatch(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 0}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"A","node_type":"agent"}},
			{"op":"add_node","node":{"node_key":"n1","title":"B","node_type":"agent"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("duplicate within batch: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("duplicate within batch: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
}

// ---- depends_on 引用不存在节点 ----

func TestApplyOps_AddNode_DependsOnUnknown(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 0}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"t","node_type":"agent","depends_on":["nope"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("unknown dep: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("unknown dep: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("unknown dep: err should mention 'unknown node', got %v", err)
	}
}

// ---- 空 ops 是合法 noop ----

func TestApplyOps_EmptyOps_NoopReturnsCurrentVersion(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 7}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 7,
		Ops:         json.RawMessage(`[]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("empty ops: err = %v, want nil", err)
	}
	if resp.NewVersion != 7 {
		t.Fatalf("empty ops: NewVersion = %d, want 7 (no bump)", resp.NewVersion)
	}
	if len(stub.bumpCalls) != 0 {
		t.Fatalf("empty ops: bump should not be called, got %d calls", len(stub.bumpCalls))
	}
	if len(stub.upsertCalls) != 0 {
		t.Fatalf("empty ops: upsert should not be called, got %d calls", len(stub.upsertCalls))
	}
	// 空 ops 必须走事务外短路：
	//   - lockCalls=0：GetDAGVersionForUpdate 不应被调（避免白付 OCC 锁代价）
	//   - readCalls=1：走 GetDAGVersion 事务外读（需要拿到当前 version 校 OCC）
	//   - listCalls=0：不需 ListNodes（空 ops 不需环检测 plan）
	if stub.getVersionCalls != 0 {
		t.Fatalf("empty ops: GetDAGVersionForUpdate should not be called (lock-free path), got %d", stub.getVersionCalls)
	}
	if stub.getVersionReadCalls != 1 {
		t.Fatalf("empty ops: GetDAGVersion read should be called exactly once, got %d", stub.getVersionReadCalls)
	}
	if stub.listCalls != 0 {
		t.Fatalf("empty ops: ListNodes should not be called, got %d", stub.listCalls)
	}
}

// TestApplyOps_EmptyOps_BaseVersionStale_ReturnsConflict 验证空 ops 也必须检查 base_version。
// 即使不进入写事务，stale base_version 仍要返回 OCC 冲突，不能被 noop 路径吞掉。
func TestApplyOps_EmptyOps_BaseVersionStale_ReturnsConflict(t *testing.T) {
	stub := &stubDAGOpsStore{currentVersion: 9}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 7, // stale
		Ops:         json.RawMessage(`[]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("empty ops stale base_version: want ErrVersionConflict, got nil")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("empty ops stale base_version: err = %v, want errors.Is ErrVersionConflict", err)
	}
	if stub.getVersionCalls != 0 {
		t.Fatalf("empty ops stale: should not lock, got getVersionCalls=%d", stub.getVersionCalls)
	}
	if stub.getVersionReadCalls != 1 {
		t.Fatalf("empty ops stale: GetDAGVersion read should be called once, got %d", stub.getVersionReadCalls)
	}
}
