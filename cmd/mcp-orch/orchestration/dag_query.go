package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// =====================================================
// DAG v2 T3.1: task_get_run service 实现
// DAG v2 T3.2: task_list_runs service 实现
// DAG v2 T3.1/T3.2: task_get_run / task_list_runs service implementation
// =====================================================
//
// service.GetRun 接通 RunStore.GetRun(run_key)，把存储域 Run 转换为
// contract.Run DTO 返回给 MCP 调用方。节点信息不内联（用户决策 A2）：
// 调用方需另查 task_get_dag 拿 DAG 模板 + 节点。
//
// service.ListRuns 接通 RunStore.ListRuns(filter)，列出指定 DAG 的最近 run。
// dag_key 必填、status 透传、limit 默认 50。
//
// service.GetRun wires through RunStore.GetRun(run_key) and converts the
// storage-domain Run into a contract.Run DTO for MCP callers. Nodes are
// intentionally not inlined (user decision A2): callers fetch the DAG
// template + nodes via task_get_dag.
//
// service.ListRuns wires through RunStore.ListRuns(filter), listing recent
// runs for a DAG. dag_key required; status passed through; limit defaults to 50.

// ErrRunNotFound 表示 GetRun 调用时 run_key 不存在。
// 与 ErrDAGNotFound 对齐：service 层 sentinel + tool 层中英双语转译。
//
// ErrRunNotFound is returned by GetRun when the supplied run_key does not
// exist. Mirrors ErrDAGNotFound's pattern: service-layer sentinel +
// bilingual translation at the tool layer.
var ErrRunNotFound = errors.New("orchestration: run_key not found")

// GetRun 按 run_key 取一条 task_dag_runs。
//
// 防御性检查与 ListRuns / StartDAG 对齐：service 自身或 runStore 未注入时统一
// 返 ErrRunStoreUnset（同一个 sentinel，调用方 errors.Is 判断更省事）。运行时
// 调用路径上 ProvideService 的 setter 注入会保证两者非 nil（fx 提供路线 N 后已守护）。
//
// 错误转译：runStore 返回的域错误若 IsNotFound 命中 → 包装 ErrRunNotFound
// sentinel；其他错误透传并附 run_key 上下文。
//
// GetRun fetches a single task_dag_runs row by run_key.
//
// Defensive checks align with ListRuns / StartDAG: if the service itself or
// runStore is unset, we return the same ErrRunStoreUnset sentinel so callers
// have a single errors.Is target. In the production path ProvideService's
// setter injection guarantees both are non-nil (route N's fx wiring guards
// that already).
//
// Error translation: when runStore returns a domain error matched by
// IsNotFound we wrap ErrRunNotFound; other errors are passed through with
// the run_key for context.
func (s *service) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	if s == nil || s.runStore == nil {
		return contract.GetRunResponse{}, ErrRunStoreUnset
	}
	runKey := strings.TrimSpace(req.RunKey)
	if runKey == "" {
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun: run_key required")
	}
	run, err := s.runStore.GetRun(ctx, runKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return contract.GetRunResponse{}, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
		}
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun(%q): %w", runKey, err)
	}
	if run == nil {
		// 防御：实现规范 GetRun 在未命中时返回 IsNotFound 域错误而非 (nil, nil)，
		// 此分支只为兜底未来实现退化（例如新 store 后端忘记包错误）。
		// Defensive: the GetRun contract returns an IsNotFound error on miss
		// rather than (nil, nil); this branch only guards against future store
		// backends that forget to wrap the error.
		return contract.GetRunResponse{}, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
	}
	return contract.GetRunResponse{Run: dagRunDTO(*run)}, nil
}

// ListRuns 列出指定 DAG 的最近 run（T3.2）。
//   - dag_key 必填；空字符串报错。
//   - status 可选（store 层 ListTaskDagRunsByKey 把空串视为不过滤；
//     合法状态枚举由 migration 0080 task_dag_runs.status CHECK 锁全集，
//     service 不重复校验，错误 status 由 DB 直接拒绝）。
//   - limit 走 shared.ClampLimit(val, 1, 200, 50)：<1 → 默认 50，>200 → cap 200。
//     M2 阶段 50 够用，200 是防呆上限（调用方传 99999999 不会透到 SQL 层）。
//     store_run.go:46 还会用 limit<=0 → 50 兜底，为例外路径多一道保险。
//   - runStore == nil 防御与 StartDAG 一致：返 ErrRunStoreUnset，避免裸构造
//     测试路径走到 nil pointer。
//
// ListRuns lists recent runs for a DAG (T3.2).
//   - dag_key required; empty string returns an error.
//   - status optional; the store treats empty as no filter and migration 0080
//     CHECK constrains the legal status set, so the service does not re-validate.
//   - limit goes through shared.ClampLimit(val, 1, 200, 50): <1 → default 50,
//     >200 → capped to 200. M2 callers rarely need more, and 200 keeps a stray
//     99999999 from reaching SQL. store_run.go:46 still defaults limit<=0 to 50.
//   - runStore == nil defense matches StartDAG: returns ErrRunStoreUnset.
func (s *service) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if s == nil || s.runStore == nil {
		return contract.ListRunsResponse{}, ErrRunStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns: dag_key required")
	}
	filter := taskdag.ListRunsFilter{
		DagKey: dagKey,
		Status: strings.TrimSpace(req.Status),
		Limit:  int32(shared.ClampLimit(int(req.Limit), 1, 200, 50)),
	}
	rows, err := s.runStore.ListRuns(ctx, filter)
	if err != nil {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns(%q): %w", dagKey, err)
	}
	return contract.ListRunsResponse{Runs: mapRuns(rows)}, nil
}

// mapRuns 把 store 层 taskdag.Run slice 转为 contract.Run slice，
// 复用 dagRunDTO 做单行转换以保持与 GetRun 路径一致的防御拷贝语义。
//
// mapRuns converts a slice of taskdag.Run into contract.Run, reusing
// dagRunDTO for per-row conversion so defensive-copy semantics stay aligned
// with the GetRun path.
func mapRuns(items []taskdag.Run) []contract.Run {
	mapped := make([]contract.Run, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagRunDTO(item))
	}
	return mapped
}

// dagRunDTO 把存储层 taskdag.Run 转成 contract.Run DTO（值拷贝 + RawMessage
// defensive copy，防上层修改 events/metadata 渗回 store 缓存）。
//
// dagRunDTO converts a storage-layer taskdag.Run into the contract.Run DTO
// (value copy + defensive RawMessage copy so callers cannot mutate
// events/metadata back into a store-side cache).
func dagRunDTO(row taskdag.Run) contract.Run {
	return contract.Run{
		ID:                 row.ID,
		RunKey:             row.RunKey,
		DagKey:             row.DagKey,
		DagVersionSnapshot: row.DagVersionSnapshot,
		TriggerSource:      row.TriggerSource,
		Status:             row.Status,
		StartedAt:          row.StartedAt,
		FinishedAt:         shared.CloneTime(row.FinishedAt),
		Events:             append([]byte(nil), row.Events...),
		BudgetUsed:         row.BudgetUsed,
		BudgetLimit:        cloneInt64(row.BudgetLimit),
		Metadata:           append([]byte(nil), row.Metadata...),
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

// =====================================================
// DAG v2 F4.1/F4.2: ApplyOps add_node + update_node helpers
// =====================================================
//
// 拆函数策略：partitionOps → planOpsBatch (调 PlanAddNodes + PlanUpdateNodes
// + DetectCycle) → persistOpsBatch (顺序 UpsertNode add 后 update)。F4.1
// 的 ensureAllAddNodeOps / runAddNodeBatch / existingNodesForPlan /
// persistAddNodeSpecs 沿用主干，新增 update 路径与之共存，让 dag.go 本体
// 行数不超守卫上限。位于同包，可见性不变。

// partitionedOps 是 ops 按 op_kind 拆分后的结果。F4.2 仅 add + update 实装；
// 其他 op_kind 由 partitionOps 直接 fail-fast 返 ErrLifecycleNotImplemented，
// 与 F4.1 ensureAllAddNodeOps 的兜底语义一致（避免进事务后中途发现）。
type partitionedOps struct {
	adds    nodeexec.Ops
	updates nodeexec.Ops
}

// partitionOps 把 ops 按 op_kind 分组：add_node → adds，update_node → updates。
// remove_node / update_dag 留给 F4.3 / F4.4，本阶段命中即返
// ErrLifecycleNotImplemented，让请求 fail-fast。
func partitionOps(ops nodeexec.Ops) (partitionedOps, error) {
	var p partitionedOps
	for i, op := range ops {
		if op == nil {
			return p, fmt.Errorf("%w: ops[%d] nil op", ErrApplyOpsInvalid, i)
		}
		switch op.Kind() {
		case nodeexec.OpKindAddNode:
			p.adds = append(p.adds, op)
		case nodeexec.OpKindUpdateNode:
			p.updates = append(p.updates, op)
		default:
			return p, fmt.Errorf("%w: ops[%d] kind=%s (only add_node/update_node implemented through F4.2)", ErrLifecycleNotImplemented, i, op.Kind())
		}
	}
	return p, nil
}

// runOpsBatch 在 PG 事务内跑一批已 partition 的 ops：F4.1 add + F4.2 update + F4.5
// dag.status 不变量。OCC 双重护栏：pre-check 比对 base_version；bump 失锁返
// ErrVersionConflict（与 F4.1 同 sentinel，调用方 errors.Is 命中相同分支）。
//
// 步骤：
//  1. GetDAGVersionForUpdate + 比对 base_version
//  2. GetDAG 读 dag.status (同事务内、上锁后读)
//  3. F4.5 不变量：若 status='running'——
//     a. parts.updates 非空 → 拒为 ErrApplyOpsInvalid
//     b. ListNodes 拿现有节点 → 验证 add_node.depends_on 全部指向一个 status='done' 节点
//        否则拒为 ErrApplyOpsInvalid
//  4. 空 ops → 直接返当前 version（noop）
//  5. plan add + plan update → 合并 adjacency → DetectCycle
//  6. 顺序 UpsertNode（先 add 后 update，update 合并 patch 与旧 Node）
//  7. BumpDAGVersion
func runOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64, parts partitionedOps) (contract.ApplyOpsResponse, error) {
	current, existing, err := preflightOpsBatch(ctx, tx, dagKey, baseVersion, parts)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	if len(parts.adds) == 0 && len(parts.updates) == 0 {
		return contract.ApplyOpsResponse{NewVersion: current}, nil
	}
	plan, err := planOpsBatch(parts, existing)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	if err := persistOpsBatch(ctx, tx, dagKey, existing, plan); err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	newVersion, err := bumpDAGVersionTx(ctx, tx, dagKey, baseVersion)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	return contract.ApplyOpsResponse{NewVersion: newVersion}, nil
}

// preflightOpsBatch 跑 runOpsBatch 的前置检查：上锁 + OCC 同庄判定 + DAG 状态读 +
// F4.5 不变量。作为 runOpsBatch 的助手拆出避免父函数超过 cyclomatic complexity 上限。
func preflightOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64, parts partitionedOps) (int64, []taskdag.Node, error) {
	current, err := tx.GetDAGVersionForUpdate(ctx, dagKey)
	if err != nil {
		return 0, nil, fmt.Errorf("get dag version: %w", err)
	}
	if current != baseVersion {
		return 0, nil, fmt.Errorf("%w: dag=%s expected=%d actual=%d", ErrVersionConflict, dagKey, baseVersion, current)
	}
	dag, err := tx.GetDAG(ctx, dagKey)
	if err != nil {
		return 0, nil, fmt.Errorf("get dag: %w", err)
	}
	if dag == nil {
		return 0, nil, fmt.Errorf("%w: dag=%s vanished mid-tx", ErrApplyOpsInvalid, dagKey)
	}
	existing, err := tx.ListNodes(ctx, dagKey)
	if err != nil {
		return 0, nil, fmt.Errorf("list nodes: %w", err)
	}
	if err := enforceRunningDAGInvariants(dag.Status, parts, existing); err != nil {
		return 0, nil, err
	}
	return current, existing, nil
}

// bumpDAGVersionTx 拼 OCC bump 与 ErrVersionConflict 翻译。拆出避免 runOpsBatch 超 CC 上限。
func bumpDAGVersionTx(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64) (int64, error) {
	newVersion, err := tx.BumpDAGVersion(ctx, dagKey, baseVersion)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return 0, fmt.Errorf("%w: dag=%s base_version=%d (lost lock?)", ErrVersionConflict, dagKey, baseVersion)
		}
		return 0, fmt.Errorf("bump version: %w", err)
	}
	return newVersion, nil
}

// enforceRunningDAGInvariants 是 F4.5 主体：在 DAG 状态为 running 时仅允许 add_node，
// 且新节点的 depends_on 必须全部指向 status='done' 的现有节点。与 PlanAddNodes 独立调
// 试点不同：后者不读 dag.status，这里专门面向 running DAG 动态补改场景。
//
// 设计取舍：
//   - update_node / remove_node / update_dag 被 partitionOps 阶段已处理 (都走
//     ErrLifecycleNotImplemented 分支，仅 update_node 在 draft 下是合法的)。本函数只
//     需在 running 下多一层抲 update_node 拒。
//   - add_node.depends_on 指向现有节点 (existing) 中 status='done' 才可插入。同批
//     加入的新节点 (status 默认 pending) 不能作为 depends_on 目标 —— 这会让新节点
//     互相等什么都不能跑。这是蓝图 v2 §5 “dynamic patch” 的宝贵限制。
func enforceRunningDAGInvariants(dagStatus string, parts partitionedOps, existing []taskdag.Node) error {
	if dagStatus != "running" {
		return nil
	}
	if len(parts.updates) > 0 {
		return fmt.Errorf("%w: dag is running, update_node not allowed (only add_node depends_on done nodes)", ErrApplyOpsInvalid)
	}
	if len(parts.adds) == 0 {
		return nil
	}
	doneSet := doneNodeKeys(existing)
	for i, op := range parts.adds {
		add, ok := op.(nodeexec.OpAddNode)
		if !ok {
			// 防御：partitionOps 保证 parts.adds 只包含 OpAddNode。
			return fmt.Errorf("%w: ops[%d] expected add_node, got %s", ErrApplyOpsInvalid, i, op.Kind())
		}
		for _, dep := range nodeexec.NormalizeDependsOn(add.Node.DependsOn) {
			if _, done := doneSet[dep]; !done {
				return fmt.Errorf("%w: dag is running, add_node %q depends_on %q must reference a done node", ErrApplyOpsInvalid, add.Node.NodeKey, dep)
			}
		}
	}
	return nil
}

// doneNodeKeys 抽出 existing 中 status=="done" 的 NodeKey 集合。
func doneNodeKeys(existing []taskdag.Node) map[string]struct{} {
	out := make(map[string]struct{}, len(existing))
	for _, n := range existing {
		if n.Status == "done" {
			out[n.NodeKey] = struct{}{}
		}
	}
	return out
}

// opsBatchPlan 是 planOpsBatch 输出：add 的 NodeSpec + update 的 Change，外加
// 已合并的环检测 adjacency。
type opsBatchPlan struct {
	addSpecs []nodeexec.NodeSpec
	updates  []nodeexec.UpdateNodeChange
}

// planOpsBatch 跑两路 plan + 合并 adjacency + 跑 DetectCycle。
//
// 关键设计：两路 plan 共享同一份 adjacency 是通过 service 层手动合并实现的，
// 而非让 plan 函数互相耦合：
//   - 先跑 PlanAddNodes：基于 existing 的 narrow 投影 → 返回 add 后的 adjacency_a。
//   - 再跑 PlanUpdateNodes：基于 existing+add 的 full 投影（add 节点 status
//     视作 pending，因为新建节点默认入态就是 pending）→ 返回 update 后的
//     adjacency_u。
//   - 合并：adjacency_u 已含 existing + update patch；adjacency_a 含 existing
//   - add 节点。最终 adjacency = adjacency_u + adjacency_a 中 add 节点。
//   - DetectCycle 跑一次合并图。
func planOpsBatch(parts partitionedOps, existing []taskdag.Node) (opsBatchPlan, error) {
	adjAdd, addSpecs, err := nodeexec.PlanAddNodes(parts.adds, existingNodesForPlan(existing))
	if err != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}
	extended := existingFullForPlan(existing, addSpecs)
	adjUpd, changes, err := nodeexec.PlanUpdateNodes(parts.updates, extended)
	if err != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}
	merged := mergeAdjacency(adjAdd, adjUpd)
	if cycleErr := nodeexec.DetectCycle(merged); cycleErr != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, cycleErr)
	}
	return opsBatchPlan{addSpecs: addSpecs, updates: changes}, nil
}

// existingNodesForPlan 把 taskdag.Node 列表转为 nodeexec.PlanAddNodes 需要的
// 最小投影（NodeKey + DependsOn）。仅 nodeexec 不依赖 taskdag 的隔离层需。
func existingNodesForPlan(nodes []taskdag.Node) []nodeexec.ExistingNode {
	out := make([]nodeexec.ExistingNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeexec.ExistingNode{
			NodeKey:   n.NodeKey,
			DependsOn: decodeDependsOn(n.DependsOn),
		})
	}
	return out
}

// existingFullForPlan 把 taskdag.Node + add 后的 spec 列表合并成
// PlanUpdateNodes 需要的 ExistingNodeFull 切片。add 节点 status 默认 pending
// （migration 0072 task_dag_nodes.status default = 'pending'）。
func existingFullForPlan(nodes []taskdag.Node, addSpecs []nodeexec.NodeSpec) []nodeexec.ExistingNodeFull {
	out := make([]nodeexec.ExistingNodeFull, 0, len(nodes)+len(addSpecs))
	for _, n := range nodes {
		out = append(out, nodeexec.ExistingNodeFull{
			NodeKey:   n.NodeKey,
			DependsOn: decodeDependsOn(n.DependsOn),
			Status:    n.Status,
		})
	}
	for _, spec := range addSpecs {
		out = append(out, nodeexec.ExistingNodeFull{
			NodeKey:   spec.NodeKey,
			DependsOn: spec.DependsOn,
			Status:    string(nodeexec.NodeStatusPending),
		})
	}
	return out
}

// mergeAdjacency 把 add 路径与 update 路径的 adjacency 合并：update 路径已
// 含 existing 节点（含未被 patch 的节点保留 existing dep）；add 路径含新增
// 节点的 dep。把 add 路径中 update 没见过的 key 补进 update 路径即可。
func mergeAdjacency(adjAdd, adjUpd map[string][]string) map[string][]string {
	merged := make(map[string][]string, len(adjAdd)+len(adjUpd))
	for k, v := range adjUpd {
		merged[k] = v
	}
	for k, v := range adjAdd {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	return merged
}

// persistOpsBatch 顺序写入 plan 结果：先 add（新节点），再 update（合并 patch
// 到旧 Node 后整行写回）。UpsertNode 走 ON CONFLICT DO UPDATE，把 plan 的
// 输出整行覆盖回库——故 update 路径必须把未在 patch 中的字段从旧 Node 复制
// 回去，否则会被空值覆盖。
func persistOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, existing []taskdag.Node, plan opsBatchPlan) error {
	if err := persistAddNodeSpecs(ctx, tx, dagKey, plan.addSpecs); err != nil {
		return err
	}
	return persistUpdateChanges(ctx, tx, dagKey, existing, plan.updates)
}

// persistAddNodeSpecs 顺序调 UpsertNode 把 NodeSpec 转为 taskdag.Node 写入表。
func persistAddNodeSpecs(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, specs []nodeexec.NodeSpec) error {
	for _, spec := range specs {
		node := taskdag.Node{
			DagKey:    dagKey,
			NodeKey:   strings.TrimSpace(spec.NodeKey),
			Title:     strings.TrimSpace(spec.Title),
			NodeType:  strings.TrimSpace(spec.NodeType),
			DependsOn: dependsOnJSON(spec.DependsOn),
			Config:    append(json.RawMessage(nil), spec.Config...),
		}
		if _, err := tx.UpsertNode(ctx, node); err != nil {
			return fmt.Errorf("upsert node %s: %w", node.NodeKey, err)
		}
	}
	return nil
}

// persistUpdateChanges 把每条 update change 合并到对应旧 Node 后整行 UpsertNode。
// 合并语义按 NodePatch 三态：
//   - Title / AssignedTo: *string nil 沿用旧值；指向 v 覆盖（含 "" 清空）
//   - DependsOn: *[]string nil 沿用旧值；指向 *[] / *[a,b] 覆盖
//   - Config: json.RawMessage len==0 或 "null" 沿用旧值；非空覆盖
//
// 同样 sqlc UpsertTaskDagNode SQL 不写 status / result / started_at 等节点
// 生命周期字段，故 patch 沿用 ApplyOps "不许改 status" 的约束。
func persistUpdateChanges(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, existing []taskdag.Node, changes []nodeexec.UpdateNodeChange) error {
	byKey := indexExistingByKey(existing)
	for _, c := range changes {
		old, ok := byKey[c.NodeKey]
		if !ok {
			// 防御：PlanUpdateNodes 已校验存在；这里命中说明并发删除/lost
			// lock。返错让 service 层把它包成 ErrApplyOpsInvalid（虽然此情况
			// 在 OCC 锁下不应发生）。
			return fmt.Errorf("update_node: node %q vanished between plan and persist", c.NodeKey)
		}
		merged := mergeNodePatch(old, c.Patch, dagKey)
		if _, err := tx.UpsertNode(ctx, merged); err != nil {
			return fmt.Errorf("upsert (update) node %s: %w", c.NodeKey, err)
		}
	}
	return nil
}

// indexExistingByKey 按 NodeKey 建索引方便 persistUpdateChanges 查旧 Node。
func indexExistingByKey(nodes []taskdag.Node) map[string]taskdag.Node {
	out := make(map[string]taskdag.Node, len(nodes))
	for _, n := range nodes {
		out[n.NodeKey] = n
	}
	return out
}

// mergeNodePatch 按 NodePatch 三态把 patch 合并进旧 Node。返回值是要 UpsertNode
// 写回的整行 taskdag.Node。
func mergeNodePatch(old taskdag.Node, patch nodeexec.NodePatch, dagKey string) taskdag.Node {
	merged := old
	merged.DagKey = dagKey
	if patch.Title != nil {
		merged.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.AssignedTo != nil {
		merged.AssignedTo = strings.TrimSpace(*patch.AssignedTo)
	}
	if patch.DependsOn != nil {
		merged.DependsOn = dependsOnJSON(nodeexec.NormalizeDependsOn(*patch.DependsOn))
	}
	if !isEmptyRawJSON(patch.Config) {
		merged.Config = append(json.RawMessage(nil), patch.Config...)
	}
	return merged
}

// isEmptyRawJSON 判定 RawMessage 是否表示「不改 Config」三态中的 nil 一态：
// len==0 或字面量 "null"。其他（含 "{}", "[]", `{"k":v}` 等）一律视为「覆盖」。
func isEmptyRawJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	return strings.TrimSpace(string(raw)) == "null"
}
