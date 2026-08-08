package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRetiredTopLevelCommandsAreStrictlyUnknown(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"docker", "plan", "requester", "grant", "run", "status", "wait"} {
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
