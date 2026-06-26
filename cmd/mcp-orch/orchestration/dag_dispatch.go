package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/scheduledstart"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"
)

// ErrDispatchStoreUnset 表示 service 被构造时没拿到 taskdag.DispatchNodeStore
// （旧测试路径 / standalone 模式）。MCP 工具层把它转成调用方可读的中英双语错误。
var ErrDispatchStoreUnset = errors.New("orchestration: dispatch store is not configured")

// ErrDispatchNodeIneligible 表示节点当前状态不属于 {pending, ready}，
// 不允许走 DispatchNode 路径（避免误覆盖 running / done / failed 等终态）。
var ErrDispatchNodeIneligible = errors.New("orchestration: node is not in pending/ready state, cannot dispatch")

// dispatchNodeWakeupKind 是 task_dispatch_node 入队的 wakeup_kind 值。
// 与 store_complete_downstream.go 的 downstreamWakeupKind 区分：手工显式
// dispatch 用 "manual_dispatch" 让运维 + 日志能一眼分流，避免与依赖完成自动
// enqueue 混淆。
const dispatchNodeWakeupKind = "manual_dispatch"

// DispatchNode 领取并调度一个可运行的 DAG 节点。
// 只接受 runtime run 上 pending/ready 节点；agent 节点必须先配置 exec.cwd，避免入队后才失败。
func (s *service) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	dagKey, nodeKey, assignedTo, runID, err := normalizeDispatchInputs(s, req)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	target, err := s.findDispatchTarget(ctx, dagKey, nodeKey, runID)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	if err := ensureDispatchEligible(target); err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	if resolveNodeType(target.NodeType) == "agent" {
		if _, err := nodeexec.ValidateLaunchCWDForNodeConfig("agent", target.Config); err != nil {
			return contract.DispatchNodeResponse{}, fmt.Errorf("orchestration: DispatchNode: agent node %s/%s requires node.config.exec.cwd before task_dispatch_node enqueue: %w", target.DagKey, target.NodeKey, err)
		}
	}
	assigned, err := s.assignAndPersist(ctx, target, assignedTo, runID)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	wakeupID, err := s.enqueueManualDispatchWakeup(ctx, dagKey, nodeKey, runID, assignedTo)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	resp := contract.DispatchNodeResponse{
		WakeupID: wakeupID,
		Enqueued: wakeupID > 0,
	}
	if assigned != nil {
		resp.Node = dagNodeDTO(*assigned)
	}
	return resp, nil
}

// DispatchRetryAlert 是派发重试达到告警阈值时发送给通知层的载荷。
type DispatchRetryAlert struct {
	DagKey        string
	NodeKey       string
	TargetAgentID string
	WakeupID      int64
	AttemptCount  int32
	RetryCount    int64
	LastError     string
}

// DispatchRetryAlertSink 是派发重试告警的窄端口。
// 业务路径只依赖该接口，具体通知渠道由 notify 包装配。
type DispatchRetryAlertSink interface {
	AlertDispatchRetry(ctx context.Context, alert DispatchRetryAlert) error
}

// recordDispatchFailedMetric 记录一次派发失败指标。
func recordDispatchFailedMetric() {
	dagmetrics.IncDispatchFailed()
}

// recordDispatchRetryMetric 记录派发重试指标，并在达到阈值时生成告警载荷。
func recordDispatchRetryMetric(w *taskdag.Wakeup, lastErr string) (DispatchRetryAlert, bool) {
	if w == nil {
		return DispatchRetryAlert{}, false
	}
	attemptCount := w.AttemptCount
	if attemptCount < 1 {
		attemptCount = 1
	}
	record := dagmetrics.RecordRetry(w.DagKey, w.NodeKey, attemptCount)
	if record.DagKey == "" || record.NodeKey == "" {
		return DispatchRetryAlert{}, false
	}
	return DispatchRetryAlert{
		DagKey:        record.DagKey,
		NodeKey:       record.NodeKey,
		TargetAgentID: w.TargetAgentID,
		WakeupID:      w.ID,
		AttemptCount:  record.AttemptCount,
		RetryCount:    int64(record.Count),
		LastError:     lastErr,
	}, record.ShouldAlert
}

// normalizeDispatchInputs trim 三个必填字段并检查 service / dispatchStore 到位。
// 拆出独立函数是为了压住 DispatchNode 主高的 CC。
func normalizeDispatchInputs(s *service, req contract.DispatchNodeRequest) (string, string, string, int64, error) {
	if s == nil || s.dispatchStore == nil {
		return "", "", "", 0, ErrDispatchStoreUnset
	}
	dagKey, nodeKey, assignedTo := strings.TrimSpace(req.DagKey), strings.TrimSpace(req.NodeKey), strings.TrimSpace(req.AssignedTo)
	if dagKey == "" || nodeKey == "" || assignedTo == "" {
		return "", "", "", 0, fmt.Errorf("orchestration: DispatchNode: dag_key/node_key/assigned_to required (got %q/%q/%q)", dagKey, nodeKey, assignedTo)
	}
	if req.RunID <= 0 {
		return "", "", "", 0, fmt.Errorf("orchestration: DispatchNode: run_id required for runtime node dispatch (got %d)", req.RunID)
	}
	return dagKey, nodeKey, assignedTo, req.RunID, nil
}

// findDispatchTarget 走 dispatchStore.ListRunNodes 拿到当前 run 的目标节点。
func (s *service) findDispatchTarget(ctx context.Context, dagKey, nodeKey string, runID int64) (*taskdag.Node, error) {
	nodes, err := s.dispatchStore.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		return nil, fmt.Errorf("orchestration: DispatchNode list run nodes %s run_id=%d: %w", dagKey, runID, err)
	}
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			return &nodes[i], nil
		}
	}
	return nil, fmt.Errorf("orchestration: DispatchNode: node %s/%s not found", dagKey, nodeKey)
}

// ensureDispatchEligible 状态闸：仅放 pending / ready 两状态过。
func ensureDispatchEligible(target *taskdag.Node) error {
	switch target.Status {
	case "pending", "ready":
		return nil
	default:
		return fmt.Errorf("%w (current_status=%q)", ErrDispatchNodeIneligible, target.Status)
	}
}

// assignAndPersist 把 assigned_to 写到 runtime node。其他列原样保留。
func (s *service) assignAndPersist(ctx context.Context, target *taskdag.Node, assignedTo string, runID int64) (*taskdag.Node, error) {
	assigned, err := s.dispatchStore.AssignNode(ctx, taskdag.AssignNodeInput{
		DagKey:     target.DagKey,
		NodeKey:    target.NodeKey,
		RunID:      runID,
		AssignedTo: assignedTo,
	})
	if err != nil {
		return nil, fmt.Errorf("orchestration: DispatchNode assign %s/%s run_id=%d: %w", target.DagKey, target.NodeKey, runID, err)
	}
	return assigned, nil
}

// enqueueManualDispatchWakeup 构建 idempotency_key 并入队 manual_dispatch wakeup。
// 同 assignee 多次 dispatch 被 ON CONFLICT 去重；换 assignee 重试得到新 row。
func (s *service) enqueueManualDispatchWakeup(ctx context.Context, dagKey, nodeKey string, runID int64, assignedTo string) (int64, error) {
	payload, err := json.Marshal(taskdag.DownstreamWakeupPayload{AgentID: assignedTo})
	if err != nil {
		return 0, fmt.Errorf("orchestration: DispatchNode marshal payload: %w", err)
	}
	wakeupID, err := s.dispatchStore.EnqueueWakeup(ctx, taskdag.EnqueueWakeupInput{
		DagKey:         dagKey,
		NodeKey:        nodeKey,
		RunID:          runID,
		WakeupKind:     dispatchNodeWakeupKind,
		TargetAgentID:  assignedTo,
		PromptPayload:  payload,
		IdempotencyKey: taskdag.ManualDispatchIdempotencyKey(dagKey, nodeKey, runID, assignedTo),
	})
	if err != nil {
		return 0, fmt.Errorf("orchestration: DispatchNode enqueue %s/%s: %w", dagKey, nodeKey, err)
	}
	return wakeupID, nil
}

type Scheduler interface {
	Tick(context.Context, time.Time) (int, error)
	Schedule(context.Context, string) error
}

// ErrSchedulerNotImplemented 表示旧 Scheduler 端口仍未接入外部实现。
// 生产 scheduled DAG 路径已走 cron.ScheduledDAGTicker；该 sentinel 仅服务保留接口和测试兼容。
var ErrSchedulerNotImplemented = errors.New("scheduler: not implemented in skeleton stage (F5.x)")

// noopScheduler 是保留接口的空实现，所有操作都返回 ErrSchedulerNotImplemented。
type noopScheduler struct{}

// Tick 触发一次调度扫描。
func (noopScheduler) Tick(context.Context, time.Time) (int, error) {
	return 0, ErrSchedulerNotImplemented
}

// Schedule 注册下一次调度唤醒。
func (noopScheduler) Schedule(_ context.Context, _ string) error { return ErrSchedulerNotImplemented }

// NewNoopScheduler 创建不执行外部调度的空实现。
func NewNoopScheduler() Scheduler { return noopScheduler{} }

// StartDAG 创建一次 DAG run，不直接改模板。
// 根节点会先变 ready；没 assigned_to 的根节点要等 task_dispatch_node。
func (s *service) StartDAG(ctx context.Context, req StartDAGRequest) (StartDAGResponse, error) {
	dagKey, dag, err := s.validateStartDAGPrereq(ctx, req)
	if err != nil {
		return StartDAGResponse{}, err
	}
	runKey := generateRunKey(dagKey, req.IdempotencyKey)
	triggerSource := strings.TrimSpace(req.TriggerSource)
	input := taskdag.CreateRunInput{RunKey: runKey, DagKey: dagKey, DagVersionSnapshot: dag.Version, TriggerSource: triggerSource}
	return s.runStartDAGWithFallback(ctx, dagKey, runKey, input)
}

// StartScheduledDAG 按计划启动 DAG，并写入本次运行记录。
func (s *service) StartScheduledDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	if s == nil || s.scheduledStartStore == nil {
		return ErrRunStoreUnset
	}
	return scheduledstart.Start(ctx, s.scheduledStartStore, req)
}

// ScheduledDAGStartService 是 cron 子包启动 scheduled DAG 时依赖的窄端口。
type ScheduledDAGStartService interface {
	StartScheduledDAG(context.Context, orchcron.ScheduledDAGStartRequest) error
}

// ProvideScheduledDAGStartService 为 fx 提供计划 DAG 启动服务。
func ProvideScheduledDAGStartService(s *service) ScheduledDAGStartService { return s }

// validateStartDAGPrereq 校验计划启动 DAG 前必须存在的依赖。
func (s *service) validateStartDAGPrereq(ctx context.Context, req StartDAGRequest) (string, *taskdag.DAG, error) {
	if s == nil || s.dagStore == nil {
		return "", nil, ErrLifecycleNotImplemented
	}
	if s.runStore == nil {
		return "", nil, ErrRunStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return "", nil, fmt.Errorf("orchestration: StartDAG: dag_key required")
	}
	dag, err := s.dagStore.GetDAG(ctx, dagKey)
	if err != nil {
		return "", nil, fmt.Errorf("orchestration: StartDAG: GetDAG(%q): %w", dagKey, err)
	}
	if dag == nil {
		return "", nil, fmt.Errorf("%w: %s", ErrDAGNotFound, dagKey)
	}
	return dagKey, dag, nil
}

// runStartDAGWithFallback 创建 run、复制节点、调度根节点，三步必须在同一事务里完成。
// 并发启动靠数据库唯一键兜住，不在这里先查再猜。
func (s *service) runStartDAGWithFallback(ctx context.Context, dagKey, runKey string, input taskdag.CreateRunInput) (StartDAGResponse, error) {
	var resp StartDAGResponse
	txErr := s.runStore.WithRunTx(ctx, func(tx taskdag.RunStore) error {
		lockedDAG, err := lockDAGForRunStart(ctx, tx, dagKey)
		if err != nil {
			return err
		}
		input.DagVersionSnapshot = lockedDAG.Version
		run, err := tx.CreateRun(ctx, input)
		if err != nil {
			return fmt.Errorf("CreateRun: %w", err)
		}
		if _, err := tx.CloneNodesForRun(ctx, dagKey, run.ID); err != nil {
			return fmt.Errorf("CloneNodesForRun: %w", err)
		}
		readyRootNodes, scheduledWakeups, err := taskdag.PromoteAndScheduleRunRoots(ctx, tx, dagKey, run.ID)
		if err != nil {
			return err
		}
		resp = contract.NewStartDAGResponse(run.ID, run.RunKey, run.DagVersionSnapshot, readyRootNodes, scheduledWakeups)
		return nil
	})
	if txErr == nil {
		return resp, nil
	}
	if !platformdb.IsUniqueViolation(txErr) {
		return StartDAGResponse{}, fmt.Errorf("orchestration: StartDAG(%q): %w", dagKey, txErr)
	}
	return s.resolveStartDAGUniqueViolation(ctx, dagKey, runKey, txErr)
}

// lockDAGForRunStart 在启动 run 前锁住模板行，确保 version 和复制出的节点来自同一版。
// 去掉这把锁会让 apply_ops 与 start 并发时产生混版 run。
func lockDAGForRunStart(ctx context.Context, tx taskdag.RunStore, dagKey string) (*taskdag.DAG, error) {
	locker, ok := tx.(interface {
		GetDAGForUpdate(context.Context, string) (*taskdag.DAG, error)
	})
	if !ok {
		return nil, fmt.Errorf("%w: run tx store does not support DAG row lock", ErrRunStoreUnset)
	}
	dag, err := locker.GetDAGForUpdate(ctx, dagKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s", ErrDAGNotFound, dagKey)
		}
		return nil, fmt.Errorf("GetDAGForUpdate: %w", err)
	}
	if dag == nil {
		return nil, fmt.Errorf("%w: %s", ErrDAGNotFound, dagKey)
	}
	return dag, nil
}

// resolveStartDAGUniqueViolation 处理 run_key 唯一键冲突后的幂等返回。
// 同一个 run_key 已经 running/succeeded 时返回已有 run。
// 如果它 failed/cancelled，调用方要换 idempotency_key，不能复用旧失败 run。
func (s *service) resolveStartDAGUniqueViolation(ctx context.Context, dagKey, runKey string, txErr error) (StartDAGResponse, error) {
	existing, getErr := s.runStore.GetRun(ctx, runKey)
	if getErr == nil && existing != nil {
		switch existing.Status {
		case "running":
			scheduledWakeups, err := s.runStore.ScheduleRootWakeups(ctx, dagKey, existing.ID)
			if err != nil {
				return StartDAGResponse{}, fmt.Errorf("orchestration: StartDAG(%q): ScheduleRootWakeups existing run %s: %w", dagKey, existing.RunKey, err)
			}
			return contract.NewExistingStartDAGResponse(existing.ID, existing.RunKey, existing.DagVersionSnapshot, existing.Status, scheduledWakeups), nil
		case "succeeded":
			return contract.NewExistingStartDAGResponse(existing.ID, existing.RunKey, existing.DagVersionSnapshot, existing.Status, 0), nil
		case "failed", "cancelled":
			return StartDAGResponse{}, &IdempotencyKeyExhaustedError{RunKey: existing.RunKey, Status: existing.Status}
		default:
			return StartDAGResponse{}, fmt.Errorf("orchestration: StartDAG(%q): unexpected run status %q for run_key=%s", dagKey, existing.Status, runKey)
		}
	}
	if getErr != nil && !platformdb.IsNotFound(getErr) {
		return StartDAGResponse{}, fmt.Errorf("orchestration: StartDAG(%q): GetRun fallback: %w (original tx error: %v)", dagKey, getErr, txErr)
	}
	return StartDAGResponse{}, fmt.Errorf("orchestration: StartDAG(%q): unresolved unique violation for run_key=%s: %w", dagKey, runKey, txErr)
}

// generateRunKey 用 dag_key 和 idempotency_key 生成 run_key；未给幂等键时使用时间戳。
func generateRunKey(dagKey, idempotencyKey string) string {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		return fmt.Sprintf("%s#run-%s", dagKey, idempotencyKey)
	}
	return fmt.Sprintf("%s#run-%d", dagKey, time.Now().UnixNano())
}
