package acpnode

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type blockingProcessFactory struct {
	release chan struct{}
	process Process
	started chan struct{}
}

func (f *blockingProcessFactory) Start(ctx context.Context, _ LaunchConfig) (Process, error) {
	close(f.started)
	select {
	case <-f.release:
	case <-ctx.Done():
		return f.process, nil
	}
	return f.process, nil
}

func TestNewClientEnforcesProcessStartupTimeout(t *testing.T) {
	p := newFakeProcess()
	factory := &blockingProcessFactory{release: make(chan struct{}), process: p, started: make(chan struct{})}
	cfg := testLaunchConfig(t)
	cfg.StartupTimeout = time.Millisecond
	_, err := NewClient(cfg, factory, nil)
	if !errors.Is(err, ErrStartupTimeout) {
		t.Fatalf("NewClient() error = %v", err)
	}
	close(factory.release)
	select {
	case <-factory.started:
	case <-time.After(time.Second):
		t.Fatal("startup factory did not return")
	}
	select {
	case <-p.waitRelease:
	case <-time.After(time.Second):
		t.Fatal("timed-out process was not cleaned up")
	}
}

func TestDefaultProcessFactoryHonorsCanceledStartupContext(t *testing.T) {
	cfg := testLaunchConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	process, err := DefaultProcessFactory().Start(ctx, cfg)
	if process != nil {
		t.Fatal("canceled default startup returned a process")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled default startup error = %v", err)
	}
}

func TestNonCooperativeProcessActionRetainsOwner(t *testing.T) {
	release := make(chan struct{})
	err := runProcessAction(time.Millisecond, func() error {
		<-release
		return nil
	})
	if !errors.Is(err, ErrStartupCleanupTimeout) {
		t.Fatalf("process action error = %v", err)
	}
	var pending *processActionPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("process action owner missing: %v", err)
	}
	close(release)
	select {
	case <-pending.Done():
	case <-time.After(time.Second):
		t.Fatal("process action owner did not finish after release")
	}
}

func TestNonCooperativeProcessWaitRetainsOwner(t *testing.T) {
	process := &nonCooperativeWaitProcess{release: make(chan struct{})}
	err := cleanupProcess(process, time.Millisecond)
	if !errors.Is(err, ErrStartupCleanupTimeout) {
		t.Fatalf("cleanup error = %v", err)
	}
	var pending *processActionPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("wait owner missing from cleanup error: %v", err)
	}
	close(process.release)
	select {
	case <-pending.Done():
	case <-time.After(time.Second):
		t.Fatal("wait owner did not finish after release")
	}
}

type nonCooperativeWaitProcess struct {
	release chan struct{}
}

func (p *nonCooperativeWaitProcess) Stdin() io.WriteCloser { return nopWriteCloser{} }
func (p *nonCooperativeWaitProcess) Stdout() io.ReadCloser {
	return io.NopCloser(strings.NewReader(""))
}
func (p *nonCooperativeWaitProcess) Stderr() io.ReadCloser {
	return io.NopCloser(strings.NewReader(""))
}
func (p *nonCooperativeWaitProcess) Wait() error {
	<-p.release
	return nil
}
func (p *nonCooperativeWaitProcess) Interrupt() error { return nil }
func (p *nonCooperativeWaitProcess) Kill() error      { return nil }
