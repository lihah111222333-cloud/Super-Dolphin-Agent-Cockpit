package heterociexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLTerminalReceiptContract(t *testing.T) {
	for _, fragment := range []string{
		"CREATE TABLE heteroci_receipt.terminal_receipts",
		"operation_id TEXT NOT NULL",
		"manifest_digest TEXT NOT NULL",
		"plan_digest TEXT NOT NULL",
		"authority_generation BIGINT NOT NULL",
		"started_at TEXT NOT NULL",
		"finished_at TEXT NOT NULL",
		"cleanup_state TEXT NOT NULL CHECK (cleanup_state = 'verified')",
		"event_seq BIGINT NOT NULL",
		"event_digest TEXT NOT NULL",
		"CREATE TABLE heteroci_receipt.receipt_events",
		PostgreSQLReceiptTerminalManifestIndex,
		PostgreSQLReceiptEventStageIndex,
	} {
		if !strings.Contains(PostgreSQLReceiptDDL, fragment) {
			t.Fatalf("PostgreSQLReceiptDDL lacks %q", fragment)
		}
	}
	if strings.Contains(strings.ToLower(PostgreSQLReceiptDDL), "sqlite") {
		t.Fatal("PostgreSQLReceiptDDL contains SQLite")
	}
	if strings.Contains(strings.ToLower(PostgreSQLReceiptDDL), "password") {
		t.Fatal("PostgreSQLReceiptDDL contains credentials")
	}
	for _, forbidden := range []string{
		"GRANT SELECT, INSERT, UPDATE ON TABLE heteroci_receipt.terminal_receipts",
		"GRANT SELECT, INSERT, UPDATE ON TABLE heteroci_receipt.receipt_events",
	} {
		if strings.Contains(PostgreSQLReceiptDDL, forbidden) {
			t.Fatalf("PostgreSQLReceiptDDL grants forbidden mutation on structured table: %q", forbidden)
		}
	}
	if !strings.Contains(PostgreSQLReceiptDDL, "GRANT SELECT, INSERT, UPDATE ON TABLE heteroci_receipt.batch_receipts") {
		t.Fatal("PostgreSQLReceiptDDL removed legacy batch UPDATE grant")
	}
}

func TestPostgreSQLTerminalReceiptEventsAndTerminal(t *testing.T) {
	state := newPostgreSQLStructuredTestState()
	store, database := openPostgreSQLStructuredTestStore(t, state)
	t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })
	lifecycle := validPostgreSQLLifecycleEvents(t, false)
	appendPostgreSQLReceiptLifecycle(t, store, lifecycle)
	terminal := lifecycle[len(lifecycle)-1]
	receipt := validPostgreSQLTerminalReceipt(t, terminal)
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("WriteTerminalReceipt() error = %v", err)
	}
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("replayed WriteTerminalReceipt() error = %v", err)
	}
	got, found, err := store.ReadTerminalReceipt(context.Background(), receipt.OperationID)
	if err != nil || !found || got != receipt {
		t.Fatalf("ReadTerminalReceipt() = %#v, %t, %v", got, found, err)
	}
	assertPostgreSQLStructuredCanonicalBytes(t, state, receipt, lifecycle)
}

func appendPostgreSQLReceiptLifecycle(t *testing.T, store *PostgreSQLReceiptStore, lifecycle []PostgreSQLReceiptEvent) {
	t.Helper()
	for _, event := range lifecycle {
		if err := store.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", event.Seq, err)
		}
	}
	if err := store.AppendEvent(context.Background(), lifecycle[len(lifecycle)-1]); err != nil {
		t.Fatalf("replayed AppendEvent(%d) error = %v", lifecycle[len(lifecycle)-1].Seq, err)
	}
}

func assertPostgreSQLEventConflictScenarios(t *testing.T, store *PostgreSQLReceiptStore, first PostgreSQLReceiptEvent) {
	t.Helper()
	if err := store.AppendEvent(context.Background(), first); err != nil {
		t.Fatalf("AppendEvent(first) error = %v", err)
	}
	gap := validPostgreSQLReceiptEvent(t, 3, PostgreSQLReceiptEventStageTerminal, PostgreSQLReceiptEventStateCompleted)
	if err := store.AppendEvent(context.Background(), gap); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("sequence gap error = %v", err)
	}
	regressed := validPostgreSQLReceiptEvent(t, 2, PostgreSQLReceiptEventStageTerminal, PostgreSQLReceiptEventStateCompleted)
	regressed.OccurredAt = first.OccurredAt.Add(-time.Second)
	regressed.EventDigest = ""
	if err := store.AppendEvent(context.Background(), regressed); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("occurred_at regression error = %v", err)
	}
	conflict := first
	conflict.Code = "different_code"
	conflict.EventDigest = ""
	if err := store.AppendEvent(context.Background(), conflict); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("event conflict error = %v", err)
	}
}

func TestPostgreSQLTerminalReceiptRejectsSequenceAndIdentityConflicts(t *testing.T) {
	state := newPostgreSQLStructuredTestState()
	store, database := openPostgreSQLStructuredTestStore(t, state)
	t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })

	first := validPostgreSQLReceiptEvent(t, 1, PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStateCompleted)
	assertPostgreSQLEventConflictScenarios(t, store, first)
	lifecycle := validPostgreSQLLifecycleEvents(t, false)
	for _, event := range lifecycle[1:] {
		if err := store.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", event.Seq, err)
		}
	}
	terminalEvent := lifecycle[len(lifecycle)-1]
	receipt := validPostgreSQLTerminalReceipt(t, terminalEvent)
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("WriteTerminalReceipt() error = %v", err)
	}
	drifted := receipt
	drifted.RequestDigest = testReceiptDigest("different-request")
	if err := store.WriteTerminalReceipt(context.Background(), drifted); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("receipt conflict error = %v", err)
	}
	missingHead := receipt
	missingHead.OperationID = strings.Repeat("b", 64)
	if _, found, err := store.ReadTerminalReceipt(context.Background(), missingHead.OperationID); err != nil || found {
		t.Fatalf("missing receipt read = %t, %v", found, err)
	}
}

func TestPostgreSQLTerminalReceiptAuthorityAndAmbiguousCommit(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*postgresqlStructuredTestAuthority)
	}{
		{name: "standby", mutate: func(value *postgresqlStructuredTestAuthority) { value.inRecovery = true }},
		{name: "schema", mutate: func(value *postgresqlStructuredTestAuthority) { value.eventTableExists = false }},
		{name: "wrong_owner", mutate: func(value *postgresqlStructuredTestAuthority) { value.tableOwnership = "receipt_events:other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newPostgreSQLStructuredTestState()
			test.mutate(&state.authority)
			store, database := openPostgreSQLStructuredTestStore(t, state)
			t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })
			event := validPostgreSQLReceiptEvent(t, 1, PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStateCompleted)
			if err := store.AppendEvent(context.Background(), event); !errors.Is(err, ErrPostgreSQLReceiptStore) {
				t.Fatalf("AppendEvent() error = %v", err)
			}
		})
	}

	state := newPostgreSQLStructuredTestState()
	store, database := openPostgreSQLStructuredTestStore(t, state)
	t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })
	lifecycle := validPostgreSQLLifecycleEvents(t, false)
	event := lifecycle[0]
	state.setCommitError(errors.New("commit acknowledgement lost"))
	if err := store.AppendEvent(context.Background(), event); err != nil {
		t.Fatalf("ambiguous AppendEvent() error = %v", err)
	}
	for _, lifecycleEvent := range lifecycle[1:] {
		if err := store.AppendEvent(context.Background(), lifecycleEvent); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", lifecycleEvent.Seq, err)
		}
	}
	terminalEvent := lifecycle[len(lifecycle)-1]
	receipt := validPostgreSQLTerminalReceipt(t, terminalEvent)
	state.setCommitError(errors.New("terminal acknowledgement lost"))
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("ambiguous WriteTerminalReceipt() error = %v", err)
	}
}

func TestPostgreSQLTerminalReceiptBlockedLifecycle(t *testing.T) {
	state := newPostgreSQLStructuredTestState()
	store, database := openPostgreSQLStructuredTestStore(t, state)
	t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })
	lifecycle := validPostgreSQLLifecycleEvents(t, true)
	for _, event := range lifecycle {
		if err := store.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", event.Seq, err)
		}
	}
	receipt := validPostgreSQLTerminalReceipt(t, lifecycle[len(lifecycle)-1])
	receipt.Status = PostgreSQLReceiptStatusBlocked
	receipt.ExitCode = -1
	receipt.ErrorCode = ""
	receipt.CapacityCode = "capacity_blocked"
	receipt.ReceiptDigest = ""
	if _, digest, err := EncodePostgreSQLTerminalReceipt(receipt); err != nil {
		t.Fatalf("encode blocked receipt: %v", err)
	} else {
		receipt.ReceiptDigest = digest
	}
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("WriteTerminalReceipt(blocked) error = %v", err)
	}
}

func TestPostgreSQLFailedLifecycleMatrix(t *testing.T) {
	for _, failureIndex := range []int{4, 5} {
		t.Run(fmt.Sprintf("failure_%d", failureIndex), func(t *testing.T) {
			events, receipt := validPostgreSQLFailedLifecycle(t, failureIndex)
			if err := validatePostgreSQLReceiptEventChain(events, receipt); err != nil {
				t.Fatalf("valid failed lifecycle error = %v", err)
			}
		})
	}
	events, receipt := validPostgreSQLFailedLifecycle(t, 5)
	if err := validatePostgreSQLReceiptEventChain(validPostgreSQLLifecycleEvents(t, false), receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("missing failure stage error = %v", err)
	}
	events[4].State = PostgreSQLReceiptEventStateFailed
	events[4].Code = "second_failure"
	if err := validatePostgreSQLReceiptEventChain(events, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("multiple failure stages error = %v", err)
	}
	events, receipt = validPostgreSQLFailedLifecycle(t, 5)
	events[6].State = PostgreSQLReceiptEventStateSkipped
	if err := validatePostgreSQLReceiptEventChain(events, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("unproven cleanup error = %v", err)
	}
	events, receipt = validPostgreSQLFailedLifecycle(t, 5)
	receipt.ErrorCode = "different_failure"
	if err := validatePostgreSQLReceiptEventChain(events, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("terminal code drift error = %v", err)
	}
}

func validPostgreSQLFailedLifecycle(t *testing.T, failureIndex int) ([]PostgreSQLReceiptEvent, PostgreSQLTerminalReceipt) {
	events := validPostgreSQLLifecycleEvents(t, false)
	for index := failureIndex; index < 6; index++ {
		events[index].State = PostgreSQLReceiptEventStateSkipped
		events[index].Code = "not_run"
	}
	events[failureIndex].State = PostgreSQLReceiptEventStateFailed
	events[failureIndex].Code = "workload_failed"
	events[6].State = PostgreSQLReceiptEventStateCompleted
	events[6].Code = ""
	events[7].State = PostgreSQLReceiptEventStateFailed
	events[7].Code = "workload_failed"
	for index := range events {
		events[index].EventDigest = ""
		_, digest, err := EncodePostgreSQLReceiptEvent(events[index])
		if err != nil {
			t.Fatalf("encode failed lifecycle event: %v", err)
		}
		events[index].EventDigest = digest
	}
	receipt := validPostgreSQLTerminalReceipt(t, events[7])
	receipt.Status = PostgreSQLReceiptStatusFailed
	receipt.ExitCode = 1
	receipt.ErrorCode = "workload_failed"
	receipt.CapacityCode = ""
	receipt.ReceiptDigest = ""
	_, digest, err := EncodePostgreSQLTerminalReceipt(receipt)
	if err != nil {
		t.Fatalf("encode failed lifecycle receipt: %v", err)
	}
	receipt.ReceiptDigest = digest
	return events, receipt
}

func TestPostgreSQLTerminalEventCodeBinding(t *testing.T) {
	for _, test := range []struct {
		status string
		code   string
	}{
		{status: PostgreSQLReceiptStatusSucceeded, code: "success_code"},
		{status: PostgreSQLReceiptStatusBlocked, code: "different_capacity"},
	} {
		events := validPostgreSQLLifecycleEvents(t, test.status == PostgreSQLReceiptStatusBlocked)
		receipt := validPostgreSQLTerminalReceipt(t, events[len(events)-1])
		receipt.Status = test.status
		receipt.ExitCode = -1
		receipt.ErrorCode = ""
		receipt.CapacityCode = "capacity_blocked"
		if test.status == PostgreSQLReceiptStatusSucceeded {
			receipt.ExitCode = 0
			receipt.CapacityCode = ""
		}
		events[len(events)-1].Code = test.code
		if err := validatePostgreSQLReceiptEventChain(events, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
			t.Fatalf("status=%s code drift error = %v", test.status, err)
		}
	}
}

func TestPostgreSQLBlockedLifecycleRequiresStableEvidence(t *testing.T) {
	lifecycle := validPostgreSQLLifecycleEvents(t, true)
	receipt := validPostgreSQLTerminalReceipt(t, lifecycle[len(lifecycle)-1])
	receipt.Status = PostgreSQLReceiptStatusBlocked
	receipt.ExitCode = -1
	receipt.ErrorCode = ""
	receipt.CapacityCode = "capacity_blocked"
	receipt.ReceiptDigest = ""
	lifecycle[0].State = PostgreSQLReceiptEventStateSkipped
	if err := validatePostgreSQLReceiptEventChain(lifecycle, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("blocked request validation gap error = %v", err)
	}
	lifecycle = validPostgreSQLLifecycleEvents(t, true)
	lifecycle[2].Code = ""
	if err := validatePostgreSQLReceiptEventChain(lifecycle, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("blocked capacity code gap error = %v", err)
	}
	lifecycle = validPostgreSQLLifecycleEvents(t, true)
	lifecycle[3].Code = ""
	if err := validatePostgreSQLReceiptEventChain(lifecycle, receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("blocked skipped code gap error = %v", err)
	}
}

func TestPostgreSQLTerminalReceiptRejectsIncompleteLifecycle(t *testing.T) {
	state := newPostgreSQLStructuredTestState()
	store, database := openPostgreSQLStructuredTestStore(t, state)
	t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })
	lifecycle := validPostgreSQLLifecycleEvents(t, false)
	for _, event := range lifecycle[:6] {
		if err := store.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", event.Seq, err)
		}
	}
	terminal := lifecycle[7]
	terminal.Seq = 7
	terminal.EventDigest = ""
	if _, digest, err := EncodePostgreSQLReceiptEvent(terminal); err != nil {
		t.Fatalf("encode incomplete terminal event: %v", err)
	} else {
		terminal.EventDigest = digest
	}
	if err := store.AppendEvent(context.Background(), terminal); err != nil {
		t.Fatalf("AppendEvent(incomplete terminal) error = %v", err)
	}
	receipt := validPostgreSQLTerminalReceipt(t, terminal)
	if err := store.WriteTerminalReceipt(context.Background(), receipt); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("incomplete lifecycle error = %v", err)
	}
}

func TestPostgreSQLReceiptReplayCommitReconcilesAndTerminalClosesOperation(t *testing.T) {
	state := newPostgreSQLStructuredTestState()
	store, database := openPostgreSQLStructuredTestStore(t, state)
	t.Cleanup(func() { closePostgreSQLStructuredTestDatabase(t, database) })
	lifecycle := validPostgreSQLLifecycleEvents(t, false)
	appendPostgreSQLReceiptLifecycle(t, store, lifecycle)
	receipt := validPostgreSQLTerminalReceipt(t, lifecycle[len(lifecycle)-1])
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("initial WriteTerminalReceipt() error = %v", err)
	}
	state.setCommitError(errors.New("event replay acknowledgement lost"))
	if err := store.AppendEvent(context.Background(), lifecycle[len(lifecycle)-1]); err != nil {
		t.Fatalf("replayed event after ambiguous commit error = %v", err)
	}
	state.setCommitError(errors.New("terminal replay acknowledgement lost"))
	if err := store.WriteTerminalReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("replayed terminal after ambiguous commit error = %v", err)
	}
	newEvent := validPostgreSQLReceiptEvent(t, 9, PostgreSQLReceiptEventStageTerminal, PostgreSQLReceiptEventStateCompleted)
	if err := store.AppendEvent(context.Background(), newEvent); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("seq9 after terminal error = %v", err)
	}
	state.mutex.Lock()
	state.row.events[8].code = "tampered"
	state.mutex.Unlock()
	if _, _, err := store.ReadTerminalReceipt(context.Background(), receipt.OperationID); err == nil {
		t.Fatal("ReadTerminalReceipt accepted tampered event chain")
	}
}

func TestPostgreSQLReceiptTimesRequireCanonicalUTC(t *testing.T) {
	event := validPostgreSQLReceiptEvent(t, 1, PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStateCompleted)
	event.OccurredAt = event.OccurredAt.In(time.FixedZone("UTC+0", 0))
	if _, _, err := EncodePostgreSQLReceiptEvent(event); err == nil {
		t.Fatal("event accepted non-canonical UTC location")
	}
	receipt := validPostgreSQLTerminalReceipt(t, validPostgreSQLReceiptEvent(t, 1, PostgreSQLReceiptEventStageTerminal, PostgreSQLReceiptEventStateCompleted))
	receipt.StartedAt = receipt.StartedAt.In(time.FixedZone("UTC+0", 0))
	if _, _, err := EncodePostgreSQLTerminalReceipt(receipt); err == nil {
		t.Fatal("receipt accepted non-canonical UTC location")
	}
}

func TestPostgreSQLReceiptStageAndStateMatrix(t *testing.T) {
	stages := []PostgreSQLReceiptEventStage{PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStageManifestBinding, PostgreSQLReceiptEventStageCapacitySnapshot, PostgreSQLReceiptEventStageReservation, PostgreSQLReceiptEventStageMaterialize, PostgreSQLReceiptEventStageExecute, PostgreSQLReceiptEventStageCleanup, PostgreSQLReceiptEventStageTerminal}
	for leftIndex, left := range stages {
		for rightIndex, right := range stages {
			if got := postgreSQLReceiptStageOrder(left) <= postgreSQLReceiptStageOrder(right); got != (leftIndex <= rightIndex) {
				t.Fatalf("stage order %q -> %q = %t", left, right, got)
			}
		}
	}
	for _, test := range []struct {
		previous PostgreSQLReceiptEventState
		next     PostgreSQLReceiptEventState
		allowed  bool
	}{{PostgreSQLReceiptEventStateStarted, PostgreSQLReceiptEventStateCompleted, true}, {PostgreSQLReceiptEventStateStarted, PostgreSQLReceiptEventStateFailed, true}, {PostgreSQLReceiptEventStateSucceeded, PostgreSQLReceiptEventStateCompleted, true}, {PostgreSQLReceiptEventStateCompleted, PostgreSQLReceiptEventStateFailed, false}, {PostgreSQLReceiptEventStateFailed, PostgreSQLReceiptEventStateCompleted, false}} {
		if got := validPostgreSQLReceiptStateTransition(test.previous, test.next); got != test.allowed {
			t.Fatalf("state transition %q -> %q = %t, want %t", test.previous, test.next, got, test.allowed)
		}
	}
}

func validPostgreSQLReceiptEvent(t *testing.T, seq int64, stage PostgreSQLReceiptEventStage, state PostgreSQLReceiptEventState) PostgreSQLReceiptEvent {
	event := PostgreSQLReceiptEvent{
		SchemaVersion:       PostgreSQLReceiptEventSchema,
		OperationID:         strings.Repeat("a", 64),
		ManifestDigest:      testReceiptDigest("manifest"),
		PlanDigest:          testReceiptDigest("plan"),
		NodeID:              "Ubuntu-01",
		NodeIDDigest:        testReceiptDigest("Ubuntu-01"),
		ExecutorKind:        "remote_node",
		AuthorityGeneration: 7,
		Seq:                 seq,
		OccurredAt:          time.Date(2026, 8, 22, 1, 2, int(seq), 0, time.UTC),
		Stage:               stage,
		State:               state,
		Code:                "ok",
	}
	_, digest, err := EncodePostgreSQLReceiptEvent(event)
	if err != nil {
		t.Fatalf("encode receipt event fixture: %v", err)
	}
	event.EventDigest = digest
	return event
}

func validPostgreSQLLifecycleEvents(t *testing.T, blocked bool) []PostgreSQLReceiptEvent {
	stages := PostgreSQLReceiptLifecycleStages()
	events := make([]PostgreSQLReceiptEvent, 0, len(stages))
	for index, stage := range stages {
		state, code := postgreSQLLifecycleFixtureState(blocked, index)
		event := validPostgreSQLReceiptEvent(t, int64(index+1), stage, state)
		event.Code = code
		event.EventDigest = ""
		_, digest, err := EncodePostgreSQLReceiptEvent(event)
		if err != nil {
			t.Fatalf("encode lifecycle event fixture: %v", err)
		}
		event.EventDigest = digest
		events = append(events, event)
	}
	return events
}

func postgreSQLLifecycleFixtureState(blocked bool, index int) (PostgreSQLReceiptEventState, string) {
	if !blocked {
		if index == 7 {
			return PostgreSQLReceiptEventStateCompleted, ""
		}
		return PostgreSQLReceiptEventStateCompleted, "ok"
	}
	switch index {
	case 2:
		return PostgreSQLReceiptEventStateBlocked, "capacity_blocked"
	case 3, 4, 5, 6:
		return PostgreSQLReceiptEventStateSkipped, "capacity_blocked"
	case 7:
		return PostgreSQLReceiptEventStateBlocked, "capacity_blocked"
	default:
		return PostgreSQLReceiptEventStateCompleted, "ok"
	}
}

func validPostgreSQLTerminalReceipt(t *testing.T, event PostgreSQLReceiptEvent) PostgreSQLTerminalReceipt {
	receipt := PostgreSQLTerminalReceipt{
		SchemaVersion:       PostgreSQLTerminalReceiptSchema,
		OperationID:         event.OperationID,
		RequestDigest:       testReceiptDigest("request"),
		ManifestDigest:      event.ManifestDigest,
		PlanDigest:          event.PlanDigest,
		NodeID:              event.NodeID,
		ExecutorKind:        event.ExecutorKind,
		AuthorityGeneration: event.AuthorityGeneration,
		StartedAt:           event.OccurredAt.Add(-time.Minute),
		FinishedAt:          event.OccurredAt,
		ExitCode:            0,
		CleanupState:        PostgreSQLReceiptCleanupVerified,
		ErrorCode:           "",
		CapacityCode:        "",
		Status:              PostgreSQLReceiptStatusSucceeded,
		EventSeq:            event.Seq,
		EventDigest:         event.EventDigest,
	}
	_, digest, err := EncodePostgreSQLTerminalReceipt(receipt)
	if err != nil {
		t.Fatalf("encode lifecycle event fixture: %v", err)
	}
	receipt.ReceiptDigest = digest
	return receipt
}

func testReceiptDigest(value string) string {
	return digestPostgreSQLReceiptBytes([]byte(value))
}
