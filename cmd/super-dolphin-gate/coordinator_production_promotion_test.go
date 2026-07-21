package main

import (
	"context"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

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
	candidateCommit := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}")
	if candidateCommit == fixture.commit {
		t.Fatal("staged-tree promotion did not advance the managed trusted ref")
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
	if tip := productionGitLine(t, "", "--git-dir="+fixture.config.TrustedRepository, "rev-parse", fixture.config.TrustedRef+"^{commit}"); tip != staged.candidateCommit {
		t.Fatalf("managed trusted ref changed during recovery: got %s, want %s", tip, staged.candidateCommit)
	}
}

func promoteProductionStagedTreeCandidate(
	t *testing.T,
	fixture productionTestFixture,
	queued localci.PromotionCandidatePlan,
) (*productionPromotionAuthority, gatecontract.AcceptedImageRecord) {
	t.Helper()
	promotion, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{digest: productionDigest("8")})
	if err != nil {
		t.Fatal(err)
	}
	service := productionCandidateBuildService{store: promotion.candidates, builder: builder, resolver: productionCandidateIdentityResolverStub{}}
	if err := service.ExecuteBuild(context.Background(), queued.WorkloadID); err != nil {
		t.Fatal(err)
	}
	controller, err := localci.NewPromotionController(promotion.candidates, promotion.state, promotion.authority, promotion.signer, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	promoted, err := promotion.accepted.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return promotion, promoted
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
