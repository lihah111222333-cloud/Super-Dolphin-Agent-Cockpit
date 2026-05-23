package orchestration

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

type Scheduler interface {
	Tick(ctx context.Context, now time.Time) (int, error)
	Schedule(ctx context.Context, dagKey string) error
}

var ErrSchedulerNotImplemented = errors.New("scheduler: not implemented in skeleton stage (F5.x)")

type noopScheduler struct{}

func (noopScheduler) Tick(_ context.Context, _ time.Time) (int, error) {
	return 0, ErrSchedulerNotImplemented
}

func (noopScheduler) Schedule(_ context.Context, _ string) error { return ErrSchedulerNotImplemented }

func NewNoopScheduler() Scheduler { return noopScheduler{} }

type scheduledDAGStarter struct{ svc *service }

func (s scheduledDAGStarter) StartDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	_, err := s.svc.StartDAG(ctx, StartDAGRequest{DagKey: req.DagKey, TriggerSource: req.TriggerSource, IdempotencyKey: req.IdempotencyKey})
	return err
}

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

func generateRunKey(dagKey, idempotencyKey string) string {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		return fmt.Sprintf("%s#run-%s", dagKey, idempotencyKey)
	}
	return fmt.Sprintf("%s#run-%d", dagKey, time.Now().UnixNano())
}

func dagVersionFor(dag *taskdag.DAG) int64 { return dag.Version }
