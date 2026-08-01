package gatehook

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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

	expectedTree := strings.TrimSpace(runTestGit(t, linked, "write-tree"))
	request, err := NormalizePreCommit(context.Background(), linked, "hook-commit-1", expectedTree)
	if err != nil {
		t.Fatalf("NormalizePreCommit: %v", err)
	}
	canonicalLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatalf("EvalSymlinks linked worktree: %v", err)
	}
	expectedParent := strings.TrimSpace(runTestGit(t, linked, "rev-parse", "HEAD"))
	if request.Submit.Repository.WorktreeRoot != canonicalLinked {
		t.Fatalf("worktree root = %q, want %q", request.Submit.Repository.WorktreeRoot, canonicalLinked)
	}
	if request.Submit.Source.Tree.SHA != expectedTree || request.Submit.Source.Tree.ParentCommitSHA != expectedParent {
		t.Fatalf("source = %#v, want tree %s parent %s", request.Submit.Source, expectedTree, expectedParent)
	}
	replay, err := NormalizePreCommit(context.Background(), linked, "hook-commit-1", expectedTree)
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

func TestNormalizePrePushAcceptsLocalRefOutsideActiveWorktree(t *testing.T) {
	repository := newTestRepository(t)
	baseSHA := strings.TrimSpace(runTestGit(t, repository, "rev-parse", "HEAD"))
	headSHA := commitTestFile(t, repository, "active worktree head\n", "活动工作树提交")
	runTestGit(t, repository, "branch", "push-candidate", baseSHA)
	zeroOID, err := gatecontract.ZeroOID(gatecontract.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("ZeroOID: %v", err)
	}
	input := strings.Join([]string{"refs/heads/push-candidate", baseSHA, "refs/heads/main", zeroOID}, " ") + "\n"

	requests, err := NormalizePrePush(context.Background(), repository, "hook-push-detached-ref", strings.NewReader(input))
	if err != nil {
		t.Fatalf("NormalizePrePush rejected non-HEAD local ref: %v", err)
	}
	rangeSource := requests[0].Submit.Source.Range
	if rangeSource.HeadSHA != baseSHA {
		t.Fatalf("range head = %s, want pushed local ref %s; active HEAD is %s", rangeSource.HeadSHA, baseSHA, headSHA)
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

func TestParsePrePushUpdateRejectsNonCanonicalOIDBeforeGit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "git-called")
	gitDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir fake git bin: %v", err)
	}
	fakeGit := filepath.Join(gitDir, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\nprintf x > \"$GATEHOOK_GIT_MARKER\"\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", gitDir)
	t.Setenv("GATEHOOK_GIT_MARKER", marker)
	repository := gitRepository{identity: RepositoryIdentity{ObjectFormat: gatecontract.GitObjectFormatSHA1}}
	validOID := strings.Repeat("a", 40)
	tests := []struct {
		name      string
		localSHA  string
		remoteSHA string
	}{
		{name: "argument remote", localSHA: validOID, remoteSHA: "--help"},
		{name: "uppercase local", localSHA: strings.ToUpper(validOID), remoteSHA: validOID},
		{name: "uppercase remote", localSHA: validOID, remoteSHA: strings.ToUpper(validOID)},
		{name: "wrong local length", localSHA: validOID[:39], remoteSHA: validOID},
		{name: "wrong remote length", localSHA: validOID, remoteSHA: validOID[:39]},
		{name: "non hex remote", localSHA: validOID, remoteSHA: strings.Repeat("g", 40)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
				t.Fatalf("remove marker: %v", err)
			}
			line := strings.Join([]string{"refs/heads/main", test.localSHA, "refs/heads/main", test.remoteSHA}, " ")
			if _, _, err := parsePrePushUpdate(context.Background(), repository, line); err == nil {
				t.Fatal("parsePrePushUpdate accepted non-canonical OID")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("Git was invoked before OID rejection: %v", err)
			}
		})
	}
}

func TestParsePrePushUpdateAcceptsCanonicalSHA256OIDs(t *testing.T) {
	repositoryPath := newTestRepositoryWithObjectFormat(t, "sha256")
	repository, err := resolveGitRepository(context.Background(), repositoryPath)
	if err != nil {
		t.Fatalf("resolveGitRepository: %v", err)
	}
	headSHA := strings.TrimSpace(runTestGit(t, repositoryPath, "rev-parse", "HEAD"))
	zeroOID, err := gatecontract.ZeroOID(gatecontract.GitObjectFormatSHA256)
	if err != nil {
		t.Fatalf("ZeroOID: %v", err)
	}
	line := strings.Join([]string{"refs/heads/main", headSHA, "refs/heads/main", zeroOID}, " ")
	update, gotZeroOID, err := parsePrePushUpdate(context.Background(), repository, line)
	if err != nil {
		t.Fatalf("parsePrePushUpdate: %v", err)
	}
	if len(update.localSHA) != 64 || update.remoteSHA != zeroOID || gotZeroOID != zeroOID {
		t.Fatalf("SHA-256 update = %#v zero=%q", update, gotZeroOID)
	}
	wrongLength := strings.Repeat("a", 40)
	line = strings.Join([]string{"refs/heads/main", headSHA, "refs/heads/main", wrongLength}, " ")
	if _, _, err := parsePrePushUpdate(context.Background(), repository, line); err == nil {
		t.Fatal("parsePrePushUpdate accepted SHA-1 width in SHA-256 repository")
	}
}

func TestVerifyActiveWorktreeIgnoresMissingUnrelatedWorktree(t *testing.T) {
	repository := newTestRepository(t)
	missing := filepath.Join(t.TempDir(), "missing-worktree")
	runTestGit(t, repository, "worktree", "add", "-b", "missing-worktree", missing, "HEAD")
	if err := os.RemoveAll(missing); err != nil {
		t.Fatalf("remove unrelated worktree: %v", err)
	}
	inventory := runTestGit(t, repository, "worktree", "list", "--porcelain")
	if !strings.Contains(inventory, missing) {
		t.Fatalf("fixture no longer contains missing worktree:\n%s", inventory)
	}
	if _, _, err := CurrentWorktreeSource(context.Background(), repository); err != nil {
		t.Fatalf("CurrentWorktreeSource rejected valid worktree: %v", err)
	}
}

func TestCountTargetWorktreeRejectsMissingAndDuplicateTarget(t *testing.T) {
	target := "/repo/current"
	if count, err := countTargetWorktree("worktree /repo/other\n\n", target); err != nil || count != 0 {
		t.Fatalf("missing target count=%d err=%v", count, err)
	}
	if err := validateTargetWorktreeCount(0, target); err == nil {
		t.Fatal("missing target was accepted")
	}
	duplicate := "worktree " + target + "\n\nworktree " + target + "\n\n"
	if count, err := countTargetWorktree(duplicate, target); err != nil || count != 2 {
		t.Fatalf("duplicate target count=%d err=%v", count, err)
	}
	if err := validateTargetWorktreeCount(2, target); err == nil {
		t.Fatal("duplicate target was accepted")
	}
}

func TestCountTargetWorktreeDecodesQuotedPath(t *testing.T) {
	target := "/repo/line\nbreak"
	output := "worktree " + strconv.Quote(target) + "\n\n"
	if count, err := countTargetWorktree(output, target); err != nil || count != 1 {
		t.Fatalf("quoted target count=%d err=%v", count, err)
	}
	if _, err := countTargetWorktree(strings.TrimRight(output, "\n"), target); err == nil {
		t.Fatal("unterminated worktree inventory was accepted")
	}
}

func TestSanitizedGitEnvironmentBindsRepositoryToCWD(t *testing.T) {
	cwdRepository := newTestRepository(t)
	hostileRepository := newTestRepository(t)
	cwdHead := commitTestFile(t, cwdRepository, "cwd repository\n", "当前仓库提交")
	hostileHead := commitTestFile(t, hostileRepository, "hostile repository\n", "污染仓库提交")
	t.Setenv("GIT_DIR", filepath.Join(hostileRepository, ".git"))
	t.Setenv("GIT_WORK_TREE", hostileRepository)
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(hostileRepository, ".git", "objects"))
	identity, source, err := CurrentWorktreeSource(context.Background(), cwdRepository)
	if err != nil {
		t.Fatalf("CurrentWorktreeSource: %v", err)
	}
	canonicalCWD, err := filepath.EvalSymlinks(cwdRepository)
	if err != nil {
		t.Fatalf("EvalSymlinks cwd repository: %v", err)
	}
	if identity.WorktreeRoot != canonicalCWD || source.Tree.ParentCommitSHA != cwdHead {
		t.Fatalf("resolved hostile repository: identity=%#v source=%#v", identity, source)
	}
	if source.Tree.ParentCommitSHA == hostileHead {
		t.Fatal("hostile Git environment replaced cwd repository HEAD")
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
