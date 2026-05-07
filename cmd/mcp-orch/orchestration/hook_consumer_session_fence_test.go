package orchestration

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

// TestHookConsumerStateChangeRespectsSessionFence verifies P22 P4
// §121/§282: an inbound StateChanged event stamped with a SessionID
// that does not match the agent's current launchSeq must be dropped.
// An empty SessionID is accepted (legacy producer compatibility).
// A matching SessionID applies as before.
func TestHookConsumerStateChangeRespectsSessionFence(t *testing.T) {
	cases := []struct {
		name      string
		agentSeq  uint64
		evSession string
		wantState string
	}{
		{
			name:      "empty_session_id_accepted",
			agentSeq:  5,
			evSession: "",
			wantState: string(agentdto.StateTurnRunning),
		},
		{
			name:      "matching_session_id_accepted",
			agentSeq:  5,
			evSession: "5",
			wantState: string(agentdto.StateTurnRunning),
		},
		{
			name:      "stale_session_id_dropped",
			agentSeq:  5,
			evSession: "4",
			wantState: string(agentdto.StateIdle),
		},
		{
			name:      "future_session_id_dropped",
			agentSeq:  5,
			evSession: "6",
			wantState: string(agentdto.StateIdle),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
			agent := addHookTestAgent(t, svc, "agent-1")
			svc.mu.Lock()
			agent.state = agentdto.StateIdle
			agent.launchSeq = tc.agentSeq
			svc.mu.Unlock()

			consumer := newHookConsumer(svc, silentLogger())
			stateChanged := agentdto.StateChanged{
				AgentSessionHeader: sharedto.AgentSessionHeader{
					AgentHeader: sharedto.AgentHeader{
						ThreadHeader: sharedto.ThreadHeader{
							EventHeader: sharedto.EventHeader{Timestamp: time.Unix(10, 0).UTC()},
						},
						AgentID: "agent-1",
					},
					SessionID: tc.evSession,
				},
				NewState: string(agentdto.StateTurnRunning),
			}
			payload := hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)
			if _, err := consumer.After(context.Background(), payload); err != nil {
				t.Fatalf("After() error = %v", err)
			}
			snap, err := svc.Snapshot(context.Background(), "agent-1")
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			if snap.State != tc.wantState {
				t.Fatalf("state = %q, want %q (agentSeq=%d, evSessionID=%q)", snap.State, tc.wantState, tc.agentSeq, tc.evSession)
			}
		})
	}
}

// TestAgentSessionFenceOK covers the pure-function helper invariants
// independently so regressions in either the empty-SessionID policy
// or the launchSeq-to-string formatting are caught directly.
func TestAgentSessionFenceOK(t *testing.T) {
	agent := &agentState{launchSeq: 7}
	if !agentSessionFenceOK(agent, "") {
		t.Fatalf("empty SessionID should be accepted as legacy input")
	}
	if !agentSessionFenceOK(agent, strconv.FormatUint(agent.launchSeq, 10)) {
		t.Fatalf("matching SessionID should be accepted")
	}
	if agentSessionFenceOK(agent, "6") {
		t.Fatalf("stale SessionID should be rejected")
	}
	if agentSessionFenceOK(nil, "7") {
		t.Fatalf("nil agent must fail closed")
	}
	// launchSeq == 0 means no session has started; any non-empty
	// SessionID should then be rejected because the agent does not
	// yet have an authoritative session to match.
	if agentSessionFenceOK(&agentState{}, "1") {
		t.Fatalf("zero launchSeq must reject a non-empty SessionID")
	}
}
