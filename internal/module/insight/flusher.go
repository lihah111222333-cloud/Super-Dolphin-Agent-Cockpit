package insight

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	observation "github.com/anthropic-ai/super-agent-v3/internal/dto/observation"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// defaultDrainTimeout is the hard upper bound on how long the flusher
// spends draining outstanding signals after ctx.Done. The P3 plan pins
// this at 5 seconds ("bounded drain 5s"); it is surfaced as a field so
// tests can shorten it.
const defaultDrainTimeout = 5 * time.Second

// Flusher is the platformrunner.Runner that drains the collector queue,
// reads facts from observation.Contract, and UPSERTs session_insights.
// It has no state beyond the injected dependencies and the collector
// queue, so the fx-wired lifecycle is simply: Run blocks until ctx
// cancels, then a bounded drain runs.
type Flusher struct {
	logger       *slog.Logger
	obs          observation.Contract
	store        insightstore.Store
	collector    *collector
	drainTimeout time.Duration
	now          func() time.Time
}

// NewFlusher wires a Flusher with its collector and dependencies. now is
// overridable for deterministic tests; defaults to time.Now.
// NewFlusher 创建flusher。
func NewFlusher(logger *slog.Logger, obs observation.Contract, store insightstore.Store, col *collector) *Flusher {
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

// Run loops until ctx cancels. On cancel it drains at most drainTimeout
// before returning so a slow terminal is never lost silently on a normal
// shutdown. A shutdown that blows past drainTimeout logs the leftover
// count and returns ctx.Err().
// Run 启动insight后台流程。
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

// drain pulls up to drainTimeout worth of pending signals off the queue
// and flushes each to the store. Uses context.Background inside the
// deadline so the per-signal DB call is not pre-cancelled by the parent
// ctx that triggered shutdown.
// drain 处理drain。
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

// handle is the single-signal path. Reads observation for the turn,
// builds the UpsertParams, and writes. Errors log-and-continue — a
// failing UPSERT should not tear the flusher down because a later
// signal for the same turn will merge via the ON CONFLICT path.
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

// buildParams reads every fact we care about from observation and packs
// it into an UpsertParams. ok=false means we could not find even a
// terminal in observation, in which case the caller decides how to
// handle (see handle for requeue semantics). Timestamps missing from
// observation fall back to the signal.Timestamp so we never send
// zero-valued timestamps through to the DB.
// buildParams 构建params。
func (f *Flusher) buildParams(sig flushSignal) (insightstore.UpsertParams, bool) {
	term, termOk := f.obs.Terminal(sig.LocalTurnID)
	if !termOk {
		return insightstore.UpsertParams{}, false
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
		return insightstore.UpsertParams{}, false
	}

	return insightstore.UpsertParams{
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

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func providerSupportsApprovalObservation(provider string) bool {
	switch provider {
	case "codex", "codexapp", "codex-app":
		return true
	default:
		return false
	}
}
