//go:build darwin || linux

package processobserve_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
)

func TestDurableStoreRepairsPartialProjectionAck(t *testing.T) {
	root := canonicalTempRoot(t)
	store := openDurableTestStore(t, root)
	decision := recordDurableTestSnapshot(t, store, probeMustFail(t, 9_999_990))
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	incident := filepath.Join(root, decision.EventID()+".incident")
	markPartialProjectionAck(t, incident)
	reopened := openDurableTestStore(t, root)
	defer reopened.Close()
	assertPartialPending(t, reopened)
	if count, err := reopened.RetryPending(context.Background()); err != nil || count != 1 {
		t.Fatalf("RetryPending() = (%d, %v), want one repaired incident", count, err)
	}
	assertRepairedProjectionAck(t, reopened)
}

func markPartialProjectionAck(t *testing.T, incident string) {
	t.Helper()
	raw, err := os.ReadFile(incident)
	if err != nil {
		t.Fatalf("ReadFile(incident): %v", err)
	}
	document := decodePartialDocument(t, raw)
	document["status"] = json.RawMessage(`"pair_pending"`)
	document["candidate"] = setProjectionAck(t, document["candidate"], true)
	document["blocked"] = setProjectionAck(t, document["blocked"], false)
	updated, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal(partial incident): %v", err)
	}
	if err := os.WriteFile(incident, updated, 0o600); err != nil {
		t.Fatalf("WriteFile(partial incident): %v", err)
	}
}

func decodePartialDocument(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("Unmarshal(incident): %v", err)
	}
	return document
}

func setProjectionAck(t *testing.T, raw json.RawMessage, acked bool) json.RawMessage {
	t.Helper()
	var projection map[string]json.RawMessage
	if err := json.Unmarshal(raw, &projection); err != nil {
		t.Fatalf("Unmarshal(projection): %v", err)
	}
	value, err := json.Marshal(acked)
	if err != nil {
		t.Fatalf("Marshal(ack): %v", err)
	}
	projection["acked"] = value
	updated, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("Marshal(projection): %v", err)
	}
	return updated
}

func assertPartialPending(t *testing.T, store *processobserve.Store) {
	t.Helper()
	decisions, err := store.ListDecisions(context.Background())
	if err != nil || len(decisions) != 1 {
		t.Fatalf("ListDecisions() = (%#v, %v), want one partial incident", decisions, err)
	}
	decision := decisions[0]
	if decision.Status() != processobserve.DecisionPairPending || !decision.CandidateProjection().Acked() || decision.BlockedProjection().Acked() {
		t.Fatalf("partial decision = %#v, want candidate ack only", decision)
	}
}

func assertRepairedProjectionAck(t *testing.T, store *processobserve.Store) {
	t.Helper()
	decisions, err := store.ListDecisions(context.Background())
	if err != nil || len(decisions) != 1 {
		t.Fatalf("ListDecisions() = (%#v, %v), want one repaired incident", decisions, err)
	}
	decision := decisions[0]
	if decision.Status() != processobserve.DecisionPersisted || !decision.CandidateProjection().Acked() || !decision.BlockedProjection().Acked() {
		t.Fatalf("repaired decision = %#v, want both projection acks", decision)
	}
}
