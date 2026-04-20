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

func TestRegisterSubscriptions_PlanParity(t *testing.T) {
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

	// TurnInputReceived no longer projects a user timeline item; dialog
	// (user/assistant) comes from thread/messages history RPC exclusively.
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
	// Give the dispatcher a brief moment; the timeline must stay at one plan item.
	assertStableItemCount(t, svc, "t1", 1, "TurnInputReceived should not add a user item to the timeline")
}

func TestPlanUpdated_StructuredCodexPayload(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		_ = dispatcher.Close()
	}()

	// Simulate Codex turn/plan/updated with structured payload.
	codexPayload := json.RawMessage(`{
		"agentId": "agent_123",
		"explanation": "并行审查前端和后端代码",
		"plan": [
			{"status": "inProgress", "step": "审查前端组件"},
			{"status": "pending", "step": "审查后端逻辑"},
			{"status": "pending", "step": "汇总审查结论"}
		],
		"threadId": "t-codex",
		"turnId": "turn-codex"
	}`)
	event.Publish(dispatcher, turndto.PlanUpdated{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t-codex"},
				AgentID:      "agent_123",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-codex"},
		},
		Payload: codexPayload,
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t-codex")
		if len(items) != 1 || items[0].Kind != "plan" {
			return false
		}
		text := items[0].Text
		// Must contain explanation, not raw JSON.
		if !contains(text, "并行审查前端和后端代码") {
			return false
		}
		// Must contain rendered steps with icons.
		if !contains(text, "🔄 1. 审查前端组件") {
			return false
		}
		if !contains(text, "⏳ 2. 审查后端逻辑") {
			return false
		}
		// Must NOT contain raw JSON braces.
		if contains(text, `"agentId"`) || contains(text, `"threadId"`) {
			return false
		}
		return true
	}, "expected structured Codex plan payload to be parsed into readable text")
}

func TestPlanDelta_StructuredStepsArray(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		_ = dispatcher.Close()
	}()

	// Simulate PlanDelta where Delta is a serialized JSON steps array
	// (from marshalPreview fallback).
	event.Publish(dispatcher, turndto.PlanDelta{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t-delta"},
				AgentID:      "agent_456",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-delta"},
		},
		Delta: `[{"status":"done","step":"lint code"},{"status":"running","step":"run tests"}]`,
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t-delta")
		if len(items) != 1 || items[0].Kind != "plan" {
			return false
		}
		text := items[0].Text
		return contains(text, "✅ 1. lint code") && contains(text, "🔄 2. run tests")
	}, "expected JSON steps array delta to be parsed into readable text")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
