package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/fxadapter"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

const (
	// scheduledDAGCron* 固定 mcp-orch 内置 scheduled DAG 扫描频率和运行时锁键。
	scheduledDAGCronSpec    = "@every 1m"
	scheduledDAGCronLockKey = "mcp-orch:scheduled-dag-cron"
)

// dagCronDaemon 定义 DAG cron 调度器的启停接口。
type dagCronDaemon interface {
	Start(context.Context) error
	Stop() error
}

// scheduledDAGCronRunner 把 DAG cron 守护进程适配为 platformrunner.Runner 接口。
type scheduledDAGCronRunner struct {
	daemon dagCronDaemon
}

// Run 启动 scheduled DAG cron 守护进程，并在上游 context 结束时关闭。
// daemon 或 ctx 缺失都直接报错，避免后台调度静默空跑。
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

// scheduledDAGStarter 把 ScheduledDAGStartService 适配为 orchcron.DAGStarter 接口。
type scheduledDAGStarter struct {
	svc orchestration.ScheduledDAGStartService
}

// StartDAG 把 cron 子包的启动请求转交给 orchestration 服务。
func (s scheduledDAGStarter) StartDAG(ctx context.Context, req orchcron.ScheduledDAGStartRequest) error {
	return s.svc.StartScheduledDAG(ctx, req)
}

// provideSQLDAGScheduleStore 创建基于 SQLite 的 DAG 计划存储，q 为 nil 时报错。
func provideSQLDAGScheduleStore(q *sqlc.Queries) (orchcron.DAGScheduleStore, error) {
	if q == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron requires sqlc queries")
	}
	return fxadapter.NewSQLDAGScheduleStore(q)
}

// provideSQLiteRuntimeLocker 创建基于 SQLite 的运行时租约锁，db 为 nil 时报错。
func provideSQLiteRuntimeLocker(db *sql.DB) (orchcron.RuntimeLocker, error) {
	if db == nil {
		return nil, errors.New("mcp-orch: scheduled dag cron runtime lock requires db")
	}
	return fxadapter.NewSQLiteRuntimeLocker(db, scheduledDAGCronLockKey)
}

// provideScheduledDAGCronRunner 组装 scheduled DAG 后台 runner。
// store、locker、service、logger 都必须显式注入，缺任一项直接 fail-fast，避免 cron 静默空跑。
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
