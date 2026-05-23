package contract

import (
	"encoding/json"
	"testing"
)

func TestStartDAGResponseJSONUsesToolFieldNames(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(StartDAGResponse{
		RunKey:  "dag-1#run-abc",
		Version: 7,
	})
	if err != nil {
		t.Fatalf("Marshal(StartDAGResponse) error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(StartDAGResponse JSON) error = %v", err)
	}
	if got["run_key"] != "dag-1#run-abc" {
		t.Fatalf("run_key = %v, want dag-1#run-abc (raw=%s)", got["run_key"], raw)
	}
	if got["version"] != float64(7) {
		t.Fatalf("version = %v, want 7 (raw=%s)", got["version"], raw)
	}
	if _, ok := got["RunKey"]; ok {
		t.Fatalf("legacy RunKey key leaked into JSON: %s", raw)
	}
	if _, ok := got["Version"]; ok {
		t.Fatalf("legacy Version key leaked into JSON: %s", raw)
	}
}
