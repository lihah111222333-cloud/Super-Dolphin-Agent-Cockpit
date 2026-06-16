package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/fxadapter"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration"
	orchcron "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sqlc"
)

const (
	scheduledDAGCronSpec    = "@every 1m"
	scheduledDAGCronLockKey = "mcp-orch:scheduled-dag-cron"
)

type dagCronDaemon interface {
	Start(context.Context) error
	Stop() error
}

type scheduledDAGCronRunner struct {
	daemon dagCronDaemon
}

// Run 启动编排后台流程。
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
	svc orchestration.ScheduledDAGStartService
}

// StartDAG 启动DAG。
func (s scheduledDAGStarter) StartDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	return s.svc.StartScheduledDAG(ctx, req)
}

func provideSQLDAGScheduleStore(q *sqlc.Queries) (orchcron.DAGScheduleStore, error) {
	if q == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron requires sqlc queries")
	}
	return fxadapter.NewSQLDAGScheduleStore(q)
}

func provideSQLiteRuntimeLocker(db *sql.DB) (orchcron.RuntimeLocker, error) {
	if db == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron runtime lock requires db")
	}
	return fxadapter.NewSQLiteRuntimeLocker(db, scheduledDAGCronLockKey)
}

// provideScheduledDAGCronRunner 提供scheduledDAGcronrunner。
func provideScheduledDAGCronRunner(
	store orchcron.DAGScheduleStore,
	locker orchcron.RuntimeLocker,
	svc orchestration.ScheduledDAGStartService,
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
