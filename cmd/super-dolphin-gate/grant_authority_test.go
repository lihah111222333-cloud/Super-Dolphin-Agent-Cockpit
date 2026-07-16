package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

func TestProductionActionGrantConfigFieldRegistryIsComplete(t *testing.T) {
	assertProductionFields(t, reflect.TypeFor[productionActionGrantAuthorityConfig](), map[string]string{
		"Signer": "grant signer identity", "PublicKey": "grant verification material",
		"PrivateKeyFile": "owner-only grant signing material", "TTLSeconds": "grant expiry",
	})
	assertProductionFields(t, reflect.TypeFor[productionActionGrantPrivateKey](), map[string]string{
		"PrivateKey": "owner-only Ed25519 private key",
	})
}

func TestProductionActionGrantAuthorityRequiresOwnerOnlyDistinctKey(t *testing.T) {
	fixture := newProductionTestFixture(t)
	privatePath := fixture.config.ActionGrantAuthority.PrivateKeyFile
	if err := os.Chmod(privatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionActionGrantService(fixture.config, &coordinatorStore{}, nil); err == nil {
		t.Fatal("production action grant authority accepted a non-0600 private key")
	}
	if err := os.Chmod(privatePath, 0o600); err != nil {
		t.Fatal(err)
	}
	reused := fixture.config
	reused.ActionGrantAuthority.Signer = reused.PromotionSigner.Signer
	if err := reused.Validate(); err == nil {
		t.Fatal("production config accepted promotion signer reuse for ActionGrant")
	}
}

func TestActionGrantStoreRejectsIncompleteSchema(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/incomplete.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE coordinator_action_grants (grant_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := ensureCoordinatorActionGrantSchema(context.Background(), database); err == nil ||
		!strings.Contains(err.Error(), "missing column") {
		t.Fatalf("ensureCoordinatorActionGrantSchema() error = %v", err)
	}
}

func TestActionGrantIssueCrashRecoveryAndConcurrentConsume(t *testing.T) {
	fixture := newActionGrantTestFixture(t)
	grant, err := fixture.service.Issue(context.Background(), fixture.intent("nonce-crash"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := fixture.service.Issue(context.Background(), fixture.intent("nonce-crash"))
	if err != nil || !reflect.DeepEqual(grant, again) {
		t.Fatalf("idempotent issue grant=%#v error=%v", again, err)
	}
	fixture.reopenStore(t)

	successes := consumeActionGrantConcurrently(t, fixture, grant, 16)
	if successes != 1 {
		t.Fatalf("successful concurrent consumes = %d, want 1", successes)
	}
	consumed, err := fixture.store.actionGrantByID(context.Background(), grant.GrantID)
	if err != nil || consumed.State != gatecontract.ActionGrantStateConsumed {
		t.Fatalf("durable consumed grant=%#v error=%v", consumed, err)
	}
	if err := fixture.verifier.VerifyActionGrant(consumed); err != nil {
		t.Fatalf("verify consumed grant: %v", err)
	}
	fixture.reopenStore(t)
	if _, err := fixture.service.Consume(context.Background(), grant, fixture.expected()); err == nil {
		t.Fatal("consume after crash recovery accepted an already consumed grant")
	}
}

func consumeActionGrantConcurrently(
	t *testing.T,
	fixture *actionGrantTestFixture,
	grant gatecontract.ActionGrant,
	workers int,
) int32 {
	t.Helper()
	var successes atomic.Int32
	var group errgroup.Group
	for range workers {
		group.Go(func() error {
			if _, err := fixture.service.Consume(context.Background(), grant, fixture.expected()); err == nil {
				successes.Add(1)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	return successes.Load()
}

func TestActionGrantRejectsBindingAndForgery(t *testing.T) {
	fixture := newActionGrantTestFixture(t)
	grant, err := fixture.service.Issue(context.Background(), fixture.intent("nonce-reject"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*actionGrantExpectation)
	}{
		{name: "audience", mutate: func(value *actionGrantExpectation) { value.Audience = gatecontract.ActionAudienceRelease }},
		{name: "ref", mutate: func(value *actionGrantExpectation) { value.Ref = "refs/heads/other" }},
		{name: "tree", mutate: func(value *actionGrantExpectation) { value.SourceTreeSHA = strings.Repeat("9", 40) }},
		{name: "generation", mutate: func(value *actionGrantExpectation) { value.Generation++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := fixture.expected()
			test.mutate(&expected)
			if err := fixture.service.Verify(context.Background(), grant, expected); err == nil {
				t.Fatal("Verify() accepted mismatched action binding")
			}
		})
	}
	forged := grant
	forged.Signature = strings.Repeat("A", len(forged.Signature))
	if err := fixture.service.Verify(context.Background(), forged, fixture.expected()); err == nil {
		t.Fatal("Verify() accepted forged signature")
	}
}

func TestActionGrantRejectsRevocationAndExpiry(t *testing.T) {
	fixture := newActionGrantTestFixture(t)
	revokedGrant, err := fixture.service.Issue(context.Background(), fixture.intent("nonce-revoke"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Revoke(context.Background(), revokedGrant.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Consume(context.Background(), revokedGrant, fixture.expected()); err == nil {
		t.Fatal("Consume() accepted revoked grant")
	}

	expiredGrant, err := fixture.service.Issue(context.Background(), fixture.intent("nonce-expire"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = expiredGrant.ExpiresAt.Add(time.Nanosecond)
	if err := fixture.service.Verify(context.Background(), expiredGrant, fixture.expected()); err == nil {
		t.Fatal("Verify() accepted expired grant")
	}
	stored, err := fixture.store.actionGrantByID(context.Background(), expiredGrant.GrantID)
	if err != nil || stored.State != gatecontract.ActionGrantStateExpired {
		t.Fatalf("expired durable grant=%#v error=%v", stored, err)
	}
}

func TestActionGrantRejectsReceiptGenerationChange(t *testing.T) {
	fixture := newActionGrantTestFixture(t)
	grant, err := fixture.service.Issue(context.Background(), fixture.intent("nonce-generation"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.receiptAuthority.accepted.Generation++
	if err := fixture.service.Verify(context.Background(), grant, fixture.expected()); err == nil {
		t.Fatal("Verify() accepted a no-longer-current receipt generation")
	}
}

func TestTypedPrePushBridgeConsumesExactGitPushGrant(t *testing.T) {
	fixture := newActionGrantTestFixture(t)
	status := gatehook.JobStatus{
		JobID: "job-action-grant", State: gatehook.JobStatePassed,
		SourceTreeSHA: fixture.submit.Source.SourceTreeSHA, ReceiptID: fixture.receipt.ReceiptID,
	}
	client := &recordingCoordinatorClient{receipt: fixture.receipt}
	bridge := &hookCoordinatorBridge{
		client: client, authority: fixture.receiptAuthority, grants: fixture.service,
	}
	request := gitPushGrantRequest{
		Status: status, Submit: fixture.submit, RemoteURL: "ssh://git@example.invalid/repository.git",
	}
	if err := bridge.AuthorizeGitPush(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	nonce := gitPushGrantNonce(fixture.receipt, request)
	grant, err := fixture.store.actionGrantByNonce(context.Background(), nonce)
	if err != nil {
		t.Fatal(err)
	}
	rangeSource := fixture.submit.Source.Range
	if grant.State != gatecontract.ActionGrantStateConsumed ||
		grant.Request.Audience != gatecontract.ActionAudienceGitPush ||
		grant.Request.RemoteURL != request.RemoteURL || grant.Request.Ref != rangeSource.RemoteRef ||
		grant.Request.OldSHA != rangeSource.ObservedRemoteSHA || grant.Request.NewSHA != rangeSource.HeadSHA {
		t.Fatalf("consumed grant did not bind exact push update: %#v", grant)
	}
}

type actionGrantTestFixture struct {
	path             string
	store            *coordinatorStore
	service          *actionGrantService
	signer           *ed25519ActionGrantSigner
	verifier         *ed25519ActionGrantVerifier
	receiptAuthority *staticHookResultReceiptAuthority
	receipt          gatecontract.ResultReceipt
	privateKey       ed25519.PrivateKey
	publicKey        ed25519.PublicKey
	signerIdentity   gatecontract.SignerIdentity
	adapter          gatecontract.TrustedAdapterIdentity
	submit           gatehook.SubmitRequest
	now              time.Time
}

func newActionGrantTestFixture(t *testing.T) *actionGrantTestFixture {
	t.Helper()
	path := t.TempDir() + "/grants.db"
	store := openActionGrantTestStore(t, path)
	submit := actionGrantTestSubmit(t)
	receiptFixture := newHookReceiptFixture(t, submit, "job-action-grant")
	persistActionGrantReceipt(t, store, submit, receiptFixture.receipt)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := gatecontract.SignerIdentity{
		KeyID: "action-grant-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	signer, err := newEd25519ActionGrantSigner(identity, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := newEd25519ActionGrantVerifier(identity, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	adapter := gatecontract.TrustedAdapterIdentity{
		Name: "git-pre-push", BinaryDigest: coordinatorDigest("d"), Signer: identity,
	}
	fixture := &actionGrantTestFixture{
		path: path, store: store, signer: signer, verifier: verifier,
		receiptAuthority: receiptFixture.authority, receipt: receiptFixture.receipt,
		privateKey: privateKey, publicKey: publicKey, signerIdentity: identity,
		adapter: adapter, submit: submit, now: time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
	}
	fixture.bindService(t)
	t.Cleanup(func() { _ = fixture.store.close() })
	return fixture
}

func (fixture *actionGrantTestFixture) bindService(t *testing.T) {
	t.Helper()
	service, err := newActionGrantService(
		fixture.store, fixture.signer, fixture.verifier, fixture.receiptAuthority,
		fixture.adapter, time.Minute, func() time.Time { return fixture.now },
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = service
}

func (fixture *actionGrantTestFixture) reopenStore(t *testing.T) {
	t.Helper()
	if err := fixture.store.close(); err != nil {
		t.Fatal(err)
	}
	fixture.store = openActionGrantTestStore(t, fixture.path)
	fixture.bindService(t)
}

func (fixture *actionGrantTestFixture) intent(nonce string) actionGrantIntent {
	rangeSource := fixture.submit.Source.Range
	return actionGrantIntent{
		Receipt: fixture.receipt, InvocationOwner: fixture.submit.Invocation.Owner,
		Audience: gatecontract.ActionAudienceGitPush, ActionPolicy: string(rangeSource.UpdateKind),
		RemoteURL: "ssh://git@example.invalid/repository.git", Ref: rangeSource.RemoteRef,
		OldSHA: rangeSource.ObservedRemoteSHA, NewSHA: rangeSource.HeadSHA, RequestNonce: nonce,
	}
}

func (fixture *actionGrantTestFixture) expected() actionGrantExpectation {
	rangeSource := fixture.submit.Source.Range
	return actionGrantExpectation{
		Audience: gatecontract.ActionAudienceGitPush, RepoID: fixture.receipt.RepoID,
		InvocationID: fixture.receipt.InvocationID, SourceTreeSHA: fixture.receipt.Source.SourceTreeSHA,
		Generation: fixture.receipt.Generation, RemoteURL: "ssh://git@example.invalid/repository.git",
		Ref: rangeSource.RemoteRef, OldSHA: rangeSource.ObservedRemoteSHA, NewSHA: rangeSource.HeadSHA,
	}
}

func openActionGrantTestStore(t *testing.T, path string) *coordinatorStore {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(coordinatorStoreSchema); err != nil {
		t.Fatal(err)
	}
	if err := ensureCoordinatorActionGrantSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(8)
	return &coordinatorStore{db: database}
}

func persistActionGrantReceipt(
	t *testing.T,
	store *coordinatorStore,
	submit gatehook.SubmitRequest,
	receipt gatecontract.ResultReceipt,
) {
	t.Helper()
	plan, err := gatecontract.BuildGatePlan(submit.Profile, submit.Source)
	if err != nil {
		t.Fatal(err)
	}
	jobID := "job-action-grant"
	if _, err := store.createJob(
		context.Background(), receipt.InvocationID, jobID, submit.Repository.WorktreeRoot,
		plan, localci.PromotionCandidatePlan{},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.startJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(
		context.Background(), "UPDATE coordinator_jobs SET removal_proof_digest = ? WHERE job_id = ?",
		coordinatorDigest("e"), jobID,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.finishJob(
		context.Background(), jobID, jobStatePassed, receipt.GateResults, "", &receipt,
	); err != nil {
		t.Fatal(err)
	}
}

func actionGrantTestSubmit(t *testing.T) gatehook.SubmitRequest {
	t.Helper()
	request := testHookSubmitRequest(t)
	request.Entrypoint = gatecontract.CIEntrypointGitPrePush
	request.Profile = gatecontract.ProfilePush
	request.Source = gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindRange, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Range: &gatecontract.RangeSource{
			BaseKind: gatecontract.BaseKindCommit, BaseSHA: strings.Repeat("1", 40),
			HeadSHA: strings.Repeat("2", 40), LocalRef: "refs/heads/main", RemoteRef: "refs/heads/main",
			ObservedRemoteSHA: strings.Repeat("1", 40), UpdateKind: gatecontract.UpdateKindFastForward,
		},
		SourceTreeSHA: strings.Repeat("3", 40),
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request
}
