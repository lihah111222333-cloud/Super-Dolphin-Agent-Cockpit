package ui

import (
	"encoding/json"
	"strings"
	"testing"
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
