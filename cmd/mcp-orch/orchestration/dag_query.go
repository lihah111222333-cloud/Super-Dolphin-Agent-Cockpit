package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

// ErrRunNotFound indicates no DAG run exists for the requested run key.
var ErrRunNotFound = errors.New("orchestration: run_key not found")

// GetRun 读取运行记录。
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
	return getRunResponse(ctx, s.runStore, runKey, run)
}

// ListRuns 列出运行记录。
func (s *service) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if s == nil || s.runStore == nil {
		return contract.ListRunsResponse{}, ErrRunStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns: dag_key required")
	}
	filter := taskdag.ListRunsFilter{DagKey: dagKey, Status: strings.TrimSpace(req.Status), Limit: int32(shared.ClampLimit(int(req.Limit), 1, 200, 50))}
	rows, err := s.runStore.ListRuns(ctx, filter)
	if err != nil {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns(%q): %w", dagKey, err)
	}
	return contract.ListRunsResponse{Runs: mapRuns(rows)}, nil
}

// TerminateDAG 处理terminateDAG。
func (s *service) TerminateDAG(ctx context.Context, req TerminateDAGRequest) error {
	dagKey, runKey, run, err := s.terminableRun(ctx, req)
	if err != nil || run == nil {
		return err
	}
	input := taskdag.TerminateRunInput{DagKey: dagKey, RunKey: runKey, RunID: run.ID, Reason: strings.TrimSpace(req.Reason)}
	result, err := s.runStore.TerminateRun(ctx, input)
	if err != nil {
		if platformdb.IsNotFound(err) {
			latest, getErr := s.runStore.GetRun(ctx, runKey)
			if getErr == nil && latest != nil && strings.TrimSpace(latest.DagKey) == dagKey && latest.Status != "running" {
				return nil
			}
		}
		return fmt.Errorf("orchestration: TerminateDAG(%q/%q): %w", dagKey, runKey, err)
	}
	return s.stopSpawnedAgentThreads(ctx, dagKey, run.ID, result.SpawnedThreadIDs)
}

// terminableRun 处理terminable运行记录。
func (s *service) terminableRun(ctx context.Context, req TerminateDAGRequest) (string, string, *taskdag.Run, error) {
	dagKey, runKey := strings.TrimSpace(req.DagKey), strings.TrimSpace(req.RunKey)
	if s == nil || s.runStore == nil {
		return "", "", nil, ErrRunStoreUnset
	}
	if dagKey == "" {
		return "", "", nil, fmt.Errorf("orchestration: TerminateDAG: dag_key required")
	}
	if runKey == "" {
		return "", "", nil, fmt.Errorf("orchestration: TerminateDAG: run_key required")
	}
	run, err := s.runStore.GetRun(ctx, runKey)
	if platformdb.IsNotFound(err) {
		return "", "", nil, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("orchestration: TerminateDAG(%q): GetRun(%q): %w", dagKey, runKey, err)
	}
	if run == nil {
		return "", "", nil, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
	}
	if strings.TrimSpace(run.DagKey) != dagKey {
		return "", "", nil, fmt.Errorf("orchestration: TerminateDAG: run_key %s does not belong to dag_key %s", runKey, dagKey)
	}
	switch run.Status {
	case "running", "cancelled":
		return dagKey, runKey, run, nil
	default:
		return dagKey, runKey, nil, nil
	}
}

// stopSpawnedAgentThreads 停止spawned代理线程。
func (s *service) stopSpawnedAgentThreads(ctx context.Context, dagKey string, runID int64, threadIDs []string) error {
	var stopErrs []error
	for _, threadID := range threadIDs {
		result, err := StopSpawnedAgent(ctx, s.agentThreads, s, threadID)
		if terminateStopResultError(result, err) {
			if err == nil {
				err = fmt.Errorf("spawned agent stop result %s", result)
			}
			stopErrs = append(stopErrs, fmt.Errorf("thread_id=%s result=%s: %w", threadID, result, err))
		}
	}
	if len(stopErrs) > 0 {
		return fmt.Errorf("orchestration: TerminateDAG(%q run_id=%d): stop spawned agents: %w", dagKey, runID, errors.Join(stopErrs...))
	}
	return nil
}

func terminateStopResultError(result StopResult, err error) bool {
	return err != nil || (result != "" && result != StopResultSuccess && result != StopResultSkippedAlreadyStopped && result != StopResultSkippedAlreadyArchived)
}

func mapRuns(items []taskdag.Run) []contract.Run {
	mapped := make([]contract.Run, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagRunDTO(item))
	}
	return mapped
}

func dagRunDTO(row taskdag.Run) contract.Run {
	out := contract.Run(row)
	out.FinishedAt = shared.CloneTime(row.FinishedAt)
	out.Events = append([]byte(nil), row.Events...)
	out.BudgetLimit = shared.CloneInt64(row.BudgetLimit)
	out.Metadata = append([]byte(nil), row.Metadata...)
	return out
}

type partitionedOps struct {
	dagUpdates nodeexec.Ops
	adds       nodeexec.Ops
	updates    nodeexec.Ops
	removes    nodeexec.Ops
}

// ErrDuplicateOpForNode rejects multiple node-scoped operations for one node in a batch.
var ErrDuplicateOpForNode = errors.New("orchestration: duplicate op for same node_key in batch")

// 同批 dedup（R2 P1）：同一个 node_key 不允许被多条 node-scoped op 同时命中，
// 护住「后写覆盖/先改再删/先加再删」这类语义歧义。重复命中 → fail-fast 包
// ErrDuplicateOpForNode / ErrApplyOpsInvalid。
func partitionOps(ops nodeexec.Ops) (partitionedOps, error) {
	var p partitionedOps
	seenNodeOps := make(map[string]seenNodeOp) // normalized node_key -> first op
	for i, op := range ops {
		if op == nil {
			return p, fmt.Errorf("%w: ops[%d] nil op", ErrApplyOpsInvalid, i)
		}
		if err := appendPartitionedOp(&p, seenNodeOps, i, op); err != nil {
			return p, err
		}
	}
	return p, nil
}

// appendPartitionedOp 追加partitionedop。
func appendPartitionedOp(p *partitionedOps, seenNodeOps map[string]seenNodeOp, index int, op nodeexec.Op) error {
	switch v := op.(type) {
	case nodeexec.OpUpdateDAG:
		if len(p.dagUpdates) > 0 {
			return fmt.Errorf("%w: ops[%d] duplicate update_dag; merge DAG metadata patch before calling ApplyOps", ErrApplyOpsInvalid, index)
		}
		p.dagUpdates = append(p.dagUpdates, op)
	case nodeexec.OpAddNode:
		if err := rememberNodeOp(seenNodeOps, v.Node.NodeKey, index, op.Kind()); err != nil {
			return err
		}
		p.adds = append(p.adds, op)
	case nodeexec.OpUpdateNode:
		if err := rememberNodeOp(seenNodeOps, v.NodeKey, index, op.Kind()); err != nil {
			return err
		}
		p.updates = append(p.updates, op)
	case nodeexec.OpRemoveNode:
		if err := rememberNodeOp(seenNodeOps, v.NodeKey, index, op.Kind()); err != nil {
			return err
		}
		p.removes = append(p.removes, op)
	default:
		return fmt.Errorf("%w: ops[%d] kind=%s", ErrLifecycleNotImplemented, index, op.Kind())
	}
	return nil
}

type seenNodeOp struct {
	index int
	kind  nodeexec.OpKind
}

func rememberNodeOp(seen map[string]seenNodeOp, nodeKey string, index int, kind nodeexec.OpKind) error {
	key := strings.TrimSpace(nodeKey)
	if key == "" {
		return nil
	}
	if prev, dup := seen[key]; dup {
		return fmt.Errorf("%w: %w: ops[%d] (%s) and ops[%d] (%s) both target node_key=%q",
			ErrApplyOpsInvalid, ErrDuplicateOpForNode, prev.index, prev.kind, index, kind, key)
	}
	seen[key] = seenNodeOp{index: index, kind: kind}
	return nil
}

// runOpsBatch executes partitioned ApplyOps in one transaction with OCC and
// running-DAG mutation guards before planning, persisting, and bumping version.
// runOpsBatch 运行opsbatch。
func runOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64, parts partitionedOps) (contract.ApplyOpsResponse, error) {
	current, existing, schedule, err := preflightOpsBatch(ctx, tx, dagKey, baseVersion, parts)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	if len(parts.dagUpdates) == 0 && len(parts.adds) == 0 && len(parts.updates) == 0 && len(parts.removes) == 0 {
		return contract.ApplyOpsResponse{NewVersion: current}, nil
	}
	plan, err := planOpsBatch(parts, existing, schedule)
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
func preflightOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64, parts partitionedOps) (int64, []taskdag.Node, taskdag.DAGSchedule, error) {
	current, err := tx.GetDAGVersionForUpdate(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("get dag version: %w", err)
	}
	if current != baseVersion {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("%w: dag=%s expected=%d actual=%d", ErrVersionConflict, dagKey, baseVersion, current)
	}
	dag, err := tx.GetDAG(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("get dag: %w", err)
	}
	if dag == nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("%w: dag=%s vanished mid-tx", ErrApplyOpsInvalid, dagKey)
	}
	activeRuns, err := tx.CountRunningRunsByDagKey(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("count running runs: %w", err)
	}
	if err := enforceRunningDAGInvariants(dag.Status, activeRuns, parts); err != nil {
		return 0, nil, taskdag.DAGSchedule{}, err
	}
	if err := rejectTerminalDAGOps(dag.Status); err != nil {
		return 0, nil, taskdag.DAGSchedule{}, err
	}
	existing, err := tx.ListNodes(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("list nodes: %w", err)
	}
	schedule, err := dagScheduleForOps(ctx, tx, dagKey, parts)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, err
	}
	return current, existing, schedule, nil
}

func dagScheduleForOps(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, parts partitionedOps) (taskdag.DAGSchedule, error) {
	if len(parts.dagUpdates) == 0 {
		return taskdag.DAGSchedule{}, nil
	}
	schedule, err := tx.GetDAGSchedule(ctx, dagKey)
	if err != nil {
		return taskdag.DAGSchedule{}, fmt.Errorf("get dag schedule: %w", err)
	}
	return schedule, nil
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

// enforceRunningDAGInvariants 是 F4.5 主体：在 DAG 状态为 running 或存在
// running run 时拒绝会改写节点结构的 ApplyOps。runtime append 尚未闭环前，
// running add_node 只写模板节点会让当前 run 看不到/调度不到新节点，因此必须
// fail-fast。update_dag 只改未来调度 / 展示元数据，当前 run 已持有 version
// snapshot，不需要阻断。
//
// 设计取舍：
//   - add_node / update_node / remove_node 在 draft 下合法，但 running 下必须显式拒绝。
//   - update_dag 允许在 active run 存在时修改计划字段，避免 UI 无法暂停/修改未来调度。
func enforceRunningDAGInvariants(dagStatus string, activeRuns int64, parts partitionedOps) error {
	reason, running := runningDAGReason(dagStatus, activeRuns)
	if !running {
		return nil
	}
	return rejectNodeOpsInRunningDAG(reason, parts)
}

func rejectTerminalDAGOps(dagStatus string) error {
	switch strings.TrimSpace(dagStatus) {
	case "done", "failed", "cancelled", "skipped":
		return fmt.Errorf("%w: dag status %q is terminal; apply_ops not allowed", ErrApplyOpsInvalid, dagStatus)
	default:
		return nil
	}
}

func runningDAGReason(dagStatus string, activeRuns int64) (string, bool) {
	if dagStatus == "running" {
		return "dag is running", true
	}
	if activeRuns > 0 {
		return "dag has active running run", true
	}
	return "", false
}

func rejectNodeOpsInRunningDAG(reason string, parts partitionedOps) error {
	for _, item := range []struct {
		name string
		ops  nodeexec.Ops
	}{
		{"add_node", parts.adds}, {"update_node", parts.updates}, {"remove_node", parts.removes},
	} {
		if len(item.ops) > 0 {
			return fmt.Errorf("%w: %s, %s not allowed while runtime append is incomplete", ErrApplyOpsInvalid, reason, item.name)
		}
	}
	return nil
}

// opsBatchPlan 是 planOpsBatch 输出：add 的 NodeSpec + update 的 Change，外加
// 已合并的环检测 adjacency。
type opsBatchPlan struct {
	dagPatches []plannedDAGPatch
	addSpecs   []nodeexec.NodeSpec
	updates    []nodeexec.UpdateNodeChange
	removes    []nodeexec.RemoveNodeChange
}

type plannedDAGPatch struct {
	patch     nodeexec.DAGPatch
	nextRunAt *time.Time
}

// planOpsBatch 跑 add/update/remove 三路 plan + 合并 adjacency + 跑 DetectCycle。
//
// 关键设计：两路 plan 共享同一份 adjacency 是通过 service 层手动合并实现的，
// 而非让 plan 函数互相耦合：
//   - 先跑 PlanAddNodes：基于 existing 的 narrow 投影 → 返回 add 后的 adjacency_a。
//   - 再跑 PlanUpdateNodes：基于 existing+add 的 full 投影（add 节点 status
//     视作 pending，因为新建节点默认入态就是 pending）→ 返回 update 后的
//     adjacency_u。
//   - 合并：adjacency_u 已含 existing + update patch；adjacency_a 含 existing
//   - add 节点。最终 adjacency = adjacency_u + adjacency_a 中 add 节点。
//   - PlanRemoveNodes 基于合并图拒绝仍被依赖的节点，再从图中删掉目标。
//   - DetectCycle 跑一次删除后的最终图。
func planOpsBatch(parts partitionedOps, existing []taskdag.Node, schedule taskdag.DAGSchedule) (opsBatchPlan, error) {
	dagPatches, err := planDAGUpdates(parts.dagUpdates, schedule)
	if err != nil {
		return opsBatchPlan{}, err
	}
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
	pruned, removes, err := nodeexec.PlanRemoveNodes(parts.removes, extended, merged)
	if err != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}
	if cycleErr := nodeexec.DetectCycle(pruned); cycleErr != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, cycleErr)
	}
	return opsBatchPlan{dagPatches: dagPatches, addSpecs: addSpecs, updates: changes, removes: removes}, nil
}

func planDAGUpdates(ops nodeexec.Ops, current taskdag.DAGSchedule) ([]plannedDAGPatch, error) {
	patches := make([]plannedDAGPatch, 0, len(ops))
	for i, op := range ops {
		update, ok := op.(nodeexec.OpUpdateDAG)
		if !ok {
			return nil, fmt.Errorf("%w: ops[%d] expected update_dag, got %s", ErrApplyOpsInvalid, i, op.Kind())
		}
		patch := normalizeDAGPatch(update.Patch)
		finalSchedule, err := validateDAGPatch(patch, current)
		if err != nil {
			return nil, err
		}
		patches = append(patches, plannedDAGPatch{patch: patch, nextRunAt: nextRunAtForFinalSchedule(patch, finalSchedule)})
	}
	return patches, nil
}

func normalizeDAGPatch(patch nodeexec.DAGPatch) nodeexec.DAGPatch {
	return nodeexec.DAGPatch{Title: trimStringPtr(patch.Title), Description: trimStringPtr(patch.Description), Trigger: trimStringPtr(patch.Trigger), CronExpr: trimStringPtr(patch.CronExpr), OwnerID: trimStringPtr(patch.OwnerID), ScheduleEnabled: patch.ScheduleEnabled}
}

// validateDAGPatch 校验DAG补丁。
func validateDAGPatch(patch nodeexec.DAGPatch, current taskdag.DAGSchedule) (taskdag.DAGSchedule, error) {
	if isEmptyDAGPatch(patch) {
		return taskdag.DAGSchedule{}, fmt.Errorf("%w: update_dag patch must set at least one field", ErrApplyOpsInvalid)
	}
	if patch.Trigger != nil && !isValidDAGTrigger(*patch.Trigger) {
		return taskdag.DAGSchedule{}, fmt.Errorf("%w: update_dag trigger %q must be one of manual/auto/scheduled/external", ErrApplyOpsInvalid, *patch.Trigger)
	}
	if patch.CronExpr != nil && *patch.CronExpr != "" {
		if _, err := orchcron.ParseDAGCronExpr(*patch.CronExpr); err != nil {
			return taskdag.DAGSchedule{}, fmt.Errorf("%w: update_dag cron_expr %q invalid: %w", ErrApplyOpsInvalid, *patch.CronExpr, err)
		}
	}
	finalSchedule := finalDAGSchedule(current, patch)
	if err := validateDAGPatchFinalSchedule(patch, finalSchedule); err != nil {
		return taskdag.DAGSchedule{}, err
	}
	return finalSchedule, nil
}

func finalDAGSchedule(current taskdag.DAGSchedule, patch nodeexec.DAGPatch) taskdag.DAGSchedule {
	schedule := taskdag.DAGSchedule{Trigger: strings.TrimSpace(current.Trigger), CronExpr: strings.TrimSpace(current.CronExpr)}
	if schedule.Trigger == "" {
		schedule.Trigger = "manual"
	}
	if patch.Trigger != nil {
		schedule.Trigger = *patch.Trigger
	}
	if patch.CronExpr != nil {
		schedule.CronExpr = *patch.CronExpr
	}
	return schedule
}

// validateDAGPatchFinalSchedule 校验DAG补丁final计划。
func validateDAGPatchFinalSchedule(patch nodeexec.DAGPatch, final taskdag.DAGSchedule) error {
	if patch.Trigger == nil && patch.CronExpr == nil && patch.ScheduleEnabled == nil {
		return nil
	}
	if final.Trigger == "scheduled" {
		if final.CronExpr == "" {
			return fmt.Errorf("%w: update_dag final trigger=scheduled requires non-empty cron_expr", ErrApplyOpsInvalid)
		}
		if _, err := orchcron.ParseDAGCronExpr(final.CronExpr); err != nil {
			return fmt.Errorf("%w: update_dag final cron_expr %q invalid: %w", ErrApplyOpsInvalid, final.CronExpr, err)
		}
		return nil
	}
	if final.CronExpr != "" {
		return fmt.Errorf("%w: update_dag cron_expr is allowed only when final trigger=scheduled; clear cron_expr when changing trigger away from scheduled", ErrApplyOpsInvalid)
	}
	return nil
}

// isEmptyDAGPatch 判断emptyDAG补丁是否可用。
func isEmptyDAGPatch(patch nodeexec.DAGPatch) bool {
	return patch.Title == nil && patch.Description == nil && patch.Trigger == nil && patch.CronExpr == nil && patch.OwnerID == nil && patch.ScheduleEnabled == nil
}

func isValidDAGTrigger(trigger string) bool {
	return trigger == "manual" || trigger == "auto" || trigger == "scheduled" || trigger == "external"
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

// existingNodesForPlan 把 taskdag.Node 列表转为 nodeexec.PlanAddNodes 需要的
// 最小投影（NodeKey + DependsOn）。仅 nodeexec 不依赖 taskdag 的隔离层需。
func existingNodesForPlan(nodes []taskdag.Node) []nodeexec.ExistingNode {
	out := make([]nodeexec.ExistingNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeexec.ExistingNode{NodeKey: n.NodeKey, DependsOn: decodeDependsOn(n.DependsOn)})
	}
	return out
}

// existingFullForPlan 把 taskdag.Node + add 后的 spec 列表合并成
// PlanUpdateNodes 需要的 ExistingNodeFull 切片。add 节点 status 默认 pending
// （migration 0072 task_dag_nodes.status default = 'pending'）。
func existingFullForPlan(nodes []taskdag.Node, addSpecs []nodeexec.NodeSpec) []nodeexec.ExistingNodeFull {
	out := make([]nodeexec.ExistingNodeFull, 0, len(nodes)+len(addSpecs))
	for _, n := range nodes {
		out = append(out, nodeexec.ExistingNodeFull{NodeKey: n.NodeKey, DependsOn: decodeDependsOn(n.DependsOn), Status: n.Status})
	}
	for _, spec := range addSpecs {
		out = append(out, nodeexec.ExistingNodeFull{NodeKey: spec.NodeKey, DependsOn: spec.DependsOn, Status: string(nodeexec.NodeStatusPending)})
	}
	return out
}

// mergeAdjacency 把 add 路径与 update 路径的 adjacency 合并：update 路径已
// 含 existing 节点（含未被 patch 的节点保留 existing dep）；add 路径含新增
// 节点的 dep。把 add 路径中 update 没见过的 key 补进 update 路径即可。
func mergeAdjacency(adjAdd, adjUpd map[string][]string) map[string][]string {
	merged := make(map[string][]string, len(adjAdd)+len(adjUpd))
	maps.Copy(merged, adjUpd)
	for k, v := range adjAdd {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	return merged
}

// persistOpsBatch 顺序写入 plan 结果：先 add（新节点），再 update（合并 patch
// 到旧 Node 后整行写回），最后 remove。UpsertNode 走 ON CONFLICT DO UPDATE，
// 把 plan 的输出整行覆盖回库——故 update 路径必须把未在 patch 中的字段从旧
// Node 复制回去，否则会被空值覆盖。
func persistOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, existing []taskdag.Node, plan opsBatchPlan) error {
	if err := persistDAGPatches(ctx, tx, dagKey, plan.dagPatches); err != nil {
		return err
	}
	if err := persistAddNodeSpecs(ctx, tx, dagKey, plan.addSpecs); err != nil {
		return err
	}
	if err := persistUpdateChanges(ctx, tx, dagKey, existing, plan.updates); err != nil {
		return err
	}
	return persistRemoveChanges(ctx, tx, dagKey, plan.removes)
}

func persistDAGPatches(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, patches []plannedDAGPatch) error {
	for _, patch := range patches {
		rows, err := tx.UpdateDAGPatch(ctx, dagPatchInput(dagKey, patch))
		if err != nil {
			return fmt.Errorf("update dag patch %s: %w", dagKey, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: update_dag %s: no rows affected", ErrApplyOpsInvalid, dagKey)
		}
	}
	return nil
}

func dagPatchInput(dagKey string, planned plannedDAGPatch) taskdag.UpdateDAGPatchInput {
	patch := planned.patch
	return taskdag.UpdateDAGPatchInput{
		DagKey:          dagKey,
		Title:           cloneStringPtr(patch.Title),
		Description:     cloneStringPtr(patch.Description),
		Trigger:         cloneStringPtr(patch.Trigger),
		CronExpr:        cloneStringPtr(patch.CronExpr),
		OwnerID:         cloneStringPtr(patch.OwnerID),
		NextRunAt:       planned.nextRunAt,
		ScheduleEnabled: patch.ScheduleEnabled,
	}
}

// nextRunAtForFinalSchedule 为final计划处理next运行记录at。
func nextRunAtForFinalSchedule(patch nodeexec.DAGPatch, schedule taskdag.DAGSchedule) *time.Time {
	if patch.Trigger == nil && patch.CronExpr == nil && patch.ScheduleEnabled == nil {
		return nil
	}
	if patch.ScheduleEnabled != nil && !*patch.ScheduleEnabled {
		return nil
	}
	if schedule.Trigger != "scheduled" || schedule.CronExpr == "" {
		return nil
	}
	next, err := orchcron.NextDAGRunAt(schedule.CronExpr, time.Now().UTC())
	if err != nil {
		return nil
	}
	return &next
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// persistAddNodeSpecs 顺序调 UpsertNode 把 NodeSpec 转为 taskdag.Node 写入表。
func persistAddNodeSpecs(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, specs []nodeexec.NodeSpec) error {
	for _, spec := range specs {
		node := taskdag.Node{DagKey: dagKey, NodeKey: strings.TrimSpace(spec.NodeKey), Title: strings.TrimSpace(spec.Title), NodeType: strings.TrimSpace(spec.NodeType), AssignedTo: strings.TrimSpace(spec.AssignedTo), DependsOn: dependsOnJSON(spec.DependsOn), Config: append(json.RawMessage(nil), spec.Config...)}
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

func persistRemoveChanges(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, changes []nodeexec.RemoveNodeChange) error {
	for _, c := range changes {
		rows, err := tx.DeleteNode(ctx, dagKey, c.NodeKey)
		if err != nil {
			return fmt.Errorf("delete node %s: %w", c.NodeKey, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: delete node %s: no rows affected (node status changed or node missing)", ErrApplyOpsInvalid, c.NodeKey)
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
