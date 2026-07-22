//go:build unix

package localci

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type promotionIdentityResolverStub struct{}

func (promotionIdentityResolverStub) ResolveCandidateIdentity(
	_ context.Context,
	_ PromotionCandidate,
	result CandidateResult,
) (gate.ImageIdentity, error) {
	return gate.ImageIdentity{
		Registry: candidateImageRepository, OCIIndexDigest: result.ImageDigest,
		PlatformManifestDigest: result.ImageDigest, ConfigDigest: digest("9"),
		RootFSDiffIDs: []string{digest("a")}, OS: "linux", Architecture: "arm64",
	}, nil
}

type promotionObserverStub struct {
	observation TrustedRefObservation
	ancestry    map[string]bool
}

func (observer *promotionObserverStub) ObserveTrustedRef(
	context.Context,
	string,
	string,
) (TrustedRefObservation, error) {
	return observer.observation, nil
}

func (observer *promotionObserverStub) IsAncestor(
	_ context.Context,
	_ string,
	_ string,
	previous string,
	next string,
) (bool, error) {
	return observer.ancestry[previous+"->"+next], nil
}

type promotionSignerFixture struct {
	identity gate.SignerIdentity
	private  ed25519.PrivateKey
}

func (signer promotionSignerFixture) SignerIdentity() gate.SignerIdentity { return signer.identity }

func (signer promotionSignerFixture) SignAcceptedImage(
	ctx context.Context,
	payload []byte,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(signer.private, payload)), nil
}

type promotionTestFixture struct {
	state      *AcceptedImageState
	store      *PromotionCandidateStore
	crypto     *acceptedImageCryptoFixture
	accepted   gate.AcceptedImageRecord
	candidate  PromotionCandidate
	observer   *promotionObserverStub
	controller *PromotionController
	createdAt  time.Time
}

func TestPromotionControllerBuildAwaitingTrustedRefAndCASPromotion(t *testing.T) {
	fixture := newPromotionTestFixture(t)
	assertPromotionWaitsAtAcceptedTip(t, fixture)
	fixture.observer.observation.Commit = fixture.candidate.TrustedCommit
	fixture.observer.observation.SourceTree = fixture.candidate.SourceTree
	if err := fixture.controller.PromoteReady(context.Background()); err != nil {
		t.Fatalf("PromoteReady(candidate tip) error = %v", err)
	}
	assertPromotionFixtureCompleted(t, fixture)
}

func TestPromotionCandidateBuildFailureGetsFreshWorkload(t *testing.T) {
	fixture := newPromotionPlanFixture(t)
	first := mustPromotionCandidatePlan(t, fixture)
	failing := mustPromotionCandidateBuilder(t, &recordingBuildKitRunner{err: errors.New("broken pipe")})
	if err := fixture.store.ExecuteBuild(context.Background(), first.WorkloadID, failing, promotionIdentityResolverStub{}); err == nil {
		t.Fatal("ExecuteBuild() accepted builder failure")
	}
	assertPromotionCandidateStatus(t, fixture.store, first.WorkloadID, PromotionCandidateFailed)
	assertPromotionCandidatePayloadCompacted(t, fixture.store, first.WorkloadID)
	second := mustPromotionCandidatePlan(t, fixture)
	if second.WorkloadID == first.WorkloadID {
		t.Fatalf("replacement workload reused terminal identity %q", first.WorkloadID)
	}
	assertPromotionCandidatePruned(t, fixture.store, first.WorkloadID)
	builder := mustPromotionCandidateBuilder(t, &recordingBuildKitRunner{digest: digest("8")})
	if err := fixture.store.ExecuteBuild(context.Background(), second.WorkloadID, builder, promotionIdentityResolverStub{}); err != nil {
		t.Fatal(err)
	}
	assertPromotionCandidateStatus(t, fixture.store, second.WorkloadID, PromotionCandidateAwaiting)
}

func TestPromotionCandidateAdvanceFailureGetsFreshWorkload(t *testing.T) {
	fixture := newPromotionPlanFixture(t)
	first := mustPromotionCandidatePlan(t, fixture)
	builder := mustPromotionCandidateBuilder(t, &recordingBuildKitRunner{digest: digest("8")})
	if err := fixture.store.ExecuteBuild(context.Background(), first.WorkloadID, builder, promotionIdentityResolverStub{}); err != nil {
		t.Fatal(err)
	}
	advanceErr := errors.New("trusted-ref CAS rejected")
	if err := fixture.store.MarkAdvanceFailed(context.Background(), first.WorkloadID, advanceErr); !errors.Is(err, advanceErr) {
		t.Fatalf("MarkAdvanceFailed() error = %v", err)
	}
	assertPromotionCandidateStatus(t, fixture.store, first.WorkloadID, PromotionCandidateAdvanceFailed)
	assertPromotionCandidatePayloadCompacted(t, fixture.store, first.WorkloadID)
	second := mustPromotionCandidatePlan(t, fixture)
	if second.WorkloadID == first.WorkloadID {
		t.Fatalf("replacement workload reused terminal identity %q", first.WorkloadID)
	}
	assertPromotionCandidatePruned(t, fixture.store, first.WorkloadID)
}

func TestUniqueCandidateForTrustedCommitRecoverySelection(t *testing.T) {
	failed := promotionCandidateForTrustedCommit(PromotionCandidateFailed, "failed")
	awaiting := promotionCandidateForTrustedCommit(PromotionCandidateAwaiting, "awaiting")
	incompatible := promotionCandidateForTrustedCommit(PromotionCandidateFailed, "incompatible")
	incompatible.SourceTree = "other-tree"
	candidate, err := uniqueCandidateForTrustedCommit(
		[]PromotionCandidate{failed, incompatible, awaiting}, awaiting.RepoID, awaiting.TrustedRef, awaiting.TrustedCommit,
	)
	if err != nil || candidate.WorkloadID != awaiting.WorkloadID {
		t.Fatalf("failed history selected %+v, err=%v", candidate, err)
	}
	queued := promotionCandidateForTrustedCommit(PromotionCandidateQueued, "queued")
	if _, err := uniqueCandidateForTrustedCommit(
		[]PromotionCandidate{queued, awaiting}, awaiting.RepoID, awaiting.TrustedRef, awaiting.TrustedCommit,
	); err == nil {
		t.Fatal("multiple active candidates were accepted")
	}
	candidate, err = uniqueCandidateForTrustedCommit(
		[]PromotionCandidate{failed}, failed.RepoID, failed.TrustedRef, failed.TrustedCommit,
	)
	if err != nil || candidate.WorkloadID != failed.WorkloadID {
		t.Fatalf("only failed candidate = %+v, err=%v", candidate, err)
	}
	matchingFailure := promotionCandidateForTrustedCommit(PromotionCandidateFailed, "matching-failure")
	candidate, err = uniqueCandidateForTrustedCommit(
		[]PromotionCandidate{failed, matchingFailure}, failed.RepoID, failed.TrustedRef, failed.TrustedCommit,
	)
	if err != nil || candidate.WorkloadID != failed.WorkloadID {
		t.Fatalf("consistent failed candidates = %+v, err=%v", candidate, err)
	}
	if _, err := uniqueCandidateForTrustedCommit(
		[]PromotionCandidate{failed, incompatible}, failed.RepoID, failed.TrustedRef, failed.TrustedCommit,
	); err == nil {
		t.Fatal("incompatible failed recovery authorities were accepted")
	}
}

func promotionCandidateForTrustedCommit(status PromotionCandidateStatus, workloadID string) PromotionCandidate {
	return PromotionCandidate{
		WorkloadID: workloadID, RepoID: "repo", TrustedRef: "refs/heads/main", TrustedCommit: "commit",
		SourceTree: "tree", PreviousTrustedCommit: "previous", ExpectedAcceptedRecordDigest: "accepted",
		ExpectedAcceptedGeneration: 1, Status: status,
	}
}

func mustPromotionCandidatePlan(t *testing.T, fixture promotionPlanFixture) PromotionCandidatePlan {
	t.Helper()
	plan, err := fixture.store.Plan(context.Background(), fixture.accepted, fixture.request)
	if err != nil || !plan.BuildRequired {
		t.Fatalf("Plan() = %+v, err=%v", plan, err)
	}
	return plan
}

func mustPromotionCandidateBuilder(t *testing.T, runner BuildKitRunner) *ImageBuilder {
	t.Helper()
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func assertPromotionCandidateStatus(t *testing.T, store *PromotionCandidateStore, workloadID string, want PromotionCandidateStatus) {
	t.Helper()
	candidate, err := store.Candidate(context.Background(), workloadID)
	if err != nil || candidate.Status != want {
		t.Fatalf("candidate %q = %+v, err=%v, want status %q", workloadID, candidate, err, want)
	}
}

func assertPromotionCandidatePayloadCompacted(t *testing.T, store *PromotionCandidateStore, workloadID string) {
	t.Helper()
	candidate, err := store.Candidate(context.Background(), workloadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.BuildRequest.SourceEntries) != 0 || candidate.BuildRequest.SourceTreeSHA != candidate.SourceTree ||
		candidate.BuildRequest.PolicyDigest != candidate.PolicyDigest {
		t.Fatalf("terminal candidate payload was not compacted safely: %+v", candidate.BuildRequest)
	}
}

func assertPromotionCandidatePruned(t *testing.T, store *PromotionCandidateStore, workloadID string) {
	t.Helper()
	if _, err := store.Candidate(context.Background(), workloadID); !errors.Is(err, ErrPromotionCandidateNotFound) {
		t.Fatalf("terminal candidate %q was retained: %v", workloadID, err)
	}
}

func assertPromotionWaitsAtAcceptedTip(t *testing.T, fixture *promotionTestFixture) {
	t.Helper()
	if err := fixture.controller.PromoteReady(context.Background()); err != nil {
		t.Fatalf("PromoteReady(old tip) error = %v", err)
	}
	current, err := fixture.state.Load(context.Background())
	if err != nil || current.Generation != 1 {
		t.Fatalf("old tip changed accepted state: generation=%d err=%v", current.Generation, err)
	}
}

func assertPromotionFixtureCompleted(t *testing.T, fixture *promotionTestFixture) {
	t.Helper()
	assertPromotedAcceptedRecord(t, fixture)
	assertPromotedCandidatePersistence(t, fixture)
}

func assertPromotedAcceptedRecord(t *testing.T, fixture *promotionTestFixture) {
	t.Helper()
	promoted, err := fixture.state.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Generation != 2 || promoted.TrustedCommit != fixture.candidate.TrustedCommit ||
		promoted.Image.PlatformManifestDigest != fixture.candidate.PlatformManifestDigest {
		t.Fatalf("promoted accepted record = %+v", promoted)
	}
	if len(promoted.Signature) == 0 {
		t.Fatal("promoted accepted record is unsigned")
	}
}

func assertPromotedCandidatePersistence(t *testing.T, fixture *promotionTestFixture) {
	t.Helper()
	awaiting, err := fixture.store.Awaiting(context.Background())
	if err != nil || len(awaiting) != 0 {
		t.Fatalf("awaiting after promotion = %d, err=%v", len(awaiting), err)
	}
	terminal, err := fixture.store.Candidate(context.Background(), fixture.candidate.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != PromotionCandidatePromoted || len(terminal.BuildRequest.SourceEntries) != 0 {
		t.Fatalf("promoted candidate retained source payload: status=%q entries=%d", terminal.Status, len(terminal.BuildRequest.SourceEntries))
	}
	if terminal.BuildRequest.SourceTreeSHA != fixture.candidate.SourceTree || terminal.BuildRequest.PolicyDigest != fixture.candidate.PolicyDigest {
		t.Fatalf("promoted candidate lost durable build identity: %+v", terminal.BuildRequest)
	}
}

func TestPromotionControllerRejectsTipTreeExpiryAndRollback(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*promotionTestFixture)
		want   error
	}{
		{name: "tree mismatch", want: ErrPromotionTrustedRefMismatch, mutate: func(fixture *promotionTestFixture) {
			fixture.observer.observation.Commit = fixture.candidate.TrustedCommit
			fixture.observer.observation.SourceTree = strings.Repeat("f", len(fixture.candidate.SourceTree))
		}},
		{name: "non ancestor rollback", want: ErrPromotionTrustedRefRollback, mutate: func(fixture *promotionTestFixture) {
			fixture.observer.observation.Commit = strings.Repeat("c", len(fixture.candidate.TrustedCommit))
			fixture.observer.observation.SourceTree = fixture.candidate.SourceTree
		}},
		{name: "expired", want: ErrPromotionCandidateExpired, mutate: func(fixture *promotionTestFixture) {
			fixture.controller.now = func() time.Time { return fixture.candidate.ExpiresAt }
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPromotionTestFixture(t)
			test.mutate(fixture)
			err := fixture.controller.PromoteReady(context.Background())
			if test.name == "tree mismatch" {
				if err == nil || !strings.Contains(err.Error(), "tree does not match") {
					t.Fatalf("PromoteReady() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("PromoteReady() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPromotionControllerRecoversAfterSignAndAfterCASCrashes(t *testing.T) {
	for _, crashPoint := range []string{"after sign", "after CAS"} {
		t.Run(crashPoint, func(t *testing.T) {
			fixture := newPromotionTestFixture(t)
			fixture.observer.observation.Commit = fixture.candidate.TrustedCommit
			fixture.observer.observation.SourceTree = fixture.candidate.SourceTree
			injected := errors.New("injected " + crashPoint)
			if crashPoint == "after sign" {
				fixture.controller.afterSign = func() error { return injected }
			} else {
				fixture.controller.afterCAS = func() error { return injected }
			}
			if err := fixture.controller.PromoteReady(context.Background()); !errors.Is(err, injected) {
				t.Fatalf("first PromoteReady() error = %v", err)
			}
			restarted := mustPromotionController(t, fixture)
			restarted.now = func() time.Time { return fixture.candidate.ExpiresAt.Add(time.Second) }
			if err := restarted.PromoteReady(context.Background()); err != nil {
				t.Fatalf("restarted PromoteReady() error = %v", err)
			}
			current, err := fixture.state.Load(context.Background())
			if err != nil || current.Generation != 2 {
				t.Fatalf("restarted promotion generation=%d err=%v", current.Generation, err)
			}
		})
	}
}

func TestPromotionControllerRejectsConcurrentCASWinner(t *testing.T) {
	fixture := newPromotionTestFixture(t)
	fixture.observer.observation.Commit = fixture.candidate.TrustedCommit
	fixture.observer.observation.SourceTree = fixture.candidate.SourceTree
	conflictCommit := strings.Repeat("c", len(fixture.candidate.TrustedCommit))
	fixture.crypto.ancestry.allowed[fixture.accepted.TrustedCommit+"->"+conflictCommit] = true
	fixture.controller.beforeCAS = func() error {
		digestValue, err := gate.AcceptedImageRecordDigest(fixture.accepted)
		if err != nil {
			return err
		}
		next := fixture.crypto.signedRecord(t, 2, digestValue, conflictCommit, fixture.candidate.SourceTree)
		return fixture.state.PromoteCAS(context.Background(), gate.PromotionRecord{
			SchemaVersion: gate.PromotionRecordSchemaVersion, ExpectedRecordDigest: digestValue,
			ExpectedGeneration: 1, Next: next,
		})
	}
	if err := fixture.controller.PromoteReady(context.Background()); !errors.Is(err, ErrAcceptedImageCASConflict) {
		t.Fatalf("PromoteReady() error = %v, want CAS conflict", err)
	}
}

func TestPromotionCandidateStoreCrashBeforeAndAfterCanonicalWriteIsIdempotent(t *testing.T) {
	for _, crashPoint := range []string{"before write", "after write"} {
		t.Run(crashPoint, func(t *testing.T) {
			fixture := newPromotionPlanFixture(t)
			injected := errors.New("injected " + crashPoint)
			if crashPoint == "before write" {
				fixture.store.beforeSave = func() error { return injected }
			} else {
				fixture.store.afterSave = func() error { return injected }
			}
			if _, err := fixture.store.Plan(context.Background(), fixture.accepted, fixture.request); !errors.Is(err, injected) {
				t.Fatalf("first Plan() error = %v", err)
			}
			fixture.store.beforeSave = nil
			fixture.store.afterSave = nil
			first, err := fixture.store.Plan(context.Background(), fixture.accepted, fixture.request)
			if err != nil {
				t.Fatal(err)
			}
			second, err := fixture.store.Plan(context.Background(), fixture.accepted, fixture.request)
			if err != nil || first.WorkloadID != second.WorkloadID {
				t.Fatalf("idempotent plan mismatch: first=%+v second=%+v err=%v", first, second, err)
			}
		})
	}
}

func TestPromotionCandidateStoreDoesNotQueueForCurrentAcceptedInputs(t *testing.T) {
	fixture := newPromotionPlanFixture(t)
	baseTree := readOnlyImageTree(t, candidateEntries(validCandidateDockerfile()))
	request := fixture.request
	request.Tree = baseTree
	request.TrustedCommit = fixture.accepted.TrustedCommit
	plan, err := fixture.store.Plan(context.Background(), fixture.accepted, request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.BuildRequired || plan.WorkloadID != "" {
		t.Fatalf("unchanged accepted inputs queued build: %+v", plan)
	}
}

func TestPromotionCandidateFieldRegistriesAreComplete(t *testing.T) {
	assertRegisteredFields(t, reflect.TypeFor[PromotionCandidatePlanRequest](), map[string]string{
		"Tree": "immutable source", "PolicyDigest": "policy binding", "Platform": "platform binding",
		"RepoID": "repository authority", "TrustedRef": "ref authority", "TrustedCommit": "candidate commit",
		"CreatedAt": "creation time", "ExpiresAt": "expiry time",
	})
	assertRegisteredFields(t, reflect.TypeFor[PromotionCandidatePlan](), map[string]string{
		"BuildRequired": "scheduler decision", "WorkloadID": "scheduler build identity",
	})
	assertRegisteredFields(t, reflect.TypeFor[PromotionCandidate](), map[string]string{
		"SchemaVersion": "canonical schema", "CandidateID": "candidate identity", "WorkloadID": "scheduler identity",
		"RepoID": "repository authority", "TrustedRef": "ref authority", "TrustedCommit": "candidate commit",
		"SourceTree": "candidate tree", "PreviousTrustedCommit": "ancestry base", "PolicyDigest": "policy binding",
		"Platform": "platform binding", "ImageSchemaVersion": "image schema", "ImageInputDigest": "input binding",
		"ContextDigest": "context binding", "InputManifestDigest": "manifest binding",
		"ToolchainDigest": "toolchain binding", "DockerfileDigest": "Dockerfile binding",
		"PlatformManifestDigest": "built artifact", "Image": "complete immutable identity", "Runner": "runner trust",
		"ExpectedAcceptedRecordDigest": "CAS digest", "ExpectedAcceptedGeneration": "CAS generation",
		"BuildRequest": "durable build payload", "PromotionMode": "trusted-ref ownership", "CreatedAt": "creation time", "ExpiresAt": "expiry time",
		"PromotionAcceptedAt": "stable signing time", "Status": "candidate lifecycle",
	})
}

type promotionPlanFixture struct {
	store    *PromotionCandidateStore
	accepted gate.AcceptedImageRecord
	request  PromotionCandidatePlanRequest
	crypto   *acceptedImageCryptoFixture
}

func newPromotionPlanFixture(t *testing.T) promotionPlanFixture {
	t.Helper()
	store, err := NewPromotionCandidateStore(acceptedImageCanonicalTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	cryptoFixture := newAcceptedImageCryptoFixture(t)
	baseTree := readOnlyImageTree(t, candidateEntries(validCandidateDockerfile()))
	baseInputs := mustResolveGateImageInputs(t, baseTree)
	accepted := cryptoFixture.signedRecord(t, 1, "", imageStateCommit, baseTree.Source.SourceTreeSHA)
	accepted.PolicyDigest = digest("d")
	accepted.ImageInputDigest = baseInputs.ImageInputDigest
	accepted.Runner.PolicyDigest = accepted.PolicyDigest
	cryptoFixture.sign(t, &accepted)
	changedEntries := cloneTreeEntries(baseTree.Entries)
	changeCandidateInput(t, changedEntries, "go.mod", "module example.invalid/promotion\n")
	changedTree := readOnlyImageTree(t, changedEntries)
	createdAt := time.Now().UTC().Truncate(time.Second)
	return promotionPlanFixture{
		store: store, accepted: accepted, crypto: cryptoFixture,
		request: PromotionCandidatePlanRequest{
			Tree: changedTree, PolicyDigest: digest("d"), Platform: "linux/arm64",
			RepoID: accepted.RepoID, TrustedRef: accepted.TrustedRef, TrustedCommit: imageStateNext,
			CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Hour),
		},
	}
}

func newPromotionTestFixture(t *testing.T) *promotionTestFixture {
	t.Helper()
	planFixture := newPromotionPlanFixture(t)
	state, _, _ := newAcceptedImageStateFixture(t)
	state.verifier = planFixture.crypto.verifier
	state.ancestry = planFixture.crypto.ancestry
	planFixture.crypto.ancestry.allowed[imageStateCommit+"->"+imageStateNext] = true
	if err := state.Bootstrap(context.Background(), planFixture.accepted); err != nil {
		t.Fatal(err)
	}
	plan, err := planFixture.store.Plan(context.Background(), planFixture.accepted, planFixture.request)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewImageBuilder(&recordingBuildKitRunner{digest: digest("8")})
	if err != nil {
		t.Fatal(err)
	}
	if err := planFixture.store.ExecuteBuild(
		context.Background(), plan.WorkloadID, builder, promotionIdentityResolverStub{},
	); err != nil {
		t.Fatal(err)
	}
	awaiting, err := planFixture.store.Awaiting(context.Background())
	if err != nil || len(awaiting) != 1 {
		t.Fatalf("awaiting candidates=%d err=%v", len(awaiting), err)
	}
	observer := &promotionObserverStub{
		observation: TrustedRefObservation{
			RepoID: planFixture.accepted.RepoID, TrustedRef: planFixture.accepted.TrustedRef,
			Commit: planFixture.accepted.TrustedCommit, SourceTree: planFixture.accepted.SourceTree,
		},
		ancestry: planFixture.crypto.ancestry.allowed,
	}
	fixture := &promotionTestFixture{
		state: state, store: planFixture.store, crypto: planFixture.crypto, accepted: planFixture.accepted,
		candidate: awaiting[0], observer: observer, createdAt: planFixture.request.CreatedAt,
	}
	fixture.controller = mustPromotionController(t, fixture)
	return fixture
}

func mustPromotionController(t *testing.T, fixture *promotionTestFixture) *PromotionController {
	t.Helper()
	controller, err := NewPromotionController(
		fixture.store, fixture.state, fixture.observer,
		promotionSignerFixture{identity: fixture.crypto.signer, private: fixture.crypto.private}, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	controller.now = func() time.Time { return fixture.createdAt.Add(time.Minute) }
	return controller
}
