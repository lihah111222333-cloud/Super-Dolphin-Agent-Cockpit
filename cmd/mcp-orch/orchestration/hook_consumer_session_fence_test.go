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

// TestHookConsumerStateChangeRespectsSessionFence 验证 StateChanged 事件的 session fence。
// SessionID 与当前 launchSeq 不一致时必须丢弃；空 SessionID 作为兼容输入允许通过。
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

// TestAgentSessionFenceOK 单独覆盖 session fence helper 的不变量。
// 空 SessionID 策略和 launchSeq 字符串格式任一回归都会在这里直接暴露。
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
	// launchSeq==0 表示尚无权威 session，任何非空 SessionID 都必须拒绝。
	if agentSessionFenceOK(&agentState{}, "1") {
		t.Fatalf("zero launchSeq must reject a non-empty SessionID")
	}
}
