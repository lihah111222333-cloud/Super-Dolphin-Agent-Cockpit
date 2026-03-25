package codexapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	xwebsocket "golang.org/x/net/websocket"
)

const (
	transportReadyTimeout        = 15 * time.Second
	transportShutdownGracePeriod = 3 * time.Second
	transportKillWaitTimeout     = 5 * time.Second
	transportStderrLimitBytes    = 8 * 1024
)

type localProcess struct {
	cmd        *exec.Cmd
	stderr     *limitedBuffer
	done       chan struct{}
	stderrDone chan struct{}
	waitErr    error
	waitMu     sync.Mutex
	exited     atomic.Bool
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

type transport struct {
	serverURL  string
	local      bool
	ws         *xwebsocket.Conn
	process    *localProcess
	processErr error
	stateMu    sync.RWMutex
	writeMu    sync.Mutex
	pending    sync.Map
	nextID     atomic.Int64
	looping    atomic.Bool
	closed     atomic.Bool
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{limit: limit} }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.buf.Len() <= b.limit {
		return n, err
	}
	raw := append([]byte(nil), b.buf.Bytes()...)
	b.buf.Reset()
	b.buf.Write(raw[len(raw)-b.limit:])
	return n, err
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLocalProcess(cmd *exec.Cmd) *localProcess {
	return &localProcess{
		cmd:        cmd,
		stderr:     newLimitedBuffer(transportStderrLimitBytes),
		done:       make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
}

func (p *localProcess) running() bool {
	return p != nil && p.cmd != nil && p.cmd.Process != nil && !p.exited.Load()
}

func (p *localProcess) setWaitErr(err error) {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	p.waitErr = err
}

func (p *localProcess) waitErrValue() error {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return p.waitErr
}

func (p *localProcess) waitForExit(timeout time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (p *localProcess) waitForStderr(timeout time.Duration) {
	select {
	case <-p.stderrDone:
	case <-time.After(timeout):
	}
}

func (p *localProcess) stderrSummary() string {
	if p == nil || p.stderr == nil {
		return ""
	}
	return strings.TrimSpace(p.stderr.String())
}

func (p *localProcess) exitError() error {
	if p == nil {
		return errors.New("codexapp: local process missing")
	}
	err := p.waitErrValue()
	if err == nil {
		err = errors.New("codexapp: local process exited unexpectedly")
	}
	if stderr := p.stderrSummary(); stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return err
}

func (p *localProcess) signal(sig syscall.Signal) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	pid := p.cmd.Process.Pid
	if pid <= 0 {
		return errors.New("codexapp: invalid local process pid")
	}
	if err := syscall.Kill(-pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	err := p.cmd.Process.Signal(sig)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func newTransport(ctx context.Context, serverURL string) (*transport, error) {
	startupCtx, cancel := withTimeout(normalizeTransportContext(ctx), transportReadyTimeout)
	defer cancel()
	t := &transport{serverURL: normalizeServerURL(serverURL)}
	if t.serverURL == "" {
		if err := t.spawnLocal(); err != nil {
			return nil, err
		}
	}
	if err := t.establish(startupCtx); err != nil {
		_ = t.Kill()
		return nil, err
	}
	return t, nil
}

func (t *transport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if t.closed.Load() {
		return nil, errors.New("codexapp: transport closed")
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	id := t.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	pc := &pendingCall{done: make(chan struct{})}
	t.pending.Store(key, pc)
	defer t.pending.Delete(key)
	if err := t.writeJSON(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case <-pc.done:
		return pc.result, pc.err
	}
}

func (t *transport) Notify(method string, params any) error {
	if t.closed.Load() {
		return errors.New("codexapp: transport closed")
	}
	return t.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *transport) ReadLoop(ctx context.Context, handler func(method string, params json.RawMessage)) {
	if !t.looping.CompareAndSwap(false, true) {
		return
	}
	defer t.looping.Store(false)
	for t.readLoopStep(ctx, handler) {
	}
}

func (t *transport) Close() error {
	if t.closed.Load() {
		return nil
	}
	_ = t.Notify("shutdown", nil)
	t.closed.Store(true)
	err := t.stopProcess(true)
	t.closeSocket()
	return err
}

func (t *transport) Kill() error {
	t.closed.Store(true)
	err := t.stopProcess(false)
	t.closeSocket()
	return err
}

func (t *transport) Running() bool {
	if t.closed.Load() || t.currentWS() == nil {
		return false
	}
	return !t.local || t.processRunning()
}

func (t *transport) reconnect(ctx context.Context) error {
	if t.closed.Load() {
		return errors.New("codexapp: transport closed")
	}
	t.closeSocket()
	if t.local && !t.processRunning() {
		if err := t.spawnLocal(); err != nil {
			return err
		}
	}
	return t.establish(ctx)
}

func (t *transport) establish(ctx context.Context) error {
	ctx = normalizeTransportContext(ctx)
	if err := t.connect(ctx); err != nil {
		return err
	}
	return t.initialize(ctx)
}

func (t *transport) connect(ctx context.Context) error {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if err := t.localProcessReady(); err != nil {
			if t.local && t.processFailure() != nil {
				return err
			}
			lastErr = err
		} else {
			lastErr = t.connectOnce(ctx)
			if lastErr == nil {
				return nil
			}
		}
		if attempt == 5 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond << attempt):
		}
	}
	return lastErr
}

func (t *transport) readLoopStep(ctx context.Context, handler func(string, json.RawMessage)) bool {
	if ctx.Err() != nil || t.closed.Load() {
		return false
	}
	ws := t.currentWS()
	if ws == nil {
		return t.endReadLoop(ctx, handler, nil, "connection unavailable")
	}
	var data string
	err := xwebsocket.Message.Receive(ws, &data)
	if err != nil {
		return t.endReadLoop(ctx, handler, err, err.Error())
	}
	return t.dispatchReadMessage([]byte(data), handler)
}

func (t *transport) spawnLocal() error {
	if t.processRunning() {
		return nil
	}
	serverURL, err := reserveServerURL()
	if err != nil {
		return err
	}
	cmd := exec.Command("codex", "app-server", "--listen", serverURL)
	cmd.Stdout = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	proc := newLocalProcess(cmd)
	t.stateMu.Lock()
	t.local = true
	t.serverURL = serverURL
	t.process = proc
	t.processErr = nil
	t.stateMu.Unlock()
	go t.collectProcessStderr(proc, stderr)
	go t.watchLocalProcess(proc)
	return nil
}

func (t *transport) collectProcessStderr(proc *localProcess, stderr io.ReadCloser) {
	defer close(proc.stderrDone)
	if stderr == nil {
		return
	}
	_, _ = io.Copy(proc.stderr, stderr)
	_ = stderr.Close()
}

func (t *transport) watchLocalProcess(proc *localProcess) {
	err := proc.cmd.Wait()
	proc.exited.Store(true)
	proc.setWaitErr(err)
	close(proc.done)
	proc.waitForStderr(time.Second)
	if t.closed.Load() {
		t.clearProcess(proc, nil)
		return
	}
	exitErr := proc.exitError()
	t.clearProcess(proc, exitErr)
	_ = proc.signal(syscall.SIGKILL)
	t.closeSocket()
	t.failPending(exitErr)
}

func (t *transport) currentProcess() *localProcess {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.process
}

func (t *transport) clearProcess(proc *localProcess, err error) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.process == proc {
		t.process = nil
	}
	if err != nil {
		t.processErr = err
	}
}

func (t *transport) processFailure() error {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.processErr
}

func (t *transport) localProcessReady() error {
	if !t.local {
		return nil
	}
	if t.processRunning() {
		return nil
	}
	if err := t.processFailure(); err != nil {
		return err
	}
	return errors.New("codexapp: local process not running")
}

func (t *transport) stopProcess(graceful bool) error {
	t.stateMu.Lock()
	proc := t.process
	t.process = nil
	t.stateMu.Unlock()
	if proc == nil {
		return nil
	}
	if graceful {
		if err := proc.signal(syscall.SIGTERM); err != nil {
			return err
		}
		if proc.waitForExit(transportShutdownGracePeriod) {
			proc.waitForStderr(time.Second)
			return nil
		}
	}
	if err := proc.signal(syscall.SIGKILL); err != nil {
		return err
	}
	if proc.waitForExit(transportKillWaitTimeout) {
		proc.waitForStderr(time.Second)
		return nil
	}
	return fmt.Errorf("codexapp: timed out waiting for local process exit: %w", proc.exitError())
}

func (t *transport) processRunning() bool {
	proc := t.currentProcess()
	return proc != nil && proc.running()
}
