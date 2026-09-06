package claudewire

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestClaudeProcessContextCancellationBeforeStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := StartWithExitContext(ctx, os.Args[0], t.TempDir(), "helper-model", "session-context-cancel", "",
		append(os.Environ(), "CLAUDEWIRE_HELPER_SLEEP=1"), func(Event) {}, contract.CLIProcessExitBinding{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartWithExitContext() = %v, want context cancellation", err)
	}
}

func TestClaudeProcessSendRejectsInvalidContextBeforeWriting(t *testing.T) {
	input := &recordingWriteCloser{}
	process := &Process{input: input, done: make(chan struct{})}
	var nilContext context.Context
	typedNil := context.Context((*nilClaudeContext)(nil))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil", ctx: nilContext, want: errContextRequired},
		{name: "typed-nil", ctx: typedNil, want: errContextRequired},
		{name: "canceled", ctx: canceled, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := process.Send(test.ctx, "session", "request", "hello")
			if !errors.Is(err, test.want) {
				t.Fatalf("Send() error = %v, want %v", err, test.want)
			}
			if input.writes != 0 {
				t.Fatalf("Send() wrote %d invalid-context frames", input.writes)
			}
		})
	}
}

type recordingWriteCloser struct{ writes int }

func (w *recordingWriteCloser) Write(p []byte) (int, error) {
	w.writes++
	return len(p), nil
}

func (*recordingWriteCloser) Close() error { return nil }

type nilClaudeContext struct{}

func (*nilClaudeContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilClaudeContext) Done() <-chan struct{}       { return nil }
func (*nilClaudeContext) Err() error                  { return nil }
func (*nilClaudeContext) Value(any) any               { return nil }

func TestClaudeWireHelperExit(t *testing.T) {
	if os.Getenv("CLAUDEWIRE_HELPER_SLEEP") == "1" {
		time.Sleep(2 * time.Second)
		return
	}
	if os.Getenv("CLAUDEWIRE_HELPER_EXIT") == "1" {
		os.Exit(23)
	}
}

func TestClaudeProcessOutputEOFWhileProcessAliveIsBounded(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "claude-eof-live.sh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec 1>&-\nsleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	process, err := StartWithExit(executable, t.TempDir(), "helper-model", "session-eof-live", "",
		os.Environ(), func(Event) {}, contract.CLIProcessExitBinding{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	select {
	case <-process.Done():
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("process Done took %s after stdout EOF while child remained alive", elapsed)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("process Done blocked after stdout EOF while child remained alive")
	}
	if err := process.Close(); err != nil {
		t.Fatalf("Close() after bounded EOF reap: %v", err)
	}
	if failure := process.Failure(); failure == nil || !strings.Contains(failure.Error(), "stdout closed before process exit") {
		t.Fatalf("Failure() = %v, want bounded stdout EOF failure", failure)
	}
}

func TestClaudeProcessHelperExplicitCloseIsNotUnexpected(t *testing.T) {
	events := make(chan contract.CLIProcessExit, 1)
	process, err := StartWithExit(os.Args[0], t.TempDir(), "helper-model", "session-close", "", append(os.Environ(), "CLAUDEWIRE_HELPER_SLEEP=1"), func(Event) {}, contract.CLIProcessExitBinding{
		AgentID: "agent-close", SessionID: "session-close", LaunchSeq: 12,
		OnExit: func(event contract.CLIProcessExit) { events <- event },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Unexpected {
			t.Fatalf("explicit close event classified unexpected: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for explicit close event")
	}
}

// TestClaudeProcessCloseAfterWait 不把 exec.Wait 已关闭管道误报为服务退出失败。
func TestClaudeProcessCloseAfterWait(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	p := &Process{input: input, output: output, done: done, guard: &processctl.Guard{}}
	for attempt := 0; attempt < 2; attempt++ {
		if err := p.Close(); err != nil {
			t.Fatalf("close after process wait, attempt %d: %v", attempt, err)
		}
	}
	if p.Running() {
		t.Fatal("completed process reported running")
	}
}

func TestClaudeProcessExitBindingEmitsExactlyOnce(t *testing.T) {
	var events []contract.CLIProcessExit
	p := &Process{done: make(chan struct{}), exitBind: contract.CLIProcessExitBinding{
		AgentID: "agent", SessionID: "session", LaunchSeq: 3,
		OnExit: func(event contract.CLIProcessExit) { events = append(events, event) },
	}, failure: io.ErrUnexpectedEOF}
	p.emitExit()
	p.emitExit()
	if len(events) != 1 || !events[0].Unexpected || !errors.Is(events[0].Err, io.ErrUnexpectedEOF) {
		t.Fatalf("exit events = %#v", events)
	}
}

// TestClaudeProcessHelperUnexpectedExit 覆盖真实 exec.Cmd Wait 路径，确保 idle 进程退出只通知一次。
func TestClaudeProcessHelperUnexpectedExit(t *testing.T) {
	events := make(chan contract.CLIProcessExit, 2)
	env := append(os.Environ(), "CLAUDEWIRE_HELPER_EXIT=1")
	process, err := StartWithExit(os.Args[0], t.TempDir(), "helper-model", "session-helper", "", env, func(Event) {}, contract.CLIProcessExitBinding{
		AgentID: "agent-helper", SessionID: "session-helper", LaunchSeq: 11,
		OnExit: func(event contract.CLIProcessExit) { events <- event },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	select {
	case event := <-events:
		if !event.Unexpected || event.AgentID != "agent-helper" || event.SessionID != "session-helper" || event.LaunchSeq != 11 {
			t.Fatalf("helper exit event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for helper exit event")
	}
	select {
	case <-events:
		t.Fatal("helper exit callback fired more than once")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestClaudeProcessExitCallbackMayCloseProcess(t *testing.T) {
	closed := make(chan error, 1)
	ready := make(chan struct{})
	var process *Process
	process, err := StartWithExit(os.Args[0], t.TempDir(), "helper-model", "session-reentrant-close", "",
		append(os.Environ(), "CLAUDEWIRE_HELPER_EXIT=1"), func(Event) {}, contract.CLIProcessExitBinding{
			AgentID: "agent-reentrant", SessionID: "session-reentrant-close", LaunchSeq: 13,
			OnExit: func(contract.CLIProcessExit) { <-ready; closed <- process.Close() },
		})
	if err != nil {
		t.Fatal(err)
	}
	close(ready)
	defer process.Close()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("reentrant Process.Close() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exit callback deadlocked while closing process")
	}
}

func TestClaudeProcessEventCallbackMayCloseProcess(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "claude-event-reentrant.sh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"session-event-reentrant\"}'\nread line\n"), 0700); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan error, 1)
	var process *Process
	process, err := StartWithExit(executable, t.TempDir(), "helper-model", "session-event-reentrant", "", os.Environ(), func(event Event) {
		if !event.IsSystemInit("session-event-reentrant") {
			t.Errorf("event = %#v, want system init for session", event)
		}
		close(entered)
		<-release
		closed <- process.Close()
	}, contract.CLIProcessExitBinding{})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event callback")
	}
	close(release)
	assertEventCallbackClosed(t, process, closed)
}

func assertEventCallbackClosed(t *testing.T, process *Process, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("reentrant event callback Process.Close() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event callback deadlocked while closing process")
	}
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not finish after reentrant event callback close")
	}
}

type failedPipeCloser struct{ failure error }

func (p failedPipeCloser) Close() error { return p.failure }

// TestClaudePipeClosePreservesFailures 防止关闭幂等处理吞掉实际 I/O 故障。
func TestClaudePipeClosePreservesFailures(t *testing.T) {
	for _, failure := range []error{io.ErrUnexpectedEOF, os.ErrPermission} {
		if err := closeProcessPipe(failedPipeCloser{failure: failure}); !errors.Is(err, failure) {
			t.Fatalf("close failure lost: got %v, want %v", err, failure)
		}
	}
}
