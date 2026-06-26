package scheduledstart

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// ErrRunStoreUnset 表示 scheduled start 没有注入 run store，调用方必须显式失败。
var ErrRunStoreUnset = errors.New("scheduled start: run store unset")

// Start 在单个事务内创建 scheduled run、克隆节点、调度根节点并推进 next_run_at。
// run_key 使用 cron idempotency key，唯一冲突时只消费已存在 run 后继续推进调度游标。
func Start(ctx context.Context, runStore taskdag.ScheduledStartStore, req orchcron.ScheduledDAGStartRequest) error {
	dagKey, dueAt, nextRunAt, idempotencyKey, err := normalizeRequest(req)
	if err != nil {
		return err
	}
	if runStore == nil {
		return ErrRunStoreUnset
	}
	runKey := generateRunKey(dagKey, idempotencyKey)
	input := taskdag.CreateRunInput{RunKey: runKey, DagKey: dagKey, TriggerSource: "scheduled"}
	txErr := runStore.WithScheduledStartTx(ctx, func(tx taskdag.ScheduledStartTxStore) error {
		lockedDAG, err := lockDAGForRunStart(ctx, tx, dagKey, dueAt)
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
		if err := rejectBlockedScheduledStart(dagKey, readyRootNodes, scheduledWakeups); err != nil {
			return err
		}
		return advanceNextRunTx(ctx, tx, dagKey, dueAt, nextRunAt)
	})
	if txErr == nil {
		return nil
	}
	if !platformdb.IsUniqueViolation(txErr) {
		return fmt.Errorf("scheduled start %q: %w", dagKey, txErr)
	}
	return advanceConsumedRun(ctx, runStore, dagKey, runKey, dueAt, nextRunAt, txErr)
}

// rejectBlockedScheduledStart 在根节点 ready 但没有 wakeup 时阻断，避免 scheduled DAG 静默空跑。
func rejectBlockedScheduledStart(dagKey string, readyRootNodes, scheduledWakeups int64) error {
	if readyRootNodes > 0 && scheduledWakeups == 0 {
		return fmt.Errorf("scheduled start %q: ready root nodes=%d but scheduled wakeups=0; root dispatch is blocked by missing assigned_to or node.config.exec", dagKey, readyRootNodes)
	}
	return nil
}

// normalizeRequest 校验 scheduled start 请求的必填字段，并返回清理后的 key 和时间。
func normalizeRequest(req orchcron.ScheduledDAGStartRequest) (string, time.Time, time.Time, string, error) {
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return "", time.Time{}, time.Time{}, "", errors.New("scheduled start: dag_key required")
	}
	if req.DueAt.IsZero() {
		return "", time.Time{}, time.Time{}, "", fmt.Errorf("scheduled start %q: due_at required", dagKey)
	}
	if req.NextRunAt.IsZero() {
		return "", time.Time{}, time.Time{}, "", fmt.Errorf("scheduled start %q: next_run_at required", dagKey)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return "", time.Time{}, time.Time{}, "", fmt.Errorf("scheduled start %q: idempotency_key required", dagKey)
	}
	if strings.TrimSpace(req.TriggerSource) != "scheduled" {
		return "", time.Time{}, time.Time{}, "", fmt.Errorf("scheduled start %q: trigger_source must be scheduled", dagKey)
	}
	return dagKey, req.DueAt, req.NextRunAt, idempotencyKey, nil
}

// lockDAGForRunStart 在事务内锁定 DAG，并确认 schedule 状态仍匹配本次 due_at。
func lockDAGForRunStart(ctx context.Context, tx taskdag.ScheduledStartTxStore, dagKey string, dueAt time.Time) (*taskdag.DAG, error) {
	dag, err := tx.GetDAGForUpdate(ctx, dagKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, fmt.Errorf("scheduled start: dag not found: %s", dagKey)
		}
		return nil, fmt.Errorf("GetDAGForUpdate: %w", err)
	}
	if dag == nil {
		return nil, fmt.Errorf("scheduled start: dag not found: %s", dagKey)
	}
	if strings.TrimSpace(dag.Trigger) != "scheduled" || strings.TrimSpace(dag.CronExpr) == "" {
		return nil, fmt.Errorf("%w: dag_key=%s schedule disabled", orchcron.ErrScheduleStateChanged, dagKey)
	}
	if dag.NextRunAt == nil || !dag.NextRunAt.Equal(dueAt) {
		return nil, fmt.Errorf("%w: dag_key=%s due_at=%s", orchcron.ErrScheduleStateChanged, dagKey, dueAt.UTC().Format(time.RFC3339Nano))
	}
	return dag, nil
}

// advanceNextRunTx 在持锁事务内推进 DAG 的 next_run_at，rows!=1 视为调度状态漂移。
func advanceNextRunTx(ctx context.Context, tx taskdag.ScheduledStartTxStore, dagKey string, dueAt, nextRunAt time.Time) error {
	rows, err := tx.UpdateScheduledDAGNextRun(ctx, dagKey, dueAt, nextRunAt)
	if err != nil {
		return fmt.Errorf("UpdateScheduledDAGNextRun: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: dag_key=%s rows=%d", orchcron.ErrScheduleStateChanged, dagKey, rows)
	}
	return nil
}

// advanceConsumedRun 处理 run_key 唯一冲突：确认既有 run 后补调度根 wakeup 并推进 next_run_at。
func advanceConsumedRun(ctx context.Context, runStore taskdag.ScheduledStartStore, dagKey, runKey string, dueAt, nextRunAt time.Time, txErr error) error {
	existing, getErr := runStore.GetRun(ctx, runKey)
	if getErr != nil {
		return fmt.Errorf("scheduled start %q: GetRun fallback: %w (original tx error: %v)", dagKey, getErr, txErr)
	}
	if existing == nil {
		return fmt.Errorf("scheduled start %q: unresolved unique violation for run_key=%s: %w", dagKey, runKey, txErr)
	}
	switch existing.Status {
	case "running", "succeeded", "failed", "cancelled":
	default:
		return fmt.Errorf("scheduled start %q: unexpected run status %q for run_key=%s", dagKey, existing.Status, runKey)
	}
	err := runStore.WithScheduledStartTx(ctx, func(tx taskdag.ScheduledStartTxStore) error {
		if _, err := lockDAGForRunStart(ctx, tx, dagKey, dueAt); err != nil {
			return err
		}
		if existing.Status == "running" {
			if _, err := tx.ScheduleRootWakeups(ctx, dagKey, existing.ID); err != nil {
				return fmt.Errorf("ScheduleRootWakeups existing run %s: %w", existing.RunKey, err)
			}
		}
		return advanceNextRunTx(ctx, tx, dagKey, dueAt, nextRunAt)
	})
	if err != nil {
		return fmt.Errorf("scheduled start %q: advance consumed run %s: %w", dagKey, runKey, err)
	}
	return nil
}

// generateRunKey 组合 DAG key 和 cron 幂等键，确保同一触发只创建一条 run。
func generateRunKey(dagKey, idempotencyKey string) string {
	return fmt.Sprintf("%s#run-%s", dagKey, strings.TrimSpace(idempotencyKey))
}
