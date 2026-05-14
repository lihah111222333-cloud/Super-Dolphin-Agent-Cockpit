package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// Scheduler 是 cron 调度器接口（骨架阶段 stub） —— 蓝图 v2 §11 阶段 F5
// + 实施计划 S2.3。F5.1-F5.3 真实实现：cron daemon 进程 + tick 扫
// next_run_at + 多实例锁。
type Scheduler interface {
	// Tick 在 now 时间扫描所有 next_run_at <= now 的 trigger=scheduled DAG，
	// 对每个调用 service.StartDAG。返回触发的 DAG 数量。
	Tick(ctx context.Context, now time.Time) (int, error)

	// Schedule 把一个 DAG 的下次触发时间写入 next_run_at（基于 cron_expr）。
	// 用于 DAG 创建/编辑后刷新调度；F5.1 真实实现。
	Schedule(ctx context.Context, dagKey string) error
}

// ErrSchedulerNotImplemented 是骨架阶段 stub 方法的 sentinel 错误。
var ErrSchedulerNotImplemented = errors.New("scheduler: not implemented in skeleton stage (F5.x)")

// noopScheduler 是骨架阶段的 stub 实现，所有方法返回 ErrSchedulerNotImplemented。
type noopScheduler struct{}

func (noopScheduler) Tick(_ context.Context, _ time.Time) (int, error) {
	return 0, ErrSchedulerNotImplemented
}

func (noopScheduler) Schedule(_ context.Context, _ string) error {
	return ErrSchedulerNotImplemented
}

// NewNoopScheduler 返回骨架阶段的 stub Scheduler。
// 生产路径在 F5.1 用 cron daemon 实现替换。
func NewNoopScheduler() Scheduler {
	return noopScheduler{}
}

// StartDAG 触发 DAG 一次新执行。F6.5 后同一 DAG 可有多个 running run：
//
//  1. validateStartDAGPrereq: 检 service 预设 + dag_key 非空 + GetDAG 预检存在性。
//     不再在应用层做 CountActiveRunsByDagKey 预检；0089 已移除 dag-level
//     single-running-run 约束。
//  2. WithRunTx 内先 GetDAGForUpdate 锁 DAG row，再原子化 CreateRun +
//     PromoteRootNodesToReady。任一失败回滚、避免“run 已建却根节点未 ready”
//     脱状态；DAG row lock 与 ApplyOps 共用同一序列化点，避免 start 与
//     remove/update 交错。
//  3. PG unique violation (SQLSTATE 23505) 后备路径（L3 GetRun-first 策略）：
//     事务已回滚后在 tx 外 GetRun(run_key)：
//     - 命中 → 同 IdempotencyKey 重入，幂等返已有 run
//     - 未命中 → 非 run_key 唯一冲突，带原始 tx error 返回
//     不依赖 ConstraintName 字符串 (PG 同时冲突哪个先报不可控)。
//
// run_key 生成：IdempotencyKey 非空 → “<dagKey>#run-<idem>”；IdempotencyKey
// 为空 → “<dagKey>#run-<unix_nano>” 保证唯一。
func (s *service) StartDAG(ctx context.Context, req StartDAGRequest) (StartDAGResponse, error) {
	dagKey, dag, err := s.validateStartDAGPrereq(ctx, req)
	if err != nil {
		return StartDAGResponse{}, err
	}
	runKey := generateRunKey(dagKey, req.IdempotencyKey)
	triggerSource := strings.TrimSpace(req.TriggerSource)
	dagVersion := dagVersionFor(dag)
	input := taskdag.CreateRunInput{
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: dagVersion,
		TriggerSource:      triggerSource,
	}
	return s.runStartDAGWithFallback(ctx, dagKey, runKey, input)
}

// validateStartDAGPrereq 检 service 预设 + dag_key 非空 + 调 GetDAG 做存在性预检。
// 不再检 CountActiveRunsByDagKey；F6.5 允许同 DAG 多 run 并发。
// 权威 DAG row lock 在 runStartDAGWithFallback 的事务内执行。
// 返回三元组 (dagKey, dag, error)。拆出 helper 让 StartDAG 主体保持在 CC≤10。
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

// runStartDAGWithFallback 走 WithRunTx GetDAGForUpdate + CreateRun + Promote。
// 失败是 PG unique violation 时：tx 已回滚，在 tx 外 GetRun(run_key) 处理
// 幂等返已有 run。GetRun miss 表示不是 run_key 幂等冲突，返回原 tx error。
// 不依赖 ConstraintName（PG 同时冲突哪个先报 OID 顺序不可控）。
func (s *service) runStartDAGWithFallback(ctx context.Context, dagKey, runKey string, input taskdag.CreateRunInput) (StartDAGResponse, error) {
	var resp StartDAGResponse
	txErr := s.runStore.WithRunTx(ctx, func(tx taskdag.RunStore) error {
		lockedDAG, err := lockDAGForRunStart(ctx, tx, dagKey)
		if err != nil {
			return err
		}
		input.DagVersionSnapshot = dagVersionFor(lockedDAG)
		run, err := tx.CreateRun(ctx, input)
		if err != nil {
			return fmt.Errorf("CreateRun: %w", err)
		}
		if _, err := tx.CloneNodesForRun(ctx, dagKey, run.ID); err != nil {
			return fmt.Errorf("CloneNodesForRun: %w", err)
		}
		if _, err := tx.PromoteRootNodesToReady(ctx, dagKey, run.ID); err != nil {
			return fmt.Errorf("PromoteRootNodesToReady: %w", err)
		}
		resp = StartDAGResponse{RunKey: run.RunKey, Version: run.DagVersionSnapshot}
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

type runStartDAGDAGLocker interface {
	GetDAGForUpdate(ctx context.Context, dagKey string) (*taskdag.DAG, error)
}

func lockDAGForRunStart(ctx context.Context, tx taskdag.RunStore, dagKey string) (*taskdag.DAG, error) {
	locker, ok := tx.(runStartDAGDAGLocker)
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

// resolveStartDAGUniqueViolation 是 WithRunTx 遭 unique violation 后的备路径。
// 事务已回滚、在 tx 外 GetRun(run_key)。拆出 helper 保 runStartDAGWithFallback
// CC≤10。
//
// 幂等语义（路线 N）：
//   - 命中 existing：
//   - status = running   → 返旧 RunKey（去重网络重试）
//   - status = succeeded → 返旧 RunKey（幂等成功结果）
//   - status = failed/cancelled → 返 ErrIdempotencyKeyExhausted
//     （调用方需换新 idempotency key 重试）
//   - 未知 status → 防御性报错
//   - 未命中 → 非 run_key 唯一冲突，返回原 tx error
//
// Idempotency semantics (route N):
//   - GetRun hit:
//   - status running   → return existing RunKey (network-retry dedup)
//   - status succeeded → return existing RunKey (idempotent success)
//   - status failed/cancelled → ErrIdempotencyKeyExhausted (caller must use new key)
//   - unknown status → defensive error
//   - GetRun miss → non-run_key unique violation, return the original tx error
//
// 设计取舍：succeeded 与 running 同 case 复用 RunKey。这是 RFC-Idempotency 标准做法
// （如 Stripe Idempotency-Key），把成功的幂等结果回放给重试调用方。
// 如团队选"更激进版 N"（succeeded 也 exhausted），需把 succeeded 移到 exhausted
// case 并补迁移说明（已交付调用方可能依赖当前语义）。
//
// Design trade-off: succeeded shares the running case to replay the idempotent
// success result, following the RFC-Idempotency convention (e.g. Stripe
// Idempotency-Key). For a "stricter route N" where succeeded is also exhausted,
// move succeeded into the exhausted case and add migration notes (existing
// callers may rely on the current semantics).
func (s *service) resolveStartDAGUniqueViolation(ctx context.Context, dagKey, runKey string, txErr error) (StartDAGResponse, error) {
	existing, getErr := s.runStore.GetRun(ctx, runKey)
	if getErr == nil && existing != nil {
		switch existing.Status {
		case "running", "succeeded":
			return StartDAGResponse{RunKey: existing.RunKey, Version: existing.DagVersionSnapshot}, nil
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

// generateRunKey 生成 task_dag_runs.run_key，与 UNIQUE 约束兼容。
func generateRunKey(dagKey, idempotencyKey string) string {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		return fmt.Sprintf("%s#run-%s", dagKey, idempotencyKey)
	}
	return fmt.Sprintf("%s#run-%d", dagKey, time.Now().UnixNano())
}

// dagVersionFor 从 contract.DAG 取 version 字段。骨架阶段 contract.DAG 未伸
// version 字段（task_dags.version 在 0072 migration 后才加，sqlc 生成中仍未
// 随 sqlc-1.30 realignment 运行），这里先返 0。F4.x ApplyOps OCC 落地后
// version 会被写进 run。
func dagVersionFor(_ *taskdag.DAG) int64 {
	return 0
}
