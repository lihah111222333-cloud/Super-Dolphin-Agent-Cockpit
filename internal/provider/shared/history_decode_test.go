package shared

import (
	"encoding/json"
	"testing"
)

func TestDecodeHistoryFieldsRejectsInvalidStructuredData(t *testing.T) {
	for _, tc := range []struct {
		name      string
		timestamp string
		metadata  json.RawMessage
	}{
		{name: "bad timestamp", timestamp: "yesterday"},
		{name: "invalid json", metadata: json.RawMessage(`{`)},
		{name: "array metadata", metadata: json.RawMessage(`[]`)},
		{name: "number metadata", metadata: json.RawMessage(`1`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := DecodeHistoryFields(tc.timestamp, tc.metadata); err == nil {
				t.Fatal("DecodeHistoryFields() error = nil, want strict decode failure")
			}
		})
	}
}

func TestDecodeHistoryFieldsAcceptsEmptyNullAndObjectMetadata(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{"kind":"history"}`)} {
		if _, _, err := DecodeHistoryFields("2026-07-14T00:00:00.123456789Z", raw); err != nil {
			t.Fatalf("DecodeHistoryFields() error = %v", err)
		}
	}
}
