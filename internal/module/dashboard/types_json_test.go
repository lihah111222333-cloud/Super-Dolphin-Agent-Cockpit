package dashboard

import (
	"encoding/json"
	"testing"
)

func TestTurnRefZeroTimestampPreservesWireShape(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(TurnRef{})
	if err != nil {
		t.Fatalf("json.Marshal(TurnRef{}) error = %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", raw, err)
	}
	timestamp, ok := fields["timestamp"]
	if !ok {
		t.Fatalf("json.Marshal(TurnRef{}) = %s, want timestamp field", raw)
	}
	if got, want := string(timestamp), `"0001-01-01T00:00:00Z"`; got != want {
		t.Fatalf("timestamp = %s, want %s", got, want)
	}
}
