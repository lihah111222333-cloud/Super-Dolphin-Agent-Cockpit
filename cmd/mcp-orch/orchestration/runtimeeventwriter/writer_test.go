package runtimeeventwriter

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type testOwner struct {
	db      *sql.DB
	unknown bool
}

func (o testOwner) WithTransaction(ctx context.Context, callback func(storedb.DBTX) error) error {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	err = callback(tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if o.unknown {
		return eventstore.ErrTransactionCommitUnknown
	}
	return nil
}

type callbackCardinalityOwner struct {
	db          *sql.DB
	invocations int
	commitOnErr bool
	concurrent  bool
	unknown     bool
}

func (o callbackCardinalityOwner) WithTransaction(ctx context.Context, callback func(storedb.DBTX) error) error {
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var callbackErr error
	if o.concurrent {
		results := make(chan error, o.invocations)
		var wg sync.WaitGroup
		for i := 0; i < o.invocations; i++ {
			wg.Add(1)
			runtimesafe.SafeGo(ctx, nil, "runtimeeventwriter.test.callback-cardinality", func(context.Context) {
				defer wg.Done()
				results <- callback(tx)
			})
		}
		for i := 0; i < o.invocations; i++ {
			callbackErr = errors.Join(callbackErr, <-results)
		}
		wg.Wait()
	} else {
		for i := 0; i < o.invocations; i++ {
			callbackErr = errors.Join(callbackErr, callback(tx))
		}
	}
	if callbackErr != nil && !o.commitOnErr {
		_ = tx.Rollback()
		return callbackErr
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if o.unknown {
		return eventstore.ErrTransactionCommitUnknown
	}
	return nil
}

type testIdentity struct {
	snapshot identity.ActiveSnapshot
	seen     storedb.DBTX
}

func (r *testIdentity) GetActiveSnapshotTx(_ context.Context, tx storedb.DBTX, _ string) (identity.ActiveSnapshot, error) {
	r.seen = tx
	return r.snapshot, nil
}

type testSource struct {
	state eventstore.SourceState
	seen  storedb.DBTX
}

type testBindingVerifier struct {
	seen    storedb.DBTX
	binding Binding
	err     error
}

func (v *testBindingVerifier) VerifyTerminalBindingTx(_ context.Context, tx storedb.DBTX, binding Binding) error {
	v.seen = tx
	v.binding = binding
	return v.err
}

func (r *testSource) GetSourceStateTx(_ context.Context, tx storedb.DBTX, _, _ string, _ eventstore.Lane) (eventstore.SourceState, error) {
	r.seen = tx
	return r.state, nil
}

type testAppender struct {
	mu    sync.Mutex
	seen  storedb.DBTX
	frame eventstore.IngressFrame
	calls int
	err   error
}

type allocatedTestAppender struct {
	testAppender
	allocated   bool
	spoof       bool
	eventStatus string
}

func (a *allocatedTestAppender) AppendAllocatedEnvelopeTx(_ context.Context, tx storedb.DBTX, sourceID, sourceEpoch string, lane eventstore.Lane, writerEpoch string, build func() (eventstore.EventEnvelope, error)) (eventstore.AppendResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = tx
	a.calls++
	a.allocated = true
	envelope, err := build()
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	hash, err := eventstore.CanonicalEnvelopeHash(envelope)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	cursor, err := eventstore.CanonicalCursor(sourceID, sourceEpoch, lane, 9)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	frame := eventstore.IngressFrame{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: lane, SourceSequence: 9, Envelope: envelope, EnvelopeHash: hash, CanonicalCursor: cursor}
	if a.spoof {
		frame.SourceID = "spoofed-source"
	}
	eventStatus := a.eventStatus
	if eventStatus == "" {
		eventStatus = "materialized"
	}
	event := eventstore.AgentEvent{IngressFrame: frame, Status: eventStatus}
	state := eventstore.SourceState{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: lane, NextSequence: 10, MaterializedSequence: 9, Revision: 2, Status: "active", WriterEpoch: writerEpoch}
	return eventstore.AppendResult{Receipt: eventstore.IngressReceipt{IngressFrame: frame, Status: "materialized"}, Event: &event, State: state}, nil
}

func TestWriterPrefersOwnerDerivedAllocator(t *testing.T) {
	db := openWriterDB(t)
	identityReader := &testIdentity{snapshot: writerSnapshot()}
	sourceReader := &testSource{state: writerState()}
	appender := &allocatedTestAppender{}
	writer, err := New(testOwner{db: db}, identityReader, sourceReader, appender)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := writer.Append(t.Context(), writerEvent()); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if !appender.allocated || appender.calls != 1 || sourceReader.seen != nil {
		t.Fatalf("allocator preference = allocated:%v calls:%d sourceSeen:%v", appender.allocated, appender.calls, sourceReader.seen != nil)
	}
}

func TestWriterRejectsSpoofedAllocatorResult(t *testing.T) {
	db := openWriterDB(t)
	appender := &allocatedTestAppender{spoof: true}
	writer, err := New(testOwner{db: db}, &testIdentity{snapshot: writerSnapshot()}, &testSource{state: writerState()}, appender)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := writer.Append(t.Context(), writerEvent()); !errors.Is(err, ErrStale) {
		t.Fatalf("Append() error = %v, want ErrStale", err)
	}
}

func TestWriterRejectsUnmaterializedAllocatorEvent(t *testing.T) {
	db := openWriterDB(t)
	appender := &allocatedTestAppender{eventStatus: "failed"}
	writer, err := New(testOwner{db: db}, &testIdentity{snapshot: writerSnapshot()}, &testSource{state: writerState()}, appender)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := writer.Append(t.Context(), writerEvent()); !errors.Is(err, ErrStale) {
		t.Fatalf("Append() error = %v, want ErrStale", err)
	}
}

func (a *testAppender) AppendTx(_ context.Context, tx storedb.DBTX, frame eventstore.IngressFrame) (eventstore.AppendResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen, a.frame, a.calls = tx, frame, a.calls+1
	if a.err != nil {
		return eventstore.AppendResult{}, a.err
	}
	return eventstore.AppendResult{Receipt: eventstore.IngressReceipt{IngressFrame: frame, Status: "materialized"}}, nil
}

func TestWriterAppendsCanonicalFrameInOneTransaction(t *testing.T) {
	db := openWriterDB(t)
	identityReader := &testIdentity{snapshot: writerSnapshot()}
	sourceReader := &testSource{state: writerState()}
	appender := &testAppender{}
	writer, err := New(testOwner{db: db}, identityReader, sourceReader, appender)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := writer.Append(t.Context(), writerEvent())
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if result.Receipt.Status != "materialized" || appender.calls != 1 {
		t.Fatalf("result/calls = %#v/%d", result, appender.calls)
	}
	if identityReader.seen == nil || identityReader.seen != sourceReader.seen || sourceReader.seen != appender.seen {
		t.Fatalf("ports did not share one transaction: identity=%T source=%T appender=%T", identityReader.seen, sourceReader.seen, appender.seen)
	}
	if appender.frame.SourceSequence != 1 || appender.frame.CanonicalCursor != "task/agent-1/inc-1/output/1" {
		t.Fatalf("frame = %#v", appender.frame)
	}
}

func TestWriterAppendBoundVerifiesBindingInAppendTransaction(t *testing.T) {
	db := openWriterDB(t)
	identityReader := &testIdentity{snapshot: writerSnapshot()}
	sourceReader := &testSource{state: writerState()}
	appender := &testAppender{}
	verifier := &testBindingVerifier{}
	writer, err := New(testOwner{db: db}, identityReader, sourceReader, appender)
	if err != nil {
		t.Fatal(err)
	}
	event := writerEvent()
	event.Binding = Binding{AgentID: "agent-1", IncarnationID: "inc-1", IncarnationGeneration: 1, Provider: "codex", ProviderSessionID: "session-1", LaunchSequence: 1}
	if _, err := writer.AppendBound(t.Context(), event, verifier); err != nil {
		t.Fatalf("AppendBound() error = %v", err)
	}
	if verifier.seen == nil || verifier.seen != identityReader.seen || verifier.seen != appender.seen {
		t.Fatalf("binding verifier and append did not share one transaction")
	}
	if verifier.binding.ProviderSessionID != "session-1" {
		t.Fatalf("binding = %#v", verifier.binding)
	}
}

func TestWriterAppendBoundRejectsMalformedBindingBeforeTransaction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Event)
	}{
		{name: "agent mismatch", mutate: func(event *Event) { event.Binding.AgentID = "other-agent" }},
		{name: "empty", mutate: func(event *Event) { event.Binding.Provider = "" }},
		{name: "oversized", mutate: func(event *Event) { event.Binding.ProviderSessionID = strings.Repeat("x", bindingTextLimit+1) }},
		{name: "invalid utf8", mutate: func(event *Event) { event.Binding.IncarnationID = string([]byte{0xff}) }},
		{name: "nul", mutate: func(event *Event) { event.Binding.Provider = "codex\x00" }},
		{name: "control", mutate: func(event *Event) { event.Binding.Provider = "code\nex" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openWriterDB(t)
			identityReader := &testIdentity{snapshot: writerSnapshot()}
			appender := &testAppender{}
			verifier := &testBindingVerifier{}
			writer, err := New(testOwner{db: db}, identityReader, &testSource{state: writerState()}, appender)
			if err != nil {
				t.Fatal(err)
			}
			event := writerEvent()
			event.Binding = Binding{AgentID: "agent-1", IncarnationID: "inc-1", IncarnationGeneration: 1, Provider: "codex", ProviderSessionID: "session-1", LaunchSequence: 1}
			tc.mutate(&event)
			if _, err := writer.AppendBound(t.Context(), event, verifier); !errors.Is(err, ErrInvalid) {
				t.Fatalf("AppendBound() error = %v, want ErrInvalid", err)
			}
			if verifier.seen != nil || identityReader.seen != nil || appender.calls != 0 {
				t.Fatalf("malformed binding entered transaction: verifier=%v identity=%v appender=%d", verifier.seen != nil, identityReader.seen != nil, appender.calls)
			}
		})
	}
}

func TestWriterRejectsGapAndStaleBeforeAppend(t *testing.T) {
	db := openWriterDB(t)
	identityReader := &testIdentity{snapshot: writerSnapshot()}
	for _, tc := range []struct {
		name  string
		state eventstore.SourceState
		want  error
	}{
		{name: "gap", state: func() eventstore.SourceState { s := writerState(); s.Status = "gap"; return s }(), want: ErrGap},
		{name: "stale writer", state: func() eventstore.SourceState { s := writerState(); s.WriterEpoch = "old"; return s }(), want: ErrStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appender := &testAppender{}
			writer, err := New(testOwner{db: db}, identityReader, &testSource{state: tc.state}, appender)
			if err != nil {
				t.Fatal(err)
			}
			_, err = writer.Append(t.Context(), writerEvent())
			if !errors.Is(err, tc.want) || appender.calls != 0 {
				t.Fatalf("Append() = %v, calls=%d", err, appender.calls)
			}
		})
	}
}

func TestWriterFencesUnknownCommit(t *testing.T) {
	db := openWriterDB(t)
	identityReader := &testIdentity{snapshot: writerSnapshot()}
	appender := &testAppender{}
	writer, err := New(testOwner{db: db, unknown: true}, identityReader, &testSource{state: writerState()}, appender)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(t.Context(), writerEvent()); !errors.Is(err, ErrCommitUnknown) {
		t.Fatalf("first Append() error = %v", err)
	}
	if _, err := writer.Append(t.Context(), writerEvent()); !errors.Is(err, ErrFenced) || appender.calls != 1 {
		t.Fatalf("replay Append() = %v, calls=%d", err, appender.calls)
	}
}

func TestWriterRequiresExactlyOneOwnerCallback(t *testing.T) {
	for _, tc := range callbackCardinalityCases() {
		t.Run(tc.name, func(t *testing.T) {
			writer, appender, result, err := appendWithCallbackCardinality(t, tc)
			assertCallbackCardinalityResult(t, tc, appender, result, err)
			assertCallbackCardinalityCommitState(t, tc, writer, err)
		})
	}
}

type callbackCardinalityCase struct {
	name          string
	invocations   int
	commitOnError bool
	concurrent    bool
	unknown       bool
}

func callbackCardinalityCases() []callbackCardinalityCase {
	return []callbackCardinalityCase{
		{name: "zero", invocations: 0},
		{name: "duplicate rollback", invocations: 2},
		{name: "duplicate concurrent rollback", invocations: 2, concurrent: true},
		{name: "duplicate commit is unknown", invocations: 2, commitOnError: true},
		{name: "duplicate owner reports unknown", invocations: 2, commitOnError: true, unknown: true},
	}
}

func appendWithCallbackCardinality(t *testing.T, tc callbackCardinalityCase) (*Writer, *testAppender, eventstore.AppendResult, error) {
	t.Helper()
	db := openWriterDB(t)
	appender := &testAppender{}
	writer, err := New(callbackCardinalityOwner{db: db, invocations: tc.invocations, commitOnErr: tc.commitOnError, concurrent: tc.concurrent, unknown: tc.unknown}, &testIdentity{snapshot: writerSnapshot()}, &testSource{state: writerState()}, appender)
	if err != nil {
		t.Fatal(err)
	}
	result, err := writer.Append(t.Context(), writerEvent())
	return writer, appender, result, err
}

func assertCallbackCardinalityResult(t *testing.T, tc callbackCardinalityCase, appender *testAppender, result eventstore.AppendResult, err error) {
	t.Helper()
	if result != (eventstore.AppendResult{}) {
		t.Fatalf("Append() result = %#v, want zero result", result)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Append() error = %v, want ErrInvalid", err)
	}
	assertCallbackAppenderCalls(t, tc, appender)
	assertCallbackCardinalityError(t, tc, err)
}

func assertCallbackAppenderCalls(t *testing.T, tc callbackCardinalityCase, appender *testAppender) {
	t.Helper()
	if tc.invocations > 1 && appender.calls != 1 {
		t.Fatalf("duplicate callback appender calls = %d, want 1", appender.calls)
	}
	if tc.invocations == 0 && appender.calls != 0 {
		t.Fatalf("zero callback appender calls = %d, want 0", appender.calls)
	}
}

func assertCallbackCardinalityError(t *testing.T, tc callbackCardinalityCase, err error) {
	t.Helper()
	if tc.invocations > 1 && !errors.Is(err, errCallbackCardinality) {
		t.Fatalf("duplicate callback error = %v, want cardinality violation", err)
	}
}

func assertCallbackCardinalityCommitState(t *testing.T, tc callbackCardinalityCase, writer *Writer, err error) {
	t.Helper()
	if tc.invocations > 1 {
		assertDuplicateCallbackCommitUnknown(t, writer, err)
		return
	}
	if errors.Is(err, ErrCommitUnknown) {
		t.Fatalf("callback error = %v, did not expect commit unknown after owner rollback", err)
	}
}

func assertDuplicateCallbackCommitUnknown(t *testing.T, writer *Writer, err error) {
	t.Helper()
	if !errors.Is(err, ErrCommitUnknown) {
		t.Fatalf("duplicate callback error = %v, want commit unknown", err)
	}
	if _, replayErr := writer.Append(t.Context(), writerEvent()); !errors.Is(replayErr, ErrFenced) {
		t.Fatalf("replayed duplicate callback error = %v, want fenced", replayErr)
	}
}

func TestWriterRejectsMalformedContent(t *testing.T) {
	db := openWriterDB(t)
	appender := &testAppender{}
	writer, err := New(testOwner{db: db}, &testIdentity{snapshot: writerSnapshot()}, &testSource{state: writerState()}, appender)
	if err != nil {
		t.Fatal(err)
	}
	event := writerEvent()
	event.ContentRole, event.ContentRef, event.MediaType = "output", "", "text/plain"
	if _, err := writer.Append(t.Context(), event); !errors.Is(err, eventstore.ErrInvalidEvent) || appender.calls != 0 {
		t.Fatalf("malformed Append() = %v, calls=%d", err, appender.calls)
	}
}

func TestWriterConcurrentAppendIsRaceSafe(t *testing.T) {
	db := openWriterDB(t)
	appender := &testAppender{}
	writer, err := New(testOwner{db: db}, &testIdentity{snapshot: writerSnapshot()}, &testSource{state: writerState()}, appender)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		runtimesafe.SafeGo(t.Context(), nil, "runtimeeventwriter.test.concurrent-append", func(context.Context) {
			defer wg.Done()
			_, _ = writer.Append(context.Background(), writerEvent())
		})
	}
	wg.Wait()
	if appender.calls != 16 {
		t.Fatalf("append calls = %d, want 16", appender.calls)
	}
}

func openWriterDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "runtime-event-writer.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writerSnapshot() identity.ActiveSnapshot {
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return identity.ActiveSnapshot{
		Reservation: identity.Reservation{ReservationID: "res-1", AgentID: "agent-1", IdentityKind: "managed_agent", SourceKind: "first_launch", SourceFenceRef: "source-fence", ReservationGeneration: 1, State: "activated", SchemaVersion: 1},
		Incarnation: identity.Incarnation{IncarnationID: "inc-1", ReservationID: "res-1", AgentID: "agent-1", IdentityKind: "managed_agent", ExternalIdentityRef: "external-1", ExternalEpoch: "epoch-1", SourceFenceRef: "source-fence", IdentityDigest: digest, IncarnationGeneration: 1, SchemaVersion: 1},
		Binding:     identity.CurrentBinding{AgentID: "agent-1", BindingVersion: 1, ReservationID: "res-1", IncarnationID: "inc-1", IdentityKind: "managed_agent", ReservationGeneration: 1, IncarnationGeneration: 1, ExternalIdentityRef: "external-1", SourceFenceRef: "source-fence", BindingDigest: digest, State: "active", SchemaVersion: 1},
	}
}

func writerState() eventstore.SourceState {
	return eventstore.SourceState{SourceID: "task/agent-1", SourceEpoch: "inc-1", Lane: eventstore.LaneOutput, NextSequence: 1, MaterializedSequence: 0, DedupFloor: 0, Revision: 1, Status: "active", WriterEpoch: "writer-1"}
}

func writerEvent() Event {
	return Event{AgentID: "agent-1", Lane: eventstore.LaneOutput, EventID: "event-1", EventType: "task.output", Provenance: "runtime", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-03T00:00:00Z", WriterEpoch: "writer-1"}
}
