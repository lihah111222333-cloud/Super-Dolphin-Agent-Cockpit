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

func TestRetiredTopLevelCommandsAreStrictlyUnknown(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"docker", "requester", "grant", "run", "status", "wait"} {
		code, stdout, stderr := executeCLI([]string{command, "obsolete-argument"})
		if code != int(gatecontract.ExitProtocol) || stdout != "" ||
			!strings.Contains(stderr, `unknown subcommand "`+command+`"`) {
			t.Fatalf("command=%q code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
}

func TestCLIErrorWriterFailureReturnsInfrastructureExit(t *testing.T) {
	t.Parallel()

	code := runCLI([]string{"unknown"}, &bytes.Buffer{}, failingWriter{err: errors.New("write failed")})
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
