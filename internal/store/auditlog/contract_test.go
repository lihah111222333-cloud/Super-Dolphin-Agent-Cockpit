package auditlog

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAuditEventJSONUsesSnakeCase(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(AuditEvent{
		ID:        1,
		Ts:        time.Unix(1, 0).UTC(),
		EventType: "dag",
		Action:    "create",
		Result:    "ok",
		Actor:     "tester",
		Target:    "dag-1",
		Detail:    "created",
		Level:     "info",
		Extra:     json.RawMessage(`{"id":"dag-1"}`),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	for _, key := range []string{
		"id",
		"ts",
		"event_type",
		"action",
		"result",
		"actor",
		"target",
		"detail",
		"level",
		"extra",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("Marshal() missing key %q in %s", key, payload)
		}
	}

	for _, key := range []string{"ID", "Ts", "EventType", "Action", "Result", "Actor", "Target", "Detail", "Level", "Extra"} {
		if _, ok := got[key]; ok {
			t.Fatalf("Marshal() unexpectedly included PascalCase key %q in %s", key, payload)
		}
	}
}
