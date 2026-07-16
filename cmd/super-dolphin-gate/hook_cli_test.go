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

	err := runHookWithConnector(
		[]string{"pre-push", "origin", "file://" + repository}, strings.NewReader(input), &bytes.Buffer{}, repository, hookTestConnector(coordinator),
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
	submitCount     int
	statusCount     int
	queuePosition   int
	passWithReceipt bool
	grantCount      int
}

func (coordinator *recordingHookCoordinator) Submit(
	_ context.Context,
	request gatehook.SubmitRequest,
) (gatehook.JobStatus, error) {
	coordinator.lastSubmit = request
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
