// Package cron 提供 DAG 改造 Scheduler 蓝图 v2 §5 所需的 cron daemon 进程入口
// 进程入口与到点触发实现：
//
//   - F5.1（本文件）：cron daemon 进程入口 + 接 robfig/cron + Tick 占位
//   - F5.2：Tick 真实业务 SQL —— 扫 next_run_at <= now 的 trigger=scheduled
//     DAG，调 StartDAG 注入适配口，避免反向依赖 orchestration 主包。
//   - F5.3：多实例锁 —— 通过 SQLite runtime_locks 防止多副本 daemon
//     重复触发同一批到期 DAG。
//
// 拆出独立子包是因为 cmd/mcp-orch/orchestration 已在包文件数预算上限（默认
// 30，含 factory.go 后实际 31 个非测试文件）。在主包再开一个 scheduler_cron.go
// 会触发 archtest 守卫；建子包同时让 cron 关注点物理隔离，便于后续 F5.2 /
// F5.3 单独演进。
//
// English summary:
// Package cron is the cron-daemon process entrypoint skeleton for the DAG
// rework Scheduler (blueprint v2 §5). It wraps robfig/cron/v3, scans due
// next_run_at rows, triggers StartDAG through an injected adapter, and gates
// each Tick with a SQLite runtime lock. Lives in its own subpackage to
// avoid pushing the orchestration package over the archtest file-count budget.
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	robcron "github.com/robfig/cron/v3"
)

// Ticker 是 cron daemon 每次到点回调的下游接口。F5.2 将由 orchestration 主
// 包实现：扫描 next_run_at <= now 的 trigger=scheduled DAG，对每个调用
// service.StartDAG。F5.1 仅 stub 调用并 log。
//
// 用接口注入而非直接依赖 orchestration.Scheduler，避免反向引用主包；同时
// 让单测可以塞 stub。
//
// Ticker is the downstream contract invoked on every cron tick. F5.2 will
// provide the real implementation in the orchestration package (scan
// next_run_at and call StartDAG); F5.1 only invokes the stub and logs.
type Ticker interface {
	Tick(ctx context.Context, now time.Time) (int, error)
}

// Config 是 CronScheduler 构造参数。
// Config holds CronScheduler construction parameters.
type Config struct {
	// Spec 是 robfig/cron 表达式或预定义快捷符（如 "@hourly" / "@every 1m"）。
	// Spec is a robfig/cron expression or predefined shortcut.
	Spec string
	// Logger 用于结构化日志输出。
	// Logger is the structured logger.
	Logger *slog.Logger
	// Ticker 是到点回调下游；F5.1 必填以暴露依赖注入；F5.2 由 orchestration
	// 提供真实实现。
	// Ticker is the downstream tick callback; required even in F5.1 to keep
	// the dependency wiring explicit.
	Ticker Ticker
	// Location 决定 cron 调度时区，默认 UTC。
	// Location controls cron timezone, default UTC.
	Location *time.Location
	// TickTimeout 是单次 Tick 的超时时间，默认 30s。
	// TickTimeout is the timeout for a single Tick, default 30s.
	TickTimeout time.Duration
}

// Sentinel errors for defensive construction / state checks.
var (
	// ErrNilTicker 表示构造时缺少 Ticker 依赖。
	// ErrNilTicker indicates a missing Ticker dependency.
	ErrNilTicker = errors.New("cron: nil ticker")
	// ErrNilLogger 表示构造时缺少 logger。
	// ErrNilLogger indicates a missing logger.
	ErrNilLogger = errors.New("cron: nil logger")
	// ErrEmptySpec 表示构造时缺少 cron 表达式。
	// ErrEmptySpec indicates a missing cron spec.
	ErrEmptySpec = errors.New("cron: empty spec")
	// ErrAlreadyStarted 表示重复 Start。
	// ErrAlreadyStarted indicates Start was called twice.
	ErrAlreadyStarted = errors.New("cron: already started")
)

// CronScheduler 是 cron daemon 进程的薄包装。
//
// 字段不可变（构造后只读）：spec/logger/ticker/cron/location。
// state 字段（started/entryID）受 mu 保护，避免 Start/Stop 并发竞争。
//
// CronScheduler is a thin cron-daemon wrapper. Immutable fields are
// established at construction; the started/entryID state is mu-protected.
type CronScheduler struct {
	spec        string
	logger      *slog.Logger
	ticker      Ticker
	cron        *robcron.Cron
	location    *time.Location
	tickTimeout time.Duration

	mu         sync.Mutex
	started    bool
	entryID    robcron.EntryID
	rootCtx    context.Context
	rootCancel context.CancelFunc
}

// NewCronScheduler 校验参数并构造一个未启动的 CronScheduler。
// NewCronScheduler validates inputs and returns an unstarted CronScheduler.
func NewCronScheduler(cfg Config) (*CronScheduler, error) {
	if cfg.Ticker == nil {
		return nil, ErrNilTicker
	}
	if cfg.Logger == nil {
		return nil, ErrNilLogger
	}
	if cfg.Spec == "" {
		return nil, ErrEmptySpec
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	tickTimeout := cfg.TickTimeout
	if tickTimeout <= 0 {
		tickTimeout = 30 * time.Second
	}
	// 预解析 spec —— 提前失败比 cron loop 内部失败更友好。
	// Pre-parse the spec to fail fast before the cron loop boots.
	parser := robcron.NewParser(
		robcron.Minute | robcron.Hour | robcron.Dom | robcron.Month | robcron.Dow | robcron.Descriptor,
	)
	if _, err := parser.Parse(cfg.Spec); err != nil {
		return nil, fmt.Errorf("cron: parse spec %q: %w", cfg.Spec, err)
	}
	c := robcron.New(
		robcron.WithLocation(loc),
		robcron.WithParser(parser),
	)
	return &CronScheduler{
		spec:        cfg.Spec,
		logger:      cfg.Logger,
		ticker:      cfg.Ticker,
		cron:        c,
		location:    loc,
		tickTimeout: tickTimeout,
	}, nil
}

// Start 把 Tick 挂到 robfig/cron 调度循环并启动 daemon。
// 第二次 Start 返回 ErrAlreadyStarted；不会自动重启 / 复用已退出的实例。
//
// Start hooks Tick into the robfig/cron loop and launches the daemon. A
// second Start returns ErrAlreadyStarted.
func (s *CronScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rootCtx, cancel := context.WithCancel(ctx)
	id, err := s.cron.AddFunc(s.spec, s.Tick)
	if err != nil {
		cancel()
		// 构造期已 Parse 过；这里仍兜底，避免 robfig 行为变化时静默吞错。
		// We pre-parsed in NewCronScheduler; keep this fallback for safety.
		return fmt.Errorf("cron: add func: %w", err)
	}
	s.entryID = id
	s.rootCtx = rootCtx
	s.rootCancel = cancel
	s.cron.Start()
	s.started = true
	s.logger.Info("cron daemon started",
		slog.String("component", "orchestration.cron"),
		slog.String("spec", s.spec),
		slog.String("location", s.location.String()),
	)
	return nil
}

// Stop 优雅关闭 cron loop。可重复调用 / 未 Start 时也安全。
// Stop 会阻塞直到 robfig/cron 内部 goroutine 退出（其 ctx.Done()）。
//
// Stop gracefully shuts down the cron loop. It is idempotent and safe to
// call without a matching Start. Blocks until robfig/cron's internal
// goroutine drains.
func (s *CronScheduler) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	c := s.cron
	cancel := s.rootCancel
	s.started = false
	s.rootCtx = nil
	s.rootCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// 不在 mu 内等待，避免与 robfig/cron 内部回调时序耦合。
	// Wait outside the mutex to avoid coupling with robfig's internal timing.
	ctx := c.Stop()
	<-ctx.Done()
	s.logger.Info("cron daemon stopped",
		slog.String("component", "orchestration.cron"),
	)
	return nil
}

// Tick 是 cron 到点回调（F5.1 占位）。F5.1 范围只 log + 委托给 Ticker；真实
// next_run_at 扫描、StartDAG 触发由 F5.2 在 Ticker 实现里落地。Tick 公开
// 是为了让单测在不启动 cron loop 的情况下直接驱动行为。
//
// Tick is the cron callback (F5.1 placeholder). Within F5.1 scope it only
// logs and delegates to Ticker; real next_run_at scanning lands in F5.2 via
// the Ticker implementation. Tick is exported so tests can exercise the
// behavior without booting the cron loop.
func (s *CronScheduler) Tick() {
	now := time.Now().In(s.location)
	s.logger.Info("cron tick scheduled",
		slog.String("component", "orchestration.cron"),
		slog.Time("now", now),
		slog.Duration("timeout", s.tickTimeout),
	)
	s.mu.Lock()
	rootCtx := s.rootCtx
	s.mu.Unlock()
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	ctx, cancel := platformconfig.WithTimeout(rootCtx, s.tickTimeout)
	defer cancel()
	n, err := s.ticker.Tick(ctx, now)
	if err != nil {
		s.logger.Warn("cron tick downstream error",
			slog.String("component", "orchestration.cron"),
			slog.Any("err", err),
		)
		return
	}
	if n > 0 {
		s.logger.Info("cron tick triggered runs",
			slog.String("component", "orchestration.cron"),
			slog.Int("triggered", n),
		)
	}
}

const (
	// TickErrorClassInfrastructure 表示数据库扫描 / 更新一类基础设施错误。
	TickErrorClassInfrastructure = "infrastructure"
	// TickErrorClassValidation 表示 cron_expr 无法解析一类配置校验错误。
	TickErrorClassValidation = "validation"
)

const (
	scheduledTriggerSource    = "scheduled"
	runtimeLockCleanupTimeout = 5 * time.Second
	runtimeLockRenewInterval  = time.Minute
)

var dagCronParser = robcron.NewParser(
	robcron.Minute | robcron.Hour | robcron.Dom | robcron.Month | robcron.Dow | robcron.Descriptor,
)

// TickError 给 Tick 调用方暴露可匹配的错误分类。
// TickError exposes a matchable error class to Tick callers.
type TickError struct {
	Class string
	Op    string
	Err   error
}

func (e *TickError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("cron tick %s error (%s): %v", e.Op, e.Class, e.Err)
}

func (e *TickError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func classifyTickError(class, op string, err error) error {
	if err == nil {
		return nil
	}
	return &TickError{Class: class, Op: op, Err: err}
}

// DueDAG 是一次 Tick 扫到的 scheduled DAG 最小投影。
// DueDAG is the minimal projection scanned by one Tick.
type DueDAG struct {
	DagKey   string
	CronExpr string
	DueAt    time.Time
}

// DAGScheduleStore scans due DAGs; schedule advancement is owned by StartDAG.
type DAGScheduleStore interface {
	DueDAGs(ctx context.Context, now time.Time) ([]DueDAG, error)
}

// DAGStarter 是 StartDAG 的反向依赖适配口，避免 cron 子包 import 父包。
// DAGStarter adapts StartDAG without importing the parent orchestration package.
type DAGStarter interface {
	StartDAG(ctx context.Context, req ScheduledDAGStartRequest) error
}

type ScheduledDAGStartRequest struct {
	DagKey         string
	TriggerSource  string
	IdempotencyKey string
	DueAt          time.Time
	NextRunAt      time.Time
}

// ScheduledDAGTicker 实现 F5.2：扫描 next_run_at 到期 DAG 并触发 StartDAG。
// ScheduledDAGTicker implements F5.2 next_run_at scanning and StartDAG triggering.
type ScheduledDAGTicker struct {
	store   DAGScheduleStore
	starter DAGStarter
	locker  RuntimeLocker
}

type ScheduledDAGTickerConfig struct {
	Store   DAGScheduleStore
	Starter DAGStarter
	Locker  RuntimeLocker
}

var (
	ErrNilScheduleStore     = errors.New("cron: nil schedule store")
	ErrNilDAGStarter        = errors.New("cron: nil dag starter")
	ErrNilLockPool          = errors.New("cron: nil runtime lock provider")
	ErrScheduleStateChanged = errors.New("cron: scheduled dag state changed before next_run_at update")
)

func NewScheduledDAGTicker(cfg ScheduledDAGTickerConfig) (*ScheduledDAGTicker, error) {
	if cfg.Store == nil {
		return nil, ErrNilScheduleStore
	}
	if cfg.Starter == nil {
		return nil, ErrNilDAGStarter
	}
	if cfg.Locker == nil {
		return nil, ErrNilLockPool
	}
	return &ScheduledDAGTicker{
		store:   cfg.Store,
		starter: cfg.Starter,
		locker:  cfg.Locker,
	}, nil
}

type RuntimeLocker interface {
	TryLock(ctx context.Context) (RuntimeLockHandle, bool, error)
}

type RuntimeLockHandle interface {
	Renew(ctx context.Context) error
	Unlock(ctx context.Context) error
}

func (t *ScheduledDAGTicker) Tick(ctx context.Context, now time.Time) (triggered int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	handle, acquired, err := t.tryRuntimeLock(ctx)
	if err != nil {
		return 0, err
	}
	if !acquired {
		return 0, nil
	}
	lockCtx, stopRenewal := context.WithCancel(ctx)
	renewErr := t.startRuntimeLockRenewal(lockCtx, handle, stopRenewal)
	defer func() {
		stopRenewal()
		if renewalErr := <-renewErr; renewalErr != nil && err == nil {
			err = classifyTickError(TickErrorClassInfrastructure, "runtime_lock_renew", renewalErr)
		}
		t.releaseRuntimeLock(handle, &err)
	}()
	dags, err := t.store.DueDAGs(lockCtx, now)
	if err != nil {
		return 0, classifyTickError(TickErrorClassInfrastructure, "scan", err)
	}
	var dagErrs []error
	for _, dag := range dags {
		if err := t.triggerDueDAG(lockCtx, dag, now); err != nil {
			dagErrs = append(dagErrs, err)
			continue
		}
		triggered++
	}
	return triggered, joinDAGErrors(dagErrs)
}

func (t *ScheduledDAGTicker) tryRuntimeLock(ctx context.Context) (RuntimeLockHandle, bool, error) {
	handle, acquired, err := t.locker.TryLock(ctx)
	if err != nil {
		return nil, false, classifyTickError(TickErrorClassInfrastructure, "try_runtime_lock", err)
	}
	return handle, acquired, nil
}

func (t *ScheduledDAGTicker) startRuntimeLockRenewal(ctx context.Context, handle RuntimeLockHandle, stop func()) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(runtimeLockRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			case <-ticker.C:
				cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), runtimeLockCleanupTimeout)
				err := handle.Renew(cleanupCtx)
				cancel()
				if err != nil {
					stop()
					errCh <- err
					return
				}
			}
		}
	}()
	return errCh
}

func (t *ScheduledDAGTicker) releaseRuntimeLock(handle RuntimeLockHandle, result *error) {
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), runtimeLockCleanupTimeout)
	defer cancel()
	if unlockErr := handle.Unlock(cleanupCtx); unlockErr != nil && *result == nil {
		*result = classifyTickError(TickErrorClassInfrastructure, "runtime_lock_release", unlockErr)
	}
}

func (t *ScheduledDAGTicker) triggerDueDAG(ctx context.Context, dag DueDAG, now time.Time) error {
	nextRunAt, err := t.nextRunAt(dag, now)
	if err != nil {
		return err
	}
	req := ScheduledDAGStartRequest{
		DagKey:         strings.TrimSpace(dag.DagKey),
		TriggerSource:  scheduledTriggerSource,
		IdempotencyKey: scheduledIdempotencyKey(dag),
		DueAt:          dag.DueAt,
		NextRunAt:      nextRunAt,
	}
	if err := t.starter.StartDAG(ctx, req); err != nil {
		return fmt.Errorf("dag_key=%s start_dag: %w", req.DagKey, err)
	}
	return nil
}

func (t *ScheduledDAGTicker) nextRunAt(dag DueDAG, now time.Time) (time.Time, error) {
	next, err := NextDAGRunAt(dag.CronExpr, now)
	if err != nil {
		return time.Time{}, classifyTickError(TickErrorClassValidation, "parse_cron_expr", fmt.Errorf("dag_key=%s cron_expr=%q: %w", dag.DagKey, dag.CronExpr, err))
	}
	return next, nil
}

func scheduledIdempotencyKey(dag DueDAG) string {
	dueAt := dag.DueAt.UTC().Format(time.RFC3339Nano)
	return "scheduled:" + strings.TrimSpace(dag.DagKey) + ":" + dueAt
}

func joinDAGErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}

// ParseDAGCronExpr parses the DAG cron contract. Bare cron expressions are
// interpreted in UTC; callers that need wall-clock local time must prefix the
// expression with CRON_TZ=<IANA>, for example:
// CRON_TZ=Asia/Shanghai 0 8 * * *.
func ParseDAGCronExpr(cronExpr string) (robcron.Schedule, error) {
	spec, err := normalizedDAGCronSpec(cronExpr)
	if err != nil {
		return nil, err
	}
	schedule, err := dagCronParser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("DAG cron_expr %q invalid; bare cron defaults to UTC, use CRON_TZ=<IANA> for local wall time: %w", strings.TrimSpace(cronExpr), err)
	}
	return schedule, nil
}

func NextDAGRunAt(cronExpr string, after time.Time) (time.Time, error) {
	schedule, err := ParseDAGCronExpr(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(after.UTC()), nil
}

func normalizedDAGCronSpec(cronExpr string) (string, error) {
	spec := strings.TrimSpace(cronExpr)
	if spec == "" {
		return "", errors.New("DAG cron_expr is empty; bare cron defaults to UTC, use CRON_TZ=<IANA> for local wall time")
	}
	fields := strings.Fields(spec)
	normalized := strings.Join(fields, " ")
	if hasDAGCronTZPrefix(fields[0]) {
		if len(fields) == 1 {
			return "", fmt.Errorf("DAG cron_expr %q missing schedule after timezone prefix", spec)
		}
		return normalized, nil
	}
	return "CRON_TZ=UTC " + normalized, nil
}

func hasDAGCronTZPrefix(field string) bool {
	return strings.HasPrefix(field, "TZ=") || strings.HasPrefix(field, "CRON_TZ=")
}
