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

var ErrRunStoreUnset = errors.New("scheduled start: run store unset")

func Start(ctx context.Context, runStore taskdag.RunStore, req orchcron.ScheduledDAGStartRequest) error {
	dagKey, dueAt, nextRunAt, idempotencyKey, err := normalizeRequest(req)
	if err != nil {
		return err
	}
	if runStore == nil {
		return ErrRunStoreUnset
	}
	runKey := generateRunKey(dagKey, idempotencyKey)
	input := taskdag.CreateRunInput{RunKey: runKey, DagKey: dagKey, TriggerSource: triggerSource(req)}
	txErr := runStore.WithRunTx(ctx, func(tx taskdag.RunStore) error {
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
		if _, _, err := taskdag.PromoteAndScheduleRunRoots(ctx, tx, dagKey, run.ID); err != nil {
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
	return dagKey, req.DueAt, req.NextRunAt, idempotencyKey, nil
}

func triggerSource(req orchcron.ScheduledDAGStartRequest) string {
	trigger := strings.TrimSpace(req.TriggerSource)
	if trigger != "" {
		return trigger
	}
	return "scheduled"
}

func lockDAGForRunStart(ctx context.Context, tx taskdag.RunStore, dagKey string, dueAt time.Time) (*taskdag.DAG, error) {
	locker, ok := tx.(interface {
		GetDAGForUpdate(context.Context, string) (*taskdag.DAG, error)
	})
	if !ok {
		return nil, ErrRunStoreUnset
	}
	dag, err := locker.GetDAGForUpdate(ctx, dagKey)
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

func advanceNextRunTx(ctx context.Context, tx taskdag.RunStore, dagKey string, dueAt, nextRunAt time.Time) error {
	updater, ok := tx.(interface {
		UpdateScheduledDAGNextRun(context.Context, string, time.Time, time.Time) (int64, error)
	})
	if !ok {
		return ErrRunStoreUnset
	}
	rows, err := updater.UpdateScheduledDAGNextRun(ctx, dagKey, dueAt, nextRunAt)
	if err != nil {
		return fmt.Errorf("UpdateScheduledDAGNextRun: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: dag_key=%s rows=%d", orchcron.ErrScheduleStateChanged, dagKey, rows)
	}
	return nil
}

func advanceConsumedRun(ctx context.Context, runStore taskdag.RunStore, dagKey, runKey string, dueAt, nextRunAt time.Time, txErr error) error {
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
	err := runStore.WithRunTx(ctx, func(tx taskdag.RunStore) error {
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

func generateRunKey(dagKey, idempotencyKey string) string {
	return fmt.Sprintf("%s#run-%s", dagKey, strings.TrimSpace(idempotencyKey))
}
