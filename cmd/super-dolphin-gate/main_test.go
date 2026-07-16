package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	cliCommitSHA = "1111111111111111111111111111111111111111"
	cliTreeSHA   = "3333333333333333333333333333333333333333"
)

func TestPlanCommandWritesCanonicalJSON(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeCLI([]string{
		"plan", "--profile", "local-fast", "--object-format", "sha1",
		"--commit", cliCommitSHA, "--source-tree", cliTreeSHA,
	})
	if code != int(gatecontract.ExitOK) {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
	var plan gatecontract.GatePlan
	if err := gatecontract.DecodeStrictJSON([]byte(stdout), &plan); err != nil {
		t.Fatalf("plan JSON error = %v\n%s", err, stdout)
	}
	if plan.Profile != gatecontract.ProfileLocalFast || plan.Source.ObjectFormat != gatecontract.GitObjectFormatSHA1 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanCommandSupportsSHA256Range(t *testing.T) {
	t.Parallel()

	sha, tree, zero := strings.Repeat("1", 64), strings.Repeat("3", 64), strings.Repeat("0", 64)
	code, _, stderr := executeCLI([]string{
		"plan", "--profile", "push", "--object-format", "sha256", "--source-tree", tree,
		"--base-kind", "empty_tree", "--head", sha, "--local-ref", "refs/heads/topic",
		"--remote-ref", "refs/heads/topic", "--observed-remote", zero, "--update-kind", "create",
	})
	if code != int(gatecontract.ExitOK) {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
}

func TestPlanRejectsMultipleSourceVariants(t *testing.T) {
	t.Parallel()

	code, _, _ := executeCLI([]string{
		"plan", "--profile", "push", "--object-format", "sha1", "--source-tree", cliTreeSHA,
		"--commit", cliCommitSHA, "--tree", cliTreeSHA,
	})
	if code != int(gatecontract.ExitSourceMismatch) {
		t.Fatalf("code = %d, want %d", code, gatecontract.ExitSourceMismatch)
	}
}

func TestPlanRejectsTreeParentOutsideCompleteTreeVariant(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"--commit", cliCommitSHA, "--parent", cliCommitSHA},
		{"--parent", cliCommitSHA},
	}
	for _, sourceFlags := range tests {
		args := []string{"plan", "--profile", "push", "--object-format", "sha1", "--source-tree", cliTreeSHA}
		args = append(args, sourceFlags...)
		code, _, stderr := executeCLI(args)
		if code != int(gatecontract.ExitSourceMismatch) {
			t.Fatalf("source flags=%v code=%d stderr=%q", sourceFlags, code, stderr)
		}
	}
}

func TestRunRejectsMissingSchedulerTokenBeforeNotWired(t *testing.T) {
	t.Parallel()

	code, _, stderr := executeCLI([]string{"run"})
	if code != int(gatecontract.ExitProtocol) || !strings.Contains(stderr, "--job-token is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRemainingUnwiredCommandsFailFastWithStableExit(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"run", "--job-token", "opaque"},
		{"receipt", "verify", "--input", "receipt.json"},
	}
	for _, args := range tests {
		code, _, stderr := executeCLI(args)
		if code != int(gatecontract.ExitInfrastructure) || !strings.Contains(stderr, "scheduler client not wired") {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestStatusAndWaitRequireJob(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"status", "wait"} {
		code, _, stderr := executeCLI([]string{command})
		if code != int(gatecontract.ExitProtocol) || !strings.Contains(stderr, "--job is required") {
			t.Fatalf("command=%s code=%d stderr=%q", command, code, stderr)
		}
	}
}

func TestReceiptVerifyRequiresInput(t *testing.T) {
	t.Parallel()

	code, _, _ := executeCLI([]string{"receipt", "verify"})
	if code != int(gatecontract.ExitProtocol) {
		t.Fatalf("code = %d, want %d", code, gatecontract.ExitProtocol)
	}
}

func TestGrantCLIExposesNoIssueOrConsumeCommand(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"issue", "consume"} {
		code, _, stderr := executeCLI([]string{"grant", command})
		if code != int(gatecontract.ExitProtocol) || !strings.Contains(stderr, "unknown grant subcommand") {
			t.Fatalf("command=%s code=%d stderr=%q", command, code, stderr)
		}
	}
}

func TestGrantVerifyRequiresInput(t *testing.T) {
	t.Parallel()
	code, _, _ := executeCLI([]string{"grant", "verify"})
	if code != int(gatecontract.ExitProtocol) {
		t.Fatalf("code = %d, want %d", code, gatecontract.ExitProtocol)
	}
}

func TestCLIErrorWriterFailureReturnsInfrastructureExit(t *testing.T) {
	t.Parallel()

	code := runCLI([]string{"run"}, &bytes.Buffer{}, failingWriter{err: errors.New("write failed")})
	if code != int(gatecontract.ExitInfrastructure) {
		t.Fatalf("code = %d, want infrastructure exit %d", code, gatecontract.ExitInfrastructure)
	}
}

func TestWriteCLIErrorPreservesWriterErrorChain(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("write failed")
	err := writeCLIError(failingWriter{err: writeErr}, errors.New("command failed"))
	if !errors.Is(err, writeErr) {
		t.Fatalf("writeCLIError() error = %v, want writer error chain", err)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func executeCLI(args []string) (int, string, string) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runCLI(args, stdout, stderr)
	return code, stdout.String(), stderr.String()
}
