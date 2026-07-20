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
	awaiting, err := fixture.store.Awaiting(context.Background())
	if err != nil || len(awaiting) != 0 {
		t.Fatalf("awaiting after promotion = %d, err=%v", len(awaiting), err)
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
		"BuildRequest": "durable build payload", "CreatedAt": "creation time", "ExpiresAt": "expiry time",
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
