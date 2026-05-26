package contract

import (
	"encoding/json"
	"testing"
)

func TestStartDAGResponseJSONUsesToolFieldNames(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(StartDAGResponse{
		RunID:            42,
		RunKey:           "dag-1#run-abc",
		Version:          7,
		ReadyRootNodes:   1,
		ScheduledWakeups: 0,
		ExecutionState:   "waiting_for_assignee",
		Warning:          "dispatch required",
	})
	if err != nil {
		t.Fatalf("Marshal(StartDAGResponse) error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(StartDAGResponse JSON) error = %v", err)
	}
	want := map[string]any{
		"run_key":           "dag-1#run-abc",
		"version":           float64(7),
		"run_id":            float64(42),
		"ready_root_nodes":  float64(1),
		"scheduled_wakeups": float64(0),
		"execution_state":   "waiting_for_assignee",
		"warning":           "dispatch required",
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s = %v, want %v (raw=%s)", key, got[key], value, raw)
		}
	}
	for _, legacy := range []string{"RunKey", "Version", "ExecutionState"} {
		if _, ok := got[legacy]; ok {
			t.Fatalf("legacy %s key leaked into JSON: %s", legacy, raw)
		}
	}
}
