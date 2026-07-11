// Package cron 实现 scheduled DAG 的 cron 守护进程。
// 它封装 robfig/cron，扫描 next_run_at 已到期的 DAG，通过注入端口启动 run，并用 runtime lock 防止多副本重复触发。
// 该包独立于 orchestration 主包，避免 cron 实现反向依赖父包装配细节。
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	robcron "github.com/robfig/cron/v3"
)

// Ticker 是 cron 每次到点回调的下游接口。
// 实现方负责扫描到期 DAG 并启动 run；这里用接口注入避免 cron 子包 import orchestration 父包。
type Ticker interface {
	Tick(ctx context.Context, now time.Time) (int, error)
}

// Config 是 CronScheduler 构造参数。
type Config struct {
	// Spec 是 robfig/cron 表达式或预定义快捷符（如 "@hourly" / "@every 1m"）。
	Spec string
	// Logger 用于结构化日志输出。
	Logger *slog.Logger
	// Ticker 是到点回调下游；缺失时构造失败，避免 cron 启动后只打日志不触发业务。
	Ticker Ticker
	// Location 决定 cron 调度时区，默认 UTC。
	Location *time.Location
	// TickTimeout 是单次 Tick 的超时时间，默认 30s。
	TickTimeout time.Duration
}

// CronScheduler 构造和状态检查使用的 sentinel 错误。
var (
	// ErrNilTicker 表示构造时缺少 Ticker 依赖。
	ErrNilTicker = errors.New("cron: nil ticker")
	// ErrNilLogger 表示构造时缺少 logger。
	ErrNilLogger = errors.New("cron: nil logger")
	// ErrEmptySpec 表示构造时缺少 cron 表达式。
	ErrEmptySpec = errors.New("cron: empty spec")
	// ErrAlreadyStarted 表示重复 Start。
	ErrAlreadyStarted = errors.New("cron: already started")
)

// CronScheduler 是 cron daemon 进程的薄包装。
// 构造后只读字段保存调度配置；started、entryID 和 rootCtx 受 mu 保护，避免 Start/Stop 并发竞争。
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
	// 预解析 spec：在 cron loop 启动前 fail-fast，避免后台 goroutine 中才暴露配置错误。
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
		// 构造期已 Parse 过；这里仍保留错误返回，避免 robfig 行为变化时静默吞错。
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
	ctx := c.Stop()
	<-ctx.Done()
	s.logger.Info("cron daemon stopped",
		slog.String("component", "orchestration.cron"),
	)
	return nil
}

// Tick 是 cron 到点回调，负责建立单次超时上下文并委托 Ticker。
// 公开该方法是为了测试可以不启动 cron loop 直接驱动一次调度扫描。
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
	// scheduledTriggerSource 是 scheduled DAG run 的 trigger_source 固定值。
	scheduledTriggerSource    = "scheduled"
	runtimeLockCleanupTimeout = 5 * time.Second
	runtimeLockRenewInterval  = time.Minute
)

// dagCronParser 是 DAG cron_expr 的共享解析器；裸表达式会在归一化阶段补 UTC。
var dagCronParser = robcron.NewParser(
	robcron.Minute | robcron.Hour | robcron.Dom | robcron.Month | robcron.Dow | robcron.Descriptor,
)

// TickError 给 Tick 调用方暴露可匹配的错误分类。
// Class 区分配置校验和基础设施失败，Op 标明失败发生在哪个扫描阶段。
type TickError struct {
	Class string
	Op    string
	Err   error
}

// Error 返回错误文本。
func (e *TickError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("cron tick %s error (%s): %v", e.Op, e.Class, e.Err)
}

// Unwrap 返回底层错误。
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
// cron 子包只需要这些字段即可计算下一次触发并构造幂等启动请求。
type DueDAG struct {
	DagKey   string
	CronExpr string
	DueAt    time.Time
}

// DAGScheduleStore 负责扫描到期 DAG；推进 next_run_at 由 StartDAG 的持久化路径负责。
type DAGScheduleStore interface {
	DueDAGs(ctx context.Context, now time.Time) ([]DueDAG, error)
}

// DAGStarter 是 StartDAG 的反向依赖适配口，避免 cron 子包 import 父包。
type DAGStarter interface {
	StartDAG(ctx context.Context, req ScheduledDAGStartRequest) error
}

// ScheduledDAGStartRequest 是 cron 子包传给 DAG 启动端口的 wire DTO。
// 它同时携带本次 due_at 和下一次 next_run_at，让启动路径能在同一事务内处理幂等与计划推进。
type ScheduledDAGStartRequest struct {
	DagKey         string
	TriggerSource  string
	IdempotencyKey string
	DueAt          time.Time
	NextRunAt      time.Time
}

// ScheduledDAGTicker 扫描 next_run_at 到期 DAG 并触发 StartDAG。
// Tick 期间持有 runtime lock，确保多副本 mcp-orch 不会重复启动同一批计划 run。
type ScheduledDAGTicker struct {
	store   DAGScheduleStore
	starter DAGStarter
	locker  RuntimeLocker
}

// ScheduledDAGTickerConfig 是 ScheduledDAGTicker 的显式依赖集合。
// Store、Starter、Locker 缺一都会 fail-fast，避免调度器启动但无法产生业务效果。
type ScheduledDAGTickerConfig struct {
	Store   DAGScheduleStore
	Starter DAGStarter
	Locker  RuntimeLocker
}

var (
	// ScheduledDAGTicker 构造和状态更新路径使用的 sentinel 错误。
	ErrNilScheduleStore     = errors.New("cron: nil schedule store")
	ErrNilDAGStarter        = errors.New("cron: nil dag starter")
	ErrNilLockPool          = errors.New("cron: nil runtime lock provider")
	ErrScheduleStateChanged = errors.New("cron: scheduled dag state changed before next_run_at update")
)

// NewScheduledDAGTicker 创建 scheduled DAG ticker；store、starter、locker 缺一即失败。
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

// RuntimeLocker 是 cron 多实例互斥锁端口。
type RuntimeLocker interface {
	TryLock(ctx context.Context) (RuntimeLockHandle, bool, error)
}

// RuntimeLockHandle 是已获取 runtime lock 的续租和释放句柄。
type RuntimeLockHandle interface {
	Renew(ctx context.Context) error
	Unlock(ctx context.Context) error
}

// Tick 扫描到期 DAG 并按计划启动 run。
// 整个扫描期间持有 runtime lock，后台续租失败会取消本轮上下文并把错误归类返回。
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

// tryRuntimeLock 获取本轮 scheduled 扫描的多实例锁。
func (t *ScheduledDAGTicker) tryRuntimeLock(ctx context.Context) (RuntimeLockHandle, bool, error) {
	handle, acquired, err := t.locker.TryLock(ctx)
	if err != nil {
		return nil, false, classifyTickError(TickErrorClassInfrastructure, "try_runtime_lock", err)
	}
	return handle, acquired, nil
}

// startRuntimeLockRenewal 在后台 goroutine 中定期续约运行时锁，续约失败时调 stop 取消上下文。
func (t *ScheduledDAGTicker) startRuntimeLockRenewal(ctx context.Context, handle RuntimeLockHandle, stop func()) <-chan error {
	errCh := make(chan error, 1)
	safego.Go(ctx, nil, "mcp-orch.cron.runtimeLockRenewal", func(runCtx context.Context) {
		ticker := time.NewTicker(runtimeLockRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
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
	})
	return errCh
}

// releaseRuntimeLock 在独立 cleanup timeout 内释放锁；释放失败会写入本轮返回错误。
func (t *ScheduledDAGTicker) releaseRuntimeLock(handle RuntimeLockHandle, result *error) {
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), runtimeLockCleanupTimeout)
	defer cancel()
	if unlockErr := handle.Unlock(cleanupCtx); unlockErr != nil && *result == nil {
		*result = classifyTickError(TickErrorClassInfrastructure, "runtime_lock_release", unlockErr)
	}
}

// triggerDueDAG 计算下一次运行时间，并通过 starter 写入本次 scheduled run。
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

// nextRunAt 校验 cron_expr 并计算本轮之后的下一次触发时间。
func (t *ScheduledDAGTicker) nextRunAt(dag DueDAG, now time.Time) (time.Time, error) {
	next, err := NextDAGRunAt(dag.CronExpr, now)
	if err != nil {
		return time.Time{}, classifyTickError(TickErrorClassValidation, "parse_cron_expr", fmt.Errorf("dag_key=%s cron_expr=%q: %w", dag.DagKey, dag.CronExpr, err))
	}
	return next, nil
}

// scheduledIdempotencyKey 用 dag_key + due_at 构造幂等键，防止同一到期点重复启动。
func scheduledIdempotencyKey(dag DueDAG) string {
	dueAt := dag.DueAt.UTC().Format(time.RFC3339Nano)
	return "scheduled:" + strings.TrimSpace(dag.DagKey) + ":" + dueAt
}

// joinDAGErrors 合并多个 DAG 启动错误；单错误保持原链路便于 errors.Is。
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

// ParseDAGCronExpr 解析 DAG cron 表达式。
// 裸表达式按 UTC 解释；需要本地墙钟时间时必须显式加 CRON_TZ=<IANA> 前缀。
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

// NextDAGRunAt 返回 after 之后的下一次 DAG 计划触发时间。
func NextDAGRunAt(cronExpr string, after time.Time) (time.Time, error) {
	schedule, err := ParseDAGCronExpr(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(after.UTC()), nil
}

// normalizedDAGCronSpec 压缩空白并为裸 cron 表达式补上 UTC 时区。
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

// hasDAGCronTZPrefix 判断 cron 首字段是否已显式声明时区。
func hasDAGCronTZPrefix(field string) bool {
	return strings.HasPrefix(field, "TZ=") || strings.HasPrefix(field, "CRON_TZ=")
}
