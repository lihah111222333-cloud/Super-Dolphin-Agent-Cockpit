package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestCommandAttemptProviderResultRejectsMalformedMaterialBeforeCASConcurrently(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-result-material-guard")
	store, err := NewAttemptStore(db, intents, allowAttemptAdmission{})
	if err != nil {
		t.Fatalf("NewAttemptStore() error = %v", err)
	}
	gateway, err := NewCommandAttemptGateway(store)
	if err != nil {
		t.Fatalf("NewCommandAttemptGateway() error = %v", err)
	}
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	malformed := []struct {
		name   string
		mutate func(*DispatchPermit)
	}{
		{name: "dispatch_receipt_overlong", mutate: func(p *DispatchPermit) { p.DispatchReceipt = strings.Repeat("r", 257) }},
		{name: "permit_invalid_utf8", mutate: func(p *DispatchPermit) { p.Permit = string([]byte{0xff}) }},
		{name: "nonce_leading_space", mutate: func(p *DispatchPermit) { p.Nonce = " " + p.Nonce }},
		{name: "dispatch_receipt_nul", mutate: func(p *DispatchPermit) { p.DispatchReceipt += "\x00suffix" }},
	}

	const workers = 8
	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			assertMalformedMaterialGuard(t, store, intent.CommandID, permit, tc.mutate, workers)
		})
	}
}

func assertMalformedMaterialGuard(t *testing.T, store *AttemptStore, commandID string, permit DispatchPermit, mutate func(*DispatchPermit), workers int) {
	t.Helper()
	start := make(chan struct{})
	errorsSeen := make(chan error, workers*2)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		runtimesafe.SafeGo(context.Background(), nil, "orchestrationcommand.test.attempt-result-material-guard", func(context.Context) {
			defer group.Done()
			bad := permit
			mutate(&bad)
			<-start
			errorsSeen <- malformedMarkError(store, bad)
			errorsSeen <- malformedReconcileError(store, bad)
		})
	}
	close(start)
	group.Wait()
	close(errorsSeen)
	for validationErr := range errorsSeen {
		if !errors.Is(validationErr, ErrAttemptPermit) {
			t.Fatalf("malformed provider result error = %v, want ErrAttemptPermit", validationErr)
		}
	}

	current, readErr := store.GetByCommand(context.Background(), commandID)
	if readErr != nil {
		t.Fatalf("GetByCommand() error = %v", readErr)
	}
	if current.Phase != AttemptEffectPending || current.Result.Kind != ProviderResultPending {
		t.Fatalf("malformed provider result mutated attempt = %#v", current)
	}
}

func malformedMarkError(store *AttemptStore, permit DispatchPermit) error {
	_, err := store.MarkProviderResult(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultUnknown}})
	return err
}

func malformedReconcileError(store *AttemptStore, permit DispatchPermit) error {
	_, err := store.ReconcileUnknown(context.Background(), ProviderResultRequest{DispatchPermit: permit, Result: ProviderResult{Kind: ProviderResultFailure, FailureCode: "provider-failed"}})
	return err
}

// assertAttemptUnknown 校验消费后持久化的 unknown 状态。
func assertAttemptUnknown(t *testing.T, attempt Attempt, label string) {
	t.Helper()
	if attempt.Phase != AttemptOutcomeUnknown {
		t.Fatalf("%s phase = %q, want outcome_unknown", label, attempt.Phase)
	}
	if attempt.Result.Kind != ProviderResultUnknown {
		t.Fatalf("%s result = %#v, want unknown", label, attempt.Result)
	}
}

// assertAttemptMaterial 校验 provider 所需材料在状态转换中保持不变。
func assertAttemptMaterial(t *testing.T, attempt Attempt, permit DispatchPermit, label string) {
	t.Helper()
	if attempt.Permit != permit.Permit {
		t.Fatalf("%s permit changed: %#v", label, attempt)
	}
	if attempt.Nonce != permit.Nonce {
		t.Fatalf("%s nonce changed: %#v", label, attempt)
	}
	if attempt.DispatchReceipt != permit.DispatchReceipt {
		t.Fatalf("%s dispatch receipt changed: %#v", label, attempt)
	}
}

// assertAttemptResolved 校验 unknown 显式 reconcile 后的终态。
func assertAttemptResolved(t *testing.T, attempt Attempt, phase AttemptPhase, receipt string, label string) {
	t.Helper()
	if attempt.Phase != phase {
		t.Fatalf("%s phase = %q, want %q", label, attempt.Phase, phase)
	}
	if attempt.Result.ReceiptID != receipt {
		t.Fatalf("%s receipt = %q, want %q", label, attempt.Result.ReceiptID, receipt)
	}
}

func TestCommandAttemptPermitConsumeIsOneShotAndReconcileOnly(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-consume-one-shot")
	store, gateway := mustNewAttemptGateway(t, db, intents, "one-shot")
	var err error
	ctx := context.Background()
	permit, err := gateway.Issue(ctx, attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	consumed, err := gateway.ConsumePermit(ctx, permit)
	if err != nil {
		t.Fatalf("ConsumePermit(first) error = %v", err)
	}
	assertAttemptUnknown(t, consumed, "consumed attempt")
	assertAttemptMaterial(t, consumed, permit, "consumed attempt")

	if _, err := gateway.Issue(ctx, attemptRequestFromIntent(intent)); !errors.Is(err, ErrAttemptUnresolved) {
		t.Fatalf("Issue(after consume) error = %v, want ErrAttemptUnresolved", err)
	}
	if _, err := gateway.Consume(ctx, permit); !errors.Is(err, ErrAttemptConsumed) || !errors.Is(err, ErrAttemptUnknownSticky) {
		t.Fatalf("Consume(replay) error = %v, want consumed + unknown-sticky", err)
	}
	if _, err := store.MarkProviderResult(ctx, ProviderResultRequest{
		DispatchPermit: permit,
		Result:         ProviderResult{Kind: ProviderResultSubmission, ReceiptID: "receipt-without-reconcile"},
	}); !errors.Is(err, ErrAttemptOutcome) {
		t.Fatalf("MarkProviderResult(after consume) error = %v, want ErrAttemptOutcome", err)
	}

	resolved, err := store.ReconcileUnknown(ctx, ProviderResultRequest{
		DispatchPermit: permit,
		Result:         ProviderResult{Kind: ProviderResultSubmission, ReceiptID: "receipt-after-reconcile"},
	})
	if err != nil {
		t.Fatalf("ReconcileUnknown() error = %v", err)
	}
	assertAttemptResolved(t, resolved, AttemptProviderAccepted, "receipt-after-reconcile", "resolved attempt")
	if _, err := gateway.Consume(ctx, permit); !errors.Is(err, ErrAttemptTerminal) {
		t.Fatalf("Consume(after reconcile) error = %v, want ErrAttemptTerminal", err)
	}
}

func TestCommandAttemptPermitConsumeConcurrentHasOneWinner(t *testing.T) {
	intent, intents, db := attemptIntentFixture(t, "attempt-consume-concurrent")
	store, gateway := mustNewAttemptGateway(t, db, intents, "concurrent")
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	permit, err := gateway.Issue(ctx, attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	const workers = 16
	results := make(chan error, workers)
	var group sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		group.Add(1)
		runtimesafe.SafeGo(ctx, nil, "orchestrationcommand.test.attempt-consume-concurrent", func(context.Context) {
			defer group.Done()
			<-start
			_, consumeErr := gateway.Consume(ctx, permit)
			results <- consumeErr
		})
	}
	close(start)
	group.Wait()
	close(results)

	winners := collectAttemptConsumeWinners(t, results)
	if winners != 1 {
		t.Fatalf("concurrent Consume() winners = %d, want 1", winners)
	}
	attempt, err := store.GetByCommand(ctx, intent.CommandID)
	if err != nil {
		t.Fatalf("GetByCommand() error = %v", err)
	}
	assertAttemptUnknown(t, attempt, "durable attempt after concurrent consume")
}

// collectAttemptConsumeWinners 汇总并发消费结果并校验 loser 错误。
func collectAttemptConsumeWinners(t *testing.T, results <-chan error) int {
	t.Helper()
	winners := 0
	for consumeErr := range results {
		if consumeErr == nil {
			winners++
			continue
		}
		if !errors.Is(consumeErr, ErrAttemptConsumed) || !errors.Is(consumeErr, ErrAttemptUnknownSticky) {
			t.Fatalf("concurrent Consume() error = %v, want consumed + unknown-sticky loser", consumeErr)
		}
	}
	return winners
}

func TestCommandAttemptPermitConsumeConcurrentAcrossConnectionsHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration-command-consume-cross-connection.sqlite")
	dbOne := openCommandIntentTestDBAtPath(t, path)
	dbTwo := openCommandIntentTestDBAtPath(t, path)
	intentsOne := mustNewAttemptIntentStore(t, dbOne, "dbOne")
	intentsTwo := mustNewAttemptIntentStore(t, dbTwo, "dbTwo")
	intent := commandIntentFixture("attempt-consume-cross-connection")
	if _, err := intentsOne.Reserve(context.Background(), intent); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	storeOne, gatewayOne := mustNewAttemptGateway(t, dbOne, intentsOne, "dbOne")
	_, gatewayTwo := mustNewAttemptGateway(t, dbTwo, intentsTwo, "dbTwo")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	permit, err := gatewayOne.Issue(ctx, attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	results := make(chan error, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	runtimesafe.SafeGo(ctx, nil, "orchestrationcommand.test.attempt-consume-cross-connection-one", func(context.Context) {
		defer group.Done()
		<-start
		_, consumeErr := gatewayOne.Consume(ctx, permit)
		results <- consumeErr
	})
	runtimesafe.SafeGo(ctx, nil, "orchestrationcommand.test.attempt-consume-cross-connection-two", func(context.Context) {
		defer group.Done()
		<-start
		_, consumeErr := gatewayTwo.Consume(ctx, permit)
		results <- consumeErr
	})
	close(start)
	group.Wait()
	close(results)

	winners := 0
	for consumeErr := range results {
		if consumeErr == nil {
			winners++
			continue
		}
		if !errors.Is(consumeErr, ErrAttemptConsumed) || !errors.Is(consumeErr, ErrAttemptUnknownSticky) {
			t.Fatalf("cross-connection Consume() error = %v, want consumed + unknown-sticky loser", consumeErr)
		}
	}
	if winners != 1 {
		t.Fatalf("cross-connection Consume() winners = %d, want 1", winners)
	}
	attempt, err := storeOne.GetByCommand(ctx, intent.CommandID)
	if err != nil {
		t.Fatalf("GetByCommand() error = %v", err)
	}
	assertAttemptUnknown(t, attempt, "durable attempt after cross-connection consume")
}

// mustNewAttemptIntentStore 构造指定连接的 intent store，失败即终止测试。
func mustNewAttemptIntentStore(t *testing.T, db *sql.DB, label string) *Store {
	t.Helper()
	intents, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(%s) error = %v", label, err)
	}
	return intents
}

// mustNewAttemptGateway 构造指定连接的 attempt store 与 gateway。
func mustNewAttemptGateway(t *testing.T, db *sql.DB, intents *Store, label string) (*AttemptStore, *CommandAttemptGateway) {
	t.Helper()
	store, err := NewAttemptStore(db, intents, allowAttemptAdmission{})
	if err != nil {
		t.Fatalf("NewAttemptStore(%s) error = %v", label, err)
	}
	gateway, err := NewCommandAttemptGateway(store)
	if err != nil {
		t.Fatalf("NewCommandAttemptGateway(%s) error = %v", label, err)
	}
	return store, gateway
}

func TestCommandAttemptPermitConsumeCrashWindowPersistsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration-command-consume-crash.sqlite")
	intent, permit, db := consumeCrashFixture(t, path)
	if err := db.Close(); err != nil {
		t.Fatalf("close after consume: %v", err)
	}

	reopenedStore, reopenedGateway := reopenConsumeCrashFixture(t, path)
	ctx := context.Background()

	unknown, err := reopenedStore.QueryUnknown(ctx, AttemptKey{Namespace: intent.IdempotencyNamespace, Digest: intent.IdempotencyKeyDigest})
	if err != nil {
		t.Fatalf("QueryUnknown(reopened) error = %v", err)
	}
	assertAttemptUnknown(t, unknown, "reopened unknown attempt")
	assertAttemptMaterial(t, unknown, permit, "reopened unknown attempt")
	if _, err := reopenedGateway.Issue(ctx, attemptRequestFromIntent(intent)); !errors.Is(err, ErrAttemptUnresolved) {
		t.Fatalf("Issue(reopened) error = %v, want ErrAttemptUnresolved", err)
	}
	resolved, err := reopenedStore.ReconcileUnknown(ctx, ProviderResultRequest{
		DispatchPermit: permit,
		Result:         ProviderResult{Kind: ProviderResultFailure, FailureCode: "provider-not-found"},
	})
	if err != nil {
		t.Fatalf("ReconcileUnknown(reopened) error = %v", err)
	}
	if resolved.Phase != AttemptProviderFailed {
		t.Fatalf("reconciled reopened attempt phase = %q", resolved.Phase)
	}
	if resolved.Result.FailureCode != "provider-not-found" {
		t.Fatalf("reconciled reopened attempt failure = %q", resolved.Result.FailureCode)
	}
}

// consumeCrashFixture 创建并消费一个用于崩溃窗口重开的 attempt。
func consumeCrashFixture(t *testing.T, path string) (Intent, DispatchPermit, *sql.DB) {
	t.Helper()
	db := openCommandIntentTestDBAtPath(t, path)
	intents := mustNewAttemptIntentStore(t, db, "first")
	intent := commandIntentFixture("attempt-consume-crash")
	if _, err := intents.Reserve(context.Background(), intent); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	_, gateway := mustNewAttemptGateway(t, db, intents, "first")
	permit, err := gateway.Issue(context.Background(), attemptRequestFromIntent(intent))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := gateway.Consume(context.Background(), permit); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	return intent, permit, db
}

// reopenConsumeCrashFixture 重开崩溃窗口数据库并返回新的存储网关。
func reopenConsumeCrashFixture(t *testing.T, path string) (*AttemptStore, *CommandAttemptGateway) {
	t.Helper()
	db := openCommandIntentTestDBAtPath(t, path)
	intents := mustNewAttemptIntentStore(t, db, "reopened")
	return mustNewAttemptGateway(t, db, intents, "reopened")
}
