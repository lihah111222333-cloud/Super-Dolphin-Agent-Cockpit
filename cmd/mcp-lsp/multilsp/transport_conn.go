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

// startTransport 建立 stdio 管道、启动语言服务器并绑定平台进程树所有权。
func startTransport(
	options transportOptions,
) (*exec.Cmd, *hiddenexec.ProcessTree, io.WriteCloser, io.ReadCloser, *limitedBuffer, error) {
	return startTransportWithStarter(options, hiddenexec.StartProcessTree)
}

// startTransportWithStarter 创建 stdio 管道并把启动失败时的 owner 交给清理路径。
func startTransportWithStarter(
	options transportOptions,
	starter func(*exec.Cmd) (*hiddenexec.ProcessTree, error),
) (*exec.Cmd, *hiddenexec.ProcessTree, io.WriteCloser, io.ReadCloser, *limitedBuffer, error) {
	cmd := hiddenexec.Command(options.Binary, options.Args...)
	cmd.Dir = options.Dir
	if len(options.Env) > 0 {
		cmd.Env = append(os.Environ(), options.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("LSP server start stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("LSP server start stdout pipe: %w", err)
	}
	stderr := &limitedBuffer{limit: stderrLimitBytes}
	cmd.Stderr = stderr
	processTree, err := starter(cmd)
	if err != nil {
		var closeErr error
		if stdinErr := stdin.Close(); stdinErr != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close LSP server stdin after process-tree startup failure: %w", stdinErr))
		}
		if stdoutErr := stdout.Close(); stdoutErr != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close LSP server stdout after process-tree startup failure: %w", stdoutErr))
		}
		return nil, processTree, nil, nil, nil, errors.Join(fmt.Errorf("LSP server start process tree: %w", err), closeErr)
	}
	return cmd, processTree, stdin, stdout, stderr, nil
}

// Close 关闭 LSP 管理器资源。
func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	t.closeMu.Lock()
	if t.closeComplete {
		result := t.closeResult
		t.closeMu.Unlock()
		return result
	}
	if attempt := t.closeAttempt; attempt != nil {
		t.closeMu.Unlock()
		<-attempt.done
		return attempt.result
	}
	attempt := &transportCloseAttempt{done: make(chan struct{})}
	t.closeAttempt = attempt
	t.closeMu.Unlock()

	firstClose := t.sealResponderAdmission()
	if firstClose {
		t.cancelActorContext()
		t.closeInput()
		t.clearPending(ErrTransportClosed)
	}
	drainErr := t.drainResponders(defaultResponderDrainTimeout)
	t.logShutdownStageResult("drain", drainErr)
	terminationErr, _ := t.terminateProcessTreeAttempt()
	t.logShutdownStageResult("terminate", terminationErr)
	waitErr := t.waitForExit(defaultShutdownTimeout)
	t.logShutdownStageResult("wait", waitErr)
	effectiveTerminationErr, releaseErr, releaseComplete := t.releaseProcessTreeAfterExit(terminationErr, waitErr)
	result := errors.Join(effectiveTerminationErr, waitErr, releaseErr, drainErr)

	t.closeMu.Lock()
	attempt.result = result
	if effectiveTerminationErr == nil && drainErr == nil && waitErr == nil && releaseComplete {
		t.closeComplete = true
		t.closeResult = result
	}
	t.closeAttempt = nil
	close(attempt.done)
	t.closeMu.Unlock()
	return result
}

// logShutdownStageResult 记录非跳过关闭阶段的结果。
func (t *transport) logShutdownStageResult(stage string, stageErr error) {
	if stageErr != nil {
		t.logShutdownStage(stage, "failed", stageErr)
		return
	}
	t.logShutdownStage(stage, "completed", nil)
}

// releaseProcessTreeAfterExit 在进程退出后以 Remaining 证据判定清理是否已经收敛。
// Darwin 可能因缺少 pidfd 等价物而拒绝破坏性信号，但关闭 stdin 后语言服务器仍会自然退出；
// 此时 exact owner 报告零成员并成功 Release，先前的零信号错误不应继续冒充 CleanupPending。
// 没有 Remaining 能力、等待超时或仍有成员时继续保留 owner 与原始终止错误。
func (t *transport) releaseProcessTreeAfterExit(terminationErr, waitErr error) (error, error, bool) {
	if waitErr != nil {
		t.logShutdownStage("release", "skipped", waitErr)
		return terminationErr, nil, false
	}
	if terminationErr != nil && !t.processTreeCanProveRemaining() {
		t.logShutdownStage("release", "skipped", terminationErr)
		return terminationErr, nil, false
	}
	releaseErr, releaseComplete := t.releaseProcessTreeAttempt()
	t.logShutdownStageResult("release", releaseErr)
	if terminationErr != nil && releaseErr == nil && releaseComplete {
		return nil, nil, true
	}
	return terminationErr, releaseErr, releaseComplete
}

// processTreeCanProveRemaining 要求 owner 提供 action-time 成员闭包，禁止仅凭根进程退出推断整棵树已清空。
func (t *transport) processTreeCanProveRemaining() bool {
	if t == nil || t.processTree == nil {
		return false
	}
	_, ok := t.processTree.(interface {
		Remaining() ([]hiddenexec.ProcessIdentity, error)
	})
	return ok
}

// drainResponders 在 timeout 内等待所有已登记的服务端请求响应 goroutine。
// 返回错误只表示排空超时；调用方仍会继续 killProcess，避免卡住的语言服务器阻塞关闭。
func (t *transport) drainResponders(timeout time.Duration) error {
	t.sealResponderAdmission()
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
	return t.terminateProcessTree()
}

// prepareProcessTreeShutdown 在协议 shutdown/exit 前通过 exact owner 入册当前后代。
// 缺少 preparation 能力时返回安全错误，由 client 继续尝试非破坏性协议关闭；绝不退回裸 PID 或 process-group 推断。
func (t *transport) prepareProcessTreeShutdown() error {
	if t == nil || t.processTree == nil {
		if t != nil {
			t.logShutdownStage("prepare", "skipped", nil)
		}
		return nil
	}
	err := t.processTree.PrepareShutdown()
	if err != nil {
		t.logShutdownStage("prepare", "failed", err)
	} else {
		t.logShutdownStage("prepare", "completed", nil)
	}
	return err
}

// terminateProcessTree 统一所有关闭路径的进程树终止与 owner 释放。
func (t *transport) terminateProcessTree() error {
	err, _ := t.terminateProcessTreeAttempt()
	if t != nil && t.done != nil {
		select {
		case <-t.done:
			if err != nil {
				return err
			}
			releaseErr, _ := t.releaseProcessTreeAttempt()
			return errors.Join(err, releaseErr)
		default:
		}
	}
	return err
}

// terminateProcessTreeAttempt 为每次关闭尝试执行一次经 owner 授权的终止动作。
// 同一并发调用共享进行中的动作；失败不锁死后续关闭尝试，成功则保持终止完成状态。
// Release 延后到 wait/remaining 验证之后，避免未退出成员提前从账本消失。
func (t *transport) terminateProcessTreeAttempt() (error, bool) {
	if t == nil {
		return nil, true
	}
	if t.processTree == nil {
		// A bare *exec.Cmd has no immutable owner identity. Refuse to rebuild a
		// tree from its current PID; only StartProcessTree may authorize signals.
		return hiddenexec.ErrProcessTreeOwnerMissing, false
	}
	if t.processTreeReleased() {
		return nil, true
	}
	t.terminationMu.Lock()
	if t.terminationComplete {
		err := t.terminationErr
		t.terminationMu.Unlock()
		return err, t.processTreeReleased()
	}
	if t.terminationInFlight {
		done := t.terminationDone
		t.terminationMu.Unlock()
		<-done
		t.terminationMu.Lock()
		err := t.terminationErr
		t.terminationMu.Unlock()
		return err, t.processTreeReleased()
	}
	done := make(chan struct{})
	t.terminationInFlight = true
	t.terminationDone = done
	t.terminationMu.Unlock()

	err := t.processTree.Terminate()
	t.terminationMu.Lock()
	t.terminationErr = err
	t.terminationInFlight = false
	if err == nil {
		t.terminationComplete = true
	}
	close(done)
	t.terminationMu.Unlock()
	return err, t.processTreeReleased()
}

// processTreeReleased 返回 owner 是否已完成 release；读取与释放动作共用同一把锁。
func (t *transport) processTreeReleased() bool {
	t.treeReleaseMu.Lock()
	defer t.treeReleaseMu.Unlock()
	return t.treeReleased
}

// releaseProcessTreeAttempt 在进程退出且 owner remaining 为空后释放权柄。
// 验证失败只保留 owner，调用方可在下一次 Close 中重试。
func (t *transport) releaseProcessTreeAttempt() (error, bool) {
	if t == nil || t.processTree == nil {
		return nil, true
	}
	t.treeReleaseMu.Lock()
	defer t.treeReleaseMu.Unlock()
	if t.treeReleased {
		return nil, true
	}
	if verifier, ok := t.processTree.(interface {
		Remaining() ([]hiddenexec.ProcessIdentity, error)
	}); ok {
		remaining, err := verifier.Remaining()
		if err != nil {
			t.markTerminationRetryNeeded(err)
			return err, false
		}
		if len(remaining) != 0 {
			err := fmt.Errorf("LSP process-tree release blocked: %w: %d members remain", hiddenexec.ErrProcessTreeRemaining, len(remaining))
			t.markTerminationRetryNeeded(err)
			return err, false
		}
	}
	if err := t.processTree.Release(); err != nil {
		t.markTerminationRetryNeeded(err)
		return err, false
	}
	t.treeReleased = true
	return nil, true
}

// markTerminationRetryNeeded 仅在 owner 报告 CleanupPending/remaining 时解除成功锁存。
// 普通 Release 瞬态错误仍只重试 Release，避免无谓重复终止动作。
func (t *transport) markTerminationRetryNeeded(err error) {
	if err == nil || (!errors.Is(err, hiddenexec.ErrProcessTreeCleanupPending) && !errors.Is(err, hiddenexec.ErrProcessTreeRemaining)) {
		return
	}
	t.terminationMu.Lock()
	t.terminationComplete = false
	t.terminationMu.Unlock()
}

// processTreeRSSBytes 通过 transport 显式 owner 读取平台进程树 RSS。
func (t *transport) processTreeRSSBytes() (uint64, error) {
	if t == nil || t.processTree == nil {
		return 0, errors.New("LSP transport process-tree owner is unavailable")
	}
	return t.processTree.RSSBytes()
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

// readFailure 在 stdout EOF 时等待子进程退出，确保 stderr 与退出码进入最终错误。
func (t *transport) readFailure(err error) error {
	if t.closed.Load() {
		return ErrTransportClosed
	}
	if errors.Is(err, io.EOF) {
		if exitErr := t.waitForExit(defaultShutdownTimeout); exitErr != nil {
			return errors.Join(err, exitErr)
		}
		if waitErr := t.waitErr(); waitErr != nil {
			return waitErr
		}
		return err
	}
	if waitErr := t.waitErr(); waitErr != nil {
		return errors.Join(err, waitErr)
	}
	return err
}

func (t *transport) stopWithError(err error) {
	t.sealResponderAdmission()
	t.cancelActorContext()
	t.clearPending(err)
	t.closeInput()
	// 先排空服务端请求响应，再杀进程，避免 writeMessage 失败继续扩散成 goroutine 泄漏。
	_ = t.drainResponders(defaultResponderDrainTimeout)
	_ = t.terminateProcessTree()
}

// abortBlockedWrite 在 request ctx 到期时打断正在阻塞的 stdin 写入。
// 这里不等待 responder 排空，避免取消路径再次被语言服务器背压拖住。
func (t *transport) abortBlockedWrite(err error) {
	t.sealResponderAdmission()
	t.cancelActorContext()
	t.clearPending(err)
	t.closeInput()
	_ = t.terminateProcessTree()
}
