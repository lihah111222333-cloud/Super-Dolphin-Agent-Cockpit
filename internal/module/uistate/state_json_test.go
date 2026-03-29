package uistate

import (
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
)

func TestUIStateTimelineByThreadJSONKey(t *testing.T) {
	t.Parallel()

	state := UIState{
		TimelineByThread: map[string][]timeline.Item{
			"thread-1": {{ID: "turn:t1", Kind: "turn_start"}},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	// Frontend expects "timelinesByThread" (with 's').
	if _, ok := raw["timelinesByThread"]; !ok {
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		t.Fatalf("JSON key \"timelinesByThread\" not found; available keys: %v", keys)
	}
}
