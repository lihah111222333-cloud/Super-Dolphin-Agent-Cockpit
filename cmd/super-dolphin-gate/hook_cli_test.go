package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
)

func TestPreCommitHookBindsStagedIndexAndReturnsActionableQueuedStatus(t *testing.T) {
	repository := newHookTestRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "add", "tracked.txt")
	wantTree := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
	coordinator := &recordingHookCoordinator{queuePosition: 2}

	err := runHookWithConnector(
		[]string{"pre-commit", "--tree", wantTree}, bytes.NewReader(nil), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
	)
	if err == nil || !strings.Contains(err.Error(), "status: super-dolphin-gate status --job") ||
		!strings.Contains(err.Error(), "wait: super-dolphin-gate wait --job") {
		t.Fatalf("pre-commit error = %v", err)
	}
	if coordinator.lastSubmit.Source.SourceTreeSHA != wantTree {
		t.Fatalf("submitted tree = %q, want staged tree %q", coordinator.lastSubmit.Source.SourceTreeSHA, wantTree)
	}
	if coordinator.grantCount != 0 {
		t.Fatalf("pre-commit issued %d action grants", coordinator.grantCount)
	}
}

func TestPreCommitRejectsInitialStagedTreeCaptureFailure(t *testing.T) {
	repository := newHookTestRepository(t)
	gateBinDirectory, _ := installAlternateIndexGate(t, repository, "", "")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("locate real git: %v", err)
	}
	binDirectory := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	gitScript := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"if [[ $# -eq 1 && $1 == write-tree ]]; then\n" +
		"  printf '%s\\n' 'simulated write-tree failure' >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"exec \"${REAL_GIT:?}\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(binDirectory, "git"), []byte(gitScript), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("/bin/bash", filepath.Join(repository, ".githooks", "pre-commit"))
	command.Dir = repository
	command.Env = hookTestEnvironment(
		"REAL_GIT="+realGit,
		"PATH="+binDirectory+string(os.PathListSeparator)+gateBinDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("pre-commit succeeded after git write-tree failed")
	}
	if !strings.Contains(string(output), "simulated write-tree failure") {
		t.Fatalf("pre-commit did not reach the staged tree capture: %q", output)
	}
	if !strings.Contains(string(output), "pre-commit blocked: cannot capture the authoritative staged tree.") {
		t.Fatalf("pre-commit output = %q", output)
	}
}

func TestPreCommitAlternateIndexTreeStaysAuthoritativeThroughCommit(t *testing.T) {
	repository := newHookTestRepository(t)
	gateBinDirectory, treeLog := installAlternateIndexGate(t, repository, "", "")

	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tree A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alternateIndex := filepath.Join(t.TempDir(), "alternate.index")
	indexedEnvironment := []string{"GIT_INDEX_FILE=" + alternateIndex}
	runHookTestGitWithEnvironment(t, repository, indexedEnvironment, "read-tree", "HEAD")
	runHookTestGitWithEnvironment(t, repository, indexedEnvironment, "add", "tracked.txt")
	treeA := strings.TrimSpace(runHookTestGitWithEnvironment(t, repository, indexedEnvironment, "write-tree"))

	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tree B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "add", "tracked.txt")
	treeB := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
	if treeA == treeB {
		t.Fatal("alternate and default indexes unexpectedly resolved to the same tree")
	}

	commitEnvironment := append(indexedEnvironment,
		"GATE_TREE_LOG="+treeLog,
		"PATH="+gateBinDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	runHookTestGitWithEnvironment(t, repository, commitEnvironment, "commit", "-m", "alternate index gate contract")
	commitTree := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD^{tree}"))
	if commitTree != treeA {
		t.Fatalf("commit tree = %s, want alternate index tree %s", commitTree, treeA)
	}
	recorded, err := os.ReadFile(treeLog)
	if err != nil {
		t.Fatal(err)
	}
	want := "closure:" + treeA + "\ncontainer-source:" + treeA + "\nwait:" + treeA + "\n"
	if string(recorded) != want {
		t.Fatalf("gate tree chain = %q, want %q", recorded, want)
	}
	if strings.Contains(string(recorded), treeB) {
		t.Fatalf("default index tree %s was used by the gate chain: %s", treeB, recorded)
	}
}

func TestPreCommitRejectsAlternateIndexDriftAfterWait(t *testing.T) {
	repository := newHookTestRepository(t)
	initialTree := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD^{tree}"))
	gateBinDirectory, treeLog := installAlternateIndexGate(t, repository, "", "; git add tracked.txt; printf 'wait completed\\n'")
	indexedEnvironment, treeA := stageAlternateIndexTree(t, repository)

	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tree B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "add", "tracked.txt")
	treeB := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
	if treeA == treeB {
		t.Fatal("alternate and default indexes unexpectedly resolved to the same tree")
	}

	commitEnvironment := append(indexedEnvironment,
		"GATE_TREE_LOG="+treeLog,
		"PATH="+gateBinDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := runHookTestGitWithEnvironmentResult(
		repository,
		commitEnvironment,
		"commit", "-m", "alternate index drift contract",
	)
	if err == nil {
		t.Fatal("commit succeeded after fake wait changed the alternate index")
	}
	if !strings.Contains(output, "wait completed") || !strings.Contains(output, "staged tree changed during gate wait") {
		t.Fatalf("commit rejection output = %q", output)
	}
	commitTree := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD^{tree}"))
	if commitTree != initialTree {
		t.Fatalf("commit tree = %s, want unchanged HEAD tree %s", commitTree, initialTree)
	}
	recorded, err := os.ReadFile(treeLog)
	if err != nil {
		t.Fatal(err)
	}
	want := "closure:" + treeA + "\ncontainer-source:" + treeA + "\nwait:" + treeA + "\n"
	if string(recorded) != want {
		t.Fatalf("gate tree chain = %q, want %q", recorded, want)
	}
	if strings.Contains(string(recorded), treeB) {
		t.Fatalf("tree B %s was treated as passed: %s", treeB, recorded)
	}
}

func TestPreCommitRejectsAlternateIndexDriftAfterSynchronousPass(t *testing.T) {
	repository := newHookTestRepository(t)
	initialTree := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD^{tree}"))
	gateBinDirectory, treeLog := installAlternateIndexGate(t, repository, "git add tracked.txt; printf 'container-source:%s\\n' \"$4\" >> \"$GATE_TREE_LOG\"", "")
	indexedEnvironment, treeA := stageAlternateIndexTree(t, repository)

	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tree B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "add", "tracked.txt")
	treeB := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
	if treeA == treeB {
		t.Fatal("alternate and default indexes unexpectedly resolved to the same tree")
	}

	output, err := runHookTestGitWithEnvironmentResult(repository, append(indexedEnvironment,
		"GATE_TREE_LOG="+treeLog,
		"PATH="+gateBinDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
	), "commit", "-m", "synchronous alternate index drift contract")
	if err == nil {
		t.Fatal("commit succeeded after synchronous gate changed the alternate index")
	}
	if !strings.Contains(output, "staged tree changed during gate wait") {
		t.Fatalf("commit rejection output = %q", output)
	}
	if commitTree := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD^{tree}")); commitTree != initialTree {
		t.Fatalf("commit tree = %s, want unchanged HEAD tree %s", commitTree, initialTree)
	}
}

func TestPreCommitRejectsMaliciousPATHWithoutProvisionedLauncher(t *testing.T) {
	repository := newHookTestRepository(t)
	repositoryRoot := strings.TrimSpace(runHookTestGit(t, mustWorkingDirectory(t), "rev-parse", "--show-toplevel"))
	hooksDirectory := filepath.Join(repository, ".githooks")
	if err := os.Mkdir(hooksDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pre-commit", "trusted-gate-launcher.sh"} {
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, ".githooks", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(hooksDirectory, name), contents, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runHookTestGit(t, repository, "config", "core.hooksPath", hooksDirectory)
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "add", "tracked.txt")

	maliciousDirectory := t.TempDir()
	maliciousLog := filepath.Join(t.TempDir(), "malicious.log")
	maliciousGate := filepath.Join(maliciousDirectory, "super-dolphin-gate")
	if err := os.WriteFile(maliciousGate, []byte("#!/usr/bin/env bash\nprintf invoked > \"$MALICIOUS_GATE_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := runHookTestGitWithEnvironmentResult(repository, []string{
		"MALICIOUS_GATE_LOG=" + maliciousLog,
		"PATH=" + maliciousDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "commit", "-m", "malicious path contract")
	if err == nil || !strings.Contains(output, "no trusted launcher is provisioned") {
		t.Fatalf("malicious PATH result error=%v output=%q", err, output)
	}
	if _, err := os.Stat(maliciousLog); !os.IsNotExist(err) {
		t.Fatalf("malicious PATH launcher was executed: %v", err)
	}
}

func installAlternateIndexGate(t *testing.T, repository, hookCommand, waitCommand string) (string, string) {
	t.Helper()
	repositoryRoot := strings.TrimSpace(runHookTestGit(t, mustWorkingDirectory(t), "rev-parse", "--show-toplevel"))
	hooksDirectory := filepath.Join(repository, ".githooks")
	if err := os.Mkdir(hooksDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	preCommit, err := os.ReadFile(filepath.Join(repositoryRoot, ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDirectory, "pre-commit"), preCommit, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher, err := os.ReadFile(filepath.Join(repositoryRoot, ".githooks", "trusted-gate-launcher.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDirectory, "trusted-gate-launcher.sh"), launcher, 0o700); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "config", "core.hooksPath", hooksDirectory)

	gateBinDirectory := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(gateBinDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	treeLog := filepath.Join(t.TempDir(), "trees.log")
	gateBin := filepath.Join(gateBinDirectory, "super-dolphin-gate")
	if hookCommand == "" {
		hookCommand = "printf 'container-source:%s\\n' \"$4\" >> \"$GATE_TREE_LOG\"; printf 'queued job=job-0123456789abcdef0123456789abcdef\\n'; exit 13"
	}
	gateScript := "#!/usr/bin/env bash\nset -euo pipefail\ncase \"$1\" in\n" +
		"  closure) case \"$2\" in check|refresh|refresh-dependencies) printf 'closure:%s\\n' \"$4\" >> \"$GATE_TREE_LOG\" ;; *) exit 64 ;; esac ;;\n" +
		"  frontend-code-size) exit 0 ;;\n" +
		"  project-map) exit 0 ;;\n" +
		"  codemap) exit 0 ;;\n" +
		"  hook) " + hookCommand + " ;;\n" +
		"  wait) printf 'wait:%s\\n' \"$5\" >> \"$GATE_TREE_LOG\"" + waitCommand + " ;;\n" +
		"  *) exit 64 ;;\nesac\n"
	if err := os.WriteFile(gateBin, []byte(gateScript), 0o700); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "config", "superdolphin.gateLauncher", gateBin)
	return gateBinDirectory, treeLog
}

func stageAlternateIndexTree(t *testing.T, repository string) ([]string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("tree A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alternateIndex := filepath.Join(t.TempDir(), "alternate.index")
	indexedEnvironment := []string{"GIT_INDEX_FILE=" + alternateIndex}
	runHookTestGitWithEnvironment(t, repository, indexedEnvironment, "read-tree", "HEAD")
	runHookTestGitWithEnvironment(t, repository, indexedEnvironment, "add", "tracked.txt")
	return indexedEnvironment, strings.TrimSpace(runHookTestGitWithEnvironment(t, repository, indexedEnvironment, "write-tree"))
}

func TestClosureVerifierIgnoresUnavailableWorktreeGeneratorSource(t *testing.T) {
	repository := strings.TrimSpace(runHookTestGit(t, mustWorkingDirectory(t), "rev-parse", "--show-toplevel"))
	candidateIndex := filepath.Join(t.TempDir(), "candidate.index")
	candidateEnvironment := []string{"GIT_INDEX_FILE=" + candidateIndex}
	runHookTestGitWithEnvironment(t, repository, candidateEnvironment, "read-tree", "HEAD")
	runHookTestGitWithEnvironment(t, repository, candidateEnvironment, "add", "-A")
	tree := strings.TrimSpace(
		runHookTestGitWithEnvironment(t, repository, candidateEnvironment, "write-tree"),
	)
	generator := filepath.Join(repository, "build", "gate", "cmd", "generate-closure", "main.go")
	backup := generator + ".closure-test-backup"
	if err := os.Rename(generator, backup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(backup, generator) })
	if err := gateclosure.CheckTree(repository, tree); err != nil {
		t.Fatalf("compiled closure verifier depended on unavailable worktree generator: %v", err)
	}
	hook, err := os.ReadFile(filepath.Join(repository, ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(hook, []byte("go run")) {
		t.Fatal("thin pre-commit hook executes go run")
	}
}

func TestPreCommitBootstrapsOnlyCompleteFirstFrontendParserClosure(t *testing.T) {
	repository := strings.TrimSpace(runHookTestGit(t, mustWorkingDirectory(t), "rev-parse", "--show-toplevel"))
	hook, err := os.ReadFile(filepath.Join(repository, ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`cat-file -e "$accepted_frontend_tree:$frontend_code_size_closure"`,
		`accepted_frontend_manifest" -ne "$accepted_frontend_generator`,
		`cat-file -e "$staged_tree:$frontend_code_size_closure"`,
		`bootstrapping the first candidate-bound frontend parser closure`,
		`frontend_code_size_dependency_migration_requested=1`,
	} {
		if !bytes.Contains(hook, []byte(fragment)) {
			t.Fatalf("pre-commit is missing first parser closure bootstrap contract %q", fragment)
		}
	}
}

func TestClosureCheckRequiresExplicitStagedTree(t *testing.T) {
	err := runClosureCheck([]string{"check"})
	if err == nil || !strings.Contains(err.Error(), "requires one --tree") {
		t.Fatalf("runClosureCheck() error = %v", err)
	}
}

func TestParseClosureCheckArgsAcceptsOnlyRegisteredActions(t *testing.T) {
	for _, action := range []string{"check", "refresh", "refresh-dependencies"} {
		gotAction, tree, err := parseClosureCheckArgs([]string{action, "--tree", "abc123"})
		if err != nil || gotAction != action || tree != "abc123" {
			t.Fatalf("parseClosureCheckArgs(%q) = (%q, %q, %v)", action, gotAction, tree, err)
		}
	}
	if _, _, err := parseClosureCheckArgs([]string{"unknown", "--tree", "abc123"}); err == nil {
		t.Fatal("parseClosureCheckArgs accepted an unregistered action")
	}
}

func TestGitHookActionsWithSameTreeUseDistinctDeliveryInvocations(t *testing.T) {
	repository := newHookTestRepository(t)
	tree := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
	coordinator := &recordingHookCoordinator{}
	for range 2 {
		if err := runHookWithConnector(
			[]string{"pre-commit", "--tree", tree}, bytes.NewReader(nil), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
		); err == nil || !strings.Contains(err.Error(), "status: super-dolphin-gate status --job") {
			t.Fatalf("runHookWithConnector() error = %v", err)
		}
	}
	if len(coordinator.submits) != 2 || coordinator.submits[0].Invocation == coordinator.submits[1].Invocation {
		t.Fatalf("same-tree hook actions reused invocation identities: %#v", coordinator.submits)
	}
}

func TestPrePushHookRejectsForgedOIDBeforeCoordinatorSubmit(t *testing.T) {
	repository := newHookTestRepository(t)
	head := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD"))
	input := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", strings.Repeat("f", len(head)), strings.Repeat("0", len(head)))
	coordinator := &recordingHookCoordinator{queuePosition: 1}

	err := runHookWithConnector(
		[]string{"pre-push", "origin", "file://" + repository}, strings.NewReader(input), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
	)
	if err == nil || !strings.Contains(err.Error(), "stdin supplied") {
		t.Fatalf("pre-push error = %v", err)
	}
	if coordinator.submitCount != 0 {
		t.Fatalf("coordinator submit count = %d", coordinator.submitCount)
	}
}

func TestPrePushHookSubmitsEveryVerifiedRef(t *testing.T) {
	repository := newHookTestRepository(t)
	head := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD"))
	runHookTestGit(t, repository, "branch", "topic", head)
	zero := strings.Repeat("0", len(head))
	input := fmt.Sprintf(
		"refs/heads/main %s refs/heads/main %s\nrefs/heads/topic %s refs/heads/topic %s\n",
		head, zero, head, zero,
	)
	coordinator := &recordingHookCoordinator{passWithReceipt: true}
	stdout := &bytes.Buffer{}

	err := runHookWithConnector(
		[]string{"pre-push", "origin", "file://" + repository}, strings.NewReader(input), stdout, repository, hookTestConnector(coordinator),
	)
	if err != nil {
		t.Fatalf("pre-push error = %v", err)
	}
	if coordinator.submitCount != 2 {
		t.Fatalf("coordinator submit count = %d, want 2", coordinator.submitCount)
	}
	if coordinator.grantCount != 2 {
		t.Fatalf("action grant count = %d, want 2", coordinator.grantCount)
	}
	if len(coordinator.grantRequests) != 2 ||
		coordinator.grantRequests[0].ActionAttemptID != coordinator.grantRequests[1].ActionAttemptID {
		t.Fatalf("multi-ref hook did not share one action attempt: %#v", coordinator.grantRequests)
	}
	if err := gatecontract.ValidateActionAttemptID(coordinator.grantRequests[0].ActionAttemptID); err != nil {
		t.Fatalf("pre-push action attempt is not high entropy: %v", err)
	}
	if strings.Count(stdout.String(), "gate hook passed: job=job-passed; receipt=receipt-valid;") != 2 ||
		!strings.Contains(stdout.String(), "status: super-dolphin-gate status --job job-passed") {
		t.Fatalf("pre-push passed evidence = %q", stdout.String())
	}
}

func TestPrePushHookWaitsQueuedJobWithinSameInvocation(t *testing.T) {
	repository := newHookTestRepository(t)
	head := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD"))
	zero := strings.Repeat("0", len(head))
	input := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", head, zero)
	coordinator := &recordingHookCoordinator{queuePosition: 1, waitWithReceipt: true}

	if err := runHookWithConnector(
		[]string{"pre-push", "origin", "file://" + repository}, strings.NewReader(input), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
	); err != nil {
		t.Fatalf("pre-push error = %v", err)
	}
	if coordinator.submitCount != 1 || coordinator.waitCount != 1 || coordinator.grantCount != 1 {
		t.Fatalf("pre-push calls submit=%d wait=%d grant=%d", coordinator.submitCount, coordinator.waitCount, coordinator.grantCount)
	}
	if coordinator.lastWait.JobID != "job-queued" ||
		coordinator.lastWait.Repository != coordinator.lastSubmit.Repository ||
		coordinator.lastWait.Invocation != coordinator.lastSubmit.Invocation {
		t.Fatalf("wait request did not bind submitted invocation: %#v", coordinator.lastWait)
	}
}

func TestPrePushHookNewAttemptAfterPartialGrantFailure(t *testing.T) {
	repository := newHookTestRepository(t)
	head := strings.TrimSpace(runHookTestGit(t, repository, "rev-parse", "HEAD"))
	runHookTestGit(t, repository, "branch", "topic", head)
	zero := strings.Repeat("0", len(head))
	input := fmt.Sprintf(
		"refs/heads/main %s refs/heads/main %s\nrefs/heads/topic %s refs/heads/topic %s\n",
		head, zero, head, zero,
	)
	coordinator := &recordingHookCoordinator{passWithReceipt: true, failGrantAt: 2}
	args := []string{"pre-push", "origin", "file://" + repository}
	if err := runHookWithConnector(
		args, strings.NewReader(input), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
	); err == nil || !strings.Contains(err.Error(), "injected grant failure") {
		t.Fatalf("first pre-push error = %v", err)
	}
	if len(coordinator.grantRequests) != 2 ||
		coordinator.grantRequests[0].ActionAttemptID != coordinator.grantRequests[1].ActionAttemptID {
		t.Fatalf("partial hook attempt boundary = %#v", coordinator.grantRequests)
	}
	failedAttempt := coordinator.grantRequests[0].ActionAttemptID

	coordinator.failGrantAt = 0
	if err := runHookWithConnector(
		args, strings.NewReader(input), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
	); err != nil {
		t.Fatalf("new pre-push attempt failed: %v", err)
	}
	if len(coordinator.grantRequests) != 4 {
		t.Fatalf("grant requests = %d, want 4", len(coordinator.grantRequests))
	}
	newAttempt := coordinator.grantRequests[2].ActionAttemptID
	if newAttempt == failedAttempt || newAttempt != coordinator.grantRequests[3].ActionAttemptID {
		t.Fatalf("new multi-ref attempt boundary failed=%q new=%q final=%q", failedAttempt, newAttempt, coordinator.grantRequests[3].ActionAttemptID)
	}
}

func TestGitHookInvalidQueuedStatusIncludesJobActions(t *testing.T) {
	status := gatehook.JobStatus{
		JobID: "job-invalid", State: gatehook.JobStateQueued,
		SourceTreeSHA: strings.Repeat("a", 40), QueuePosition: 0,
	}
	err := gitHookDecision(status, status.SourceTreeSHA, nil)
	if err == nil || !strings.Contains(err.Error(), "queued job requires positive queue_position") ||
		!strings.Contains(err.Error(), "status: super-dolphin-gate status --job job-invalid") ||
		!strings.Contains(err.Error(), "wait: super-dolphin-gate wait --job job-invalid") {
		t.Fatalf("invalid queued status error = %v", err)
	}
}

type recordingHookCoordinator struct {
	lastSubmit      gatehook.SubmitRequest
	submits         []gatehook.SubmitRequest
	submitCount     int
	statusCount     int
	queuePosition   int
	passWithReceipt bool
	waitWithReceipt bool
	waitCount       int
	lastWait        gatehook.WaitRequest
	grantCount      int
	failGrantAt     int
	grantRequests   []gitPushGrantRequest
}

func (coordinator *recordingHookCoordinator) Submit(
	_ context.Context,
	request gatehook.SubmitRequest,
) (gatehook.JobStatus, error) {
	coordinator.lastSubmit = request
	coordinator.submits = append(coordinator.submits, request)
	coordinator.submitCount++
	return coordinator.status(request.Source.SourceTreeSHA), nil
}

func (coordinator *recordingHookCoordinator) Status(
	_ context.Context,
	request gatehook.StatusRequest,
) (gatehook.JobStatus, error) {
	coordinator.statusCount++
	return coordinator.status(request.ExpectedSourceTreeSHA), nil
}

func (coordinator *recordingHookCoordinator) Wait(
	_ context.Context,
	request gatehook.WaitRequest,
) (gatehook.JobStatus, error) {
	coordinator.waitCount++
	coordinator.lastWait = request
	if coordinator.waitWithReceipt {
		return gatehook.JobStatus{
			JobID: request.JobID, State: gatehook.JobStatePassed,
			SourceTreeSHA: coordinator.lastSubmit.Source.SourceTreeSHA, ReceiptID: "receipt-valid",
		}, nil
	}
	return gatehook.JobStatus{}, fmt.Errorf("unexpected wait")
}

func (*recordingHookCoordinator) Close() error { return nil }

func (coordinator *recordingHookCoordinator) AuthorizeGitPush(
	_ context.Context,
	request gitPushGrantRequest,
) error {
	if request.Status.State != gatehook.JobStatePassed || request.Submit.Source.Range == nil || request.RemoteURL == "" {
		return fmt.Errorf("invalid git.push grant request")
	}
	coordinator.grantCount++
	coordinator.grantRequests = append(coordinator.grantRequests, request)
	if coordinator.failGrantAt == coordinator.grantCount {
		return fmt.Errorf("injected grant failure")
	}
	return nil
}

func (coordinator *recordingHookCoordinator) status(tree string) gatehook.JobStatus {
	if coordinator.passWithReceipt {
		return gatehook.JobStatus{
			JobID: "job-passed", State: gatehook.JobStatePassed,
			SourceTreeSHA: tree, ReceiptID: "receipt-valid",
		}
	}
	return gatehook.JobStatus{
		JobID: "job-queued", State: gatehook.JobStateQueued,
		QueuePosition: coordinator.queuePosition, SourceTreeSHA: tree,
	}
}

func hookTestConnector(coordinator hookCoordinator) hookCoordinatorConnector {
	return func(context.Context) (hookCoordinator, error) { return coordinator, nil }
}

func newHookTestRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runHookTestGit(t, repository, "init", "-b", "main")
	runHookTestGit(t, repository, "config", "user.name", "Hook Test")
	runHookTestGit(t, repository, "config", "user.email", "hook@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHookTestGit(t, repository, "add", "tracked.txt")
	runHookTestGit(t, repository, "commit", "-m", "初始提交")
	return repository
}

func runHookTestGit(t *testing.T, directory string, args ...string) string {
	return runHookTestGitWithEnvironment(t, directory, nil, args...)
}

func runHookTestGitWithEnvironment(t *testing.T, directory string, extraEnvironment []string, args ...string) string {
	t.Helper()
	output, err := runHookTestGitWithEnvironmentResult(directory, extraEnvironment, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return output
}

func runHookTestGitWithEnvironmentResult(directory string, extraEnvironment []string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = hookTestEnvironment(extraEnvironment...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func hookTestEnvironment(extraEnvironment ...string) []string {
	overridden := map[string]struct{}{"GIT_CONFIG_NOSYSTEM": {}, "GIT_INDEX_FILE": {}}
	for _, entry := range extraEnvironment {
		key, _, found := strings.Cut(entry, "=")
		if found {
			overridden[key] = struct{}{}
		}
	}
	environment := make([]string, 0, len(os.Environ())+len(extraEnvironment)+1)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, ok := overridden[key]; ok {
				continue
			}
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GIT_CONFIG_NOSYSTEM=1")
	return append(environment, extraEnvironment...)
}
