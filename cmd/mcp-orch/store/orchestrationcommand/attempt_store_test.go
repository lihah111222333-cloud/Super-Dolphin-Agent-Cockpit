package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type allowAttemptAdmission struct{}

func (allowAttemptAdmission) VerifyAttemptAdmission(context.Context, AttemptRequest, Record) error {
	return nil
}

func (allowAttemptAdmission) VerifyAttemptAdmissionTx(context.Context, storedb.DBTX, AttemptRequest, Record) error {
	return nil
}

type legacyAttemptAdmission struct{}

func (legacyAttemptAdmission) VerifyAttemptAdmission(context.Context, AttemptRequest, Record) error {
	return nil
}

type transactionProbeIntentReader struct{ record Record }

func (r transactionProbeIntentReader) Get(context.Context, string) (Record, error) {
	return Record{}, errors.New("legacy intent reader must not be called")
}

func (r transactionProbeIntentReader) GetTx(ctx context.Context, dbtx storedb.DBTX, _ string) (Record, error) {
	if _, err := dbtx.ExecContext(ctx, "CREATE TEMP TABLE attempt_tx_probe (value INTEGER NOT NULL)"); err != nil {
		return Record{}, err
	}
	if _, err := dbtx.ExecContext(ctx, "INSERT INTO attempt_tx_probe(value) VALUES (1)"); err != nil {
		return Record{}, err
	}
	return r.record, nil
}

type transactionProbeAdmission struct{}

func (transactionProbeAdmission) VerifyAttemptAdmission(context.Context, AttemptRequest, Record) error {
	return errors.New("legacy admission verifier must not be called")
}

func (transactionProbeAdmission) VerifyAttemptAdmissionTx(ctx context.Context, dbtx storedb.DBTX, _ AttemptRequest, _ Record) error {
	var value int
	if err := dbtx.QueryRowContext(ctx, "SELECT value FROM attempt_tx_probe").Scan(&value); err != nil {
		return err
	}
	if value != 1 {
		return errors.New("transaction probe marker mismatch")
	}
	return nil
}

func TestCommandAttemptStoreRequiresAdmissionVerifier(t *testing.T) {
	db := openCommandIntentTestDB(t)
	intents, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := NewAttemptStore(db, intents, nil); err == nil {
		t.Fatal("NewAttemptStore() accepted nil admission verifier")
	}
}

func TestCommandAttemptStoreRejectsNonTransactionalAdmissionVerifier(t *testing.T) {
	db := openCommandIntentTestDB(t)
	intents, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := NewAttemptStore(db, intents, legacyAttemptAdmission{}); !errors.Is(err, ErrAttemptTransaction) {
		t.Fatalf("NewAttemptStore(legacy admission) error = %v, want ErrAttemptTransaction", err)
	}
}

func TestCommandAttemptReserveBindsIntentAdmissionAndInsertToOneTransaction(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-transaction-boundary")
	record, err := intents.Get(context.Background(), intent.CommandID)
	if err != nil {
		t.Fatalf("Get(intent) error = %v", err)
	}
	store, err := NewAttemptStore(db, transactionProbeIntentReader{record: record}, transactionProbeAdmission{})
	if err != nil {
		t.Fatalf("NewAttemptStore() error = %v", err)
	}
	gateway, err := NewCommandAttemptGateway(store)
	if err != nil {
		t.Fatalf("NewCommandAttemptGateway() error = %v", err)
	}
	if _, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
}

func attemptIntentFixture(t *testing.T, commandID string) (Intent, *Store, *sql.DB) {
	t.Helper()
	db := openCommandIntentTestDB(t)
	intents, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	intent := commandIntentFixture(commandID)
	if _, err := intents.Reserve(context.Background(), intent); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	return intent, intents, db
}

func attemptRequestFromIntent(intent Intent) AttemptRequest {
	return AttemptRequest{CommandID: intent.CommandID, WakeupID: "wakeup-1", WakeupFence: "fence-1", IdempotencyNamespace: intent.IdempotencyNamespace, IdempotencyKeyDigest: intent.IdempotencyKeyDigest, RequestDigest: intent.RequestDigest}
}

func TestCommandAttemptRecordBeforeEffectReplayNeverReturnsSecondPermit(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-replay")
	store, gateway := mustNewAttemptGateway(t, db, intents, "replay")
	var err error
	first, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue(first) error = %v", err)
	}
	if first.AttemptID == "" || first.DispatchReceipt == "" || first.Permit == "" || first.Nonce == "" {
		t.Fatal("first permit is missing durable attempt material")
	}
	if _, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent)); !errors.Is(err, ErrAttemptUnresolved) {
		t.Fatalf("Issue(replay) error = %v, want ErrAttemptUnresolved", err)
	}
	attempt, err := store.GetByCommand(context.Background(), intent.CommandID)
	if err != nil {
		t.Fatalf("GetByCommand() error = %v", err)
	}
	if attempt.Phase != AttemptEffectPending || attempt.AttemptNumber != 1 {
		t.Fatalf("durable attempt = %#v, want one pending attempt", attempt)
	}
}

func TestCommandAttemptRecordBeforeEffectReplayFailsClosed(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-record-replay")
	store, _ := mustNewAttemptGateway(t, db, intents, "record-replay")
	req := attemptRequestFromIntent(intent)
	first, err := store.RecordBeforeEffect(context.Background(), req)
	if err != nil || first.Permit == "" {
		t.Fatalf("RecordBeforeEffect(first) = %#v, %v", first, err)
	}
	replay, err := store.RecordBeforeEffect(context.Background(), req)
	if !errors.Is(err, ErrAttemptUnresolved) {
		t.Fatalf("RecordBeforeEffect(replay) error = %v, want ErrAttemptUnresolved", err)
	}
	if replay != (Attempt{}) {
		t.Fatalf("RecordBeforeEffect(replay) returned attempt/permit: %#v", replay)
	}
}

func TestCommandAttemptUnknownIsStickyUntilExplicitReconcile(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-unknown")
	store, gateway := mustNewAttemptGateway(t, db, intents, "unknown")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	unknown, err := store.MarkProviderResult(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultUnknown}})
	if err != nil {
		t.Fatalf("MarkProviderResult(unknown) error = %v", err)
	}
	assertAttemptUnknown(t, unknown, "unknown result")
	if _, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent)); !errors.Is(err, ErrAttemptUnresolved) {
		t.Fatalf("Issue after unknown error = %v, want unresolved", err)
	}
	if _, err := store.MarkProviderResult(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultUnknown}}); !errors.Is(err, ErrAttemptUnknownSticky) {
		t.Fatalf("second unknown result error = %v, want sticky error", err)
	}
	resolved, err := store.ReconcileUnknown(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultSubmission, ReceiptID: "provider-receipt-1"}})
	if err != nil {
		t.Fatalf("ReconcileUnknown() error = %v", err)
	}
	assertAttemptResolved(t, resolved, AttemptProviderAccepted, "provider-receipt-1", "resolved attempt")
	replay, err := store.ReconcileUnknown(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultSubmission, ReceiptID: "provider-receipt-1"}})
	if err == nil || !errors.Is(err, ErrAttemptUnresolved) {
		t.Fatalf("second reconcile error = %v, want unresolved", err)
	}
	_ = replay
}

func TestCommandAttemptPendingProviderResultIsRejectedBeforeMutation(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-pending-result")
	store, gateway := mustNewAttemptGateway(t, db, intents, "pending-result")
	var err error
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := store.MarkProviderResult(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultPending}}); !errors.Is(err, ErrAttemptOutcome) {
		t.Fatalf("pending provider result error = %v, want ErrAttemptOutcome", err)
	}
	attempt, err := store.GetByCommand(context.Background(), intent.CommandID)
	if err != nil {
		t.Fatalf("GetByCommand() error = %v", err)
	}
	if attempt.Phase != AttemptEffectPending || attempt.Result.Kind != ProviderResultPending {
		t.Fatalf("attempt after pending result = %#v, want effect_pending/pending", attempt)
	}
}

func TestCommandAttemptStaleFenceAndDigestFailBeforeEffect(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-stale")
	store, gateway := mustNewAttemptGateway(t, db, intents, "stale")
	req := attemptRequestFromIntent(intent)
	if _, err := gateway.Issue(context.Background(), req); err != nil {
		t.Fatalf("Issue(valid) error = %v", err)
	}
	req.WakeupFence = "old-fence"
	if _, err := gateway.Issue(context.Background(), req); err == nil {
		t.Fatal("stale fence unexpectedly issued a permit")
	}
	req = attemptRequestFromIntent(intent)
	req.RequestDigest = strings.Repeat("c", 64)
	if _, err := gateway.Issue(context.Background(), req); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("different request digest error = %v, want ErrAttemptConflict", err)
	}
	if attempt, err := store.GetByCommand(context.Background(), intent.CommandID); err != nil || attempt.AttemptNumber != 1 {
		t.Fatalf("failed pre-effect validation altered durable attempt: %#v, %v", attempt, err)
	}
}

func TestCommandAttemptConcurrentIssueHasOneDurableWinner(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-race")
	store, err := NewAttemptStore(db, intents, allowAttemptAdmission{})
	if err != nil {
		t.Fatalf("NewAttemptStore() error = %v", err)
	}
	gateway, err := NewCommandAttemptGateway(store)
	if err != nil {
		t.Fatalf("NewCommandAttemptGateway() error = %v", err)
	}
	const workers = 12
	results := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		runtimesafe.SafeGo(context.Background(), nil, "orchestrationcommand.test.attempt-issue-concurrent", func(context.Context) {
			defer group.Done()
			_, issueErr := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
			results <- issueErr
		})
	}
	group.Wait()
	close(results)
	winners := 0
	for issueErr := range results {
		if issueErr == nil {
			winners++
			continue
		}
		if !errors.Is(issueErr, ErrAttemptUnresolved) {
			t.Fatalf("concurrent Issue() error = %v, want unresolved loser", issueErr)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent Issue() winners = %d, want 1", winners)
	}
}
