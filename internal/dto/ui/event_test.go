package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

func TestUIThreadPatchWireKeepsGenerationForSequenceRestart(t *testing.T) {
	t.Parallel()

	var patch UIThreadPatch
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","source":"turn/started","sequence":1,"generation":2}`), &patch); err != nil {
		t.Fatalf("json.Unmarshal(UIThreadPatch) error = %v", err)
	}
	data, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("json.Marshal(UIThreadPatch) error = %v", err)
	}
	if !strings.Contains(string(data), `"generation":2`) {
		t.Fatalf("UIThreadPatch JSON = %s, want generation for sequence restart semantics", data)
	}
}

func TestAgentBoardOutcomeValidationAndWireFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	valid := []agentdto.Outcome{
		{Kind: agentdto.OutcomeKindSuccess, Summary: "done", CompletedAt: now},
		{Kind: agentdto.OutcomeKindFailure, Reason: "failed", CompletedAt: now},
		{Kind: agentdto.OutcomeKindStopped, Reason: "cancelled", CompletedAt: now},
	}
	for _, outcome := range valid {
		if err := outcome.Validate(); err != nil {
			t.Fatalf("Outcome(%s).Validate() error = %v", outcome.Kind, err)
		}
	}
	for _, outcome := range []agentdto.Outcome{
		{Kind: agentdto.OutcomeKindSuccess, CompletedAt: now},
		{Kind: agentdto.OutcomeKindFailure, CompletedAt: now},
		{Kind: agentdto.OutcomeKindStopped, CompletedAt: now},
	} {
		if err := outcome.Validate(); err == nil {
			t.Fatalf("Outcome(%s).Validate() error = nil, want missing required field", outcome.Kind)
		}
	}
	board := &agentdto.BoardView{
		ID: "agent-1", ThreadID: "thread-1", ParentAgentID: "agent-root", Name: "worker",
		Assignment: &agentdto.Assignment{Title: "实现契约", Description: "连接权威字段", AssignedAt: now},
		Progress:   agentdto.Progress{Status: "running", UpdatedAt: now},
		Outcome:    &valid[0],
	}
	if err := board.Validate(); err != nil {
		t.Fatalf("BoardView.Validate() error = %v", err)
	}
	data, err := json.Marshal(UIThreadPatch{ThreadID: "thread-1", Agent: board})
	if err != nil {
		t.Fatalf("json.Marshal(UIThreadPatch) error = %v", err)
	}
	for _, field := range []string{`"agent"`, `"threadId"`, `"parentAgentId"`, `"assignment"`, `"assignedAt"`, `"progress"`, `"currentStep":null`, `"completedSteps":null`, `"totalSteps":null`, `"outcome"`, `"completedAt"`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("UIThreadPatch JSON = %s, want field %s", data, field)
		}
	}
}
