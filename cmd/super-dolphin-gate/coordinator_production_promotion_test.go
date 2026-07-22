package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func startProductionCandidateBuild(
	t *testing.T,
	config productionCoordinatorConfig,
	workloadID string,
) (*productionPromotionAuthority, <-chan error) {
	t.Helper()
	promotion, err := newProductionPromotionAuthority(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{digest: productionDigest("8")})
	if err != nil {
		t.Fatal(err)
	}
	service := productionCandidateBuildService{
		store: promotion.candidates, accepted: promotion.accepted, authority: promotion.authority,
		builder: builder, resolver: productionCandidateIdentityResolverStub{}, promotionPoll: 5 * time.Millisecond,
	}
	completed := make(chan error, 1)
	buildCtx := t.Context()
	var worker sync.WaitGroup
	worker.Go(func() {
		completed <- service.ExecuteBuild(buildCtx, workloadID)
	})
	t.Cleanup(worker.Wait)
	return promotion, completed
}

func requireProductionCandidateBuildCompletion(t *testing.T, completed <-chan error, timeoutMessage string) {
	t.Helper()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal(timeoutMessage)
	}
}

func TestProductionFailedCandidateRecoveryRollsBackTrustedRef(t *testing.T) {
	fixture := newProductionTestFixture(t)
	staged := planProductionStagedTreePromotion(t, fixture)
	candidate, err := staged.planner.candidates.Candidate(context.Background(), staged.queued.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	recoverProductionFailedCandidateBuild(t, fixture, staged, candidate)
}

func TestProductionStagedTreeInterruptedBuildRecoveryPreservesAcceptedTrust(t *testing.T) {
	fixture := newProductionTestFixture(t)
	staged := planProductionStagedTreePromotion(t, fixture)
	assertProductionStagedTreeCandidate(t, fixture, &staged)
	candidate, err := staged.planner.candidates.Candidate(context.Background(), staged.queued.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := recoverProductionFailedCandidateBuild(t, fixture, staged, candidate)
	replacement, replacementCandidate := replacementProductionCandidate(t, staged)
	recoverProductionAwaitingCandidate(t, fixture, replacement, replacementCandidate, interrupted)
}

func TestProductionAdvanceFailureRecoveryRestoresTipAndCreatesFreshWorkload(t *testing.T) {
	fixture := newProductionTestFixture(t)
	staged := planProductionStagedTreePromotion(t, fixture)
	candidate := buildProductionStagedTreeCandidate(t, staged)
	recoverProductionAdvanceFailure(t, fixture, staged, candidate)
}

func TestProductionCandidateBuildDependencyWaitsForAcceptedPromotion(t *testing.T) {
	fixture := newProductionTestFixture(t)
	staged := planProductionStagedTreePromotion(t, fixture)
	assertProductionStagedTreeCandidate(t, fixture, &staged)
	promotion, completed := startProductionCandidateBuild(t, fixture.config, staged.queued.WorkloadID)
	waitForProductionCandidateStatus(t, promotion.candidates, staged.queued.WorkloadID, localci.PromotionCandidateAwaiting)
	waitForProductionTrustedTip(t, fixture.config, staged.candidateCommit)
	select {
	case err := <-completed:
		t.Fatalf("candidate build dependency completed before accepted promotion: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	controller, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer, 5*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireProductionCandidateBuildCompletion(t, completed, "candidate build dependency did not complete after accepted promotion")
	promoted, err := promotion.accepted.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Generation != 2 || promoted.TrustedCommit != staged.candidateCommit || promoted.SourceTree != staged.stagedTree {
		t.Fatalf("accepted promotion = %+v", promoted)
	}
}

func TestProductionExternalCandidateBuildWaitsForTrustedRefPromotion(t *testing.T) {
	fixture := newProductionTestFixture(t)
	candidateCommit, tree := commitProductionCandidate(t, fixture)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, tree.Source)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := newProductionCandidateSubmissionPlanner(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := planner.PlanCandidate(context.Background(), imageEnsureRequest{
		RepositoryRoot: fixture.sourceRepo, Plan: plan, JobSourceTreeSHA: tree.Source.SourceTreeSHA,
	})
	if err != nil || !queued.BuildRequired {
		t.Fatalf("PlanCandidate(external accepted tip) = %+v, err=%v", queued, err)
	}
	promotion, completed := startProductionCandidateBuild(t, fixture.config, queued.WorkloadID)
	waitForProductionCandidateStatus(t, promotion.candidates, queued.WorkloadID, localci.PromotionCandidateAwaiting)
	select {
	case err := <-completed:
		t.Fatalf("external candidate dependency completed at the accepted trusted tip: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "fetch", "-q", "--no-tags", "--", fixture.sourceRepo, candidateCommit)
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "update-ref", fixture.config.TrustedRef, candidateCommit, fixture.commit)
	controller, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer, 5*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireProductionCandidateBuildCompletion(t, completed, "external candidate dependency did not complete after trusted ref promotion")
}

func waitForProductionCandidateStatus(
	t *testing.T,
	store *localci.PromotionCandidateStore,
	workloadID string,
	want localci.PromotionCandidateStatus,
) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		candidate, err := store.Candidate(context.Background(), workloadID)
		if err == nil && candidate.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("candidate %q did not reach status %q", workloadID, want)
}

func waitForProductionTrustedTip(t *testing.T, config productionCoordinatorConfig, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tip := productionGitLine(t, "", "--git-dir="+config.TrustedRepository, "rev-parse", config.TrustedRef+"^{commit}")
		if tip == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("trusted ref did not advance to %s", want)
}

func buildProductionStagedTreeCandidate(t *testing.T, staged productionStagedTreePromotionFixture) localci.PromotionCandidate {
	t.Helper()
	builder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{digest: productionDigest("8")})
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.planner.candidates.ExecuteBuild(context.Background(), staged.queued.WorkloadID, builder, productionCandidateIdentityResolverStub{}); err != nil {
		t.Fatal(err)
	}
	candidate, err := staged.planner.candidates.Candidate(context.Background(), staged.queued.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func recoverProductionAdvanceFailure(t *testing.T, fixture productionTestFixture, staged productionStagedTreePromotionFixture, candidate localci.PromotionCandidate) {
	t.Helper()
	if err := staged.planner.authority.advanceTrustedRef(context.Background(), fixture.commit, candidate.TrustedCommit, candidate.SourceTree); err != nil {
		t.Fatal(err)
	}
	advanceErr := errors.New("injected trusted-ref advance uncertainty")
	if err := staged.planner.candidates.MarkAdvanceFailed(context.Background(), staged.queued.WorkloadID, advanceErr); !errors.Is(err, advanceErr) {
		t.Fatalf("MarkAdvanceFailed() error = %v", err)
	}
	restarted, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedProductionCandidate(context.Background(), restarted, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if tip := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}"); tip != fixture.commit {
		t.Fatalf("recovered trusted ref = %s, want %s", tip, fixture.commit)
	}
	replacement, err := staged.planner.PlanCandidate(context.Background(), staged.request)
	if err != nil || replacement.WorkloadID == staged.queued.WorkloadID {
		t.Fatalf("replacement PlanCandidate() = %+v, err=%v", replacement, err)
	}
}

func TestProductionCommitAndRangeCandidatesKeepExternalTrustedRefContract(t *testing.T) {
	for _, sourceKind := range []gatecontract.SourceKind{gatecontract.SourceKindCommit, gatecontract.SourceKindRange} {
		t.Run(string(sourceKind), func(t *testing.T) {
			candidate := planExternalTrustedRefCandidate(t, sourceKind)
			buildExternalTrustedRefCandidate(t, candidate)
			assertExternalTrustedRefPromotion(t, candidate)
		})
	}
}

type externalTrustedRefCandidateFixture struct {
	fixture         productionTestFixture
	planner         *productionCandidateSubmissionPlanner
	queued          localci.PromotionCandidatePlan
	candidateCommit string
}

func planExternalTrustedRefCandidate(t *testing.T, sourceKind gatecontract.SourceKind) externalTrustedRefCandidateFixture {
	t.Helper()
	fixture := newProductionTestFixture(t)
	candidateCommit, tree := commitProductionCandidate(t, fixture)
	source := productionPromotionSource(t, sourceKind, fixture.commit, candidateCommit, tree.Source.SourceTreeSHA)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, source)
	if err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "fetch", "-q", "--no-tags", "--", fixture.sourceRepo, candidateCommit)
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "update-ref", fixture.config.TrustedRef, candidateCommit, fixture.commit)
	planner, err := newProductionCandidateSubmissionPlanner(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := planner.PlanCandidate(context.Background(), imageEnsureRequest{
		RepositoryRoot: fixture.sourceRepo, Plan: plan, JobSourceTreeSHA: source.SourceTreeSHA,
	})
	if err != nil || !queued.BuildRequired {
		t.Fatalf("PlanCandidate(%s) = %+v, err=%v", sourceKind, queued, err)
	}
	assertExternalTrustedRefCandidate(t, planner, queued)
	return externalTrustedRefCandidateFixture{
		fixture: fixture, planner: planner, queued: queued, candidateCommit: candidateCommit,
	}
}

func assertExternalTrustedRefCandidate(t *testing.T, planner *productionCandidateSubmissionPlanner, queued localci.PromotionCandidatePlan) {
	t.Helper()
	candidate, err := planner.candidates.Candidate(context.Background(), queued.WorkloadID)
	if err != nil || candidate.PromotionMode != localci.PromotionCandidateModeExternalRef {
		t.Fatalf("external candidate = %+v, err=%v", candidate, err)
	}
}

func buildExternalTrustedRefCandidate(t *testing.T, candidate externalTrustedRefCandidateFixture) {
	t.Helper()
	promotion, completed := startProductionCandidateBuild(t, candidate.fixture.config, candidate.queued.WorkloadID)
	controller, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer, 5*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForProductionCandidateStatus(t, promotion.candidates, candidate.queued.WorkloadID, localci.PromotionCandidateAwaiting)
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if tip := productionGitLine(t, "", "--git-dir="+candidate.fixture.config.TrustedRepository, "rev-parse", candidate.fixture.config.TrustedRef+"^{commit}"); tip != candidate.candidateCommit {
		t.Fatalf("service rewrote external trusted ref: got %s, want %s", tip, candidate.candidateCommit)
	}
}

func assertExternalTrustedRefPromotion(t *testing.T, candidate externalTrustedRefCandidateFixture) {
	t.Helper()
	promotion, err := newProductionPromotionAuthority(context.Background(), candidate.fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := promotion.accepted.Load(context.Background())
	if err != nil || promoted.Generation != 2 || promoted.TrustedCommit != candidate.candidateCommit {
		t.Fatalf("external promotion = %+v, err=%v", promoted, err)
	}
}

func productionPromotionSource(t *testing.T, kind gatecontract.SourceKind, base, head, tree string) gatecontract.SourceSpec {
	t.Helper()
	source := gatecontract.SourceSpec{Kind: kind, ObjectFormat: gatecontract.GitObjectFormatSHA1, SourceTreeSHA: tree}
	switch kind {
	case gatecontract.SourceKindCommit:
		source.Commit = &gatecontract.CommitSource{SHA: head}
	case gatecontract.SourceKindRange:
		source.Range = &gatecontract.RangeSource{
			BaseKind: gatecontract.BaseKindCommit, BaseSHA: base, HeadSHA: head,
			LocalRef: "refs/heads/main", RemoteRef: "refs/heads/main", ObservedRemoteSHA: base,
			UpdateKind: gatecontract.UpdateKindFastForward,
		}
	default:
		t.Fatalf("unsupported external source kind %q", kind)
	}
	return source
}

// recoverProductionFailedCandidateBuild 模拟构建失败后旧协议已推进 ref 的重启恢复。
func recoverProductionFailedCandidateBuild(t *testing.T, fixture productionTestFixture, staged productionStagedTreePromotionFixture, candidate localci.PromotionCandidate) *productionPromotionAuthority {
	t.Helper()
	failingBuilder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{err: errors.New("broken pipe")})
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.planner.candidates.ExecuteBuild(context.Background(), staged.queued.WorkloadID, failingBuilder, productionCandidateIdentityResolverStub{}); err == nil {
		t.Fatal("ExecuteBuild() accepted a failed candidate build")
	}
	if err := staged.planner.authority.advanceTrustedRef(context.Background(), fixture.commit, candidate.TrustedCommit, candidate.SourceTree); err != nil {
		t.Fatal(err)
	}
	interrupted, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedProductionCandidate(context.Background(), interrupted, 20*time.Millisecond); err != nil {
		t.Fatalf("recoverInterruptedProductionCandidate(building) error = %v", err)
	}
	if tip := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}"); tip != fixture.commit {
		t.Fatalf("recovered failed build tip = %s, want accepted %s", tip, fixture.commit)
	}
	if _, err := interrupted.accepted.Load(context.Background()); err != nil {
		t.Fatalf("accepted.Load() after failed build recovery error = %v", err)
	}
	return interrupted
}

// recoverProductionAwaitingCandidate 证明 awaiting 与推进 ref 之间崩溃后重放同一入口可继续晋升。
func recoverProductionAwaitingCandidate(t *testing.T, fixture productionTestFixture, replacement localci.PromotionCandidatePlan, candidate localci.PromotionCandidate, interrupted *productionPromotionAuthority) {
	t.Helper()
	builder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{digest: productionDigest("8")})
	if err != nil {
		t.Fatal(err)
	}
	replayProductionAwaitingCandidateBuild(t, fixture, replacement, interrupted, builder)
	restarted, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedProductionCandidate(context.Background(), restarted, 20*time.Millisecond); err != nil {
		t.Fatalf("recoverInterruptedProductionCandidate(awaiting) error = %v", err)
	}
	promoted, err := restarted.accepted.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Generation != 2 || promoted.TrustedCommit != candidate.TrustedCommit || promoted.SourceTree != candidate.SourceTree {
		t.Fatalf("recovered accepted record = %+v", promoted)
	}
}

// replayProductionAwaitingCandidateBuild 模拟 scheduler 恢复已完成构建但尚未推进 ref 的 workload。
func replayProductionAwaitingCandidateBuild(t *testing.T, fixture productionTestFixture, replacement localci.PromotionCandidatePlan, interrupted *productionPromotionAuthority, builder *localci.ImageBuilder) {
	t.Helper()
	if err := interrupted.candidates.ExecuteBuild(context.Background(), replacement.WorkloadID, builder, productionCandidateIdentityResolverStub{}); err != nil {
		t.Fatal(err)
	}
	if tip := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}"); tip != fixture.commit {
		t.Fatalf("completeBuild advanced trusted ref: got %s, want %s", tip, fixture.commit)
	}
	candidate, err := interrupted.candidates.Candidate(context.Background(), replacement.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := interrupted.authority.advanceTrustedRef(
		context.Background(), fixture.commit, candidate.TrustedCommit, candidate.SourceTree,
	); err != nil {
		t.Fatal(err)
	}
}

type productionStagedTreePromotionFixture struct {
	source          gatecontract.SourceSpec
	stagedTree      string
	planner         *productionCandidateSubmissionPlanner
	request         imageEnsureRequest
	queued          localci.PromotionCandidatePlan
	candidateCommit string
}

func planProductionStagedTreePromotion(
	t *testing.T,
	fixture productionTestFixture,
) productionStagedTreePromotionFixture {
	t.Helper()
	changeProductionBuildInput(t, fixture.sourceRepo, "go.mod", "module example.invalid/staged-promotion\n")
	runProductionGit(t, fixture.sourceRepo, "add", "--", "go.mod", "build/gate/runtime-deps.lock")
	stagedTree := productionGitLine(t, fixture.sourceRepo, "write-tree")
	source := gatecontract.SourceSpec{
		Kind:         gatecontract.SourceKindTree,
		ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Tree: &gatecontract.TreeSource{
			SHA:             stagedTree,
			ParentCommitSHA: fixture.commit,
		},
		SourceTreeSHA: stagedTree,
	}
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, source)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := newProductionCandidateSubmissionPlanner(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	request := imageEnsureRequest{
		RepositoryRoot:   fixture.sourceRepo,
		Plan:             plan,
		JobSourceTreeSHA: stagedTree,
	}
	queued, err := planner.PlanCandidate(context.Background(), request)
	if err != nil || !queued.BuildRequired {
		t.Fatalf("PlanCandidate(staged tree) = %+v, err=%v", queued, err)
	}
	return productionStagedTreePromotionFixture{
		source:     source,
		stagedTree: stagedTree,
		planner:    planner,
		request:    request,
		queued:     queued,
	}
}

func assertProductionStagedTreeCandidate(
	t *testing.T,
	fixture productionTestFixture,
	staged *productionStagedTreePromotionFixture,
) {
	t.Helper()
	candidate, err := staged.planner.candidates.Candidate(context.Background(), staged.queued.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	candidateCommit := candidate.TrustedCommit
	if candidateCommit == fixture.commit {
		t.Fatal("staged-tree promotion did not create a distinct managed candidate commit")
	}
	if tree := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", candidateCommit+"^{tree}"); tree != staged.stagedTree {
		t.Fatalf("managed candidate tree = %s, want staged tree %s", tree, staged.stagedTree)
	}
	if parents := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-list", "--parents", "-n", "1", candidateCommit); parents != candidateCommit+" "+fixture.commit {
		t.Fatalf("managed candidate parents = %q", parents)
	}
	if sourceTip := productionGitLine(t, fixture.sourceRepo, "rev-parse", "refs/heads/main^{commit}"); sourceTip != fixture.commit {
		t.Fatalf("staged-tree promotion polluted source ref: got %s, want %s", sourceTip, fixture.commit)
	}
	if tip := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}"); tip != fixture.commit {
		t.Fatalf("candidate planning advanced trusted ref: got %s, want %s", tip, fixture.commit)
	}
	staged.candidateCommit = candidateCommit
}

func assertProductionStagedTreeRecovery(
	t *testing.T,
	fixture productionTestFixture,
	staged productionStagedTreePromotionFixture,
) {
	t.Helper()
	recovered, err := staged.planner.PlanCandidate(context.Background(), staged.request)
	if err != nil || !recovered.BuildRequired || recovered.WorkloadID != staged.queued.WorkloadID {
		t.Fatalf("PlanCandidate(staged tree recovery) = %+v, err=%v, first=%+v", recovered, err, staged.queued)
	}
	if tip := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}"); tip != fixture.commit {
		t.Fatalf("candidate planning changed trusted ref during recovery: got %s, want %s", tip, fixture.commit)
	}
}

// replacementProductionCandidate 要求失败终态绝不复用已终结的 scheduler workload。
func replacementProductionCandidate(t *testing.T, staged productionStagedTreePromotionFixture) (localci.PromotionCandidatePlan, localci.PromotionCandidate) {
	t.Helper()
	replacement, err := staged.planner.PlanCandidate(context.Background(), staged.request)
	if err != nil || !replacement.BuildRequired || replacement.WorkloadID == staged.queued.WorkloadID {
		t.Fatalf("replacement PlanCandidate() = %+v, err=%v, failed=%+v", replacement, err, staged.queued)
	}
	candidate, err := staged.planner.candidates.Candidate(context.Background(), replacement.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	return replacement, candidate
}

func promoteProductionStagedTreeCandidate(
	t *testing.T,
	fixture productionTestFixture,
	queued localci.PromotionCandidatePlan,
) (*productionPromotionAuthority, gatecontract.AcceptedImageRecord) {
	t.Helper()
	promotion, completed := startProductionCandidateBuild(t, fixture.config, queued.WorkloadID)
	controller, err := localci.NewPromotionController(promotion.candidates, promotion.state, promotion.authority, promotion.signer, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	waitForProductionCandidateStatus(t, promotion.candidates, queued.WorkloadID, localci.PromotionCandidateAwaiting)
	waitForProductionTrustedTip(t, fixture.config, productionCandidateTrustedCommit(t, promotion.candidates, queued.WorkloadID))
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	promoted, err := promotion.accepted.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return promotion, promoted
}

func productionCandidateTrustedCommit(
	t *testing.T,
	store *localci.PromotionCandidateStore,
	workloadID string,
) string {
	t.Helper()
	candidate, err := store.Candidate(context.Background(), workloadID)
	if err != nil {
		t.Fatal(err)
	}
	return candidate.TrustedCommit
}

func assertProductionStagedTreePromoted(
	t *testing.T,
	promoted gatecontract.AcceptedImageRecord,
	staged productionStagedTreePromotionFixture,
) {
	t.Helper()
	if promoted.Generation != 2 || promoted.TrustedCommit != staged.candidateCommit || promoted.SourceTree != staged.stagedTree {
		t.Fatalf("promoted staged-tree record = %+v", promoted)
	}
}

func assertProductionStagedTreeExecutable(
	t *testing.T,
	fixture productionTestFixture,
	promotion *productionPromotionAuthority,
	promoted gatecontract.AcceptedImageRecord,
	staged productionStagedTreePromotionFixture,
) {
	t.Helper()
	readOnlyTree, err := localci.LoadReadOnlyGitTree(context.Background(), fixture.sourceRepo, staged.source)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := localci.NewTruthImageEnsurer(promotion.accepted, promotion.candidates)
	if err != nil {
		t.Fatal(err)
	}
	result, err := truth.EnsureImage(context.Background(), localci.TruthImageEnsureRequest{
		Tree: readOnlyTree, PolicyDigest: promoted.PolicyDigest, Platform: fixture.config.Platform,
	})
	if err != nil || result.Status != localci.TruthImageEnsureAccepted ||
		result.AcceptedRecord.Generation != 2 || result.AcceptedRecord.SourceTree != staged.stagedTree {
		t.Fatalf("EnsureImage(staged tree after promotion) = %+v, err=%v", result, err)
	}
}
