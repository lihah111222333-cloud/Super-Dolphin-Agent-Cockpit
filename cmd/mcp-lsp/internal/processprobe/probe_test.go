package processprobe_test

import (
	"context"
	"os"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

func TestProbeCurrentProcessIsReadOnly(t *testing.T) {
	snapshot, err := processprobe.Probe(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("Probe(current pid) error = %v", err)
	}
	assertCurrentSnapshot(t, snapshot)
}

func assertCurrentSnapshot(t *testing.T, snapshot processprobe.Snapshot) {
	t.Helper()
	if !snapshot.Valid() {
		t.Fatal("current process snapshot is invalid")
	}
	if !snapshot.Alive() {
		t.Fatal("current process snapshot is not alive")
	}
	if snapshot.AuthorityDecision() != processprobe.AuthorityNoSignal {
		t.Fatalf("authority decision = %q, want no_signal", snapshot.AuthorityDecision())
	}
	if snapshot.SignalSent() {
		t.Fatal("read-only probe reported a signal")
	}
	if snapshot.IdentityComplete() {
		t.Fatal("platform PID evidence unexpectedly claimed lifecycle identity")
	}
	missing := snapshot.MissingFields()
	for _, required := range lifecycleFields() {
		if !contains(missing, required) {
			t.Fatalf("missing fields = %v, want %q", missing, required)
		}
	}
}

func lifecycleFields() []string {
	return []string{"receipt_id", "owner_instance_id", "workspace_hash", "generation", "client_start", "binary_digest"}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProbeRejectsInvalidPID(t *testing.T) {
	snapshot, err := processprobe.Probe(context.Background(), 1)
	if err == nil {
		t.Fatal("Probe(pid=1) error = nil")
	}
	if snapshot.Valid() || snapshot.Reason() == "" {
		t.Fatalf("invalid probe snapshot = %#v, want invalid with reason", snapshot)
	}
}
