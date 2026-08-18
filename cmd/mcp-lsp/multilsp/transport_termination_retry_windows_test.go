//go:build windows

package multilsp

import (
	"bufio"
	"os/exec"
	"testing"
)

func TestTransportWaitMarksExitedServerUnavailableAndFailsPending(t *testing.T) {
	tr, pending := startExitedTransportForTest(t)
	waitForTransportExitForTest(t, tr)
	assertTransportClosedForTest(t, tr)
	assertPendingFailedForTest(t, pending)
}

func startExitedTransportForTest(t *testing.T) (*transport, chan pendingResult) {
	t.Helper()
	stderr := &limitedBuffer{limit: stderrLimitBytes}
	cmd := exec.Command("cmd.exe", "/d", "/s", "/c", "echo FATAL ERROR: heap out of memory 1>&2 & exit /b 134")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tr := &transport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		stderr:  stderr,
		done:    make(chan struct{}),
		pending: make(map[string]chan pendingResult),
	}
	pending := make(chan pendingResult, 1)
	tr.pending["1"] = pending
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() { tr.wait() })
	return tr, pending
}
