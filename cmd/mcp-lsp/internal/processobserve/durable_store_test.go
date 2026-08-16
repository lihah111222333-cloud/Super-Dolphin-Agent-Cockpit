package processobserve_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

func TestDurableStorePersistsAtomicNoSignalIncident(t *testing.T) {
	root := canonicalTempRoot(t)
	store := openDurableTestStore(t, root)
	defer store.Close()
	snapshot := probeMustFail(t, 9_999_997)
	decision := recordDurableTestSnapshot(t, store, snapshot)
	assertPersistedNoSignal(t, decision)
	assertIncidentContent(t, root)
	reopened := openDurableTestStore(t, root)
	defer reopened.Close()
	assertReopenedDecision(t, reopened, decision)
}

func TestDurableStorePendingFanoutSurvivesReopenAndRetries(t *testing.T) {
	root := canonicalTempRoot(t)
	store := openDurableTestStore(t, root)
	snapshot := probeMustFail(t, 9_999_996)
	store.InjectProjectionFailureOnceForTest()
	decision, err := store.RecordGhost(context.Background(), snapshot)
	if err == nil {
		t.Fatal("RecordGhost() error = nil after injected fan-out failure")
	}
	assertPendingDecision(t, decision)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openDurableTestStore(t, root)
	defer reopened.Close()
	assertRetriedDecision(t, reopened, decision)
}

func TestDurableStoreRejectsUnsafeRootAndHardlink(t *testing.T) {
	root := canonicalTempRoot(t)
	assertUnsafeRootRejected(t, root)
	assertHardlinkRejected(t, root)
}

func openDurableTestStore(t *testing.T, root string) *processobserve.Store {
	t.Helper()
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() error = %v", err)
	}
	return store
}

func recordDurableTestSnapshot(t *testing.T, store *processobserve.Store, snapshot processprobe.Snapshot) processobserve.Decision {
	t.Helper()
	decision, err := store.RecordGhost(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("RecordGhost() error = %v", err)
	}
	return decision
}

func assertPersistedNoSignal(t *testing.T, decision processobserve.Decision) {
	t.Helper()
	if decision.Status() != processobserve.DecisionPersisted || decision.SignalSent() {
		t.Fatalf("decision = %#v, want persisted no-signal incident", decision)
	}
	if !sameProjectionIdentity(decision) {
		t.Fatal("candidate and blocked projections do not share incident identity")
	}
	if decision.EventID() == "" || decision.OperationID() == "" || decision.BucketKey() == "" {
		t.Fatalf("decision keys missing: event=%q operation=%q bucket=%q", decision.EventID(), decision.OperationID(), decision.BucketKey())
	}
}

func sameProjectionIdentity(decision processobserve.Decision) bool {
	candidate := decision.CandidateProjection()
	blocked := decision.BlockedProjection()
	return candidate.EventID() == blocked.EventID() && candidate.OperationID() == blocked.OperationID()
}

func assertIncidentContent(t *testing.T, root string) {
	t.Helper()
	raw := readIncidentBytes(t, root)
	if strings.Contains(raw, root) || strings.Contains(raw, "argv") || strings.Contains(raw, "token") {
		t.Fatalf("durable incident leaked sensitive value: %q", raw)
	}
	if got := incidentFileCount(t, root); got != 1 {
		t.Fatalf("incident file count = %d, want one atomic incident", got)
	}
}

func assertReopenedDecision(t *testing.T, store *processobserve.Store, want processobserve.Decision) {
	t.Helper()
	decisions, err := store.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("reopened decisions = %#v, want one incident", decisions)
	}
	got := decisions[0]
	if got.EventID() != want.EventID() || got.Status() != processobserve.DecisionPersisted {
		t.Fatalf("reopened decision = %#v, want persisted incident %q", got, want.EventID())
	}
}

func assertPendingDecision(t *testing.T, decision processobserve.Decision) {
	t.Helper()
	if decision.Status() != processobserve.DecisionPairPending {
		t.Fatalf("status = %q, want pair_pending", decision.Status())
	}
	if decision.CandidateProjection().Acked() || decision.BlockedProjection().Acked() {
		t.Fatal("failed fan-out acknowledged a projection")
	}
}

func assertRetriedDecision(t *testing.T, store *processobserve.Store, want processobserve.Decision) {
	t.Helper()
	count, err := store.RetryPending(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("RetryPending() = (%d, %v), want one successful retry", count, err)
	}
	decisions, err := store.ListDecisions(context.Background())
	if err != nil || len(decisions) != 1 {
		t.Fatalf("ListDecisions() = (%#v, %v), want one incident", decisions, err)
	}
	got := decisions[0]
	if got.EventID() != want.EventID() || got.OperationID() != want.OperationID() || got.Status() != processobserve.DecisionPersisted {
		t.Fatalf("retry changed incident identity: before=%q/%q after=%q/%q", want.EventID(), want.OperationID(), got.EventID(), got.OperationID())
	}
	if !got.CandidateProjection().Acked() || !got.BlockedProjection().Acked() {
		t.Fatal("retry did not acknowledge both projections")
	}
}

func assertHardlinkRejected(t *testing.T, root string) {
	t.Helper()
	store := openDurableTestStore(t, root)
	_ = recordDurableTestSnapshot(t, store, probeMustFail(t, 9_999_995))
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lock := filepath.Join(root, ".store.lock")
	other := filepath.Join(t.TempDir(), "hardlink")
	linkHardlinkOrSkip(t, lock, other)
	if _, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true}); err == nil {
		t.Fatal("OpenDurableStore() accepted hardlinked lock")
	}
}

func linkHardlinkOrSkip(t *testing.T, source string, target string) {
	t.Helper()
	if err := os.Link(source, target); err != nil {
		if errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EPERM) {
			t.Skipf("hardlink unavailable on test filesystem: %v", err)
		}
		t.Fatalf("hardlink lock: %v", err)
	}
}

func incidentFileCount(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", root, err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".incident") {
			count++
		}
	}
	return count
}

func readIncidentBytes(t *testing.T, root string) string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", root, err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".incident") {
			data, err := os.ReadFile(filepath.Join(root, entry.Name()))
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", entry.Name(), err)
			}
			return string(data)
		}
	}
	t.Fatal("incident file not found")
	return ""
}
