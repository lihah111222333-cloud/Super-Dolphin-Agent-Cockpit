package terminaloutcome

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	_ "modernc.org/sqlite"
)

const terminalSecretFixture = "provider-token=secret-value /private/agent/config.go"

func TestCommitTerminalOutcomeCASReplayConflictAndPublicOnlyStorage(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	commit := terminalCommitFixture("terminal:event-1")
	commit = activateCommitFixture(t, store, commit)

	first, err := store.CommitTerminalOutcome(ctx, commit)
	if err != nil {
		t.Fatalf("CommitTerminalOutcome(first) error = %v", err)
	}
	if first.Replayed {
		t.Fatal("CommitTerminalOutcome(first).Replayed = true, want false")
	}
	replayed, err := store.CommitTerminalOutcome(ctx, commit)
	if err != nil {
		t.Fatalf("CommitTerminalOutcome(replay) error = %v", err)
	}
	if !replayed.Replayed || replayed.OutboxID != first.OutboxID {
		t.Fatalf("replay = %#v, want same outbox id %d and Replayed", replayed, first.OutboxID)
	}
	driftedReplay := commit
	driftedReplay.PublicOutcome.Code = "DIFFERENT_FAILURE"
	if _, err := store.CommitTerminalOutcome(ctx, driftedReplay); !errors.Is(err, contract.ErrTerminalOutcomeConflict) {
		t.Fatalf("CommitTerminalOutcome(drifted replay) error = %v, want ErrTerminalOutcomeConflict", err)
	}

	conflict := commit
	conflict.Identity.EventID = "terminal:event-2"
	conflict.Identity.TerminalIdentity = "terminal:identity-2"
	if _, err := store.CommitTerminalOutcome(ctx, conflict); !errors.Is(err, contract.ErrTerminalOutcomeConflict) {
		t.Fatalf("CommitTerminalOutcome(conflict) error = %v, want ErrTerminalOutcomeConflict", err)
	}

	var durable string
	if err := db.QueryRowContext(ctx, `
		SELECT public_outcome_json || ' ' || public_report
		FROM public_terminal_outcome_history
		WHERE agent_id = ?
	`, commit.Identity.AgentID).Scan(&durable); err != nil {
		t.Fatalf("query durable outcome: %v", err)
	}
	if strings.Contains(durable, terminalSecretFixture) {
		t.Fatalf("durable public outcome leaked raw provider detail: %q", durable)
	}
}

func TestCommitTerminalOutcomeNormalizesTimestampForColdReplay(t *testing.T) {
	store, _ := newTestStore(t)
	commit := terminalCommitFixture("terminal:event-submillisecond")
	commit.OccurredAt = commit.OccurredAt.Add(987654 * time.Nanosecond)
	commit.PublicOutcome.CompletedAt = commit.OccurredAt
	commit = activateCommitFixture(t, store, commit)

	first, err := store.CommitTerminalOutcome(context.Background(), commit)
	if err != nil {
		t.Fatalf("CommitTerminalOutcome(first) error = %v", err)
	}
	replayed, err := store.CommitTerminalOutcome(context.Background(), commit)
	if err != nil || !replayed.Replayed {
		t.Fatalf("CommitTerminalOutcome(cold replay) = %#v, %v; want replay", replayed, err)
	}
	if first.Outcome.OccurredAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("stored occurredAt = %s, want millisecond precision", first.Outcome.OccurredAt)
	}
}

func TestLoadTerminalOutcomeCurrentHeadMissingFailsFast(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.LoadTerminalOutcomeCurrentHead(context.Background(), "missing-agent"); !errors.Is(err, contract.ErrTerminalOutcomeHeadNotFound) {
		t.Fatalf("LoadTerminalOutcomeCurrentHead() error = %v, want ErrTerminalOutcomeHeadNotFound", err)
	}
}

func TestLoadTerminalOutcomeCurrentHeadRejectsCorruptDurableIdentity(t *testing.T) {
	store, db := newTestStore(t)
	commit := activateCommitFixture(t, store, terminalCommitFixture("terminal:corrupt-head"))
	if _, err := store.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	if _, err := db.Exec(`
		UPDATE terminal_outcome_current_heads
		SET public_thread_id = ''
		WHERE agent_id = ?
	`, commit.Identity.AgentID); err != nil {
		t.Fatalf("corrupt terminal current head: %v", err)
	}

	if _, err := store.LoadTerminalOutcomeCurrentHead(context.Background(), commit.Identity.AgentID); err == nil {
		t.Fatal("LoadTerminalOutcomeCurrentHead() error = nil, want corrupt identity rejection")
	}
}

func TestLoadTerminalOutcomeCurrentHeadRoundTripPreservesFullIdentityAndVersion(t *testing.T) {
	store, _ := newTestStore(t)
	commit := activateCommitFixture(t, store, terminalCommitFixture("terminal:head-roundtrip"))
	if _, err := store.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}

	head, err := store.LoadTerminalOutcomeCurrentHead(context.Background(), commit.Identity.AgentID)
	if err != nil {
		t.Fatalf("LoadTerminalOutcomeCurrentHead() error = %v", err)
	}
	if head.Capability != commit.Identity.Capability ||
		head.AgentID != commit.Identity.AgentID ||
		head.PublicThreadID != commit.Identity.PublicThreadID ||
		head.ProviderTurnID != commit.Identity.ProviderTurnID ||
		head.SessionID != commit.Identity.SessionID ||
		head.Generation != commit.Identity.Generation ||
		head.ExpectedActiveState != commit.Identity.ExpectedActiveState ||
		head.Version != commit.Identity.HeadVersion ||
		head.State != "terminal" ||
		head.TerminalEventID != commit.Identity.EventID ||
		head.TerminalIdentity != commit.Identity.TerminalIdentity ||
		!head.UpdatedAt.Equal(commit.OccurredAt) {
		t.Fatalf("durable current head roundtrip = %#v, want full canonical identity %#v", head, commit.Identity)
	}
}

func TestCommitTerminalOutcomeConcurrentCASHasOneWinner(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	base := activateCommitFixture(t, store, terminalCommitFixture("terminal:event-base"))
	const workers = 24
	var (
		wg       sync.WaitGroup
		winners  int
		replays  int
		conflict int
		mu       sync.Mutex
	)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			commit := terminalCommitFixture("terminal:event-" + string(rune('a'+i)))
			commit.Identity.HeadVersion = base.Identity.HeadVersion
			commit.Identity.TerminalIdentity = commit.Identity.EventID + ":identity"
			result, err := store.CommitTerminalOutcome(ctx, commit)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && !result.Replayed:
				winners++
			case err == nil && result.Replayed:
				replays++
			case errors.Is(err, contract.ErrTerminalOutcomeConflict):
				conflict++
			default:
				t.Errorf("CommitTerminalOutcome concurrent error = %v", err)
			}
		}()
	}
	wg.Wait()
	if winners != 1 || replays != 0 || conflict != workers-1 {
		t.Fatalf("winners=%d replays=%d conflicts=%d, want 1/0/%d", winners, replays, conflict, workers-1)
	}
}

func TestOutboxCrashWindowReplaysIdempotently(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	commit := terminalCommitFixture("terminal:event-crash")
	commit = activateCommitFixture(t, store, commit)
	committed, err := store.CommitTerminalOutcome(ctx, commit)
	if err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}

	// 模拟 DB commit 后、projector ack 前崩溃：新 worker 必须重新 claim 同一记录。
	first, err := store.ClaimTerminalOutcomeOutbox(ctx, "worker-a", time.Minute, 10)
	if err != nil || len(first) != 1 || first[0].ID != committed.OutboxID {
		t.Fatalf("first claim = %#v, %v; want outbox %d", first, err, committed.OutboxID)
	}
	if _, err := store.db.Exec("UPDATE terminal_outcome_outbox_v2 SET lease_expires_at = 0 WHERE id = ?", committed.OutboxID); err != nil {
		t.Fatalf("expire first worker lease: %v", err)
	}
	replayed, err := store.ClaimTerminalOutcomeOutbox(ctx, "worker-b", time.Minute, 10)
	if err != nil || len(replayed) != 1 || replayed[0].ID != committed.OutboxID {
		t.Fatalf("replay claim = %#v, %v; want outbox %d", replayed, err, committed.OutboxID)
	}
	if err := store.MarkTerminalOutcomeProjected(ctx, replayed[0].ID, "worker-b", replayed[0].ClaimToken); err != nil {
		t.Fatalf("MarkTerminalOutcomeProjected() error = %v", err)
	}
	empty, err := store.ClaimTerminalOutcomeOutbox(ctx, "worker-c", time.Minute, 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("claim after ack = %#v, %v; want empty", empty, err)
	}
}

func TestCommitTerminalOutcomeRollsBackPublicRecordWhenOutboxEnqueueFails(t *testing.T) {
	store, db := newTestStore(t)
	commit := activateCommitFixture(t, store, terminalCommitFixture("terminal:event-rollback"))
	if _, err := db.Exec("DROP TABLE terminal_outcome_outbox_v2"); err != nil {
		t.Fatalf("drop outbox table: %v", err)
	}
	if _, err := store.CommitTerminalOutcome(context.Background(), commit); err == nil {
		t.Fatal("CommitTerminalOutcome() error = nil, want outbox enqueue failure")
	}
	for _, table := range []string{"public_terminal_outcome_history", "terminal_outcome_private_dag_payloads"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s row count = %d, want rollback to zero", table, count)
		}
	}
	var state string
	if err := db.QueryRow("SELECT state FROM terminal_outcome_current_heads WHERE agent_id = ?", commit.Identity.AgentID).Scan(&state); err != nil {
		t.Fatalf("read current head after rollback: %v", err)
	}
	if state != "active" {
		t.Fatalf("current head state after rollback = %q, want active", state)
	}
}

func TestCanonicalTerminalIdentityRejectsMissingOrMismatchedFence(t *testing.T) {
	base := terminalCommitFixture("terminal:event-validation")
	tests := []struct {
		name   string
		mutate func(*contract.TerminalOutcomeCommit)
	}{
		{name: "agent", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.AgentID = "" }},
		{name: "public thread", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.PublicThreadID = "" }},
		{name: "provider turn", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.ProviderTurnID = "" }},
		{name: "session", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.SessionID = "" }},
		{name: "generation", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.Generation = 0 }},
		{name: "event", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.EventID = "" }},
		{name: "terminal", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.TerminalIdentity = "" }},
		{name: "expected state", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.ExpectedActiveState = "" }},
		{name: "head version", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.HeadVersion = 0 }},
		{name: "terminal expected state", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.ExpectedActiveState = "failed" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want fence rejection")
			}
		})
	}
}

func activateCommitFixture(t *testing.T, store *Store, commit contract.TerminalOutcomeCommit) contract.TerminalOutcomeCommit {
	t.Helper()
	head, err := store.ActivateTerminalOutcomeHead(context.Background(), activationFromCommit(commit))
	if err != nil {
		t.Fatalf("ActivateTerminalOutcomeHead() error = %v", err)
	}
	commit.Identity.HeadVersion = head.Version
	return commit
}

func TestTerminalOutcomeCommitRejectsUnknownDurableFields(t *testing.T) {
	commit := terminalCommitFixture("terminal:event-unknown")
	raw, err := json.Marshal(commit)
	if err != nil {
		t.Fatalf("marshal terminal outcome commit: %v", err)
	}
	mutated := strings.Replace(string(raw), `"projectionKind":`, `"rawProviderReason":"`+terminalSecretFixture+`","projectionKind":`, 1)
	var decoded contract.TerminalOutcomeCommit
	if err := json.Unmarshal([]byte(mutated), &decoded); err == nil {
		t.Fatal("json.Unmarshal() error = nil, want unknown raw provider field rejection")
	}
}

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})
	if _, err := db.Exec(terminalOutcomeSchemaForTest); err != nil {
		t.Fatalf("create terminal outcome schema: %v", err)
	}
	return New(db), db
}

func terminalCommitFixture(eventID string) contract.TerminalOutcomeCommit {
	occurredAt := time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC)
	publicError := &turndto.PublicErrorV1{
		Code: "PROVIDER_FAILED", Title: "本次执行失败", Message: "Provider 未能完成本次执行。",
		DiagnosticID: "diag-0123456789abcdef", RecoveryActions: []string{"copy_diagnostics"},
	}
	return contract.TerminalOutcomeCommit{
		SchemaVersion:  2,
		ProjectionKind: "turn_completed",
		Identity: contract.CanonicalTerminalIdentity{
			Capability: contract.TerminalOutcomeCapabilityV2,
			AgentID:    "agent-1", PublicThreadID: "thread-1", ProviderTurnID: "turn-1",
			SessionID: "session-1", Generation: 7, EventID: eventID,
			TerminalIdentity: eventID + ":identity", ExpectedActiveState: "turn_running", HeadVersion: 1,
		},
		PublicOutcome: contract.PublicOutcome{
			Kind: "failure", Code: "failed", PublicError: publicError, CompletedAt: occurredAt,
		},
		PublicReport: "turn failure: Provider 未能完成本次执行。 (diagnostic id: diag-0123456789abcdef)",
		OccurredAt:   occurredAt,
	}
}

const terminalOutcomeSchemaForTest = `
CREATE TABLE terminal_outcome_heads (
	agent_id TEXT PRIMARY KEY,
	capability TEXT NOT NULL,
	public_thread_id TEXT NOT NULL,
	provider_turn_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	generation INTEGER NOT NULL,
	event_id TEXT NOT NULL,
	terminal_identity TEXT NOT NULL,
	expected_active_state TEXT NOT NULL,
	state TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE public_terminal_outcomes (
	agent_id TEXT PRIMARY KEY,
	schema_version INTEGER NOT NULL,
	projection_kind TEXT NOT NULL,
	public_thread_id TEXT NOT NULL,
	provider_turn_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	generation INTEGER NOT NULL,
	event_id TEXT NOT NULL UNIQUE,
	terminal_identity TEXT NOT NULL UNIQUE,
	public_outcome_json TEXT NOT NULL,
	public_report TEXT NOT NULL,
	occurred_at INTEGER NOT NULL
);
CREATE TABLE terminal_outcome_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL UNIQUE,
	payload_json TEXT NOT NULL,
	status TEXT NOT NULL,
	claimed_by TEXT NOT NULL DEFAULT '',
	claimed_at INTEGER,
	projected_at INTEGER,
	created_at INTEGER NOT NULL
);
CREATE TABLE terminal_outcome_current_heads (
	agent_id TEXT PRIMARY KEY,
	capability TEXT NOT NULL,
	public_thread_id TEXT NOT NULL,
	provider_turn_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	generation INTEGER NOT NULL,
	expected_active_state TEXT NOT NULL,
	version INTEGER NOT NULL,
	state TEXT NOT NULL,
	terminal_event_id TEXT NOT NULL DEFAULT '',
	terminal_identity TEXT NOT NULL DEFAULT '',
	activated_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE public_terminal_outcome_history (
	terminal_identity TEXT PRIMARY KEY,
	event_id TEXT NOT NULL UNIQUE,
	agent_id TEXT NOT NULL,
	head_version INTEGER NOT NULL,
	schema_version INTEGER NOT NULL,
	projection_kind TEXT NOT NULL,
	public_thread_id TEXT NOT NULL,
	provider_turn_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	generation INTEGER NOT NULL,
	expected_active_state TEXT NOT NULL,
	public_outcome_json TEXT NOT NULL,
	public_report TEXT NOT NULL,
	occurred_at INTEGER NOT NULL
);
CREATE TABLE terminal_outcome_private_dag_payloads (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	terminal_identity TEXT NOT NULL UNIQUE,
	owner_agent_id TEXT NOT NULL,
	public_thread_id TEXT NOT NULL,
	provider_turn_id TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE terminal_outcome_outbox_v2 (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	terminal_identity TEXT NOT NULL UNIQUE,
	event_id TEXT NOT NULL UNIQUE,
	public_payload_json TEXT NOT NULL,
	private_dag_payload_id INTEGER,
	status TEXT NOT NULL,
	claimed_by TEXT NOT NULL DEFAULT '',
	claim_token TEXT NOT NULL DEFAULT '',
	lease_expires_at INTEGER,
	attempt_count INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	projected_at INTEGER,
	created_at INTEGER NOT NULL
);`
