package claudecli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// processSig is the internal signal abstraction used by the transport so that
// platform implementations can map it onto syscall.Signal (Unix) or
// TerminateProcess (Windows) without leaking syscall types into shared code.
type processSig int

const (
	sigInterrupt processSig = iota
	sigTerminate
	sigForceKill
)

const (
	shutdownGracePeriod  = 3 * time.Second
	shutdownPollInterval = 50 * time.Millisecond
	stderrLimitBytes     = 8 * 1024
	maxCLILineBytes      = 20 << 20
)

type transport struct {
	cmd     *exec.Cmd
	guard   *processGuard
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stdoutR io.ReadCloser
	stderr  *limitedBuffer
	done    chan struct{}
	doneErr error
	doneMu  sync.Mutex
	writeMu sync.Mutex
}

// newTransport 创建传输。
func newTransport(binary string, args []string, cwd string, env []string) (*transport, error) {
	if binary == "" {
		binary = defaultClaudeCLIBin
	}
	binary = resolveClaudeBinary(binary)
	cmd := exec.Command(binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	baseEnv := os.Environ()
	if len(env) > 0 {
		baseEnv = append(baseEnv, env...)
	}
	cmd.Env = ensureLoopbackNoProxy(baseEnv)
	setClaudeProcessAttrs(cmd)
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
	tr := &transport{
		cmd:     cmd,
		guard:   attachProcessGuard(cmd),
		stdin:   stdin,
		stdout:  scanner,
		stdoutR: stdout,
		stderr:  stderr,
		done:    make(chan struct{}),
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				select {
				case <-tr.done:
				default:
					close(tr.done)
				}
			}
		}()
		tr.wait()
	}()
	return tr, nil
}

// ensureLoopbackNoProxy guarantees loopback hosts are in NO_PROXY. Claude CLI
// (Node) honors HTTP_PROXY/HTTPS_PROXY for all outbound requests including the
// loopback MCP endpoints we host — if the user runs a proxy like clash and
// has not set NO_PROXY, the MCP handshake is routed through the proxy and
// hangs, causing the CLI to emit an empty-error result event.
func ensureLoopbackNoProxy(env []string) []string {
	const loopbacks = "127.0.0.1,localhost,::1"
	var existing []string
	filtered := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(key, "NO_PROXY") {
			existing = append(existing, strings.TrimPrefix(kv, key+"="))
			continue
		}
		filtered = append(filtered, kv)
	}
	merged := mergeCSV(append(existing, loopbacks)...)
	return append(filtered, "NO_PROXY="+merged, "no_proxy="+merged)
}

// mergeCSV 合并csv。
func mergeCSV(parts ...string) string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, group := range parts {
		for _, item := range strings.Split(group, ",") {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, ",")
}

// Send 向底层传输写入请求。
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

// Receive 从底层传输读取事件。
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

// Close 关闭claudecli provider资源。
func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	t.closeInput()
	err := t.signalProcess(sigTerminate)
	t.waitForExit(shutdownGracePeriod)
	if t.Running() {
		_ = t.signalProcess(sigForceKill)
	}
	<-t.done
	return normalizeSignalError(err)
}

// Kill 终止底层进程或连接。
func (t *transport) Kill() error {
	if t == nil {
		return nil
	}
	t.closeInput()
	err := t.signalProcess(sigForceKill)
	<-t.done
	return normalizeSignalError(err)
}

// Running 返回底层进程是否仍在运行。
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

// readyForSend 为send判断claudecli provider。
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
	t.guard.close()
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
	if t.stdoutR != nil {
		_ = t.stdoutR.Close()
		t.stdoutR = nil
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

func (t *transport) signalProcess(sig processSig) error {
	pid, err := t.ensureProcessAlive()
	if err != nil {
		return err
	}
	if pid == 0 {
		return nil
	}
	return signalClaudeProcess(t.cmd, t.guard, sig)
}

func normalizeSignalError(err error) error {
	if err == nil || isProcessGoneErr(err) {
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

// String 返回字符串表示。
func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Write 写入claudecli provider。
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
