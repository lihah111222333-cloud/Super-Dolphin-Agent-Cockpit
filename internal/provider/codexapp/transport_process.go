package codexapp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

func (t *transport) spawnLocal() error {
	if t.processRunning() {
		return nil
	}
	serverURL, err := reserveServerURL()
	if err != nil {
		return err
	}
	argv := []string{"codex", "app-server", "--listen", serverURL}
	pkglogger.Info("codexapp: spawning local app-server", "argv", argv)
	cmd := exec.Command(argv[0], argv[1:]...)
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

// localPID returns the PID of the local app-server process, or 0 if unavailable.
func (t *transport) localPID() int {
	proc := t.currentProcess()
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return 0
	}
	return proc.cmd.Process.Pid
}
