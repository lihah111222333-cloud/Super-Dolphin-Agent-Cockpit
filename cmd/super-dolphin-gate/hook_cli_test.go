package main

import (
	"bytes"
	"context"
	"encoding/json"
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
		[]string{"pre-commit"}, bytes.NewReader(nil), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
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

func TestClosureVerifierIgnoresUnavailableWorktreeGeneratorSource(t *testing.T) {
	repository := strings.TrimSpace(runHookTestGit(t, mustWorkingDirectory(t), "rev-parse", "--show-toplevel"))
	tree := strings.TrimSpace(runHookTestGit(t, repository, "write-tree"))
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

func TestGitHookActionsWithSameTreeUseDistinctDeliveryInvocations(t *testing.T) {
	repository := newHookTestRepository(t)
	coordinator := &recordingHookCoordinator{}
	for range 2 {
		if err := runHookWithConnector(
			[]string{"pre-commit"}, bytes.NewReader(nil), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
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

func TestPreCommitAndCodexPassedEvidenceBindsReceiptAndStatus(t *testing.T) {
	repository := newHookTestRepository(t)
	coordinator := &recordingHookCoordinator{passWithReceipt: true}
	gitOutput := &bytes.Buffer{}
	if err := runHookWithConnector(
		[]string{"pre-commit"}, bytes.NewReader(nil), gitOutput, repository, hookTestConnector(coordinator),
	); err != nil {
		t.Fatalf("pre-commit error = %v", err)
	}
	for _, want := range []string{
		"job=job-passed", "receipt=receipt-valid", "source_tree=", "status: super-dolphin-gate status --job job-passed",
	} {
		if !strings.Contains(gitOutput.String(), want) {
			t.Fatalf("pre-commit passed evidence missing %q: %q", want, gitOutput.String())
		}
	}

	codexOutput := &bytes.Buffer{}
	if err := runHookWithConnector(
		[]string{"codex"}, strings.NewReader(codexHookPayload(repository, false)), codexOutput,
		repository, hookTestConnector(coordinator),
	); err != nil {
		t.Fatalf("Codex hook error = %v", err)
	}
	var decision gatehook.CodexDecision
	if err := json.Unmarshal(codexOutput.Bytes(), &decision); err != nil {
		t.Fatalf("Codex decision JSON error = %v: %s", err, codexOutput.Bytes())
	}
	if !decision.Continue || decision.Decision != "" {
		t.Fatalf("Codex passed decision = %#v", decision)
	}
	for _, want := range []string{
		"job=job-passed", "receipt=receipt-valid", "source_tree=", "status: super-dolphin-gate status --job job-passed",
	} {
		if !strings.Contains(decision.Reason, want) {
			t.Fatalf("Codex passed evidence missing %q: %#v", want, decision)
		}
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

func TestCodexHookQueuedAndMaliciousInputAlwaysEmitJSON(t *testing.T) {
	repository := newHookTestRepository(t)
	coordinator := &recordingHookCoordinator{queuePosition: 3}
	payload := codexHookPayload(repository, false)
	stdout := &bytes.Buffer{}
	if err := runHookWithConnector(
		[]string{"codex"}, strings.NewReader(payload), stdout, repository, hookTestConnector(coordinator),
	); err != nil {
		t.Fatalf("runHookWithConnector() error = %v", err)
	}
	assertBlockedCodexJSON(t, stdout.Bytes(), "queue_position=3")
	if coordinator.grantCount != 0 {
		t.Fatalf("Codex hook issued %d action grants", coordinator.grantCount)
	}

	stdout.Reset()
	malicious := strings.TrimSuffix(payload, "}\n") + `,"unknown":"\"}\nnot-json"}` + "\n"
	if err := runHookWithConnector(
		[]string{"codex"}, strings.NewReader(malicious), stdout, repository, hookTestConnector(coordinator),
	); err != nil {
		t.Fatalf("malicious hook error = %v", err)
	}
	assertBlockedCodexJSON(t, stdout.Bytes(), "unknown field")
}

func TestCodexRecursiveStopUsesInvocationStatus(t *testing.T) {
	repository := newHookTestRepository(t)
	coordinator := &recordingHookCoordinator{queuePosition: 1}
	stdout := &bytes.Buffer{}
	err := runHookWithConnector(
		[]string{"codex"}, strings.NewReader(codexHookPayload(repository, true)), stdout,
		repository, hookTestConnector(coordinator),
	)
	if err != nil {
		t.Fatalf("recursive hook error = %v", err)
	}
	if coordinator.statusCount != 1 || coordinator.submitCount != 0 {
		t.Fatalf("status=%d submit=%d", coordinator.statusCount, coordinator.submitCount)
	}
	assertBlockedCodexJSON(t, stdout.Bytes(), "status: super-dolphin-gate status --job")
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
	context.Context,
	gatehook.WaitRequest,
) (gatehook.JobStatus, error) {
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
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func codexHookPayload(repository string, stopActive bool) string {
	return fmt.Sprintf(
		`{"session_id":"session-1","turn_id":"turn-1","cwd":%q,"hook_event_name":"Stop","permission_mode":"default","stop_hook_active":%t}`+"\n",
		repository,
		stopActive,
	)
}

func assertBlockedCodexJSON(t *testing.T, payload []byte, contains string) {
	t.Helper()
	var decision gatehook.CodexDecision
	if err := json.Unmarshal(payload, &decision); err != nil {
		t.Fatalf("decision JSON error = %v: %s", err, payload)
	}
	if decision.Decision != "block" || !strings.Contains(decision.Reason, contains) {
		t.Fatalf("decision = %#v", decision)
	}
}
