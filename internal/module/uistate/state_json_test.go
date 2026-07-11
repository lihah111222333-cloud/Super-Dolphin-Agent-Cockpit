package uistate

import (
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate/timeline"
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

func TestUIStateTimelineItemsUseFrontendKeys(t *testing.T) {
	t.Parallel()

	state := UIState{
		TimelineByThread: map[string][]timeline.Item{
			"thread-1": {{
				ID:        "approval-1",
				Kind:      "approval",
				CallID:    "call-1",
				RequestID: 7,
				ToolName:  "shell",
				ItemType:  "request_user_input",
			}},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var raw struct {
		TimelinesByThread map[string][]map[string]any `json:"timelinesByThread"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items := raw.TimelinesByThread["thread-1"]
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item["callId"] != "call-1" || item["requestId"] != float64(7) {
		t.Fatalf("timeline item keys = %#v, want callId/requestId", item)
	}
	if _, ok := item["call_id"]; ok {
		t.Fatalf("timeline item unexpectedly kept snake_case call_id: %#v", item)
	}
	if _, ok := item["request_id"]; ok {
		t.Fatalf("timeline item unexpectedly kept snake_case request_id: %#v", item)
	}
}
