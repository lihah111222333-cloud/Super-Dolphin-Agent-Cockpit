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
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"
)

type EventBus = *event.Dispatcher
type agentState = agentRuntime
type eventPublisher func(EventBus, *agentState, []any)

type activeTurnFinalizationKind struct {
	trigger    string
	errorText  string
	clearError bool
}

type activeTurnRecoveryKind struct {
	recoveredTrigger string
	errorText        string
	clearError       bool
	recover          func(context.Context, *service, *agentRuntime) error
}

const (
	eventTypeStateChanged         = "state_changed"
	eventTypeAgentLaunched        = "agent_launched"
	eventTypeAgentStopped         = "agent_stopped"
	eventTypeAgentRecovering      = "agent_recovering"
	eventTypeAgentFailed          = "agent_failed"
	eventTypeAgentRuntimeReported = "agent_runtime_reported"
	eventTypeTurnStalled          = "turn_stalled"
	eventTypeTurnResumed          = "turn_resumed"
)

var eventPublishers = map[string]eventPublisher{
	eventTypeStateChanged:         publishStateChangedEvent,
	eventTypeAgentLaunched:        publishAgentLaunchedEvent,
	eventTypeAgentStopped:         publishAgentStoppedEvent,
	eventTypeAgentRecovering:      publishAgentRecoveringEvent,
	eventTypeAgentFailed:          publishAgentFailedEvent,
	eventTypeAgentRuntimeReported: publishAgentRuntimeReportedEvent,
	eventTypeTurnStalled:          publishTurnStalledEvent,
	eventTypeTurnResumed:          publishTurnResumedEvent,
}

func emitEvent(bus EventBus, eventType string, agentID string, fields ...any) {
	if bus == nil {
		return
	}
	agent, values := eventAgent(agentID, fields)
	publish, ok := eventPublishers[eventType]
	if !ok {
		return
	}
	publish(bus, agent, values)
}

func publishStateChangedEvent(bus EventBus, agent *agentState, values []any) {
	if len(values) < 2 {
		return
	}
	event.Publish(bus, agentdto.StateChanged{
		AgentSessionHeader: agentSessionHeader(agent),
		OldState:           eventString(values, 0),
		NewState:           agentStateValue(agent),
		Trigger:            eventString(values, 1),
	})
}

func publishAgentLaunchedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	provider, _ := snapshotProvider(agent)
	event.Publish(bus, agentdto.AgentLaunched{
		AgentSessionHeader: agentSessionHeader(agent),
		CWD:                eventString(values, 0),
		Name:               agent.name,
		Provider:           provider,
	})
}

func publishAgentStoppedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentStopped{
		AgentSessionHeader: agentSessionHeader(agent),
		Reason:             eventString(values, 0),
	})
}

func publishAgentRecoveringEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentRecovering{
		AgentSessionHeader: agentSessionHeader(agent),
		Reason:             eventString(values, 0),
	})
}

func publishAgentFailedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentFailed{
		AgentSessionHeader: agentSessionHeader(agent),
		Error:              eventString(values, 0),
		Recoverable:        eventBool(values, 1),
	})
}

func publishAgentRuntimeReportedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentRuntimeReported{
		AgentSessionHeader: agentSessionHeader(agent),
		Port:               eventInt(values, 0),
		Provider:           eventString(values, 1),
	})
}

func publishTurnStalledEvent(bus EventBus, agent *agentState, values []any) {
	turnID := eventString(values, 1)
	if turnID == "" {
		return
	}
	event.Publish(bus, turndto.TurnStalled{
		TurnHeader: turnHeader(agent, eventString(values, 0), turnID, eventTime(values, 4)),
		Reason:     eventString(values, 2),
		StalledMS:  stalledMilliseconds(eventDuration(values, 3)),
	})
}

func publishTurnResumedEvent(bus EventBus, agent *agentState, values []any) {
	turnID := eventString(values, 1)
	if turnID == "" {
		return
	}
	event.Publish(bus, turndto.TurnResumed{
		TurnHeader: turnHeader(agent, eventString(values, 0), turnID, eventTime(values, 3)),
		Reason:     eventString(values, 2),
	})
}

func resetLaunchState(agent *agentState) {
	if agent == nil {
		return
	}
	agent.cmd = nil
	agent.monitoredSeq = 0
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.remoteThreadID = ""
	agent.remoteAgentID = ""
	agent.startedAt = time.Time{}
	agent.updatedAt = time.Time{}
	agent.exitedAt = nil
}

func cleanupAgentState(agent *agentState) {
	if agent == nil {
		return
	}
	if agent.queue != nil {
		agent.queue.Clear()
	}
	agent.activeTurnID = ""
	agent.threadID = ""
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
		s.publishStateChanged(agent, before, kind.recoveredTrigger)
	}
	return true, nil
}

func (s *service) ensureTurnStartedLocked(
	ctx context.Context,
	agent *agentState,
	trigger string,
	states ...string,
) error {
	if agent == nil {
		return formatIllegalTransitionError(ctx, agent, "", trigger, errIllegalStateTransition)
	}
	if agent.state == agentdto.StateTurnStarting {
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted)
	}
	if agentStateMatches(agent.state, states...) {
		return nil
	}
	return formatIllegalTransitionError(ctx, agent, agent.state, trigger, errIllegalStateTransition)
}

func agentStateMatches(state string, states ...string) bool {
	state = strings.TrimSpace(state)
	for _, candidate := range states {
		if state == strings.TrimSpace(candidate) {
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

func lookupAgentByIDLocked(agents map[string]*agentState, agentID string) (*agentState, error) {
	agentID = strings.TrimSpace(agentID)
	// Primary lookup: by orchestration agent name (map key).
	agent, ok := agents[agentID]
	if ok {
		return agent, nil
	}
	// Reverse lookup: by remoteAgentID or remoteThreadID assigned by main app.
	// Hook events from the main app carry the remote ID, not the local name.
	for _, candidate := range agents {
		if candidate.remoteAgentID == agentID || candidate.remoteThreadID == agentID {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
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

func (s *service) withDAGStore(fn func(taskdag.Store) error) error {
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

func agentSessionHeader(agent *agentState) shareddto.AgentSessionHeader {
	return shareddto.AgentSessionHeader{
		AgentHeader: agentHeader(agent),
		SessionID:   agentSessionID(agent),
	}
}

func agentHeader(agent *agentState) shareddto.AgentHeader {
	agentID := ""
	threadID := ""
	if agent != nil {
		agentID = agent.id
		threadID = agent.threadID
	}
	return shareddto.AgentHeader{
		ThreadHeader: shareddto.ThreadHeader{
			EventHeader: shareddto.EventHeader{Timestamp: agentEventTime(agent)},
			ThreadID:    threadID,
		},
		AgentID: agentID,
	}
}

func agentEventTime(agent *agentState) time.Time {
	if agent == nil {
		return shareddto.FirstEventTime()
	}
	if !agent.updatedAt.IsZero() {
		return agent.updatedAt
	}
	if !agent.startedAt.IsZero() {
		return agent.startedAt
	}
	if agent.exitedAt != nil && !agent.exitedAt.IsZero() {
		return *agent.exitedAt
	}
	return shareddto.FirstEventTime()
}

func agentSessionID(agent *agentState) string {
	if agent == nil || agent.launchSeq == 0 {
		return ""
	}
	return strconv.FormatUint(agent.launchSeq, 10)
}

func turnHeader(agent *agentState, threadID, turnID string, timestamp time.Time) shareddto.TurnHeader {
	header := agentHeader(agent)
	if threadID != "" {
		header.ThreadID = threadID
	}
	if !timestamp.IsZero() {
		header.ThreadHeader.EventHeader.Timestamp = timestamp
	}
	return shareddto.TurnHeader{
		AgentHeader:  header,
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}
}

func stalledMilliseconds(stalled time.Duration) int64 {
	if stalled <= 0 {
		return 0
	}
	return stalled.Milliseconds()
}

func eventAgent(agentID string, fields []any) (*agentState, []any) {
	if len(fields) == 0 {
		return fallbackAgent(agentID), fields
	}
	agent, ok := fields[0].(*agentState)
	if !ok {
		return fallbackAgent(agentID), fields
	}
	if agent == nil {
		return fallbackAgent(agentID), fields[1:]
	}
	return agent, fields[1:]
}

func fallbackAgent(agentID string) *agentState {
	if agentID == "" {
		return nil
	}
	return &agentState{id: agentID}
}

func agentStateValue(agent *agentState) string {
	if agent == nil {
		return ""
	}
	return agent.state
}

func eventString(fields []any, index int) string {
	if index >= len(fields) {
		return ""
	}
	value, _ := fields[index].(string)
	return value
}

func eventBool(fields []any, index int) bool {
	if index >= len(fields) {
		return false
	}
	value, _ := fields[index].(bool)
	return value
}

func eventInt(fields []any, index int) int {
	if index >= len(fields) {
		return 0
	}
	value, _ := fields[index].(int)
	return value
}

func eventDuration(fields []any, index int) time.Duration {
	if index >= len(fields) {
		return 0
	}
	value, _ := fields[index].(time.Duration)
	return value
}

func eventTime(fields []any, index int) time.Time {
	if index >= len(fields) {
		return time.Time{}
	}
	value, _ := fields[index].(time.Time)
	return value
}
