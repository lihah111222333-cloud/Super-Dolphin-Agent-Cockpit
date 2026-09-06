package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type allowSubmissionAuthority struct{ err error }

type unknownDBTX struct{}

func (unknownDBTX) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}
func (unknownDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error)       { return nil, nil }
func (unknownDBTX) QueryContext(context.Context, string, ...any) (*sql.Rows, error) { return nil, nil }
func (unknownDBTX) QueryRowContext(context.Context, string, ...any) *sql.Row        { return nil }

func (v allowSubmissionAuthority) Verify(context.Context, storedb.DBTX, DurableSubmission, SubmissionResolution) error {
	return v.err
}

func durableSubmissionFixture(id string) DurableSubmissionRequest {
	return DurableSubmissionRequest{CommandID: id, CommandVersion: 1, SubjectSelector: "agent:one", CallerPrincipalRef: "principal/default", ScopeRef: "scope/default", SourceFenceRef: "source-fence-v1", TargetFenceRef: "target-fence-v1", IdempotencyNamespace: "send:one", IdempotencyKeyDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PendingCursor: "cursor:1", PendingCursorDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
}

func durableCommandFence(req DurableSubmissionRequest, prefix string) CommandFence {
	return CommandFence{Ref: prefix + ":fence", Digest: "9999999999999999999999999999999999999999999999999999999999999999", Cursor: prefix + ":cursor", CommandID: req.CommandID, CommandVersion: req.CommandVersion, SubjectSelector: req.SubjectSelector}
}
func newDurableSubmissionFixture(t *testing.T, id string) (*DurableSubmissionStore, DurableSubmissionRequest, *sql.DB) {
	t.Helper()
	db := openCommandIntentTestDB(t)
	if err := EnsureCandidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	intent := commandIntentFixture(id)
	store, err := NewDurableSubmissionStore(db, allowSubmissionAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeIntentForCandidate(db, intent); err != nil {
		t.Fatal(err)
	}
	req := durableSubmissionFixture(id)
	req.IdempotencyNamespace = intent.IdempotencyNamespace
	req.IdempotencyKeyDigest = intent.IdempotencyKeyDigest
	req.RequestDigest = intent.RequestDigest
	return store, req, db
}
func storeIntentForCandidate(db *sql.DB, intent Intent) (Record, error) {
	s, err := NewStore(db)
	if err != nil {
		return Record{}, err
	}
	return s.Reserve(context.Background(), intent)
}

// assertDurableBinding 校验投影保留 intent 的权限与 fence 绑定。
func assertDurableBinding(t *testing.T, request DurableSubmissionRequest, label string) {
	t.Helper()
	if request.CallerPrincipalRef != "principal/default" {
		t.Fatalf("%s caller binding = %q", label, request.CallerPrincipalRef)
	}
	if request.ScopeRef != "scope/default" {
		t.Fatalf("%s scope binding = %q", label, request.ScopeRef)
	}
	if request.SourceFenceRef != "source-fence-v1" {
		t.Fatalf("%s source fence = %q", label, request.SourceFenceRef)
	}
	if request.TargetFenceRef != "target-fence-v1" {
		t.Fatalf("%s target fence = %q", label, request.TargetFenceRef)
	}
}

// assertProjectionIntentMatch 校验事务内读取的 submission 与 intent 一致。
func assertProjectionIntentMatch(t *testing.T, submission DurableSubmission, intent Intent) {
	t.Helper()
	if submission.Request.CallerPrincipalRef != intent.CallerPrincipalRef {
		t.Fatalf("transaction projection caller got=%q intent=%q", submission.Request.CallerPrincipalRef, intent.CallerPrincipalRef)
	}
	if submission.Request.SourceFenceRef != intent.SourceFenceRef {
		t.Fatalf("transaction projection source fence got=%q intent=%q", submission.Request.SourceFenceRef, intent.SourceFenceRef)
	}
}

// assertDurableResolveResponses 校验并发 resolve 的成功与冲突响应。
func assertDurableResolveResponses(t *testing.T, errs <-chan error) {
	t.Helper()
	responses := 0
	var responseErrors []error
	for err := range errs {
		if err == nil {
			responses++
			continue
		}
		if errors.Is(err, ErrDurableSubmissionConflict) {
			responseErrors = append(responseErrors, err)
			continue
		}
		t.Fatalf("unexpected concurrent resolve error = %v", err)
	}
	if responses != 2 {
		t.Fatalf("responses=%d errors=%v", responses, responseErrors)
	}
}

// newDurableSubmissionRaceStores 打开共享数据库并构造两个独立连接的 store。
func newDurableSubmissionRaceStores(t *testing.T, path string) (*DurableSubmissionStore, *DurableSubmissionStore, Intent) {
	t.Helper()
	db1 := openCommandIntentTestDBAtPath(t, path)
	db2 := openCommandIntentTestDBAtPath(t, path)
	if err := EnsureCandidateSchema(context.Background(), db1); err != nil {
		t.Fatal(err)
	}
	intent := commandIntentFixture("submission-durable-race")
	if _, err := storeIntentForCandidate(db1, intent); err != nil {
		t.Fatal(err)
	}
	s1, err := NewDurableSubmissionStore(db1, allowSubmissionAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewDurableSubmissionStore(db2, allowSubmissionAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	return s1, s2, intent
}

func TestDurableSubmissionPrepareResolveAndStickyReplay(t *testing.T) {
	store, req, _ := newDurableSubmissionFixture(t, "submission-durable-sticky")
	first, err := store.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Resolution != AcceptPending || first.Revision != 1 {
		t.Fatalf("first=%#v", first)
	}
	r := SubmissionResolution{Request: first.Request, Resolution: AcceptAccepted, AcceptedCommandFence: durableCommandFence(first.Request, "accept"), AcceptReceiptRef: "accept:1", AcceptReceiptDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", RecoveryReceiptRef: "recovery:1", RecoveryReceiptDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ExpectedRevision: 1}
	accepted, err := store.Resolve(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.Resolve(context.Background(), r)
	if err != nil || replay != accepted {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	r.AcceptReceiptRef = "accept:drift"
	if _, err := store.Resolve(context.Background(), r); !errors.Is(err, ErrDurableSubmissionConflict) {
		t.Fatalf("drift err=%v", err)
	}
}

func TestDurableSubmissionReopenAndConcurrentResolveOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "submission.sqlite")
	s1, s2, intent := newDurableSubmissionRaceStores(t, path)
	req := durableSubmissionFixture(intent.CommandID)
	req.IdempotencyNamespace = intent.IdempotencyNamespace
	req.IdempotencyKeyDigest = intent.IdempotencyKeyDigest
	req.RequestDigest = intent.RequestDigest
	first, err := s1.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	res := SubmissionResolution{Request: first.Request, Resolution: AcceptAborted, RejectedCommandFence: durableCommandFence(first.Request, "reject"), RejectReceiptRef: "reject:1", RejectReceiptDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", RecoveryReceiptRef: "recovery:1", RecoveryReceiptDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ExpectedRevision: 1}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, s := range []*DurableSubmissionStore{s1, s2} {
		x := s
		wg.Add(1)
		runtimesafe.SafeGo(context.Background(), nil, "orchestrationcommand.test.durable-submission-concurrent-resolve", func(context.Context) {
			defer wg.Done()
			_, e := x.Resolve(context.Background(), res)
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	assertDurableResolveResponses(t, errs)
	got, err := s2.Get(context.Background(), DurableSubmissionKey{CommandID: req.CommandID, SubmissionID: first.Request.SubmissionID})
	if err != nil {
		t.Fatalf("got error = %v", err)
	}
	if got.Resolution != AcceptAborted {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestDurableSubmissionPrepareTxUsesCallerTransaction(t *testing.T) {
	db := openCommandIntentTestDB(t)
	db.SetMaxOpenConns(1)
	if err := EnsureCandidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	intent := commandIntentFixture("submission-durable-tx-owner")
	if _, err := storeIntentForCandidate(db, intent); err != nil {
		t.Fatal(err)
	}
	store, err := NewDurableSubmissionStore(db, allowSubmissionAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	req := durableSubmissionFixture(intent.CommandID)
	req.IdempotencyNamespace, req.IdempotencyKeyDigest, req.RequestDigest = intent.IdempotencyNamespace, intent.IdempotencyKeyDigest, intent.RequestDigest
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareTx(context.Background(), tx, req)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if prepared.Resolution != AcceptPending {
		t.Fatalf("transaction-bound result = %#v, want pending", prepared)
	}
}

func TestDurableSubmissionProjectionBindsIntentAndReadsInTransaction(t *testing.T) {
	store, req, db := newDurableSubmissionFixture(t, "submission-projection")
	prepared, err := store.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	assertDurableBinding(t, prepared.Request, "submission binding")
	acceptedFence := durableCommandFence(prepared.Request, "projection-accept")
	accepted, err := store.Resolve(context.Background(), SubmissionResolution{Request: prepared.Request, Resolution: AcceptAccepted, AcceptedCommandFence: acceptedFence, AcceptReceiptRef: "accept:projection", AcceptReceiptDigest: strings.Repeat("d", 64), RecoveryReceiptRef: "recovery:projection", RecoveryReceiptDigest: strings.Repeat("e", 64), ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ResolutionKind != ResolutionAccepted {
		t.Fatalf("accepted projection resolution = %q", accepted.ResolutionKind)
	}
	if accepted.AcceptedCommandFence != acceptedFence {
		t.Fatalf("accepted projection fence = %#v", accepted.AcceptedCommandFence)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := store.GetTx(context.Background(), tx, DurableSubmissionKey{CommandID: req.CommandID, SubmissionID: prepared.Request.SubmissionID})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.GetIntentTx(context.Background(), tx, req.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	assertProjectionIntentMatch(t, got, intent.Intent)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	bad := prepared.Request
	bad.ScopeRef = "scope/forged"
	if _, err := store.Prepare(context.Background(), bad); !errors.Is(err, ErrDurableSubmissionConflict) {
		t.Fatalf("forged scope error = %v", err)
	}
}

func TestDurableSubmissionPrepareRejectsMissingBinding(t *testing.T) {
	store, req, _ := newDurableSubmissionFixture(t, "submission-missing-binding")
	req.CallerPrincipalRef = ""
	if _, err := store.Prepare(context.Background(), req); !errors.Is(err, ErrDurableSubmissionConflict) {
		t.Fatalf("missing caller binding error = %v", err)
	}
}

func TestTransactionBoundReadsRejectBareAndTypedNilDBTX(t *testing.T) {
	store, req, db := newDurableSubmissionFixture(t, "submission-tx-guard")
	key := DurableSubmissionKey{CommandID: req.CommandID, SubmissionID: "missing"}
	if _, err := store.GetTx(context.Background(), db, key); err == nil {
		t.Fatal("GetTx accepted bare *sql.DB")
	}
	var nilTx *sql.Tx
	var dbtx storedb.DBTX = nilTx
	if _, err := store.GetTx(context.Background(), dbtx, key); err == nil {
		t.Fatal("GetTx accepted typed-nil *sql.Tx")
	}
	if _, err := store.GetTx(context.Background(), unknownDBTX{}, key); err == nil {
		t.Fatal("GetTx accepted unknown DBTX implementation")
	}
	if _, err := store.GetIntentTx(context.Background(), db, req.CommandID); err == nil {
		t.Fatal("GetIntentTx accepted bare *sql.DB")
	}
	intentStore, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := intentStore.GetTx(context.Background(), db, req.CommandID); err == nil {
		t.Fatal("Store.GetTx accepted bare *sql.DB")
	}
}
