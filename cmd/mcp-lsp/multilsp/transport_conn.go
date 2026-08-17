package multilsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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
	supervised, err := hiddenexec.NewPlatformSupervisedCommand(options.Binary, options.Args...)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("construct LSP server process owner: %w", err)
	}
	return startTransportWithProcessCommand(options, transportProcessCommand{
		cmd:   supervised.Command(),
		start: supervised.StartProcessTree,
		close: supervised.Close,
	})
}

type transportProcessCommand struct {
	cmd   *exec.Cmd
	start func() (*hiddenexec.ProcessTree, error)
	close func() error
}

// startTransportWithStarter 创建 stdio 管道并把启动失败时的 owner 交给清理路径。
func startTransportWithStarter(
	options transportOptions,
	starter func(*exec.Cmd) (*hiddenexec.ProcessTree, error),
) (*exec.Cmd, *hiddenexec.ProcessTree, io.WriteCloser, io.ReadCloser, *limitedBuffer, error) {
	cmd := hiddenexec.Command(options.Binary, options.Args...)
	return startTransportWithProcessCommand(options, transportProcessCommand{
		cmd:   cmd,
		start: func() (*hiddenexec.ProcessTree, error) { return starter(cmd) },
	})
}

// startTransportWithProcessCommand 配置 stdio 并把平台监管 owner 原子移交给 transport。
func startTransportWithProcessCommand(
	options transportOptions,
	processCommand transportProcessCommand,
) (*exec.Cmd, *hiddenexec.ProcessTree, io.WriteCloser, io.ReadCloser, *limitedBuffer, error) {
	cmd := processCommand.cmd
	if cmd == nil || processCommand.start == nil {
		return nil, nil, nil, nil, nil, closeTransportProcessCommand(processCommand, errors.New("LSP server process command is incomplete"))
	}
	cmd.Dir = options.Dir
	if len(options.Env) > 0 {
		cmd.Env = append(os.Environ(), options.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, nil, closeTransportProcessCommand(processCommand, fmt.Errorf("LSP server start stdin pipe: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, nil, closeTransportProcessCommand(processCommand, fmt.Errorf("LSP server start stdout pipe: %w", err))
	}
	stderr := &limitedBuffer{limit: stderrLimitBytes}
	cmd.Stderr = stderr
	processTree, err := processCommand.start()
	if err != nil {
		var closeErr error
		if stdinErr := stdin.Close(); stdinErr != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close LSP server stdin after process-tree startup failure: %w", stdinErr))
		}
		if stdoutErr := stdout.Close(); stdoutErr != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close LSP server stdout after process-tree startup failure: %w", stdoutErr))
		}
		if processTree == nil {
			closeErr = errors.Join(closeErr, closeTransportProcessCommand(processCommand, nil))
		}
		return nil, processTree, nil, nil, nil, errors.Join(fmt.Errorf("LSP server start process tree: %w", err), closeErr)
	}
	return cmd, processTree, stdin, stdout, stderr, nil
}

// closeTransportProcessCommand 关闭尚未移交给 ProcessTree 的平台控制资源并保留原始错误。
func closeTransportProcessCommand(processCommand transportProcessCommand, prior error) error {
	if processCommand.close == nil {
		return prior
	}
	return errors.Join(prior, processCommand.close())
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
			t.logReadFailure(err)
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
	headerCount := 0
	for {
		line, err := t.stdout.ReadString('\n')
		if err != nil {
			return nil, newLSPFramingError("read_header", line, headerCount, 0, len(line), err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		headerCount++
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, newLSPFramingError("malformed_header", line, headerCount, 0, 0, nil)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 {
			if err == nil {
				err = errors.New("negative Content-Length")
			}
			return nil, newLSPFramingError("invalid_content_length", value, headerCount, 0, 0, err)
		}
	}
	if length < 0 {
		return nil, newLSPFramingError("missing_content_length", "", headerCount, 0, 0, nil)
	}
	body := make([]byte, length)
	readBytes, err := io.ReadFull(t.stdout, body)
	if err != nil {
		return nil, newLSPFramingError("read_body", "", headerCount, length, readBytes, err)
	}
	t.logJSONRPCShape(body)
	return body, nil
}

// logJSONRPCShape 在完整 stdio 帧边界记录脱敏 JSON-RPC 形状，追踪字段 presence 且不记录协议正文。
// 仅显式开启 MCP_LSP_TRACE_LSP_SHAPES 时输出，避免改变正常生产日志量与内容契约。
func (t *transport) logJSONRPCShape(payload []byte) {
	if t == nil || os.Getenv("MCP_LSP_TRACE_LSP_SHAPES") != "1" || t.logger == nil {
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.logger.Warn("LSP JSON-RPC shape", "parse_error", true, "payload_bytes", len(payload), "payload_sha256", digestLSPShape(payload))
		return
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result, resultPresent := fields["result"]
	_, errorPresent := fields["error"]
	method := strings.Trim(string(fields["method"]), `"`)
	idDigest := ""
	if id, ok := fields["id"]; ok {
		idDigest = digestLSPShape(id)
	}
	t.logger.Info("LSP JSON-RPC shape", "keys", keys, "method", method, "id_sha256", idDigest,
		"result_present", resultPresent, "result_is_null", resultPresent && strings.TrimSpace(string(result)) == "null",
		"error_present", errorPresent, "payload_bytes", len(payload), "payload_sha256", digestLSPShape(payload))
}

func digestLSPShape(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// jsonRPCShapeSummary 为协议错误附加脱敏字段形状，保证缺字段根因可复查而不泄露正文。
func jsonRPCShapeSummary(payload []byte) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Sprintf("parse_error=true,payload_bytes=%d,payload_sha256=%s", len(payload), digestLSPShape(payload))
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result, resultPresent := fields["result"]
	_, errorPresent := fields["error"]
	idDigest := ""
	if id, ok := fields["id"]; ok {
		idDigest = digestLSPShape(id)
	}
	return fmt.Sprintf("keys=%s,method=%s,id_sha256=%s,result_present=%t,result_is_null=%t,error_present=%t,payload_bytes=%d,payload_sha256=%s",
		strings.Join(keys, ","), strings.Trim(string(fields["method"]), `"`), idDigest,
		resultPresent, resultPresent && strings.TrimSpace(string(result)) == "null", errorPresent, len(payload), digestLSPShape(payload))
}

type lspFramingError struct {
	kind            string
	headerCount     int
	observedPresent bool
	observedBytes   int
	observedSHA256  string
	expectedBytes   int
	receivedBytes   int
	cause           error
}

// newLSPFramingError 对异常 stdout 行只保留长度和 SHA-256，避免诊断日志泄露协议正文。
func newLSPFramingError(kind, observed string, headerCount, expectedBytes, receivedBytes int, cause error) error {
	result := &lspFramingError{
		kind:            kind,
		headerCount:     headerCount,
		observedPresent: observed != "",
		observedBytes:   len(observed),
		expectedBytes:   expectedBytes,
		receivedBytes:   receivedBytes,
		cause:           cause,
	}
	if observed != "" {
		digest := sha256.Sum256([]byte(observed))
		result.observedSHA256 = hex.EncodeToString(digest[:])
	}
	return result
}

func (e *lspFramingError) Error() string {
	if e == nil {
		return "LSP framing failed"
	}
	return fmt.Sprintf(
		"LSP framing failed kind=%s header_count=%d observed_present=%t observed_bytes=%d observed_sha256=%s expected_bytes=%d received_bytes=%d",
		e.kind,
		e.headerCount,
		e.observedPresent,
		e.observedBytes,
		e.observedSHA256,
		e.expectedBytes,
		e.receivedBytes,
	)
}

func (e *lspFramingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
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
		t.logTransportWriteFailure("header", err)
		return t.joinWaitError(err)
	}
	if _, err := stdin.Write(payload); err != nil {
		t.logTransportWriteFailure("body", err)
		return t.joinWaitError(err)
	}
	return nil
}

// logTransportWriteFailure 记录首个 stdio 写失败的低敏边界事实；平台文件只补充
// Windows 的启动/管道诊断，非 Windows 保持原有日志语义。
func (t *transport) logTransportWriteFailure(stage string, writeErr error) {
	if t == nil || writeErr == nil || !t.writeFailureLogged.CompareAndSwap(false, true) {
		return
	}
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if logger == nil {
		return
	}
	fields := transportLifecycleLogFields(t.binaryPath, t.workingDirectory, t.argumentCount, t.envOverrideCount, t.cmd, t.processTree)
	fields = append(fields, "event", "lsp_stdio_write", "stage", stage, "action_result", "failed")
	fields = append(fields, lspTransportWriteFailureFields(t, stage, writeErr)...)
	logger.Warn("LSP stdio write", fields...)
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
	stderr, stderrBytes, stderrTruncated := t.stderr.Snapshot()
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		switch {
		case err != nil:
			err = fmt.Errorf("%w: %s", err, stderr)
		case !t.closed.Load():
			err = errors.New(stderr)
		}
	}
	if !t.closed.Load() {
		t.markUnexpectedExit(err)
	}
	t.doneMu.Lock()
	t.doneErr = err
	t.doneMu.Unlock()
	actionResult := "completed"
	if err != nil {
		actionResult = "failed"
	}
	t.logProcessLifecycle("wait", actionResult, err, stderr, stderrBytes, stderrTruncated)
	t.cancelActorContext()
	close(t.done)
}

// markUnexpectedExit 在语言服务器自行退出时立即封闭 transport，并失败所有未完成请求。
// 退出码与 stderr 保留在包装错误中，调用方可通过 ErrTransportClosed 识别不可用状态。
func (t *transport) markUnexpectedExit(cause error) {
	if cause == nil {
		cause = errors.New("language server exited unexpectedly")
	}
	failure := transportUnavailableError(cause)
	t.sealResponderAdmission()
	t.cancelActorContext()
	t.clearPending(failure)
	t.closeInput()
}

func transportUnavailableError(cause error) error {
	if cause == nil || errors.Is(cause, ErrTransportClosed) {
		if cause == nil {
			return ErrTransportClosed
		}
		return cause
	}
	return fmt.Errorf("%w: %w", ErrTransportClosed, cause)
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
	// The read/write failure path may already have terminated the exact tree,
	// waited for every member, and released its owner before manager shutdown
	// begins. Preparation is then complete by stronger evidence; re-entering a
	// released owner would turn an idempotent Close into a false lifecycle error.
	if t.processTreeReleased() {
		t.logShutdownStage("prepare", "skipped", nil)
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
		t.logProcessTreeReleaseEvidence("already_released", false, false, 0, nil)
		return nil, true
	}
	remainingChecked := false
	remainingCountPresent := false
	remainingCount := 0
	if verifier, ok := t.processTree.(interface {
		Remaining() ([]hiddenexec.ProcessIdentity, error)
	}); ok {
		remainingChecked = true
		remaining, err := verifier.Remaining()
		if err != nil {
			t.markTerminationRetryNeeded(err)
			t.logProcessTreeReleaseEvidence("failed", remainingChecked, false, 0, err)
			return err, false
		}
		remainingCountPresent = true
		remainingCount = len(remaining)
		if len(remaining) != 0 {
			err := fmt.Errorf("LSP process-tree release blocked: %w: %d members remain", hiddenexec.ErrProcessTreeRemaining, len(remaining))
			t.markTerminationRetryNeeded(err)
			t.logProcessTreeReleaseEvidence("failed", remainingChecked, remainingCountPresent, remainingCount, err)
			return err, false
		}
	}
	if err := t.processTree.Release(); err != nil {
		t.markTerminationRetryNeeded(err)
		t.logProcessTreeReleaseEvidence("failed", remainingChecked, remainingCountPresent, remainingCount, err)
		return err, false
	}
	t.treeReleased = true
	t.logProcessTreeReleaseEvidence("released", remainingChecked, remainingCountPresent, remainingCount, nil)
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
			return transportUnavailableError(errors.Join(err, exitErr))
		}
		if waitErr := t.waitErr(); waitErr != nil {
			return transportUnavailableError(waitErr)
		}
		return err
	}
	if waitErr := t.waitErr(); waitErr != nil {
		return transportUnavailableError(errors.Join(err, waitErr))
	}
	return err
}

func (t *transport) stopWithError(err error) {
	if !t.sealResponderAdmission() {
		return
	}
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
