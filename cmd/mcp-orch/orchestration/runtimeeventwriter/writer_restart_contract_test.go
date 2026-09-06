package runtimeeventwriter

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

// TestWriterRestartPreservesIdentityAndSequenceContract proves the durable
// boundary rather than only the in-memory writer fence: a reopened writer
// rejects a duplicate event identity, keeps the original sequence, and
// allocates the next event at the following dense sequence. The adapter's
// notifier is also required to observe the canonical row after COMMIT.
func TestWriterRestartPreservesIdentityAndSequenceContract(t *testing.T) {
	path := t.TempDir() + "/writer-restart.sqlite"
	db, _, _, writer, active := openRestartWriter(t, path)
	notifier := &restartCommitNotifier{db: db}
	adapter := mustRestartAdapter(t, writer, notifier)

	first := realRuntimeEvent(active.AgentID, "writer-restart-duplicate")
	assertRestartFirst(t, adapter, first, notifier)
	if err := db.Close(); err != nil {
		t.Fatalf("close first database = %v", err)
	}

	reopened, _, reopenedEvents, reopenedWriter := reopenRestartWriter(t, path)
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedNotifier := &restartCommitNotifier{db: reopened}
	reopenedAdapter := mustRestartAdapter(t, reopenedWriter, reopenedNotifier)

	assertRestartDuplicate(t, reopenedAdapter, first, reopenedNotifier)
	assertRestartNext(t, reopenedAdapter, reopenedEvents, active, reopenedNotifier)
}

func openRestartWriter(t *testing.T, path string) (*sql.DB, *identity.Store, *eventstore.Store, *Writer, identity.CurrentBinding) {
	t.Helper()
	db := openRealWriterDB(t, path)
	identityStore := mustRealIdentityStore(t, db)
	active := seedRealActiveIdentity(t, identityStore)
	eventStore := mustRealEventStore(t, db, identityStore)
	return db, identityStore, eventStore, mustRealWriter(t, eventStore, identityStore), active
}

func reopenRestartWriter(t *testing.T, path string) (*sql.DB, *identity.Store, *eventstore.Store, *Writer) {
	t.Helper()
	db := openRealWriterDB(t, path)
	identityStore := mustRealIdentityStore(t, db)
	eventStore := mustRealEventStore(t, db, identityStore)
	return db, identityStore, eventStore, mustRealWriter(t, eventStore, identityStore)
}

func mustRestartAdapter(t *testing.T, writer *Writer, notifier eventstore.EventNotifier) *CandidateIngressAdapter {
	t.Helper()
	adapter, err := NewCandidateIngressAdapter(writer, notifier)
	if err != nil {
		t.Fatalf("NewCandidateIngressAdapter() error = %v", err)
	}
	return adapter
}

func assertRestartFirst(t *testing.T, adapter *CandidateIngressAdapter, event Event, notifier *restartCommitNotifier) {
	t.Helper()
	result, err := adapter.Append(t.Context(), event)
	if err != nil || result.Receipt.SourceSequence != 1 || result.Event == nil {
		t.Fatalf("first append = %#v, error=%v, want materialized sequence 1", result, err)
	}
	if notifier.count() != 1 {
		t.Fatalf("first notification count = %d, want 1", notifier.count())
	}
}

func assertRestartDuplicate(t *testing.T, adapter *CandidateIngressAdapter, event Event, notifier *restartCommitNotifier) {
	t.Helper()
	duplicate, err := adapter.Append(t.Context(), event)
	if !errors.Is(err, eventstore.ErrEventConflict) || duplicate != (eventstore.AppendResult{}) {
		t.Fatalf("duplicate append after reopen = %#v, error=%v, want conflict and zero result", duplicate, err)
	}
	if notifier.count() != 0 {
		t.Fatalf("duplicate notification count = %d, want 0", notifier.count())
	}
}

func assertRestartNext(t *testing.T, adapter *CandidateIngressAdapter, events *eventstore.Store, active identity.CurrentBinding, notifier *restartCommitNotifier) {
	t.Helper()
	next, err := adapter.Append(t.Context(), realRuntimeEvent(active.AgentID, "writer-restart-next"))
	if err != nil || next.Receipt.SourceSequence != 2 || next.Event == nil {
		t.Fatalf("next append after reopen = %#v, error=%v, want materialized sequence 2", next, err)
	}
	if notifier.count() != 1 {
		t.Fatalf("next notification count = %d, want 1", notifier.count())
	}
	state, err := events.GetSourceState(t.Context(), "task/"+active.AgentID, active.IncarnationID, eventstore.LaneOutput)
	if err != nil {
		t.Fatalf("GetSourceState(after reopen) error = %v", err)
	}
	if state.NextSequence != 3 || state.MaterializedSequence != 2 || state.Status != "active" {
		t.Fatalf("source state after reopen = %#v, want next=3 materialized=2 active", state)
	}
}

type restartCommitNotifier struct {
	mu     sync.Mutex
	db     *sql.DB
	events []string
}

func (n *restartCommitNotifier) Notify(event eventstore.AgentEvent) error {
	if n == nil || n.db == nil {
		return fmt.Errorf("restart notifier database is unavailable")
	}
	var rows int
	if err := n.db.QueryRow("SELECT COUNT(*) FROM orchestration_agent_events WHERE event_id = ?", event.Envelope.EventID).Scan(&rows); err != nil {
		return fmt.Errorf("probe committed event: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("probe committed event rows = %d, want 1", rows)
	}
	n.mu.Lock()
	n.events = append(n.events, event.Envelope.EventID)
	n.mu.Unlock()
	return nil
}

func (n *restartCommitNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.events)
}
