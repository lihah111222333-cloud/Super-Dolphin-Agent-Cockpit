package timeline_test

import (
	"encoding/json"
	"testing"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	"github.com/kelindar/event"
)

func TestRegisterSubscriptions_PlanAndUserParity(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		_ = dispatcher.Close()
	}()

	event.Publish(dispatcher, turndto.PlanDelta{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
		Delta: "step one",
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "plan" && items[0].Text == "step one"
	}, "expected one plan item after plan delta")

	event.Publish(dispatcher, turndto.PlanUpdated{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
		Payload: json.RawMessage(`{"steps":["a"]}`),
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "plan" && items[0].Text == `{"steps":["a"]}`
	}, "expected plan update to merge into existing plan item")

	event.Publish(dispatcher, turndto.TurnInputReceived{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
		InputType: "text",
		RequestID: 7,
		Source:    "user",
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 2 && items[1].Kind == "user" && items[1].Text == "user" && items[1].RequestID == 7
	}, "expected one user item after turn input received")
}

func TestRegisterSubscriptions_ErrorParity(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		_ = dispatcher.Close()
	}()

	event.Publish(dispatcher, agentdto.AgentError{
		AgentSessionHeader: shared.AgentSessionHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
		},
		Message: "runtime boom",
	})
	event.Publish(dispatcher, agentdto.AgentFailed{
		AgentSessionHeader: shared.AgentSessionHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
		},
		Error: "agent failed",
	})
	event.Publish(dispatcher, turndto.TurnCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: false,
		Error:   "turn failed",
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		errorCount := 0
		failedTurn := false
		for _, item := range items {
			if item.Kind == "error" {
				errorCount++
			}
			if item.Kind == "turn_end" && item.Status == "failed" {
				failedTurn = true
			}
		}
		return errorCount == 3 && failedTurn
	}, "expected agent and turn failures to map to error items")
}

func TestRegisterSubscriptions_ToolCallDistinctByToolAndCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		_ = dispatcher.Close()
	}()

	publish := func(tool string) {
		event.Publish(dispatcher, tooldto.ToolCallBegin{
			ToolCallHeader: shared.ToolCallHeader{
				TurnHeader: shared.TurnHeader{
					AgentHeader: shared.AgentHeader{
						ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
						AgentID:      "agent-1",
					},
					TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
				},
				CallID:   "call-1",
				ToolName: tool,
			},
		})
	}

	publish("bash")
	publish("lsp_edit")
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 2 && items[0].Tool == "bash" && items[1].Tool == "lsp_edit"
	}, "expected same callID with different tools to keep two timeline items")
}
