package multilsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	// defaultResponderDrainTimeout bounds how long Close() waits for
	// in-flight server-request responder goroutines to drain. Keep it
	// ≤ defaultShutdownTimeout so Close() as a whole still fits the
	// caller-side stop budget (P22 P2 LSP-S3, plan §492).
	defaultResponderDrainTimeout = 2 * time.Second
	stderrLimitBytes             = 8 * 1024
)

func startTransport(options transportOptions) (*exec.Cmd, io.WriteCloser, io.ReadCloser, *limitedBuffer, error) {
	cmd := hiddenexec.Command(options.Binary, options.Args...)
	cmd.Dir = options.Dir
	if len(options.Env) > 0 {
		cmd.Env = append(os.Environ(), options.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("LSP server start stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, fmt.Errorf("LSP server start stdout pipe: %w", err)
	}
	stderr := &limitedBuffer{limit: stderrLimitBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, nil, fmt.Errorf("LSP server start process: %w", err)
	}
	return cmd, stdin, stdout, stderr, nil
}

func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	if !t.closed.CompareAndSwap(false, true) {
		return t.waitForExit(defaultShutdownTimeout)
	}
	t.closeInput()
	t.clearPending(ErrTransportClosed)
	drainErr := t.drainResponders(defaultResponderDrainTimeout)
	return errors.Join(t.killProcess(), t.waitForExit(defaultShutdownTimeout), drainErr)
}

// drainResponders waits up to timeout for every in-flight
// server-request responder goroutine registered via spawnResponder.
// A non-nil error indicates some responder outlived the drain budget;
// the caller still proceeds to killProcess so a stuck peer cannot
// pin shutdown.
func (t *transport) drainResponders(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		t.responderWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("LSP server-request responders did not drain within %s", timeout)
	}
}

func (t *transport) readLoop() {
	for {
		payload, err := t.readMessage()
		if err != nil {
			t.stopWithError(t.readFailure(err))
			return
		}
		if err := t.dispatchMessage(payload); err != nil {
			t.stopWithError(err)
			return
		}
	}
}

func (t *transport) readMessage() (json.RawMessage, error) {
	length := -1
	for {
		line, err := t.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("LSP malformed header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("LSP invalid Content-Length %q", value)
		}
	}
	if length < 0 {
		return nil, errors.New("LSP missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(t.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (t *transport) writeMessage(message any) error {
	if t.closed.Load() {
		return ErrTransportClosed
	}
	payload, err := protocol.EncodeMessage(message)
	if err != nil {
		return fmt.Errorf("LSP encode message: %w", err)
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return ErrTransportClosed
	}
	if _, err := fmt.Fprintf(t.stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return t.joinWaitError(err)
	}
	if _, err := t.stdin.Write(payload); err != nil {
		return t.joinWaitError(err)
	}
	return nil
}

func (t *transport) wait() {
	err := t.cmd.Wait()
	if stderr := strings.TrimSpace(t.stderr.String()); stderr != "" {
		switch {
		case err != nil:
			err = fmt.Errorf("%w: %s", err, stderr)
		case !t.closed.Load():
			err = errors.New(stderr)
		}
	}
	t.doneMu.Lock()
	t.doneErr = err
	t.doneMu.Unlock()
	close(t.done)
}

func (t *transport) waitErr() error {
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	return t.doneErr
}

func (t *transport) waitForExit(timeout time.Duration) error {
	select {
	case <-t.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("LSP process did not exit within %s", timeout)
	}
}

func (t *transport) killProcess() error {
	if t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	if err := t.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (t *transport) closeInput() {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
}

func (t *transport) readFailure(err error) error {
	if t.closed.Load() {
		return ErrTransportClosed
	}
	if waitErr := t.waitErr(); waitErr != nil {
		if errors.Is(err, io.EOF) {
			return waitErr
		}
		return errors.Join(err, waitErr)
	}
	return err
}

func (t *transport) stopWithError(err error) {
	t.closed.Store(true)
	t.clearPending(err)
	t.closeInput()
	// Drain in-flight server-request responders before killing the
	// process so writeMessage failures do not cascade into goroutine
	// leaks (P22 P2 LSP-S3).
	_ = t.drainResponders(defaultResponderDrainTimeout)
	_ = t.killProcess()
}
