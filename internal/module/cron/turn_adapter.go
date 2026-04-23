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
	turn "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// TurnServiceAdapter implements TurnSubmitter on top of turn.Service.
// It bridges the cron scheduler's narrow submit-and-observe contract
// into the real turn preparation + start + track stack.
//
// Scope — phase 2b-integrate (v1):
//   - StartTurn: requires the job row to already have ThreadID; the
//     adapter resolves an existing Session via contract.SessionResolver,
//     runs PrepareTurn -> StartTurn, and returns the handle's local id
//     as the recorded turn_id. Agent bootstrap (creating an agent when
//     the job fires for the first time) is NOT in this cut — that
//     bootstrap pattern belongs in a follow-up that also fills
//     thread_id onto the job row before the first tick.
//   - LookupByDedupeKey: always returns Found=false. A proper lookup
//     requires persisting dedupe_key on the turn row (schema change)
//     plus a query API on turn.Service; both are follow-up work. The
//     scheduler will therefore treat every crash-recovery branch as
//     "never submitted" for v1, which is safe: the still-present
//     submit phase's CAS(pending->submitting) guards a second
//     StartTurn on the same run.
//   - Observe: delegates to turn.Service.TrackTurn; a not-found error
//     maps to ErrTurnNotFound so the scheduler marks the run
//     observe_lost per the P1b plan.
type TurnServiceAdapter struct {
	svc      turn.Service
	resolver contract.SessionResolver
	logger   *slog.Logger
}

var _ TurnSubmitter = (*TurnServiceAdapter)(nil)

// NewTurnServiceAdapter wires the adapter. Either dependency being nil
// is a programmer error; callers should fall back to NoopTurnSubmitter
// before reaching this constructor.
func NewTurnServiceAdapter(logger *slog.Logger, svc turn.Service, resolver contract.SessionResolver) *TurnServiceAdapter {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &TurnServiceAdapter{svc: svc, resolver: resolver, logger: logger}
}

// ErrJobNotBootstrapped is returned by StartTurn when the job row has
// no ThreadID yet. Bootstrap of the background thread/agent is out of
// scope for this PR; scheduler marks the run failed on this error and
// will retry per its retry budget.
var ErrJobNotBootstrapped = errors.New("cron: job thread_id is empty (agent/thread bootstrap not yet supported)")

// StartTurn runs the PrepareTurn -> StartTurn pipeline. It does NOT
// thread dedupe_key into the provider layer (no support yet) — the
// phase-1 store CAS(pending->submitting) is currently the only guard
// against double StartTurn within a run.
func (a *TurnServiceAdapter) StartTurn(ctx context.Context, req StartTurnRequest) (StartTurnResult, error) {
	if a == nil || a.svc == nil || a.resolver == nil {
		return StartTurnResult{}, errors.New("cron: turn adapter not wired")
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return StartTurnResult{}, ErrJobNotBootstrapped
	}
	session, err := a.resolver.ResolveSession(ctx, threadID)
	if err != nil {
		return StartTurnResult{}, fmt.Errorf("cron: resolve session: %w", err)
	}
	prepared, err := a.svc.PrepareTurn(ctx, session, a.buildPrepareInput(req))
	if err != nil {
		return StartTurnResult{}, fmt.Errorf("cron: prepare turn: %w", err)
	}
	handle, err := a.svc.StartTurn(ctx, session, prepared)
	if err != nil {
		return StartTurnResult{}, fmt.Errorf("cron: start turn: %w", err)
	}
	turnID := strings.TrimSpace(handle.LocalID())
	if turnID == "" {
		// A StartTurn that returns without a local id is a turn.Service
		// contract violation. Fail loudly so the scheduler marks the
		// run failed instead of persisting a phantom turn_id.
		return StartTurnResult{}, errors.New("cron: turn.StartTurn returned empty local id")
	}
	return StartTurnResult{
		TurnID:   turnID,
		ThreadID: strings.TrimSpace(session.ThreadID()),
		AgentID:  strings.TrimSpace(req.AgentID),
	}, nil
}

// LookupByDedupeKey is a placeholder pending the dedupe-key column on
// the turn row (and the corresponding store query). The scheduler
// treats Found=false as "never submitted" and proceeds with the
// normal pending->submitting path; the store's CAS sequence plus the
// phase-1 unique(dedupe_key) constraint on cron_job_runs catches
// duplicate submits within the cron layer even without turn-side
// support.
func (a *TurnServiceAdapter) LookupByDedupeKey(_ context.Context, _ string) (ObservedTurn, error) {
	return ObservedTurn{Found: false}, nil
}

// Observe attaches a cheap liveness check via turn.Service.TrackTurn.
// A tracker miss ("turn not found") maps to ErrTurnNotFound so the
// scheduler marks the run observe_lost per the P1b plan; every other
// error surfaces wrapped so the scheduler can log it.
func (a *TurnServiceAdapter) Observe(ctx context.Context, turnID string) error {
	if a == nil || a.svc == nil {
		return errors.New("cron: turn adapter not wired")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("cron: Observe requires a turn id")
	}
	if _, err := a.svc.TrackTurn(ctx, turnID); err != nil {
		// turn.Service currently wraps "turn not found" as a plain
		// errors.New — compare on substring until a sentinel lands.
		if strings.Contains(strings.ToLower(err.Error()), "turn not found") {
			return ErrTurnNotFound
		}
		return fmt.Errorf("cron: track turn: %w", err)
	}
	return nil
}

// buildPrepareInput translates the cron-side StartTurnRequest into
// turn.PrepareInput. Fields absent from the cron row (Files / Images /
// CandidateSkills / MCPSnapshot / ...) default to their zero values;
// turn.Service's PrepareTurn owns the fallback policy.
func (a *TurnServiceAdapter) buildPrepareInput(req StartTurnRequest) turn.PrepareInput {
	skills := make([]providerdto.SkillRef, 0, len(req.Skills))
	for _, s := range req.Skills {
		if name := strings.TrimSpace(s); name != "" {
			skills = append(skills, providerdto.SkillRef{Name: name})
		}
	}
	runtimeCfg := decodeRuntimeConfig(req.Config)
	return turn.PrepareInput{
		Prompt:              req.Prompt,
		Skills:              skills,
		Provider:            strings.TrimSpace(req.Provider),
		Model:               strings.TrimSpace(req.Model),
		AgentID:             strings.TrimSpace(req.AgentID),
		CWD:                 strings.TrimSpace(req.CWD),
		ThreadRuntimeConfig: runtimeCfg,
	}
}

// decodeRuntimeConfig turns a JSON blob into a map[string]any, or nil
// when the blob is missing/invalid. A decode error is silently
// dropped — turn.Service treats nil config as "no overrides" and a
// malformed config has already been rejected at cron create time by
// the service-layer ResolveCodexIdentity check (phase 2a), so reaching
// this branch implies the row was mutated after validation.
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
