package standalone

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestExecutionAPIsRejectNilContextsBeforeLifecycleWork(t *testing.T) {
	e := NewExecution(Config{})
	var nilContext context.Context
	typedNil := context.Context((*nilExecutionContext)(nil))
	for _, test := range []struct {
		name string
		call func(context.Context) error
	}{
		{name: "Launch", call: func(ctx context.Context) error {
			_, err := e.Launch(ctx, contract.LaunchRequest{AgentID: "agent"})
			return err
		}},
		{name: "SubmitTurn", call: func(ctx context.Context) error {
			_, err := e.SubmitTurn(ctx, contract.TurnSubmission{AgentID: "agent"})
			return err
		}},
		{name: "WaitForSessionReady", call: func(ctx context.Context) error {
			return e.WaitForSessionReady(ctx, "agent", time.Second)
		}},
	} {
		t.Run(test.name+"/nil", func(t *testing.T) {
			err := test.call(nilContext)
			if !errors.Is(err, ErrExecutionContextRequired) {
				t.Fatalf("error = %v, want ErrExecutionContextRequired", err)
			}
		})
		t.Run(test.name+"/typed-nil", func(t *testing.T) {
			err := test.call(typedNil)
			if !errors.Is(err, ErrExecutionContextRequired) {
				t.Fatalf("error = %v, want ErrExecutionContextRequired", err)
			}
		})
	}
}

func TestExecutionLifecycleAPIsRejectNilTypedNilAndCanceledContexts(t *testing.T) {
	e := NewExecution(Config{})
	var nilContext context.Context
	typedNil := context.Context((*nilExecutionContext)(nil))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	errorCalls := []struct {
		name string
		call func(context.Context) error
	}{
		{name: "Stop", call: func(ctx context.Context) error { return e.Stop(ctx, "missing") }},
		{name: "Archive", call: func(ctx context.Context) error { return e.Archive(ctx, "missing") }},
		{name: "Interrupt", call: func(ctx context.Context) error { return e.Interrupt(ctx, "missing", "") }},
		{name: "Fork", call: func(ctx context.Context) error {
			_, err := e.Fork(ctx, "missing", contract.LaunchRequest{AgentID: "child"})
			return err
		}},
		{name: "ActivatePersistedRuntime", call: func(ctx context.Context) error {
			return e.ActivatePersistedRuntime(ctx, orchestration.PersistedRuntimeActivation{Provider: "unknown"})
		}},
	}
	for _, test := range errorCalls {
		t.Run(test.name+"/nil", func(t *testing.T) {
			if err := test.call(nilContext); !errors.Is(err, ErrExecutionContextRequired) {
				t.Fatalf("error = %v, want ErrExecutionContextRequired", err)
			}
		})
		t.Run(test.name+"/typed-nil", func(t *testing.T) {
			if err := test.call(typedNil); !errors.Is(err, ErrExecutionContextRequired) {
				t.Fatalf("error = %v, want ErrExecutionContextRequired", err)
			}
		})
		t.Run(test.name+"/canceled", func(t *testing.T) {
			if err := test.call(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}

	if e.IsRunning(nilContext, "missing") {
		t.Fatal("IsRunning(nil context) reported a live session")
	}
	if e.IsRunning(typedNil, "missing") {
		t.Fatal("IsRunning(typed-nil context) reported a live session")
	}
	if e.IsRunning(canceled, "missing") {
		t.Fatal("IsRunning(canceled context) reported a live session")
	}
}

func TestExecutionCanceledLifecycleDoesNotMutateSession(t *testing.T) {
	e := NewExecution(Config{})
	session := &cliSession{provider: Provider{Name: "codex"}, threadID: "thread", turnID: "turn"}
	e.sessions["agent"] = session
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := e.Stop(canceled, "agent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	if err := e.Archive(canceled, "agent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Archive() error = %v, want context.Canceled", err)
	}
	if err := e.Interrupt(canceled, "agent", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Interrupt() error = %v, want context.Canceled", err)
	}
	if session.stopped || session.interruptRequestID != "" {
		t.Fatalf("canceled lifecycle call mutated session: stopped=%v interrupt=%q", session.stopped, session.interruptRequestID)
	}
}

func TestInternalStopSessionReclaimsCanceledLaunchReservation(t *testing.T) {
	e := NewExecution(Config{})
	session := &cliSession{readyCh: make(chan struct{})}
	ctx, cancel := session.bindLaunchContext(context.Background())
	e.sessions["agent"] = session
	cancel()

	if err := e.stopSession(session); err != nil {
		t.Fatalf("stopSession() = %v, want successful owner cleanup", err)
	}
	if !session.stopped {
		t.Fatal("owner cleanup did not mark the canceled session stopped")
	}
	if _, err := e.session("agent"); err != nil {
		t.Fatalf("uncertain launch reservation was discarded: %v", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("launch context = %v, want context.Canceled", ctx.Err())
	}
}

type nilExecutionContext struct{}

func (*nilExecutionContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilExecutionContext) Done() <-chan struct{}       { return nil }
func (*nilExecutionContext) Err() error                  { return nil }
func (*nilExecutionContext) Value(any) any               { return nil }
