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

// ErrDispatchIncomplete 表示检测到历史半写 assignment 但没有可继续投递的 wakeup。
var ErrDispatchIncomplete = errors.New("orchestration: node dispatch incomplete")

// dispatchNodeWakeupKind 是 task_dispatch_node 入队的 wakeup_kind 值。
// 与 store_complete_downstream.go 的 downstreamWakeupKind 区分：手工显式
// dispatch 用 "manual_dispatch" 让运维 + 日志能一眼分流，避免与依赖完成自动
// enqueue 混淆。
const dispatchNodeWakeupKind = "manual_dispatch"

// DispatchNode 保持 service 对外 RPC/tool 方法不变，并把节点派发逻辑委托给 dagController。
func (s *service) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	return s.dagFacade().DispatchNode(ctx, req)
}

// DispatchNode 领取并调度一个可运行的 DAG 节点。
// 只接受 runtime run 上 pending/ready 节点；agent 节点必须先配置 exec.cwd，避免入队后才失败。
func (c *dagController) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	dagKey, nodeKey, assignedTo, runID, err := normalizeDispatchInputs(c, req)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	target, err := c.findDispatchTarget(ctx, dagKey, nodeKey, runID)
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
	if err := c.blockDispatchIncomplete(ctx, target, assignedTo, runID); err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	result, err := c.assignAndEnqueueDispatch(ctx, target, assignedTo, runID)
	if err != nil {
		return contract.DispatchNodeResponse{}, err
	}
	resp := contract.DispatchNodeResponse{
		WakeupID: result.WakeupID,
		Enqueued: result.WakeupID > 0,
	}
	if result.Node != nil {
		resp.Node = dagNodeDTO(*result.Node)
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
	dagmetrics.DefaultRegistry().IncDispatchFailed()
}

// recordDispatchRetryMetric 记录派发重试指标，并在达到阈值时生成告警载荷。
func recordDispatchRetryMetric(w *taskdag.Wakeup, lastErr string) (DispatchRetryAlert, bool) {
	if w == nil {
		return DispatchRetryAlert{}, false
	}
	attemptCount := max(w.AttemptCount, 1)
	record := dagmetrics.DefaultRegistry().RecordRetry(w.DagKey, w.NodeKey, attemptCount)
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

// normalizeDispatchInputs trim 三个必填字段并检查 dagController / dispatchStore 到位。
// 拆出独立函数是为了压住 DispatchNode 主高的 CC。
func normalizeDispatchInputs(c *dagController, req contract.DispatchNodeRequest) (string, string, string, int64, error) {
	if c == nil || c.dispatchStore == nil {
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
func (c *dagController) findDispatchTarget(ctx context.Context, dagKey, nodeKey string, runID int64) (*taskdag.Node, error) {
	nodes, err := c.dispatchStore.ListRunNodes(ctx, dagKey, runID)
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

// blockDispatchIncomplete 在重复派发前标记历史半写节点并阻断。
// 这能把 assign 成功但 wakeup 入队失败的旧状态显式暴露为 dispatch_incomplete。
func (c *dagController) blockDispatchIncomplete(ctx context.Context, target *taskdag.Node, assignedTo string, runID int64) error {
	if strings.TrimSpace(target.AssignedTo) == "" {
		return nil
	}
	result, err := c.dispatchStore.MarkDispatchIncompleteIfMissingWakeup(ctx, taskdag.MarkDispatchIncompleteInput{
		DagKey:     target.DagKey,
		NodeKey:    target.NodeKey,
		RunID:      runID,
		AssignedTo: target.AssignedTo,
	})
	if err != nil {
		return fmt.Errorf("orchestration: DispatchNode preflight %s/%s run_id=%d: %w", target.DagKey, target.NodeKey, runID, err)
	}
	if result != nil && result.Marked {
		return fmt.Errorf("%w: node %s/%s run_id=%d assigned_to=%q has no active wakeup; status=dispatch_incomplete", ErrDispatchIncomplete, target.DagKey, target.NodeKey, runID, assignedTo)
	}
	return nil
}

// assignAndEnqueueDispatch 在 store 事务里同时写 assigned_to 和 manual_dispatch wakeup。
// 任何一步失败都不能留下半写 assignment。
func (c *dagController) assignAndEnqueueDispatch(ctx context.Context, target *taskdag.Node, assignedTo string, runID int64) (*taskdag.AssignNodeAndEnqueueWakeupResult, error) {
	payload, err := json.Marshal(taskdag.DownstreamWakeupPayload{AgentID: assignedTo})
	if err != nil {
		return nil, fmt.Errorf("orchestration: DispatchNode marshal payload: %w", err)
	}
	result, err := c.dispatchStore.AssignNodeAndEnqueueWakeup(ctx, taskdag.AssignNodeAndEnqueueWakeupInput{
		Assign: taskdag.AssignNodeInput{
			DagKey:     target.DagKey,
			NodeKey:    target.NodeKey,
			RunID:      runID,
			AssignedTo: assignedTo,
		},
		Wakeup: taskdag.EnqueueWakeupInput{
			DagKey:         target.DagKey,
			NodeKey:        target.NodeKey,
			RunID:          runID,
			WakeupKind:     dispatchNodeWakeupKind,
			TargetAgentID:  assignedTo,
			PromptPayload:  payload,
			IdempotencyKey: taskdag.ManualDispatchIdempotencyKey(target.DagKey, target.NodeKey, runID, assignedTo),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("orchestration: DispatchNode assign+enqueue %s/%s run_id=%d: %w", target.DagKey, target.NodeKey, runID, err)
	}
	return result, nil
}

// Scheduler 是保留给旧调度 wiring 的最小端口。
// 生产 scheduled DAG 已由 cron.ScheduledDAGTicker 承担；这里保持显式未实现边界。
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

// StartDAG 保持 service 对外 RPC/tool 方法不变，并把 DAG 启动逻辑委托给 dagController。
func (s *service) StartDAG(ctx context.Context, req StartDAGRequest) (StartDAGResponse, error) {
	return s.dagFacade().StartDAG(ctx, req)
}

// StartDAG 创建一次 DAG run，不直接改模板。
// 根节点会先变 ready；没 assigned_to 的根节点要等 task_dispatch_node。
func (c *dagController) StartDAG(ctx context.Context, req StartDAGRequest) (StartDAGResponse, error) {
	dagKey, dag, err := c.validateStartDAGPrereq(ctx, req)
	if err != nil {
		return StartDAGResponse{}, err
	}
	runKey := generateRunKey(dagKey, req.IdempotencyKey)
	triggerSource := strings.TrimSpace(req.TriggerSource)
	input := taskdag.CreateRunInput{RunKey: runKey, DagKey: dagKey, DagVersionSnapshot: dag.Version, TriggerSource: triggerSource}
	return c.runStartDAGWithFallback(ctx, dagKey, runKey, input)
}

// StartScheduledDAG 保持 service 对外 RPC/tool 方法不变，并把计划启动逻辑委托给 dagController。
func (s *service) StartScheduledDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	return s.dagFacade().StartScheduledDAG(ctx, req)
}

// StartScheduledDAG 按计划启动 DAG，并写入本次运行记录。
func (c *dagController) StartScheduledDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	if c == nil || c.scheduledStartStore == nil {
		return ErrRunStoreUnset
	}
	return scheduledstart.Start(ctx, c.scheduledStartStore, req)
}

// ScheduledDAGStartService 是 cron 子包启动 scheduled DAG 时依赖的窄端口。
type ScheduledDAGStartService interface {
	StartScheduledDAG(context.Context, orchcron.ScheduledDAGStartRequest) error
}

// validateStartDAGPrereq 校验计划启动 DAG 前必须存在的依赖。
func (c *dagController) validateStartDAGPrereq(ctx context.Context, req StartDAGRequest) (string, *taskdag.DAG, error) {
	if c == nil || c.dagStore == nil {
		return "", nil, ErrLifecycleNotImplemented
	}
	if c.runStore == nil {
		return "", nil, ErrRunStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return "", nil, fmt.Errorf("orchestration: StartDAG: dag_key required")
	}
	dag, err := c.dagStore.GetDAG(ctx, dagKey)
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
func (c *dagController) runStartDAGWithFallback(ctx context.Context, dagKey, runKey string, input taskdag.CreateRunInput) (StartDAGResponse, error) {
	var resp StartDAGResponse
	txErr := c.runStore.WithRunTx(ctx, func(tx taskdag.RunStore) error {
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
	return c.resolveStartDAGUniqueViolation(ctx, dagKey, runKey, txErr)
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
func (c *dagController) resolveStartDAGUniqueViolation(ctx context.Context, dagKey, runKey string, txErr error) (StartDAGResponse, error) {
	existing, getErr := c.runStore.GetRun(ctx, runKey)
	if getErr == nil && existing != nil {
		switch existing.Status {
		case "running":
			scheduledWakeups, err := c.runStore.ScheduleRootWakeups(ctx, dagKey, existing.ID)
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
