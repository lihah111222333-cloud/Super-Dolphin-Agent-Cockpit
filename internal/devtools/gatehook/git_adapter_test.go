package gatehook

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestNormalizePreCommitUsesActiveLinkedWorktreeIndex(t *testing.T) {
	repository := newTestRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runTestGit(t, repository, "worktree", "add", "-b", "linked", linked, "HEAD")
	writeTestFile(t, linked, "tracked.txt", "staged linked worktree\n")
	runTestGit(t, linked, "add", "tracked.txt")

	request, err := NormalizePreCommit(context.Background(), linked, "hook-commit-1")
	if err != nil {
		t.Fatalf("NormalizePreCommit: %v", err)
	}
	canonicalLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatalf("EvalSymlinks linked worktree: %v", err)
	}
	expectedTree := strings.TrimSpace(runTestGit(t, linked, "write-tree"))
	expectedParent := strings.TrimSpace(runTestGit(t, linked, "rev-parse", "HEAD"))
	if request.Submit.Repository.WorktreeRoot != canonicalLinked {
		t.Fatalf("worktree root = %q, want %q", request.Submit.Repository.WorktreeRoot, canonicalLinked)
	}
	if request.Submit.Source.Tree.SHA != expectedTree || request.Submit.Source.Tree.ParentCommitSHA != expectedParent {
		t.Fatalf("source = %#v, want tree %s parent %s", request.Submit.Source, expectedTree, expectedParent)
	}
	replay, err := NormalizePreCommit(context.Background(), linked, "hook-commit-1")
	if err != nil {
		t.Fatalf("NormalizePreCommit replay: %v", err)
	}
	if replay.Submit.Invocation != request.Submit.Invocation {
		t.Fatalf("replay identity changed: %#v -> %#v", request.Submit.Invocation, replay.Submit.Invocation)
	}
}

func TestNormalizePrePushClassifiesExactUpdates(t *testing.T) {
	repository := newTestRepository(t)
	baseSHA := strings.TrimSpace(runTestGit(t, repository, "rev-parse", "HEAD"))
	headSHA := commitTestFile(t, repository, "main head\n", "主分支提交")
	forceBaseSHA := createDivergentCommit(t, repository, baseSHA)
	zeroOID, err := gatecontract.ZeroOID(gatecontract.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("ZeroOID: %v", err)
	}
	tests := []struct {
		name       string
		remoteSHA  string
		updateKind gatecontract.UpdateKind
		baseKind   gatecontract.BaseKind
	}{
		{name: "create", remoteSHA: zeroOID, updateKind: gatecontract.UpdateKindCreate, baseKind: gatecontract.BaseKindEmptyTree},
		{name: "fast forward", remoteSHA: baseSHA, updateKind: gatecontract.UpdateKindFastForward, baseKind: gatecontract.BaseKindCommit},
		{name: "force", remoteSHA: forceBaseSHA, updateKind: gatecontract.UpdateKindForce, baseKind: gatecontract.BaseKindCommit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := prePushFixture(t, headSHA, test.remoteSHA)
			requests, err := NormalizePrePush(context.Background(), repository, "hook-push-1-"+test.name, strings.NewReader(input))
			if err != nil {
				t.Fatalf("NormalizePrePush: %v", err)
			}
			rangeSource := requests[0].Submit.Source.Range
			if rangeSource.UpdateKind != test.updateKind || rangeSource.BaseKind != test.baseKind {
				t.Fatalf("range source = %#v", rangeSource)
			}
			if rangeSource.HeadSHA != headSHA || rangeSource.ObservedRemoteSHA != test.remoteSHA {
				t.Fatalf("range identity = %#v", rangeSource)
			}
		})
	}
}

func TestNormalizePrePushRejectsSHAFromAnotherWorktreeRepository(t *testing.T) {
	firstRepository := newTestRepository(t)
	foreignSHA := commitTestFile(t, firstRepository, "foreign\n", "外部提交")
	secondRepository := newTestRepository(t)
	secondBase := strings.TrimSpace(runTestGit(t, secondRepository, "rev-parse", "HEAD"))
	input := prePushFixture(t, foreignSHA, secondBase)
	_, err := NormalizePrePush(context.Background(), secondRepository, "hook-push-mismatch", strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "stdin supplied") {
		t.Fatalf("error = %v, want active ref mismatch", err)
	}
}

func TestPassedDecisionBlocksAfterWorktreeTreeDrift(t *testing.T) {
	repository := newTestRepository(t)
	_, source, err := CurrentWorktreeSource(context.Background(), repository)
	if err != nil {
		t.Fatalf("CurrentWorktreeSource initial: %v", err)
	}
	status := JobStatus{
		JobID: "job-drift", State: JobStatePassed, SourceTreeSHA: source.SourceTreeSHA, ReceiptID: "receipt-drift",
	}
	writeTestFile(t, repository, "tracked.txt", "changed after pass\n")
	_, currentSource, err := CurrentWorktreeSource(context.Background(), repository)
	if err != nil {
		t.Fatalf("CurrentWorktreeSource current: %v", err)
	}
	decision, err := DecisionForStatus(status, currentSource.SourceTreeSHA)
	if err != nil {
		t.Fatalf("DecisionForStatus: %v", err)
	}
	if decision.Decision != "block" || !strings.Contains(decision.Reason, "tree changed") {
		t.Fatalf("decision = %#v", decision)
	}
}

func createDivergentCommit(t *testing.T, repository, baseSHA string) string {
	t.Helper()
	runTestGit(t, repository, "branch", "force-base", baseSHA)
	runTestGit(t, repository, "checkout", "force-base")
	forceBaseSHA := commitTestFile(t, repository, "force base\n", "分叉提交")
	runTestGit(t, repository, "checkout", "main")
	return forceBaseSHA
}

func prePushFixture(t *testing.T, headSHA, baseSHA string) string {
	t.Helper()
	fixture := strings.ReplaceAll(loadFixture(t, "git/pre-push.txt"), "__HEAD__", headSHA)
	return strings.ReplaceAll(fixture, "__BASE__", baseSHA)
}
