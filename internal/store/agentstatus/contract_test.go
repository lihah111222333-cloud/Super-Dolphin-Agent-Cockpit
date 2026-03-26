package agentstatus

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentStatusJSONUsesSnakeCase(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(AgentStatus{
		AgentID:     "agent-1",
		AgentName:   "Agent One",
		SessionID:   "session-1",
		Status:      "running",
		StagnantSec: 42,
		Error:       "boom",
		OutputTail:  json.RawMessage(`{"ok":true}`),
		CreatedAt:   time.Unix(1, 0).UTC(),
		UpdatedAt:   time.Unix(2, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	for _, key := range []string{
		"agent_id",
		"agent_name",
		"session_id",
		"status",
		"stagnant_sec",
		"error",
		"output_tail",
		"created_at",
		"updated_at",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("Marshal() missing key %q in %s", key, payload)
		}
	}

	for _, key := range []string{"AgentID", "AgentName", "SessionID", "StagnantSec", "OutputTail", "CreatedAt", "UpdatedAt"} {
		if _, ok := got[key]; ok {
			t.Fatalf("Marshal() unexpectedly included PascalCase key %q in %s", key, payload)
		}
	}
}
