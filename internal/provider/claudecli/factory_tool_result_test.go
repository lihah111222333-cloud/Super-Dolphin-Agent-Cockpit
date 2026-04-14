package claudecli

import (
	"encoding/json"
	"testing"
)

func TestDecodeUserMessageBlockCapturesToolResultContent(t *testing.T) {
	events, err := decodeUserMessageBlock(json.RawMessage(`{
		"type": "tool_result",
		"tool_use_id": "call-1",
		"tool_name": "read_file",
		"content": [{"type": "text", "text": "preview body"}]
	}`), map[string]any{"thread_id": "thread-1", "turn_id": "turn-1"})
	if err != nil {
		t.Fatalf("decodeUserMessageBlock() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if got := events[0].EventType; got != "tool:use_end" {
		t.Fatalf("EventType = %q, want tool:use_end", got)
	}
	data, _ := events[0].Data.(map[string]any)
	if got := dataString(data, "result"); got != "preview body" {
		t.Fatalf("result = %q, want preview body", got)
	}
	if got := dataString(data, "call_id"); got != "call-1" {
		t.Fatalf("call_id = %q, want call-1", got)
	}
}

func TestDecodeUserMessageBlockMapsToolResultErrorContent(t *testing.T) {
	events, err := decodeUserMessageBlock(json.RawMessage(`{
		"type": "tool_result",
		"tool_use_id": "call-2",
		"tool_name": "read_file",
		"is_error": true,
		"content": "fatal"
	}`), map[string]any{"thread_id": "thread-1", "turn_id": "turn-1"})
	if err != nil {
		t.Fatalf("decodeUserMessageBlock() error = %v", err)
	}
	data, _ := events[0].Data.(map[string]any)
	if dataBool(data, "success") {
		t.Fatalf("success = true, want false")
	}
	if got := dataString(data, "error"); got != "fatal" {
		t.Fatalf("error = %q, want fatal", got)
	}
}
