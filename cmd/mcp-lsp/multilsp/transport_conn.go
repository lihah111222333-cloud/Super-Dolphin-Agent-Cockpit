package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	// defaultResponderDrainTimeout 限制 Close 等待服务端请求响应 goroutine 的时间。
	// 它必须短于整体 shutdown 超时，保证调用方的停止预算不会被响应排空拖穿。
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

// Close 关闭 LSP 管理器资源。
func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	if !t.closed.CompareAndSwap(false, true) {
		return t.waitForExit(defaultShutdownTimeout)
	}
	t.cancelActorContext()
	t.closeInput()
	t.clearPending(ErrTransportClosed)
	drainErr := t.drainResponders(defaultResponderDrainTimeout)
	return errors.Join(t.killProcess(), t.waitForExit(defaultShutdownTimeout), drainErr)
}

// drainResponders 在 timeout 内等待所有已登记的服务端请求响应 goroutine。
// 返回错误只表示排空超时；调用方仍会继续 killProcess，避免卡住的语言服务器阻塞关闭。
func (t *transport) drainResponders(timeout time.Duration) error {
	drainCtx, cancel := platformconfig.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan struct{})
	safego.Go(drainCtx, nil, "mcp-lsp.transport.drain-responders", func(context.Context) {
		t.responderWG.Wait()
		close(done)
	})
	select {
	case <-done:
		return nil
	case <-drainCtx.Done():
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

// readMessage 按 LSP Content-Length framing 读取一条完整 JSON-RPC 消息。
// header 缺失或长度非法会直接返回错误，避免把半包当成空通知继续处理。
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

// writeMessage 序列化并按 LSP framing 写入子进程 stdin。
// 写入失败会合并 wait 错误，帮助调用方区分编码问题和语言服务器提前退出。
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
	stdin := t.stdinForWrite()
	if stdin == nil {
		return ErrTransportClosed
	}
	if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return t.joinWaitError(err)
	}
	if _, err := stdin.Write(payload); err != nil {
		return t.joinWaitError(err)
	}
	return nil
}

// writeMessageContext 用调用方 ctx 监督底层 stdin 写入。
// 写入被管道背压卡住时会关闭 stdin 并终止 LSP 进程，让 request deadline 能按时返回。
func (t *transport) writeMessageContext(ctx context.Context, message any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	safego.Go(ctx, nil, "mcp-lsp.transport.write-message", func(context.Context) {
		done <- t.writeMessage(message)
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.abortBlockedWrite(ctx.Err())
		return ctx.Err()
	}
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
	t.cancelActorContext()
	close(t.done)
}

func (t *transport) cancelActorContext() {
	if t != nil && t.cancelActors != nil {
		t.cancelActors()
	}
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
	stdin := t.takeStdin()
	if stdin != nil {
		_ = stdin.Close()
	}
}

func (t *transport) stdinForWrite() io.WriteCloser {
	t.stdinMu.Lock()
	defer t.stdinMu.Unlock()
	return t.stdin
}

func (t *transport) takeStdin() io.WriteCloser {
	t.stdinMu.Lock()
	defer t.stdinMu.Unlock()
	stdin := t.stdin
	t.stdin = nil
	return stdin
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
	t.cancelActorContext()
	t.clearPending(err)
	t.closeInput()
	// 先排空服务端请求响应，再杀进程，避免 writeMessage 失败继续扩散成 goroutine 泄漏。
	_ = t.drainResponders(defaultResponderDrainTimeout)
	_ = t.killProcess()
}

// abortBlockedWrite 在 request ctx 到期时打断正在阻塞的 stdin 写入。
// 这里不等待 responder 排空，避免取消路径再次被语言服务器背压拖住。
func (t *transport) abortBlockedWrite(err error) {
	t.closed.Store(true)
	t.cancelActorContext()
	t.clearPending(err)
	t.closeInput()
	_ = t.killProcess()
}
