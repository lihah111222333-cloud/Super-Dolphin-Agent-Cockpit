package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func resetLaunchState(agent *agentState) {
	if agent == nil {
		return
	}
	agent.cmd = nil
	agent.monitoredSeq = 0
	agent.stopRequested = false
	clearAgentTurnStateLocked(agent)
	agent.remoteThreadID = ""
	agent.remoteAgentID = ""
	agent.startedAt = time.Time{}
	agent.updatedAt = time.Time{}
}

func cleanupAgentState(agent *agentState) {
	if agent == nil {
		return
	}
	if agent.queue != nil {
		agent.queue.Clear()
	}
	// Stop path: the active turn is being torn down, so turn-state fields
	// must all go to zero together. clearAgentTurnStateLocked covers
	// activeTurnID + threadID + exitedAt in one place.
	clearAgentTurnStateLocked(agent)
}

func (s *service) prepareLaunchLocked(ctx context.Context, agent *agentState) error {
	if agent == nil {
		return errAgentNotFound
	}
	if agent.queue != nil {
		agent.queue.Clear()
	}
	return s.prepareLaunchStateLocked(ctx, agent)
}

func (s *service) markStoppingLocked(ctx context.Context, agent *agentState, reason string) (bool, error) {
	if agent == nil {
		return false, errAgentNotFound
	}
	if agent.stopRequested {
		setStopReasonIfEmpty(agent, reason)
		return false, nil
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerStopRequested); err != nil {
		return false, err
	}
	agent.stopRequested = true
	setStopReasonIfEmpty(agent, reason)
	cleanupAgentState(agent)
	return true, nil
}

func (s *service) commitLaunchFailureLocked(
	ctx context.Context,
	agent *agentState,
	launchErr error,
	details ...string,
) error {
	if launchErr == nil {
		return nil
	}
	if agent != nil {
		values := append(append([]string(nil), details...), launchErr.Error())
		agent.lastError = shared.FirstTrimmed(values...)
		s.logger.Warn("orchestration: launch failure committed",
			"agent_id", agent.id, "state", agent.state, "error", launchErr,
			"details", strings.Join(details, "; "))
	}
	if agent == nil {
		return launchErr
	}
	if fireErr := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchFailed); fireErr != nil {
		return errors.Join(launchErr, fireErr)
	}
	return launchErr
}

func (s *service) commitLaunchSuccessLocked(ctx context.Context, agent *agentState) error {
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerLaunchSucceeded); err != nil {
		if agent != nil {
			agent.lastError = err.Error()
		}
		return err
	}
	s.publishAgentLaunched(agent)
	return nil
}

func (s *service) finalizeActiveTurnLocked(
	ctx context.Context,
	agent *agentState,
	turnID string,
	kind activeTurnFinalizationKind,
) error {
	if agent == nil {
		return errAgentNotFound
	}
	turnID = strings.TrimSpace(turnID)
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if activeTurnID == "" {
		return errTurnNotActive
	}
	if turnID != "" && activeTurnID != turnID {
		return errTurnNotActive
	}
	if kind.clearError {
		agent.lastError = ""
	} else {
		agent.lastError = strings.TrimSpace(kind.errorText)
	}
	if err := s.fireOrForceLocked(ctx, agent, kind.trigger); err != nil {
		return err
	}
	agent.activeTurnID = ""
	return nil
}

func (s *service) forceIdleAfterTurnTerminalLocked(
	ctx context.Context,
	agent *agentState,
	turnID string,
	kind activeTurnRecoveryKind,
) (bool, error) {
	if agent == nil {
		return false, errAgentNotFound
	}
	if !canForceIdleAfterTurnTerminal(agent, turnID) {
		return false, errTurnNotActive
	}
	before := agent.state
	agent.activeTurnID = ""
	if kind.clearError {
		agent.lastError = ""
	} else {
		agent.lastError = strings.TrimSpace(kind.errorText)
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	if kind.recover != nil {
		if err := kind.recover(ctx, s, agent); err != nil {
			return false, err
		}
	}
	if before != agent.state && strings.TrimSpace(kind.recoveredTrigger) != "" {
		s.publishStateChanged(agent, string(before), kind.recoveredTrigger)
	}
	return true, nil
}

func (s *service) ensureTurnStartedLocked(
	ctx context.Context,
	agent *agentState,
	trigger agentdto.AgentTrigger,
	states ...agentdto.AgentState,
) error {
	if agent == nil {
		return formatIllegalTransitionError(ctx, agent, "", string(trigger), errIllegalStateTransition)
	}
	if agent.state == agentdto.StateTurnStarting {
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted)
	}
	if agentStateMatches(agent.state, states...) {
		return nil
	}
	return formatIllegalTransitionError(ctx, agent, string(agent.state), string(trigger), errIllegalStateTransition)
}

func agentStateMatches(state agentdto.AgentState, states ...agentdto.AgentState) bool {
	for _, candidate := range states {
		if state == candidate {
			return true
		}
	}
	return false
}

func (s *service) withAgentLocked(agentID string, fn func(*agentState) error) error {
	if s == nil {
		return fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := lookupAgentByIDLocked(s.agents, agentID)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (s *service) withAgentReadLocked(agentID string, fn func(*agentState) error) error {
	if s == nil {
		return fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, err := lookupAgentByIDLocked(s.agents, agentID)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (s *service) withAgentReadLockedByAgentID(agentID string, fn func(*agentState) error) error {
	if s == nil {
		return fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, err := lookupAgentByIdentityLocked(s.agents, agentID, agentIdentityLocalOnly)
	if err != nil {
		return err
	}
	return fn(agent)
}

// agentIdentityKind narrows how an external identifier may be looked
// up against the in-memory agents map. The distinction matters for
// the P4 identity-fence contract (plan §63 / §121 / §282):
//
//   - agentIdentityLocalOnly: only the persisted orchestration
//     agent_id (map key) is consulted. Use this for callers that
//     already hold a local authoritative id (e.g. API-facing lookups). Reverse
//     lookups are denied so a remote id cannot be silently accepted
//     outside a trusted hook boundary.
//
//   - agentIdentityAny: local id first, then reverse by remoteAgentID
//     or remoteThreadID. Use this only for the inbound hook path,
//     which receives events stamped with remote IDs from the main app.
//
// Pre-P4 the helper performed agentIdentityAny unconditionally, which
// meant every caller silently inherited the reverse-lookup trust
// domain. The named kind turns the assumption into a declaration.
type agentIdentityKind int

const (
	agentIdentityLocalOnly agentIdentityKind = iota
	agentIdentityAny
)

// lookupAgentByIDLocked performs a reverse-capable lookup and is kept
// for in-tree callers that still need the legacy behavior (hook /
// event ingestion, and the withAgentLocked / withAgentReadLocked
// helpers consumed by those paths). New callers should prefer
// lookupAgentByIdentityLocked with an explicit identity kind. The
// reverse-lookup literal comparisons are pinned to this file by the
// archtest in
// internal/archtest/orchestration_agent_identity_reverse_lookup_guard_test.go.
func lookupAgentByIDLocked(agents map[string]*agentState, agentID string) (*agentState, error) {
	return lookupAgentByIdentityLocked(agents, agentID, agentIdentityAny)
}

// lookupAgentByIdentityLocked resolves an agent handle against an
// explicitly-declared identity kind. See agentIdentityKind for the
// trust implications of each kind.
func lookupAgentByIdentityLocked(agents map[string]*agentState, agentID string, kind agentIdentityKind) (*agentState, error) {
	agentID = strings.TrimSpace(agentID)
	// Primary lookup: by persisted orchestration agent_id (map key).
	if agent, ok := agents[agentID]; ok {
		return agent, nil
	}
	if kind == agentIdentityLocalOnly {
		return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
	}
	// Reverse lookup (agentIdentityAny only): by remoteAgentID or
	// remoteThreadID assigned by the main app. Hook events carry the
	// remote id, not necessarily the persisted orchestration agent_id.
	for _, candidate := range agents {
		if candidate.remoteAgentID == agentID || candidate.remoteThreadID == agentID {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

// agentSessionFenceOK enforces the P4 session-identity fence on
// inbound hook payloads that carry an AgentSessionHeader. If the
// event is stamped with a SessionID, it must match the agent's
// current session (== agent.launchSeq formatted as decimal); a
// mismatch means the event belongs to a prior launch generation and
// must be dropped. An empty SessionID is accepted as legacy /
// compatibility input because older producers did not emit it.
func agentSessionFenceOK(agent *agentState, evSessionID string) bool {
	if agent == nil {
		return false
	}
	ev := strings.TrimSpace(evSessionID)
	if ev == "" {
		return true
	}
	return ev == agentSessionID(agent)
}

func lookupAgentBySeqLocked(
	agents map[string]*agentState,
	agentID string,
	launchSeq uint64,
) (*agentState, error) {
	agent, err := lookupAgentByIDLocked(agents, agentID)
	if err != nil {
		return nil, err
	}
	if agent.launchSeq != launchSeq {
		return nil, fmt.Errorf("%w: %s/%d", errAgentNotFound, strings.TrimSpace(agentID), launchSeq)
	}
	return agent, nil
}

func (s *service) withDAGStore(fn func(taskdag.OrchestrationStore) error) error {
	if s == nil || s.dagStore == nil {
		return errors.New("dag store is not configured")
	}
	return fn(s.dagStore)
}

func decodeLegacyAlias[C any, L any](
	raw []byte,
	current *C,
	aliasFn func(*C, *L) error,
) error {
	return decodeLegacyAliasWith(raw, current, aliasFn, json.Unmarshal)
}

func decodeLegacyAliasWith[C any, L any](
	raw []byte,
	current *C,
	aliasFn func(*C, *L) error,
	decode func([]byte, any) error,
) error {
	if decode == nil {
		decode = json.Unmarshal
	}
	if err := decode(raw, current); err != nil {
		return err
	}
	var legacy L
	if err := decode(raw, &legacy); err != nil {
		return err
	}
	return aliasFn(current, &legacy)
}

func agentSessionID(agent *agentState) string {
	if agent == nil || agent.launchSeq == 0 {
		return ""
	}
	return strconv.FormatUint(agent.launchSeq, 10)
}
