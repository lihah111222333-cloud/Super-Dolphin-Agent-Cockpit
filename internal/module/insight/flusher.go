package insight

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	observation "github.com/anthropic-ai/super-agent-v3/internal/dto/observation"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// defaultDrainTimeout 是 ctx 取消后排空队列的最长等待时间，固定 5s，测试可缩短。
const defaultDrainTimeout = 5 * time.Second

// Flusher 是 platformrunner.Runner 实现，负责从 collector 队列排出信号、
// 读取 observation.Contract 中的事实，并 UPSERT session_insights 表。
// 除注入依赖和 collector 队列外无额外状态，生命周期由 fx lifecycle 驱动。
type Flusher struct {
	logger       *slog.Logger
	obs          observation.Contract
	store        Writer
	collector    *collector
	drainTimeout time.Duration
	now          func() time.Time
}

// NewFlusher 创建 Flusher，注入 collector 和依赖项。now 可在测试中覆盖以固定时间，生产代码默认使用 time.Now。
func NewFlusher(logger *slog.Logger, obs observation.Contract, store Writer, col *collector) *Flusher {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Flusher{
		logger:       logger,
		obs:          obs,
		store:        store,
		collector:    col,
		drainTimeout: defaultDrainTimeout,
		now:          time.Now,
	}
}

var _ contract.Runner = (*Flusher)(nil)

// Run 阻塞直到 ctx 取消，取消后执行有界 drain 再返回。
// 若 drain 超出 drainTimeout，记录剩余信号数并返回 ctx.Err()。
func (f *Flusher) Run(ctx context.Context) error {
	if f.collector == nil || f.collector.queue == nil {
		// Nothing to drain; mirror the platformrunner.Runner contract and
		// wait for ctx to fire so the run.Group stays happy.
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			f.drain()
			return ctx.Err()
		case sig, ok := <-f.collector.queue:
			if !ok {
				return nil
			}
			f.handle(ctx, sig)
		}
	}
}

// drain 在 drainTimeout 内排空队列，把每个信号写入 store。
// 内部使用 context.Background() 避免被已取消的父 ctx 中断数据库写入。
func (f *Flusher) drain() {
	if f.drainTimeout <= 0 {
		return
	}
	drainCtx, cancel := ctxutil.WithTimeout(context.Background(), f.drainTimeout)
	defer cancel()
	drained := 0
	for {
		select {
		case sig, ok := <-f.collector.queue:
			if !ok {
				if drained > 0 {
					f.logger.Info("insight: drain complete", slog.Int("count", drained))
				}
				return
			}
			f.handle(drainCtx, sig)
			drained++
		case <-drainCtx.Done():
			remaining := len(f.collector.queue)
			if remaining > 0 || drained > 0 {
				f.logger.Warn("insight: drain timeout",
					slog.Int("drained", drained),
					slog.Int("remaining", remaining),
				)
			}
			return
		}
	}
}

// handle 处理单条信号：读取 observation，构建 UpsertParams 并写入 store。
// UPSERT 失败时记录日志继续，不终止 flusher，因为后续同一轮的信号会通过 ON CONFLICT 路径合并。
func (f *Flusher) handle(ctx context.Context, sig flushSignal) {
	params, ok := f.buildParams(sig)
	if !ok {
		// observation has nothing for this turn — likely a transient
		// race where the terminal event arrived before observation's
		// own subscribers processed the same event. Requeue once and
		// drop after that to avoid an infinite cycle.
		if sig.Retried {
			return
		}
		sig.Retried = true
		select {
		case f.collector.queue <- sig:
		default:
		}
		return
	}
	if _, err := f.store.Upsert(ctx, params); err != nil {
		f.logger.Warn("insight: upsert failed",
			slog.String("local_turn_id", sig.LocalTurnID),
			slog.String("thread_id", sig.ThreadID),
			slog.String("error", err.Error()),
		)
	}
}

// buildParams 从 observation 中读取所有维度，组装 UpsertParams。
// ok=false 表示 observation 中还没有该轮的 terminal 记录，调用方负责决定是否重试（见 handle）。
// 时间戳缺失时回退到 signal.Timestamp，避免向 DB 写入零值。
func (f *Flusher) buildParams(sig flushSignal) (UpsertParams, bool) {
	term, termOk := f.obs.Terminal(sig.LocalTurnID)
	if !termOk {
		return UpsertParams{}, false
	}
	tokens, _ := f.obs.Tokens(sig.LocalTurnID)
	counts, _ := f.obs.Counts(sig.LocalTurnID)
	times, _ := f.obs.Timestamps(sig.LocalTurnID)
	providerTurnID, _ := f.obs.ResolveProviderTurn(sig.LocalTurnID)
	skills := f.obs.SkillsSelected(sig.LocalTurnID)

	completedAt := times.CompletedAt
	if completedAt.IsZero() {
		completedAt = sig.Timestamp
	}
	if completedAt.IsZero() {
		completedAt = f.now()
	}
	var durationMS int32
	if !times.StartedAt.IsZero() && !completedAt.IsZero() {
		d := completedAt.Sub(times.StartedAt)
		if d > 0 {
			// Clamp to int32 max so a pathological delta cannot
			// overflow. 2^31 ms ~= 24.8 days which is far beyond any
			// legitimate turn.
			if d > time.Duration(1<<30)*time.Millisecond {
				durationMS = 1 << 30
			} else {
				durationMS = int32(d / time.Millisecond)
			}
		}
	}
	skillsJSON, err := json.Marshal(skills)
	if err != nil {
		return UpsertParams{}, false
	}

	return UpsertParams{
		ThreadID:                 sig.ThreadID,
		AgentID:                  sig.AgentID,
		SessionID:                "",
		Provider:                 sig.Provider,
		LocalTurnID:              sig.LocalTurnID,
		ProviderTurnID:           providerTurnID,
		StartedAt:                times.StartedAt,
		CompletedAt:              completedAt,
		DurationMS:               durationMS,
		Success:                  cloneBoolPtr(term.Success),
		Status:                   mapTerminalKindToStatus(string(term.Kind)),
		StopReason:               term.Reason,
		ToolCalls:                counts.ToolCalls,
		ToolCallsObserved:        counts.ToolCallsObserved,
		ToolFailures:             counts.ToolFailures,
		ToolFailuresObserved:     counts.ToolFailuresObserved,
		ApprovalRequests:         counts.ApprovalRequests,
		ApprovalRequestsObserved: counts.ApprovalRequestsObserved || providerSupportsApprovalObservation(sig.Provider),
		TokenInput:               int32(tokens.Input),
		TokenOutput:              int32(tokens.Output),
		TokenTotal:               int32(tokens.Total),
		TokenSnapshotObserved:    tokens.Observed,
		ContextWindowTokens:      int32(tokens.ContextWindowTokens),
		UIProjection:             tokens.Projection,
		SkillsSelected:           skillsJSON,
		CreatedAt:                f.now(),
		UpdatedAt:                f.now(),
	}, true
}

// cloneBoolPtr 深拷贝一个 bool 指针，nil 时返回 nil。
func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// providerSupportsApprovalObservation 判断指定 provider 是否天然支持审批请求观测。
// codex 系列 provider 自动报告审批请求数，无需 observation 层补全。
func providerSupportsApprovalObservation(provider string) bool {
	switch provider {
	case "codex", "codexapp", "codex-app":
		return true
	default:
		return false
	}
}
