package buslog

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBusExceptionLogJSONUsesSnakeCase(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(BusExceptionLog{
		ID:        1,
		Ts:        time.Unix(1, 0).UTC(),
		Category:  "rpc",
		Severity:  "error",
		Source:    "dashboard",
		ToolName:  "task_get_dag",
		Message:   "failed",
		Traceback: "stack",
		Extra:     json.RawMessage(`{"retry":false}`),
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
		"category",
		"severity",
		"source",
		"tool_name",
		"message",
		"traceback",
		"extra",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("Marshal() missing key %q in %s", key, payload)
		}
	}

	for _, key := range []string{"ID", "Ts", "Category", "Severity", "Source", "ToolName", "Message", "Traceback", "Extra"} {
		if _, ok := got[key]; ok {
			t.Fatalf("Marshal() unexpectedly included PascalCase key %q in %s", key, payload)
		}
	}
}
