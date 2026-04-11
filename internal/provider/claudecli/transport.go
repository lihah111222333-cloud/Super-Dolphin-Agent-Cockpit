package claudecli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	shutdownGracePeriod  = 3 * time.Second
	shutdownPollInterval = 50 * time.Millisecond
	stderrLimitBytes     = 8 * 1024
	maxCLILineBytes      = 20 << 20
)

type transport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stderr  *limitedBuffer
	done    chan struct{}
	doneErr error
	doneMu  sync.Mutex
	writeMu sync.Mutex
}

func newTransport(binary string, args []string, cwd string, env []string) (*transport, error) {
	if binary == "" {
		binary = defaultClaudeCLIBin
	}
	cmd := exec.Command(binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := newLimitedBuffer(stderrLimitBytes)
	cmd.Stderr = stderr
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxCLILineBytes)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tr := &transport{cmd: cmd, stdin: stdin, stdout: scanner, stderr: stderr, done: make(chan struct{})}
	go tr.wait()
	return tr, nil
}

func (t *transport) Send(msg []byte) error {
	if t == nil {
		return errors.New("transport is nil")
	}
	payload := append([]byte(nil), msg...)
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return errors.New("transport stdin is not ready")
	}
	_, err := t.stdin.Write(payload)
	return err
}

func (t *transport) Receive() ([]byte, error) {
	if t == nil || t.stdout == nil {
		return nil, io.EOF
	}
	if t.stdout.Scan() {
		return append([]byte(nil), t.stdout.Bytes()...), nil
	}
	if err := t.stdout.Err(); err != nil {
		return nil, err
	}
	if err := t.waitErr(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	t.closeInput()
	err := t.signalProcess(syscall.SIGTERM)
	t.waitForExit(shutdownGracePeriod)
	if t.Running() {
		_ = t.signalProcess(syscall.SIGKILL)
	}
	<-t.done
	return normalizeSignalError(err)
}

func (t *transport) Kill() error {
	if t == nil {
		return nil
	}
	t.closeInput()
	err := t.signalProcess(syscall.SIGKILL)
	<-t.done
	return normalizeSignalError(err)
}

func (t *transport) Running() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.done:
		return false
	default:
	}
	pid, err := t.ensureProcessAlive()
	return err == nil && pid > 0
}

func (t *transport) readyForSend() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.done:
		return false
	default:
	}
	t.writeMu.Lock()
	stdinReady := t.stdin != nil
	t.writeMu.Unlock()
	if !stdinReady {
		return false
	}
	if t.cmd == nil || t.cmd.Process == nil {
		return true
	}
	return t.Running()
}

func (t *transport) wait() {
	err := t.cmd.Wait()
	t.doneMu.Lock()
	t.doneErr = err
	t.doneMu.Unlock()
	close(t.done)
}

func (t *transport) waitErr() error {
	select {
	case <-t.done:
	default:
		return nil
	}
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	return t.doneErr
}

func (t *transport) closeInput() {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
}

func (t *transport) waitForExit(timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(shutdownPollInterval)
	defer deadline.Stop()
	defer ticker.Stop()
	for t.Running() {
		select {
		case <-t.done:
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (t *transport) signalProcess(sig syscall.Signal) error {
	pid, err := t.ensureProcessAlive()
	if err != nil {
		return err
	}
	if pid == 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}

func normalizeSignalError(err error) error {
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.buf.Len() > b.limit {
		raw := b.buf.Bytes()
		b.buf.Reset()
		b.buf.Write(raw[len(raw)-b.limit:])
	}
	return n, err
}
