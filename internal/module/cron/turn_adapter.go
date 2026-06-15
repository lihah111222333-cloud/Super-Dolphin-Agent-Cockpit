package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// TurnServiceAdapter implements TurnSubmitter on top of
// contract.CronTurnExecutor. It bridges the cron scheduler's narrow
// submit-and-observe contract into the real turn preparation + start +
// track stack.
//
// Scope — phase 2b-integrate (v1):
//   - StartTurn: requires the job row to already have ThreadID; the
//     adapter resolves an existing Session via contract.SessionResolver,
//     runs CronPrepareTurn -> CronStartTurn, and returns the handle's
//     local id as the recorded turn_id. Agent bootstrap (creating an
//     agent when the job fires for the first time) is NOT in this cut —
//     that bootstrap pattern belongs in a follow-up that also fills
//     thread_id onto the job row before the first tick.
//   - LookupByDedupeKey: delegates to CronTurnExecutor.CronLookupByDedupeKey,
//     which reads the in-memory tracker. That covers the common
//     crash-recovery window (same process, transient failure between
//     StartTurn returning and the run row CAS-advancing to
//     submitted); a process restart erases the tracker, so the SQL
//     persistence half of the P1b plan remains a follow-up. ok=false
//     is mapped to ObservedTurn{Found:false} so the scheduler treats
//     every tracker miss as "never submitted" per the plan.
//   - Observe: delegates to CronTurnExecutor.CronTrackTurn; a not-found
//     error maps to ErrTurnNotFound so the scheduler marks the run
//     observe_lost per the P1b plan.
//
// 这个适配器只把 cron 请求翻译给 turn 层。run/job 的保存、重试和
// observe_lost 都留给 Scheduler。
type TurnServiceAdapter struct {
	svc      contract.CronTurnExecutor
	resolver contract.SessionResolver
	logger   *slog.Logger
	// bootstrapper is optional. When set, StartTurn invokes it on a job
	// row whose ThreadID is still empty, then proceeds with the
	// freshly-minted thread. A nil bootstrapper preserves the v1
	// behavior of surfacing ErrJobNotBootstrapped so the scheduler
	// marks the run failed and retries per its budget.
	bootstrapper ThreadBootstrapper
}

var _ TurnSubmitter = (*TurnServiceAdapter)(nil)

// NewTurnServiceAdapter wires the adapter. Either dependency being nil
// is a programmer error; callers should fall back to NoopTurnSubmitter
// before reaching this constructor.
// NewTurnServiceAdapter 创建 cron 到 turn service 的适配器。
func NewTurnServiceAdapter(logger *slog.Logger, svc contract.CronTurnExecutor, resolver contract.SessionResolver) *TurnServiceAdapter {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &TurnServiceAdapter{svc: svc, resolver: resolver, logger: logger}
}

// WithBootstrapper attaches a ThreadBootstrapper used for first-trigger
// jobs. The method is wired from the fx factory (provideTurnSubmitter)
// so the public constructor surface stays backwards-compatible; tests
// can call it directly.
// WithBootstrapper 设置 turn 适配器使用的线程引导器。
func (a *TurnServiceAdapter) WithBootstrapper(b ThreadBootstrapper) *TurnServiceAdapter {
	if a == nil {
		return nil
	}
	a.bootstrapper = b
	return a
}

// ErrJobNotBootstrapped is returned by StartTurn when the job row has
// no ThreadID yet. Bootstrap of the background thread/agent is out of
// scope for this PR; scheduler marks the run failed on this error and
// will retry per its retry budget.
var ErrJobNotBootstrapped = errors.New("cron: job thread_id is empty (agent/thread bootstrap not yet supported)")

// StartTurn runs the CronPrepareTurn -> CronStartTurn pipeline.
// DedupeKey is forwarded to the turn layer so a subsequent
// LookupByDedupeKey can resolve the submission within the same process;
// cross-process crash recovery still relies on the phase-1 store
// CAS(pending->submitting) gate until the dedupe_key SQL column lands.
//
// StartTurn 只负责准备并启动 turn，返回本地 turn_id。
// 它不写 cron_runs，也不处理终态事件。
func (a *TurnServiceAdapter) StartTurn(ctx context.Context, req StartTurnRequest) (StartTurnResult, error) {
	if a == nil || a.svc == nil || a.resolver == nil {
		return StartTurnResult{}, errors.New("cron: turn adapter not wired")
	}
	session, agentID, err := a.resolveThreadAgent(ctx, req)
	if err != nil {
		return StartTurnResult{}, err
	}
	turnID, err := a.executeTurn(ctx, session, req)
	if err != nil {
		return StartTurnResult{}, err
	}
	return StartTurnResult{
		TurnID:   turnID,
		ThreadID: strings.TrimSpace(session.ThreadID()),
		AgentID:  agentID,
	}, nil
}

func (a *TurnServiceAdapter) resolveThreadAgent(ctx context.Context, req StartTurnRequest) (contract.Session, string, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	agentID := strings.TrimSpace(req.AgentID)
	if threadID == "" {
		// First-trigger path: ask the bootstrapper to mint a thread
		// (and usually an agent) for this job. Keep the bootstrap call
		// narrow — the scheduler is the one that persists the returned
		// IDs via SetActiveTurn once StartTurn itself succeeds.
		bootstrapped, err := a.bootstrapFirstRun(ctx, req)
		if err != nil {
			return nil, "", err
		}
		threadID = strings.TrimSpace(bootstrapped.ThreadID)
		if bootstrappedAgentID := strings.TrimSpace(bootstrapped.AgentID); bootstrappedAgentID != "" {
			agentID = bootstrappedAgentID
		}
	}
	session, err := a.resolver.ResolveSession(ctx, threadID)
	if err != nil {
		return nil, "", fmt.Errorf("cron: resolve session: %w", err)
	}
	// agent_id 可来自 job 或 bootstrap；真正落库的 thread_id 以
	// session.ThreadID() 为准。
	return session, agentID, nil
}

func (a *TurnServiceAdapter) executeTurn(ctx context.Context, session contract.Session, req StartTurnRequest) (string, error) {
	prepared, err := a.svc.CronPrepareTurn(ctx, session, a.buildPrepareInput(req))
	if err != nil {
		return "", fmt.Errorf("cron: prepare turn: %w", err)
	}
	handle, err := a.svc.CronStartTurn(ctx, session, prepared)
	if err != nil {
		return "", fmt.Errorf("cron: start turn: %w", err)
	}
	turnID := strings.TrimSpace(handle.LocalID())
	if turnID == "" {
		// A CronStartTurn that returns without a local id is a
		// contract violation. Fail loudly so the scheduler marks the
		// run failed instead of persisting a phantom turn_id.
		// 这里必须返回错误，不能造临时 id；否则恢复时无法重新 Observe。
		return "", errors.New("cron: CronStartTurn returned empty local id")
	}
	return turnID, nil
}

// bootstrapFirstRun is the adapter-local helper that invokes the
// ThreadBootstrapper on a job whose cron_jobs.thread_id is still
// empty. When no bootstrapper is wired, the helper preserves the v1
// contract by returning ErrJobNotBootstrapped so the scheduler can
// fail the run and retry; a non-empty ThreadID from the bootstrapper
// is required — an empty result is treated as a bootstrap failure
// because we cannot proceed to CronPrepareTurn / CronStartTurn without
// a thread.
//
// bootstrap 只用于首次触发且 job.thread_id 为空。
// 返回的 ID 只有在 StartTurn 成功后才会被保存。
func (a *TurnServiceAdapter) bootstrapFirstRun(ctx context.Context, req StartTurnRequest) (BootstrapResult, error) {
	if a == nil || a.bootstrapper == nil {
		return BootstrapResult{}, ErrJobNotBootstrapped
	}
	res, err := a.bootstrapper.BootstrapThread(ctx, BootstrapRequest{
		JobID:    strings.TrimSpace(req.JobID),
		Provider: strings.TrimSpace(req.Provider),
		Model:    strings.TrimSpace(req.Model),
		CWD:      strings.TrimSpace(req.CWD),
		Name:     strings.TrimSpace(req.JobID),
		Config:   req.Config,
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("cron: bootstrap thread: %w", err)
	}
	if strings.TrimSpace(res.ThreadID) == "" {
		return BootstrapResult{}, errors.New("cron: bootstrap returned empty thread id")
	}
	return res, nil
}

// LookupByDedupeKey queries the in-memory turn tracker for a
// non-terminal turn that registered dedupeKey. See the type-level doc
// comment for the cross-process caveat: a process restart erases the
// tracker, so the scheduler still falls back to "never submitted" in
// that case and relies on the store's CAS(pending->submitting) guard.
// Empty dedupeKey short-circuits to Found=false without hitting the
// service so callers that haven't opted into dedupe see a cheap
// deterministic answer.
//
// Lookup 只给恢复查重用。它不能启动 provider；查不到时怎么处理交给 Scheduler。
func (a *TurnServiceAdapter) LookupByDedupeKey(ctx context.Context, dedupeKey string) (ObservedTurn, error) {
	if a == nil || a.svc == nil {
		return ObservedTurn{Found: false}, errors.New("cron: turn adapter not wired")
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return ObservedTurn{Found: false}, nil
	}
	status, found, err := a.svc.CronLookupByDedupeKey(ctx, key)
	if err != nil {
		return ObservedTurn{Found: false}, fmt.Errorf("cron: lookup by dedupe key: %w", err)
	}
	if !found {
		return ObservedTurn{Found: false}, nil
	}
	// Prefer the local tracker id as the ObservedTurn.TurnID so the
	// scheduler's bookkeeping stays consistent with StartTurn's return
	// value (which also records handle.LocalID() as the turn_id).
	turnID := strings.TrimSpace(status.LocalID)
	if turnID == "" {
		turnID = strings.TrimSpace(status.ProviderID)
	}
	return ObservedTurn{Found: true, TurnID: turnID}, nil
}

// Observe attaches a cheap liveness check via CronTurnExecutor.CronTrackTurn.
// A tracker miss ("turn not found") maps to ErrTurnNotFound so the
// scheduler marks the run observe_lost per the P1b plan; every other
// error surfaces wrapped so the scheduler can log it.
//
// Observe 只接管已提交的 turn。找不到或无权追踪时，上层应转成 observe_lost。
func (a *TurnServiceAdapter) Observe(ctx context.Context, turnID string) error {
	if a == nil || a.svc == nil {
		return errors.New("cron: turn adapter not wired")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("cron: Observe requires a turn id")
	}
	if _, err := a.svc.CronTrackTurn(ctx, turnID); err != nil {
		// CronTrackTurn currently wraps "turn not found" as a plain
		// errors.New — compare on substring until a sentinel lands.
		if strings.Contains(strings.ToLower(err.Error()), "turn not found") {
			return ErrTurnNotFound
		}
		return fmt.Errorf("cron: track turn: %w", err)
	}
	return nil
}

// buildPrepareInput translates the cron-side StartTurnRequest into
// contract.CronPrepareInput. Fields absent from the cron row (Files /
// Images / CandidateSkills / MCPSnapshot / ...) default to their zero
// values; the turn layer's PrepareTurn owns the fallback policy.
//
// 这里只做字段投影，不添加 cron 专属默认值。坏配置应该在 Create/Update 时被挡住。
func (a *TurnServiceAdapter) buildPrepareInput(req StartTurnRequest) contract.CronPrepareInput {
	skills := make([]providerdto.SkillRef, 0, len(req.Skills))
	for _, s := range req.Skills {
		if name := strings.TrimSpace(s); name != "" {
			skills = append(skills, providerdto.SkillRef{Name: name})
		}
	}
	runtimeCfg := decodeRuntimeConfig(req.Config)
	return contract.CronPrepareInput{
		Prompt:              req.Prompt,
		Skills:              skills,
		Provider:            strings.TrimSpace(req.Provider),
		Model:               strings.TrimSpace(req.Model),
		AgentID:             strings.TrimSpace(req.AgentID),
		CWD:                 strings.TrimSpace(req.CWD),
		ThreadRuntimeConfig: runtimeCfg,
		DedupeKey:           strings.TrimSpace(req.DedupeKey),
	}
}

// decodeRuntimeConfig turns a JSON blob into a map[string]any, or nil
// when the blob is missing/invalid. A decode error is silently
// dropped — the turn layer treats nil config as "no overrides" and a
// malformed config has already been rejected at cron create time by
// the service-layer ResolveCodexIdentity check (phase 2a), so reaching
// this branch implies the row was mutated after validation.
//
// 这里不是新的容错层。要改变坏 config 的处理方式，先改 service 校验和存储约束。
func decodeRuntimeConfig(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
