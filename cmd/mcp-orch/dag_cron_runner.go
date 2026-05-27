package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/fxadapter"
	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	scheduledDAGCronSpec           = "@every 1m"
	scheduledDAGCronAdvisoryLockID = int64(0x5350444743524f4e) // "SPDGCRON"
)

type dagCronDaemon interface {
	Start(context.Context) error
	Stop() error
}

type scheduledDAGCronRunner struct {
	daemon dagCronDaemon
}

func (r scheduledDAGCronRunner) Run(ctx context.Context) error {
	if r.daemon == nil {
		return errors.New("mcp-orch: scheduled dag cron daemon is nil")
	}
	if ctx == nil {
		return errors.New("mcp-orch: scheduled dag cron context is nil")
	}
	select {
	case <-ctx.Done():
		return nil
	default:
	}
	if err := r.daemon.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return r.daemon.Stop()
}

type scheduledDAGStarter struct {
	svc contract.OrchestrationService
}

func (s scheduledDAGStarter) StartDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	_, err := s.svc.StartDAG(ctx, contract.StartDAGRequest{
		DagKey:         req.DagKey,
		TriggerSource:  req.TriggerSource,
		IdempotencyKey: req.IdempotencyKey,
	})
	return err
}

func provideSQLDAGScheduleStore(q *sqlc.Queries) (orchcron.DAGScheduleStore, error) {
	if q == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron requires sqlc queries")
	}
	return fxadapter.NewSQLDAGScheduleStore(q)
}

func providePGAdvisoryLocker(pool *pgxpool.Pool) (orchcron.AdvisoryLocker, error) {
	if pool == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron advisory lock requires db pool")
	}
	return fxadapter.NewPGAdvisoryLocker(pool, scheduledDAGCronAdvisoryLockID)
}

func provideScheduledDAGCronRunner(
	store orchcron.DAGScheduleStore,
	locker orchcron.AdvisoryLocker,
	svc contract.OrchestrationService,
	logger *slog.Logger,
) (platformrunner.Runner, error) {
	if store == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron store is nil")
	}
	if locker == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron locker is nil")
	}
	if svc == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron service is nil")
	}
	if logger == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron logger is nil")
	}
	ticker, err := orchcron.NewScheduledDAGTicker(orchcron.ScheduledDAGTickerConfig{
		Store:   store,
		Starter: scheduledDAGStarter{svc: svc},
		Locker:  locker,
	})
	if err != nil {
		return nil, err
	}
	daemon, err := orchcron.NewCronScheduler(orchcron.Config{
		Spec:   scheduledDAGCronSpec,
		Logger: logger,
		Ticker: ticker,
	})
	if err != nil {
		return nil, err
	}
	return scheduledDAGCronRunner{daemon: daemon}, nil
}
