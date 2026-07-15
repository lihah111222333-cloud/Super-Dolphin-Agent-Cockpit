package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

func TestWorkspaceRunCreatedTimestampsAreRequired(t *testing.T) {
	zero := time.Time{}
	nonZero := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	assertWorkspaceRunCreatedTimes(t, WorkspaceRunCreated{CreatedAt: zero, UpdatedAt: zero}, "0001-01-01T00:00:00Z")
	assertWorkspaceRunCreatedTimes(t, WorkspaceRunCreated{CreatedAt: nonZero, UpdatedAt: nonZero}, "2026-07-15T04:05:06Z")
}

func assertWorkspaceRunCreatedTimes(t *testing.T, event WorkspaceRunCreated, want string) {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, field := range []string{"created_at", "updated_at"} {
		var got string
		if err := json.Unmarshal(fields[field], &got); err != nil {
			t.Fatalf("%s is not a required timestamp: %v; JSON = %s", field, err, raw)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", field, got, want)
		}
	}
}
