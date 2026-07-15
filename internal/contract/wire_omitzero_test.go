package contract

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOptionalValueStructsOmitZeroAndKeepNonZero(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		zero     any
		nonZero  any
		fieldKey string
	}{
		{name: "agent task budget", zero: AgentTask{}, nonZero: AgentTask{Budget: AgentTaskBudget{MaxMinutes: 15}}, fieldKey: "budget"},
		{name: "dream runtime policy", zero: DreamOptions{}, nonZero: DreamOptions{RuntimePolicy: StrictDreamRuntimePolicy()}, fieldKey: "runtime_policy"},
		{name: "change request review gate", zero: ChangeRequest{}, nonZero: ChangeRequest{ReviewGate: ChangeRequestReviewGate{Status: ChangeRequestReviewGateOpen}}, fieldKey: "review_gate"},
		{name: "change request external ref", zero: ChangeRequest{}, nonZero: ChangeRequest{External: ChangeRequestExternalRef{Provider: "github"}}, fieldKey: "external"},
		{name: "commit created at", zero: ChangeRequestCommit{}, nonZero: ChangeRequestCommit{CreatedAt: now}, fieldKey: "created_at"},
		{name: "check updated at", zero: ChangeRequestCheck{}, nonZero: ChangeRequestCheck{UpdatedAt: now}, fieldKey: "updated_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertJSONFieldPresence(t, tt.zero, tt.fieldKey, false)
			assertJSONFieldPresence(t, tt.nonZero, tt.fieldKey, true)
		})
	}
}

func assertJSONFieldPresence(t *testing.T, value any, field string, want bool) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, got := fields[field]; got != want {
		t.Fatalf("field %q presence = %v, want %v; JSON = %s", field, got, want, raw)
	}
}
