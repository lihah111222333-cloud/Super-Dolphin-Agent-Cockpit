package heterociowner

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestCommitOperationIsIdempotentAndChainsRevision(t *testing.T) {
	owner := newTestOwner(t)
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	request := firstOperationRequest(fence)
	first, err := CommitOperation(context.Background(), owner.db, request)
	if err != nil {
		t.Fatalf("CommitOperation: %v", err)
	}
	replay, err := CommitOperation(context.Background(), owner.db, request)
	if err != nil {
		t.Fatalf("replay CommitOperation: %v", err)
	}
	assertOperationReplay(t, first, replay)
	assertFirstOperationChain(t, first)
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current.Revision != 2 {
		t.Fatalf("current fence = %#v, %v", current, err)
	}
}

func firstOperationRequest(fence heteroci.OwnerFence) OperationRequest {
	return OperationRequest{
		Fence:            fence,
		OperationID:      heteroci.OperationID(strings.Repeat("a", 64)),
		Kind:             "plan_and_reserve",
		RequestBytes:     []byte(`{"run_id":"run-a"}`),
		ResultBytes:      []byte(`{"ok":true}`),
		ProjectionDigest: testDigest("1"),
	}
}

func assertOperationReplay(t *testing.T, first, replay OperationRecord) {
	t.Helper()
	if !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay changed record: first=%#v replay=%#v", first, replay)
	}
}

func assertFirstOperationChain(t *testing.T, record OperationRecord) {
	t.Helper()
	if record.Revision != 2 || record.PreviousJournal != zeroDigest || record.JournalDigest == zeroDigest {
		t.Fatalf("first journal record = %#v", record)
	}
}

func TestCommitOperationRejectsDriftAndStaleFence(t *testing.T) {
	owner := newTestOwner(t)
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	request := validOperationRequest(fence, "b", "payload")
	if _, err := CommitOperation(context.Background(), owner.db, request); err != nil {
		t.Fatalf("CommitOperation: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*OperationRequest)
	}{
		{name: "kind", mutate: func(request *OperationRequest) { request.Kind = "stop" }},
		{name: "request bytes", mutate: func(request *OperationRequest) { request.RequestBytes = []byte("different") }},
		{name: "result bytes", mutate: func(request *OperationRequest) { request.ResultBytes = []byte(`{"started":false}`) }},
		{name: "projection digest", mutate: func(request *OperationRequest) { request.ProjectionDigest = testDigest("3") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conflict := request
			tc.mutate(&conflict)
			if _, err := CommitOperation(context.Background(), owner.db, conflict); !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("drift error = %v, want ErrOperationConflict", err)
			}
		})
	}
	stale := validOperationRequest(fence, "c", "stale")
	if _, err := CommitOperation(context.Background(), owner.db, stale); err == nil {
		t.Fatal("stale fence was accepted")
	}
}

func TestCommitOperationRejectsTamperedJournalIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		column string
		value  any
	}{
		{name: "request digest", column: "request_digest", value: testDigest("f")},
		{name: "result digest", column: "result_digest", value: testDigest("f")},
		{name: "journal digest", column: "journal_digest", value: testDigest("e")},
		{name: "previous journal digest", column: "prev_journal_digest", value: testDigest("d")},
		{name: "invalid projection digest", column: "projection_digest", value: "invalid"},
		{name: "request bytes without digest", column: "request_bytes", value: []byte("tampered request")},
		{name: "result bytes without digest", column: "result_bytes", value: []byte("tampered result")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := newTestOwner(t)
			fence, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			request := firstOperationRequest(fence)
			first, err := CommitOperation(context.Background(), owner.db, request)
			if err != nil {
				t.Fatalf("CommitOperation: %v", err)
			}
			tamperOperationField(t, owner, first, tc.column, tc.value)
			before := readOperationStoreState(t, owner, first)
			if _, err := CommitOperation(context.Background(), owner.db, request); err == nil {
				t.Fatal("tampered replay was accepted")
			} else if errors.Is(err, ErrOperationConflict) {
				t.Fatalf("tampered replay returned ErrOperationConflict: %v", err)
			}
			after := readOperationStoreState(t, owner, first)
			if after != before {
				t.Fatalf("tampered replay changed owner state: before=%#v after=%#v", before, after)
			}
		})
	}
}

type operationStoreState struct {
	headRevision   int64
	headJournal    string
	operationCount int
}

func tamperOperationField(t *testing.T, owner *Owner, record OperationRecord, column string, value any) {
	t.Helper()
	result, err := owner.db.ExecContext(context.Background(), "UPDATE heteroci_routing_operations SET "+column+" = ? WHERE scope = ? AND owner_epoch = ? AND operation_id = ?", value, record.Scope, record.OwnerEpoch, record.OperationID)
	if err != nil {
		t.Fatalf("tamper %s: %v", column, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("tamper %s rows affected: %v", column, err)
	}
	if rows != 1 {
		t.Fatalf("tamper %s affected %d rows, want 1", column, rows)
	}
}

func readOperationStoreState(t *testing.T, owner *Owner, record OperationRecord) operationStoreState {
	t.Helper()
	var state operationStoreState
	if err := owner.db.QueryRowContext(context.Background(), "SELECT revision, journal_digest FROM heteroci_owner_heads WHERE scope = ?", record.Scope).Scan(&state.headRevision, &state.headJournal); err != nil {
		t.Fatalf("read owner head state: %v", err)
	}
	if err := owner.db.QueryRowContext(context.Background(), "SELECT count(*) FROM heteroci_routing_operations WHERE scope = ? AND owner_epoch = ?", record.Scope, record.OwnerEpoch).Scan(&state.operationCount); err != nil {
		t.Fatalf("read operation row count: %v", err)
	}
	return state
}

func validOperationRequest(fence heteroci.OwnerFence, id, payload string) OperationRequest {
	return OperationRequest{
		Fence:            fence,
		OperationID:      heteroci.OperationID(strings.Repeat(id, 64)),
		Kind:             "start",
		RequestBytes:     []byte(payload),
		ResultBytes:      []byte(`{"started":true}`),
		ProjectionDigest: testDigest("2"),
	}
}
