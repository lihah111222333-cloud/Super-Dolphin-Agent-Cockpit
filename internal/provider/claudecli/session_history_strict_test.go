package claudecli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToProviderHistoryRejectsBadRecordWithProviderAndIndex(t *testing.T) {
	for _, messages := range [][]Message{
		{{Timestamp: "bad"}},
		{{Timestamp: "2026-07-14T00:00:00Z"}, {Metadata: json.RawMessage(`[]`)}},
	} {
		_, err := toProviderHistory(messages)
		if err == nil || !strings.Contains(err.Error(), "claudecli history message") {
			t.Fatalf("toProviderHistory() error = %v", err)
		}
	}
}
