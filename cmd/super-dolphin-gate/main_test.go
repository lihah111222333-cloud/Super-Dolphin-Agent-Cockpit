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

func TestRunRejectsMissingSchedulerTokenBeforeNotWired(t *testing.T) {
	t.Parallel()

	code, _, stderr := executeCLI([]string{"run"})
	if code != int(gatecontract.ExitProtocol) || !strings.Contains(stderr, "--job-token is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestUnwiredCommandsFailFastWithStableExit(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"run", "--job-token", "opaque"},
		{"submit", "--profile", "push", "--object-format", "sha1", "--commit", cliCommitSHA, "--source-tree", cliTreeSHA},
		{"status", "--job", "job-1"},
		{"receipt", "verify", "--input", "receipt.json"},
	}
	for _, args := range tests {
		code, _, stderr := executeCLI(args)
		if code != int(gatecontract.ExitInfrastructure) || !strings.Contains(stderr, "scheduler client not wired") {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr)
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

func TestCLIErrorWriterFailurePreservesOriginalExitCode(t *testing.T) {
	t.Parallel()

	code := runCLI([]string{"run"}, &bytes.Buffer{}, failingWriter{})
	if code != int(gatecontract.ExitProtocol) {
		t.Fatalf("code = %d, want original protocol exit %d", code, gatecontract.ExitProtocol)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func executeCLI(args []string) (int, string, string) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runCLI(args, stdout, stderr)
	return code, stdout.String(), stderr.String()
}
