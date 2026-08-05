package acpnode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func assertJoinedErrors(t *testing.T, label string, got error, wants ...error) {
	t.Helper()
	for _, want := range wants {
		if !errors.Is(got, want) {
			t.Fatalf("%s omitted %v: %v", label, want, got)
		}
	}
}

func assertErrorContains(t *testing.T, label string, got error, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(got.Error(), fragment) {
			t.Fatalf("%s omitted %q: %v", label, fragment, got)
		}
	}
}

func markTestInitialized(client *Client) {
	client.mu.Lock()
	client.initialized = true
	client.caps = CapabilitySnapshot{ProtocolVersion: ProtocolVersion, Capabilities: map[string]json.RawMessage{"loadSession": json.RawMessage(`true`)}}
	client.mu.Unlock()
}

type processFactoryFunc func(context.Context, LaunchConfig) (Process, error)

func (f processFactoryFunc) Start(ctx context.Context, cfg LaunchConfig) (Process, error) {
	return f(ctx, cfg)
}

type delayedFactory struct {
	release  chan struct{}
	returned chan struct{}
	process  Process
}

func (f *delayedFactory) Start(context.Context, LaunchConfig) (Process, error) {
	<-f.release
	close(f.returned)
	return f.process, nil
}

type partialProcess struct {
	waitErr     error
	killErr     error
	waitRelease chan struct{}
	waitOnce    sync.Once
}

type nopWriteCloser struct{ io.Reader }

func (nopWriteCloser) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (nopWriteCloser) Close() error              { return nil }

func (p *partialProcess) Stdin() io.WriteCloser { return nopWriteCloser{Reader: strings.NewReader("")} }
func (p *partialProcess) Stdout() io.ReadCloser { return nil }
func (p *partialProcess) Stderr() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }
func (p *partialProcess) Wait() error {
	<-p.waitRelease
	return p.waitErr
}
func (p *partialProcess) Interrupt() error { return nil }
func (p *partialProcess) Kill() error {
	p.waitOnce.Do(func() { close(p.waitRelease) })
	return p.killErr
}

type delayedTestStdin struct {
	io.WriteCloser
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func (w *delayedTestStdin) Write(data []byte) (int, error) {
	n, err := w.WriteCloser.Write(data)
	w.enteredOnce.Do(func() { close(w.entered) })
	<-w.release
	return n, err
}

func (w *delayedTestStdin) releaseWrite() {
	w.releaseOnce.Do(func() { close(w.release) })
}

func (w *delayedTestStdin) Close() error {
	w.releaseWrite()
	return w.WriteCloser.Close()
}

type delayedTestProcess struct {
	Process
	stdin *delayedTestStdin
}

func (p *delayedTestProcess) Stdin() io.WriteCloser { return p.stdin }

type blockingWriteCloser struct {
	release   chan struct{}
	started   chan struct{}
	once      sync.Once
	startOnce sync.Once
}

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockingWriteCloser) Close() error {
	w.once.Do(func() { close(w.release) })
	return nil
}

type blockingWriterProcess struct {
	*fakeProcess
	stdin *blockingWriteCloser
}

func newBlockingWriterProcess() *blockingWriterProcess {
	return &blockingWriterProcess{fakeProcess: newFakeProcess(), stdin: &blockingWriteCloser{release: make(chan struct{}), started: make(chan struct{})}}
}

func (p *blockingWriterProcess) Stdin() io.WriteCloser { return p.stdin }

func (p *blockingWriterProcess) stdinStarted() <-chan struct{} { return p.stdin.started }
