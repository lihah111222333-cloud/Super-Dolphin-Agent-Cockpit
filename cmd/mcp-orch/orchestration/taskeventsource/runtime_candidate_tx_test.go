package taskeventsource

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

type txCandidateOwner struct {
	db        *sql.DB
	passDB    bool
	unknown   bool
	callCount int
}

type txCandidateSkippingOwner struct{}

func (txCandidateSkippingOwner) WithTransaction(context.Context, func(storedb.DBTX) error) error {
	return nil
}

func (o *txCandidateOwner) WithTransaction(ctx context.Context, callback func(storedb.DBTX) error) error {
	o.callCount++
	if o.passDB {
		return callback(o.db)
	}
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	callbackErr := callback(tx)
	if callbackErr != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(callbackErr, rollbackErr)
		}
		return callbackErr
	}
	if o.unknown {
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("simulated commit outcome unknown")
	}
	return tx.Commit()
}

type txCandidateIdentityReader struct {
	snapshot identity.ActiveSnapshot
	txdb     storedb.DBTX
	calls    int
	err      error
}

func (r *txCandidateIdentityReader) GetActiveSnapshotTx(_ context.Context, txdb storedb.DBTX, _ string) (identity.ActiveSnapshot, error) {
	r.calls++
	r.txdb = txdb
	return r.snapshot, r.err
}

type txCandidateSourceReader struct {
	state eventstore.SourceState
	txdb  storedb.DBTX
	calls int
	err   error
}

func (r *txCandidateSourceReader) GetSourceStateTx(_ context.Context, txdb storedb.DBTX, _ string, _ string, _ eventstore.Lane) (eventstore.SourceState, error) {
	r.calls++
	r.txdb = txdb
	return r.state, r.err
}

type txCandidateAppender struct {
	frame eventstore.IngressFrame
	txdb  storedb.DBTX
	calls int
	write bool
	err   error
}

func (a *txCandidateAppender) AppendTx(ctx context.Context, txdb storedb.DBTX, frame eventstore.IngressFrame) (eventstore.AppendResult, error) {
	a.calls++
	a.txdb = txdb
	a.frame = frame
	if a.write {
		if _, err := txdb.ExecContext(ctx, `INSERT INTO tx_candidate_probe(value) VALUES ('append')`); err != nil {
			return eventstore.AppendResult{}, err
		}
	}
	if a.err != nil {
		return eventstore.AppendResult{}, a.err
	}
	return eventstore.AppendResult{Receipt: eventstore.IngressReceipt{IngressFrame: frame, Status: "materialized"}}, nil
}

func TestRuntimeEventTxCandidateUsesOneTransactionDBTX(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	owner := &txCandidateOwner{db: db}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	sourceReader := &txCandidateSourceReader{state: candidateFixtureStateReader().state}
	appender := &txCandidateAppender{write: true}
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)

	if _, err := candidate.Append(t.Context(), candidateRuntimeEvent()); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	assertTxCandidateCallCounts(t, owner, identityReader, sourceReader, appender)
	assertTxCandidateSharedDBTX(t, identityReader, sourceReader, appender)
	assertTxCandidateFrame(t, appender.frame)
	assertTxCandidateProbeCount(t, db, 1)
}

// assertTxCandidateCallCounts 确认一次 candidate 只执行一个 owner callback 和一个 append。
func assertTxCandidateCallCounts(t *testing.T, owner *txCandidateOwner, identityReader *txCandidateIdentityReader, sourceReader *txCandidateSourceReader, appender *txCandidateAppender) {
	t.Helper()
	if owner.callCount != 1 || identityReader.calls != 1 || sourceReader.calls != 1 || appender.calls != 1 {
		t.Fatalf("transaction call counts = owner:%d identity:%d source:%d appender:%d, want 1 each", owner.callCount, identityReader.calls, sourceReader.calls, appender.calls)
	}
}

// assertTxCandidateSharedDBTX 确认 identity、source 和 appender 收到同一个 tx-bound DBTX。
func assertTxCandidateSharedDBTX(t *testing.T, identityReader *txCandidateIdentityReader, sourceReader *txCandidateSourceReader, appender *txCandidateAppender) {
	t.Helper()
	if identityReader.txdb == nil || sourceReader.txdb == nil || appender.txdb == nil {
		t.Fatal("transaction-bound DBTX was not passed to every owner")
	}
	if identityReader.txdb != sourceReader.txdb || sourceReader.txdb != appender.txdb {
		t.Fatalf("owner DBTX values differ: identity=%T source=%T appender=%T", identityReader.txdb, sourceReader.txdb, appender.txdb)
	}
	if _, ok := appender.txdb.(*sql.Tx); !ok {
		t.Fatalf("appender DBTX type = %T, want *sql.Tx", appender.txdb)
	}
}

// assertTxCandidateFrame 确认 sequence 只复用了 durable source state 的 NextSequence。
func assertTxCandidateFrame(t *testing.T, frame eventstore.IngressFrame) {
	t.Helper()
	if frame.SourceSequence != 7 || frame.SourceID != "task/candidate-agent" || frame.SourceEpoch != "candidate-incarnation" {
		t.Fatalf("appender frame source = %#v", frame)
	}
}

func TestRuntimeEventTxCandidateFailureRollsBackEveryWrite(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	failure := errors.New("append failed")
	owner := &txCandidateOwner{db: db}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	sourceReader := &txCandidateSourceReader{state: candidateFixtureStateReader().state}
	appender := &txCandidateAppender{write: true, err: failure}
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)

	result, err := candidate.Append(t.Context(), candidateRuntimeEvent())
	if !errors.Is(err, failure) {
		t.Fatalf("Append() error = %v, want append failure", err)
	}
	if result != (eventstore.AppendResult{}) {
		t.Fatalf("failed append result = %#v, want zero result", result)
	}
	assertTxCandidateProbeCount(t, db, 0)
}

func TestRuntimeEventTxCandidateRejectsGapBeforeAppender(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	owner := &txCandidateOwner{db: db}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	state := candidateFixtureStateReader().state
	state.Status = "gap"
	sourceReader := &txCandidateSourceReader{state: state}
	appender := &txCandidateAppender{write: true}
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)

	result, err := candidate.Append(t.Context(), candidateRuntimeEvent())
	if !errors.Is(err, ErrRuntimeEventTxCandidateGap) || !errors.Is(err, eventstore.ErrEventGap) {
		t.Fatalf("Append() error = %v, want candidate/event gap", err)
	}
	if result != (eventstore.AppendResult{}) || appender.calls != 0 {
		t.Fatalf("gap result/appender calls = %#v/%d, want zero/0", result, appender.calls)
	}
	assertTxCandidateProbeCount(t, db, 0)
}

func TestRuntimeEventTxCandidateMapsAppenderGapAndRollsBack(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	owner := &txCandidateOwner{db: db}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	sourceReader := &txCandidateSourceReader{state: candidateFixtureStateReader().state}
	appender := &txCandidateAppender{write: true, err: eventstore.ErrEventGap}
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)

	_, err := candidate.Append(t.Context(), candidateRuntimeEvent())
	if !errors.Is(err, ErrRuntimeEventTxCandidateGap) || !errors.Is(err, eventstore.ErrEventGap) {
		t.Fatalf("Append() error = %v, want candidate/event gap", err)
	}
	assertTxCandidateProbeCount(t, db, 0)
}

func TestRuntimeEventTxCandidateFencesUnknownCommitAndRejectsReplay(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	owner := &txCandidateOwner{db: db, unknown: true}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	sourceReader := &txCandidateSourceReader{state: candidateFixtureStateReader().state}
	appender := &txCandidateAppender{write: true}
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)

	_, err := candidate.Append(t.Context(), candidateRuntimeEvent())
	if !errors.Is(err, ErrRuntimeEventTxCandidateCommitUnknown) {
		t.Fatalf("first Append() error = %v, want commit unknown", err)
	}
	if owner.callCount != 1 || appender.calls != 1 {
		t.Fatalf("unknown commit call counts = owner:%d appender:%d, want 1/1", owner.callCount, appender.calls)
	}
	assertTxCandidateProbeCount(t, db, 1)

	_, err = candidate.Append(t.Context(), candidateRuntimeEvent())
	if !errors.Is(err, ErrRuntimeEventTxCandidateFenced) || !errors.Is(err, ErrRuntimeEventTxCandidateCommitUnknown) {
		t.Fatalf("replayed Append() error = %v, want fenced/unknown", err)
	}
	if owner.callCount != 1 || appender.calls != 1 {
		t.Fatalf("replayed call counts = owner:%d appender:%d, want unchanged 1/1", owner.callCount, appender.calls)
	}
}

func TestRuntimeEventTxCandidateRejectsNilAndNonTransactionDependencies(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	owner := &txCandidateOwner{db: db}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	sourceReader := &txCandidateSourceReader{state: candidateFixtureStateReader().state}
	appender := &txCandidateAppender{}
	assertTxCandidateRejectsNilDependencies(t, owner, identityReader, sourceReader, appender)
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)
	if _, err := candidate.Append(nil, candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventTxCandidateInvalid) {
		t.Fatalf("Append(nil context) error = %v, want invalid", err)
	}
	if owner.callCount != 0 {
		t.Fatalf("nil context owner calls = %d, want 0", owner.callCount)
	}
	if skipped, err := NewRuntimeEventTxCandidate(txCandidateSkippingOwner{}, identityReader, sourceReader, appender); err != nil {
		t.Fatalf("NewRuntimeEventTxCandidate(skipping owner) error = %v", err)
	} else if _, err := skipped.Append(t.Context(), candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventTxCandidateInvalid) {
		t.Fatalf("Append(skipping owner) error = %v, want invalid", err)
	}
	assertTxCandidateRejectsNonTransactionCallback(t, candidate, owner, appender)
}

// assertTxCandidateRejectsNilDependencies 锁定 constructor 对 nil 和 typed-nil owner 依赖的 fail-fast 行为。
func assertTxCandidateRejectsNilDependencies(t *testing.T, owner *txCandidateOwner, identityReader *txCandidateIdentityReader, sourceReader *txCandidateSourceReader, appender *txCandidateAppender) {
	t.Helper()
	if _, err := NewRuntimeEventTxCandidate(nil, identityReader, sourceReader, appender); err == nil {
		t.Fatal("nil transaction owner unexpectedly accepted")
	}
	var nilOwner RuntimeEventTransactionOwnerFunc
	if _, err := NewRuntimeEventTxCandidate(nilOwner, identityReader, sourceReader, appender); err == nil {
		t.Fatal("nil function transaction owner unexpectedly accepted")
	}
	var typedNilOwner *txCandidateOwner
	if _, err := NewRuntimeEventTxCandidate(typedNilOwner, identityReader, sourceReader, appender); err == nil {
		t.Fatal("typed-nil transaction owner unexpectedly accepted")
	}
	var typedNilIdentity *txCandidateIdentityReader
	if _, err := NewRuntimeEventTxCandidate(owner, typedNilIdentity, sourceReader, appender); err == nil {
		t.Fatal("typed-nil identity reader unexpectedly accepted")
	}
	var typedNilSource *txCandidateSourceReader
	if _, err := NewRuntimeEventTxCandidate(owner, identityReader, typedNilSource, appender); err == nil {
		t.Fatal("typed-nil source reader unexpectedly accepted")
	}
	var typedNilAppender *txCandidateAppender
	if _, err := NewRuntimeEventTxCandidate(owner, identityReader, sourceReader, typedNilAppender); err == nil {
		t.Fatal("typed-nil appender unexpectedly accepted")
	}
}

// assertTxCandidateRejectsNonTransactionCallback 锁定 owner 不得把裸 *sql.DB 传入 callback。
func assertTxCandidateRejectsNonTransactionCallback(t *testing.T, candidate *RuntimeEventTxCandidate, owner *txCandidateOwner, appender *txCandidateAppender) {
	t.Helper()
	owner.passDB = true
	if _, err := candidate.Append(t.Context(), candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventTxCandidateInvalid) {
		t.Fatalf("Append(*sql.DB callback) error = %v, want invalid", err)
	}
	if appender.calls != 0 {
		t.Fatalf("non-transaction callback appender calls = %d, want 0", appender.calls)
	}
}

func TestRuntimeEventTxCandidateMissingStateAndMalformedEventAreZeroWrite(t *testing.T) {
	db := openTxCandidateDB(t)
	createTxCandidateProbe(t, db)
	owner := &txCandidateOwner{db: db}
	identityReader := &txCandidateIdentityReader{snapshot: candidateFixtureReader().snapshot}
	sourceReader := &txCandidateSourceReader{state: candidateFixtureStateReader().state, err: errors.New("source state missing")}
	appender := &txCandidateAppender{write: true}
	candidate := mustRuntimeEventTxCandidate(t, owner, identityReader, sourceReader, appender)
	if _, err := candidate.Append(t.Context(), candidateRuntimeEvent()); !errors.Is(err, ErrRuntimeEventCandidateUnavailable) {
		t.Fatalf("missing source state error = %v, want unavailable", err)
	}
	if appender.calls != 0 {
		t.Fatalf("missing source state appender calls = %d, want 0", appender.calls)
	}
	assertTxCandidateProbeCount(t, db, 0)

	sourceReader.err = nil
	event := candidateRuntimeEvent()
	event.EventType = ""
	if _, err := candidate.Append(t.Context(), event); !errors.Is(err, eventstore.ErrInvalidEvent) {
		t.Fatalf("malformed event error = %v, want invalid event", err)
	}
	if appender.calls != 0 {
		t.Fatalf("malformed event appender calls = %d, want 0", appender.calls)
	}
	assertTxCandidateProbeCount(t, db, 0)
}

func mustRuntimeEventTxCandidate(t *testing.T, owner RuntimeEventTransactionOwner, identities RuntimeEventTransactionIdentityReader, states RuntimeEventTransactionSourceReader, appender RuntimeEventTransactionAppender) *RuntimeEventTxCandidate {
	t.Helper()
	candidate, err := NewRuntimeEventTxCandidate(owner, identities, states, appender)
	if err != nil {
		t.Fatalf("NewRuntimeEventTxCandidate() error = %v", err)
	}
	return candidate
}

func openTxCandidateDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "runtime-event-candidate.sqlite")})
	if err != nil {
		t.Fatalf("open transaction candidate database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close transaction candidate database: %v", err)
		}
	})
	return db
}

func createTxCandidateProbe(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE tx_candidate_probe (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create transaction candidate probe: %v", err)
	}
}

func assertTxCandidateProbeCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM tx_candidate_probe`).Scan(&got); err != nil {
		t.Fatalf("count transaction candidate probe rows: %v", err)
	}
	if got != want {
		t.Fatalf("transaction candidate probe rows = %d, want %d", got, want)
	}
}

var _ RuntimeEventTransactionOwner = (*txCandidateOwner)(nil)
var _ RuntimeEventTransactionIdentityReader = (*txCandidateIdentityReader)(nil)
var _ RuntimeEventTransactionSourceReader = (*txCandidateSourceReader)(nil)
var _ RuntimeEventTransactionAppender = (*txCandidateAppender)(nil)
