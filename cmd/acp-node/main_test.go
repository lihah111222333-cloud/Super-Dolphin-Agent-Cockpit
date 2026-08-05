package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/acpnode"
)

type commandFakeProcess struct {
	clientIn   *io.PipeWriter
	peerIn     *io.PipeReader
	clientOut  *io.PipeReader
	peerOut    *io.PipeWriter
	release    chan struct{}
	once       sync.Once
	kills      int
	releaseErr error
	killErr    error
	waitErr    error
	mu         sync.Mutex
}

type commandFakeFactory struct{ process *commandFakeProcess }

func runCommandTestAsync(t *testing.T, action func()) {
	t.Helper()
	group := &sync.WaitGroup{}
	group.Go(action)
	t.Cleanup(group.Wait)
}

func (f commandFakeFactory) Start(context.Context, acpnode.LaunchConfig) (acpnode.Process, error) {
	return f.process, nil
}

func newCommandFakeProcess() *commandFakeProcess {
	peerIn, clientIn := io.Pipe()
	clientOut, peerOut := io.Pipe()
	return &commandFakeProcess{clientIn: clientIn, peerIn: peerIn, clientOut: clientOut, peerOut: peerOut, release: make(chan struct{})}
}

func (p *commandFakeProcess) Stdin() io.WriteCloser { return p.clientIn }
func (p *commandFakeProcess) Stdout() io.ReadCloser { return p.clientOut }
func (p *commandFakeProcess) Stderr() io.ReadCloser { return io.NopCloser(&emptyReader{}) }
func (p *commandFakeProcess) Wait() error           { <-p.release; return p.waitErr }
func (p *commandFakeProcess) Interrupt() error      { return nil }
func (p *commandFakeProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	p.mu.Unlock()
	p.once.Do(func() {
		close(p.release)
		p.releaseErr = p.peerOut.Close()
	})
	return errors.Join(p.releaseErr, p.killErr)
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestRunRequiresExperimentalGate(t *testing.T) {
	err := run(context.Background(), nil, nil)
	if !errors.Is(err, errExperimentalGate) {
		t.Fatalf("run() error = %v", err)
	}
}

func TestParseRunFlagsRequiresExplicitEnvironmentAllowlist(t *testing.T) {
	_, err := parseRunFlags([]string{
		"--enable-experimental-acp",
		"--env", "PATH=/usr/bin",
	})
	if err == nil || !strings.Contains(err.Error(), "--allow-env") {
		t.Fatalf("parseRunFlags() error = %v, want explicit allowlist failure", err)
	}
	flags, err := parseRunFlags([]string{
		"--enable-experimental-acp",
		"--env", "PATH=/usr/bin",
		"--allow-env", "PATH",
	})
	if err != nil || len(flags.allowEnv) != 1 || flags.allowEnv[0] != "PATH" {
		t.Fatalf("explicit allowlist parse = %#v, err=%v", flags, err)
	}
}

func TestRunReachesValidatedACPHandshakeAndClosesInjectedProcess(t *testing.T) {
	p := newCommandFakeProcess()
	factory := commandFakeFactory{process: p}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{
		"--enable-experimental-acp",
		"--executable", os.Args[0],
		"--cwd", t.TempDir(),
		"--env", "PATH=/usr/bin",
		"--allow-env", "PATH",
		"--startup-timeout", "1s",
		"--request-timeout", "1s",
		"--shutdown-timeout", "1ms",
	}
	runDone := make(chan error, 1)
	runCommandTestAsync(t, func() { runDone <- run(ctx, args, factory) })
	peer := bufio.NewReader(p.peerIn)
	var request map[string]json.RawMessage
	if err := json.NewDecoder(peer).Decode(&request); err != nil {
		t.Fatal(err)
	}
	var id json.RawMessage
	if err := json.Unmarshal(request["id"], &id); err != nil {
		t.Fatal(err)
	}
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{},
		},
	}
	if err := json.NewEncoder(p.peerOut).Encode(response); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not close after context cancellation")
	}
	p.mu.Lock()
	kills := p.kills
	p.mu.Unlock()
	if kills != 1 {
		t.Fatalf("kill count = %d", kills)
	}
}

func TestLogRunFailureRedactsPeerPathAndErrorText(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logRunFailure(fmt.Errorf("peer=secret-peer path=/private/secret/file: protocol password=secret-value"))
	text := output.String()
	for _, plain := range []string{"secret-peer", "/private/secret/file", "secret-value"} {
		if strings.Contains(text, plain) {
			t.Fatalf("log leaked %q: %s", plain, text)
		}
	}
	if !strings.Contains(text, "acp-node run failed") {
		t.Fatalf("missing fixed log message: %s", text)
	}
}

func TestRunJoinsPrimaryAndProcessCleanupErrors(t *testing.T) {
	p := newCommandFakeProcess()
	p.killErr = errors.New("kill cleanup failed")
	p.waitErr = errors.New("wait cleanup failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{
		"--enable-experimental-acp",
		"--executable", os.Args[0],
		"--cwd", t.TempDir(),
		"--env", "PATH=/usr/bin",
		"--allow-env", "PATH",
		"--shutdown-timeout", "1ms",
	}
	done := make(chan error, 1)
	runCommandTestAsync(t, func() { done <- run(ctx, args, commandFakeFactory{process: p}) })
	peer := bufio.NewReader(p.peerIn)
	var request map[string]json.RawMessage
	if err := json.NewDecoder(peer).Decode(&request); err != nil {
		t.Fatal(err)
	}
	var id json.RawMessage
	if err := json.Unmarshal(request["id"], &id); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(p.peerOut).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !strings.Contains(err.Error(), "kill cleanup failed") || !strings.Contains(err.Error(), "wait cleanup failed") {
			t.Fatalf("cleanup errors were not joined: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return after bounded cleanup")
	}
}
