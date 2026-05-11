// Package cron 提供 DAG 改造 Scheduler 蓝图 v2 §5 所需的 cron daemon 进程入口
// 骨架（F5.1）。该包目前只负责 robfig/cron 库的薄包装 + 占位 Tick：
//
//   - F5.1（本文件）：cron daemon 进程入口 + 接 robfig/cron + Tick 占位
//   - F5.2（留位）：Tick 真实业务 SQL —— 扫 next_run_at <= now 的 trigger=scheduled
//     DAG，调 service.StartDAG；本包将以 Ticker 接口注入实现，避免反向依赖
//     orchestration 主包。
//   - F5.3（留位）：多实例锁 —— 通过 advisory lock / leader election 防止
//     多副本 daemon 重复触发同一行。
//
// 拆出独立子包是因为 cmd/mcp-orch/orchestration 已在包文件数预算上限（默认
// 30，含 factory.go 后实际 31 个非测试文件）。在主包再开一个 scheduler_cron.go
// 会触发 archtest 守卫；建子包同时让 cron 关注点物理隔离，便于后续 F5.2 /
// F5.3 单独演进。
//
// English summary:
// Package cron is the cron-daemon process entrypoint skeleton for the DAG
// rework Scheduler (blueprint v2 §5). It is a thin wrapper over
// robfig/cron/v3 plus a placeholder Tick; real next_run_at scanning lands in
// F5.2, multi-instance locking lands in F5.3. Lives in its own subpackage to
// avoid pushing the orchestration package over the archtest file-count budget.
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	spec     string
	logger   *slog.Logger
	ticker   Ticker
	cron     *robcron.Cron
	location *time.Location

	mu      sync.Mutex
	started bool
	entryID robcron.EntryID
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
		spec:     cfg.Spec,
		logger:   cfg.Logger,
		ticker:   cfg.Ticker,
		cron:     c,
		location: loc,
	}, nil
}

// Start 把 Tick 挂到 robfig/cron 调度循环并启动 daemon。
// 第二次 Start 返回 ErrAlreadyStarted；不会自动重启 / 复用已退出的实例。
//
// Start hooks Tick into the robfig/cron loop and launches the daemon. A
// second Start returns ErrAlreadyStarted.
func (s *CronScheduler) Start(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	id, err := s.cron.AddFunc(s.spec, s.Tick)
	if err != nil {
		// 构造期已 Parse 过；这里仍兜底，避免 robfig 行为变化时静默吞错。
		// We pre-parsed in NewCronScheduler; keep this fallback for safety.
		return fmt.Errorf("cron: add func: %w", err)
	}
	s.entryID = id
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
	s.started = false
	s.mu.Unlock()
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
		slog.String("phase", "F5.1-placeholder"),
	)
	// 即使是占位也走一次下游，便于 wiring 早期暴露 nil/panic 风险。
	// Even as a placeholder we delegate once, surfacing wiring issues early.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
