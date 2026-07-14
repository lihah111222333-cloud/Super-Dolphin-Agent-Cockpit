package codexapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToProviderHistoryRejectsBadRecordWithProviderAndIndex(t *testing.T) {
	for _, messages := range [][]Message{
		{{Timestamp: "bad"}},
		{{Timestamp: "2026-07-14T00:00:00Z"}, {Metadata: json.RawMessage(`1`)}},
	} {
		_, err := toProviderHistory(messages)
		if err == nil || !strings.Contains(err.Error(), "codexapp history message") {
			t.Fatalf("toProviderHistory() error = %v", err)
		}
	}
}
