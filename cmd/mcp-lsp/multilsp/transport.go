package multilsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	defaultRequestTimeout = 60 * time.Second
	jsonRPCMethodNotFound = -32601
	jsonRPCInternalError  = -32603
)

// ErrLSPResponseTimeout distinguishes a completed write whose peer did not
// answer before the request budget. It still joins DeadlineExceeded for the
// MCP error envelope, but manager retry must never turn this terminal result
// into a later success from a duplicate semantic request.
var ErrLSPResponseTimeout = errors.New("LSP response timeout")

// ServerRequestHandler 处理 LSP 服务端主动发起的请求。
type ServerRequestHandler func(context.Context, string, json.RawMessage) (any, error)

// ServerNotificationSender 让服务端通知处理器沿用当前 transport 回发通知。
// 回发入口只暴露通用 LSP method/params，不把任何语言服务协议硬编码到 transport。
type ServerNotificationSender func(context.Context, string, any) error

// ServerNotificationHandler 处理未被标准 NotificationHandler 识别的服务端通知。
// handler 返回 ErrMethodNotSupported 时保留未知通知的兼容忽略语义；其他错误直接终止 transport。
type ServerNotificationHandler func(context.Context, string, json.RawMessage, ServerNotificationSender) error

// transportOptions 配置 transport 启动参数。
type transportOptions struct {
	Binary, Dir               string
	Args, Env                 []string
	NotificationHandler       protocol.NotificationHandler
	RequestHandler            ServerRequestHandler
	ServerNotificationHandler ServerNotificationHandler
}

type processTreeOwner interface {
	Terminate() error
	Release() error
	RSSBytes() (uint64, error)
	PrepareShutdown() error
}

// jdtlsLaunchArguments 仅依据固定 launcher 参数识别 JDTLS，供 serverInfo 缺失时维持稳定身份。
func jdtlsLaunchArguments(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "org.eclipse.equinox.launcher") || strings.Contains(lower, "jdt-language-server") {
			return true
		}
	}
	return false
}

type transportCloseAttempt struct {
	done   chan struct{}
	result error
}

// transport 封装 LSP 子进程的 stdin/stdout 通信，管理 pending 请求和响应派发。
type transport struct {
	cmd                       *exec.Cmd
	processTree               processTreeOwner
	stdin                     io.WriteCloser
	stdinMu                   sync.Mutex
	stdout                    *bufio.Reader
	stderr                    *limitedBuffer
	notificationHandler       protocol.NotificationHandler
	requestHandler            ServerRequestHandler
	serverNotificationHandler ServerNotificationHandler
	logger                    *slog.Logger
	writeMu                   sync.Mutex
	pendingMu                 sync.Mutex
	pending                   map[string]chan pendingResult
	nextID                    atomic.Int64
	closed                    atomic.Bool
	closeMu                   sync.Mutex
	closeAttempt              *transportCloseAttempt
	closeComplete             bool
	closeResult               error
	done                      chan struct{}
	doneMu                    sync.Mutex
	doneErr                   error
	binaryPath                string
	workingDirectory          string
	argumentCount             int
	envOverrideCount          int
	jdtlsLaunch               bool
	startupDiagnostics        []any
	startedAt                 time.Time
	writeFailureLogged        atomic.Bool
	terminationMu             sync.Mutex
	terminationInFlight       bool
	terminationDone           chan struct{}
	terminationComplete       bool
	terminationErr            error
	treeReleaseMu             sync.Mutex
	treeReleased              bool
	actorCtx                  context.Context
	cancelActors              context.CancelFunc

	responderMu              sync.Mutex
	responderAdmissionClosed bool
	responderWG              sync.WaitGroup
}

// pendingResult 保存单次 LSP 请求的响应结果，通过 channel 传回调用方。
type pendingResult struct {
	result json.RawMessage
	err    error
}

// newTransport 启动 LSP 子进程并初始化 transport，启动 readLoop 和 wait goroutine。
func newTransport(options transportOptions) (*transport, error) {
	cmd, processTree, stdin, stdout, stderr, err := startTransport(options)
	return newTransportFromStarted(options, cmd, processTree, stdin, stdout, stderr, err)
}

// newTransportWithStarter 启动并绑定 LSP transport 的 exact process-tree owner。
func newTransportWithStarter(
	options transportOptions,
	starter func(*exec.Cmd) (*hiddenexec.ProcessTree, error),
) (*transport, error) {
	cmd, processTree, stdin, stdout, stderr, err := startTransportWithStarter(options, starter)
	return newTransportFromStarted(options, cmd, processTree, stdin, stdout, stderr, err)
}

// newTransportFromStarted 将已启动的受管进程组装为 transport，并统一接管启动失败清理。
func newTransportFromStarted(
	options transportOptions,
	cmd *exec.Cmd,
	processTree *hiddenexec.ProcessTree,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
	stderr *limitedBuffer,
	startErr error,
) (*transport, error) {
	if startErr != nil {
		logTransportStartupFailure(options, cmd, processTree, startErr)
		return nil, errors.Join(startErr, cleanupFailedProcessTree(processTree))
	}
	actorCtx, cancelActors := context.WithCancel(context.Background())
	t := &transport{
		cmd:                       cmd,
		processTree:               processTree,
		stdin:                     stdin,
		stdout:                    bufio.NewReader(stdout),
		stderr:                    stderr,
		notificationHandler:       options.NotificationHandler,
		requestHandler:            options.RequestHandler,
		serverNotificationHandler: options.ServerNotificationHandler,
		logger:                    pkglogger.Get(),
		pending:                   map[string]chan pendingResult{},
		done:                      make(chan struct{}),
		actorCtx:                  actorCtx,
		cancelActors:              cancelActors,
		binaryPath:                options.Binary,
		workingDirectory:          options.Dir,
		argumentCount:             len(options.Args),
		envOverrideCount:          len(options.Env),
		jdtlsLaunch:               jdtlsLaunchArguments(options.Args),
		startupDiagnostics:        lspStartupDiagnosticFields(options, cmd),
		startedAt:                 time.Now(),
	}
	t.logProcessLifecycle("start", "completed", nil, "", 0, false)
	safego.Go(actorCtx, nil, "mcp-lsp.transport.wait", func(context.Context) {
		t.wait()
	})
	safego.Go(actorCtx, nil, "mcp-lsp.transport.read-loop", func(context.Context) {
		t.readLoop()
	})
	return t, nil
}

// logTransportStartupFailure 记录启动阶段的脱敏失败事实，保留是否已取得 exact owner。
func logTransportStartupFailure(options transportOptions, cmd *exec.Cmd, processTree processTreeOwner, startErr error) {
	logger := pkglogger.Get()
	if logger == nil {
		return
	}
	fields := transportLifecycleLogFields(
		options.Binary,
		options.Dir,
		len(options.Args),
		len(options.Env),
		cmd,
		processTree,
	)
	fields = append(fields, "event", "lsp_process_lifecycle", "stage", "start", "action_result", "failed")
	fields = append(fields, platformshared.SafePayloadLogFields("start_error", startErr.Error())...)
	logger.Warn("LSP process lifecycle", fields...)
}

// logProcessLifecycle 记录不含参数、环境值、完整路径或 stderr 原文的进程生命周期事实。
func (t *transport) logProcessLifecycle(stage, actionResult string, lifecycleErr error, stderr string, stderrBytes int64, stderrTruncated bool) {
	if t == nil {
		return
	}
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if logger == nil {
		return
	}
	fields := transportLifecycleLogFields(
		t.binaryPath,
		t.workingDirectory,
		t.argumentCount,
		t.envOverrideCount,
		t.cmd,
		t.processTree,
	)
	fields = append(fields, "event", "lsp_process_lifecycle", "stage", stage, "action_result", actionResult)
	if lifecycleErr != nil || actionResult == "failed" {
		fields = append(fields, t.startupDiagnostics...)
	}
	if stderrBytes > 0 {
		fields = append(fields, "stderr_total_bytes", stderrBytes, "stderr_truncated", stderrTruncated)
		fields = append(fields, platformshared.SafePayloadLogFields("stderr_tail", stderr)...)
	}
	if lifecycleErr != nil {
		fields = append(fields, platformshared.SafePayloadLogFields("lifecycle_error", lifecycleErr.Error())...)
	}
	if lifecycleErr != nil || actionResult == "failed" {
		logger.Warn("LSP process lifecycle", fields...)
		return
	}
	logger.Info("LSP process lifecycle", fields...)
}

func transportLifecycleLogFields(
	binaryPath, workingDirectory string,
	argumentCount, envOverrideCount int,
	cmd *exec.Cmd,
	processTree processTreeOwner,
) []any {
	processID := 0
	processStatePresent := false
	processExited := false
	exitCodePresent := false
	exitCode := 0
	if cmd != nil && cmd.Process != nil {
		processID = cmd.Process.Pid
	}
	if cmd != nil && cmd.ProcessState != nil {
		processStatePresent = true
		processExited = cmd.ProcessState.Exited()
		if processExited {
			exitCodePresent = true
			exitCode = cmd.ProcessState.ExitCode()
		}
	}
	fields := []any{
		"server_role", "language_server_stdio",
		"server_binary_basename", filepath.Base(filepath.Clean(binaryPath)),
		"argument_count", argumentCount,
		"env_override_count", envOverrideCount,
		"process_id", processID,
		"process_id_observation_only", true,
		"exact_process_tree_owner_present", processTree != nil,
		"process_state_present", processStatePresent,
		"process_exited", processExited,
		"exit_code_present", exitCodePresent,
	}
	if exitCodePresent {
		fields = append(fields, "exit_code", exitCode)
	}
	fields = append(fields, platformshared.SafePathLogFields("server_binary_path", binaryPath)...)
	fields = append(fields, platformshared.SafePathLogFields("working_directory", workingDirectory)...)
	return fields
}

// logReadFailure 把 LSP framing、EOF 和 stderr 事实分类记录，禁止输出协议行或服务端 stderr 原文。
func (t *transport) logReadFailure(readErr error) {
	if t == nil || readErr == nil {
		return
	}
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if logger == nil {
		return
	}
	actionResult := "failed"
	if t.closed.Load() && errors.Is(readErr, io.EOF) {
		actionResult = "expected_eof"
	}
	fields := transportLifecycleLogFields(
		t.binaryPath,
		t.workingDirectory,
		t.argumentCount,
		t.envOverrideCount,
		t.cmd,
		t.processTree,
	)
	fields = append(fields, "event", "lsp_stdio_read", "stage", "read_frame", "action_result", actionResult)
	var framingErr *lspFramingError
	if errors.As(readErr, &framingErr) {
		fields = append(fields,
			"failure_kind", framingErr.kind,
			"header_count", framingErr.headerCount,
			"observed_present", framingErr.observedPresent,
			"observed_bytes", framingErr.observedBytes,
			"observed_sha256", framingErr.observedSHA256,
			"expected_bytes", framingErr.expectedBytes,
			"received_bytes", framingErr.receivedBytes,
		)
	} else {
		fields = append(fields, "failure_kind", "io_or_dispatch")
	}
	fields = append(fields, platformshared.SafePayloadLogFields("read_error", readErr.Error())...)
	if t.stderr != nil {
		stderr, totalBytes, truncated := t.stderr.Snapshot()
		if totalBytes > 0 {
			fields = append(fields, "stderr_total_bytes", totalBytes, "stderr_truncated", truncated)
			fields = append(fields, platformshared.SafePayloadLogFields("stderr_tail", stderr)...)
		}
	}
	if actionResult == "expected_eof" {
		logger.Info("LSP stdio read", fields...)
		return
	}
	logger.Warn("LSP stdio read", fields...)
}

// cleanupFailedProcessTree consumes the startup owner transferred through the
// startTransport error tuple. StartProcessTree may have already started the
// exact cmd.Wait goroutine; ProcessTree methods are therefore the sole retry
// interface, and Release runs only after termination/remaining convergence.
func cleanupFailedProcessTree(processTree *hiddenexec.ProcessTree) error {
	return cleanupProcessTreeOwner(processTree)
}

// request 发送一次 JSON-RPC 请求并等待匹配 id 的响应。
// pending 表写入和清理都在超时/写失败路径中成对执行，避免调用方取消后留下悬挂 channel。
func (t *transport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	startedAt := time.Now()
	ctx = platformshared.NonNilContext(ctx)
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, defaultRequestTimeout)
	defer cancel()
	if err := t.requestPreflight(ctx); err != nil {
		return nil, err
	}
	request, err := protocol.BuildRequest(method, t.nextID.Add(1), params)
	if err != nil {
		return nil, err
	}
	key, result := normalizeID(request.ID), make(chan pendingResult, 1)
	if err := t.addPending(key, result); err != nil {
		return nil, err
	}
	if err := t.writeMessageContext(ctx, request); err != nil {
		t.removePending(key)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			t.abortBlockedWrite(err)
		}
		return nil, err
	}
	if err := t.afterRequestWrite(key); err != nil {
		return nil, err
	}
	select {
	case outcome := <-result:
		return t.completeRequestOutcome(outcome)
	case <-ctx.Done():
		t.removePending(key)
		t.logResponseTimeout(method, time.Since(startedAt))
		// 请求已经完整写入后，调用方超时只移除 pending。终止 transport 会丢弃
		// 语言服务器正在进行的 workspace load，使大仓库的每次重试都从零开始。
		// writeMessageContext 自己仍会在真正的阻塞写超时时调用 abortBlockedWrite。
		return nil, errors.Join(ErrLSPResponseTimeout, ctx.Err())
	}
}

// logResponseTimeout 记录响应等待超时，但不终止仍在处理 workspace 的语言服务器。
func (t *transport) logResponseTimeout(method string, elapsed time.Duration) {
	logger := pkglogger.Get()
	if logger == nil {
		return
	}
	args := []any{"event", "lsp_request_response_timeout", "lsp_method", method, "request_phase", "response_wait", "elapsed", elapsed, "transport_closed", t.closed.Load()}
	if t.cmd != nil && t.cmd.Process != nil {
		args = append(args, "server_pid", t.cmd.Process.Pid)
	}
	logger.Warn("LSP request timed out waiting for response", args...)
}

// notify 向 LSP 服务端发送无需响应的通知消息。
func (t *transport) notify(ctx context.Context, method string, params any) error {
	ctx = platformshared.NonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	notification, err := protocol.BuildNotification(method, params)
	if err != nil {
		return err
	}
	if err := t.writeMessageContext(ctx, notification); err != nil {
		return err
	}
	return t.checkFatalChildCrash()
}

// logShutdownStage 记录关闭阶段的脱敏结构化结果，不输出命令、路径、PID 或协议 payload。
func (t *transport) logShutdownStage(stage, actionResult string, stageErr error) {
	if t == nil {
		return
	}
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if logger == nil {
		return
	}
	args := []any{
		"event", "lsp_shutdown_stage",
		"stage", stage,
		"action_result", actionResult,
	}
	if stage == "prepare" || strings.HasPrefix(stage, "protocol_") {
		// prepare/protocol 阶段本身不具备破坏性信号权限。
		args = append(args, "signal_sent", false)
	}
	if stageErr != nil {
		args = append(args, platformshared.SafePayloadLogFields("shutdown_error", stageErr.Error())...)
	}
	if actionResult == "failed" {
		logger.Warn("LSP shutdown stage", args...)
		return
	}
	logger.Info("LSP shutdown stage", args...)
}

// logProcessTreeReleaseEvidence 记录 Remaining 与 Release 的精确收敛证据；
// 查询失败时 remaining_count_present=false，禁止把未知成员数伪装成零。
func (t *transport) logProcessTreeReleaseEvidence(actionResult string, remainingChecked, remainingCountPresent bool, remainingCount int, releaseErr error) {
	if t == nil {
		return
	}
	logger := t.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if logger == nil {
		return
	}
	fields := []any{
		"event", "lsp_process_tree_release",
		"action_result", actionResult,
		"release_result", actionResult,
		"remaining_checked", remainingChecked,
		"remaining_count_present", remainingCountPresent,
	}
	if remainingCountPresent {
		fields = append(fields, "remaining_count", remainingCount)
	}
	if releaseErr != nil {
		fields = append(fields, platformshared.SafePayloadLogFields("release_error", releaseErr.Error())...)
		logger.Warn("LSP process tree release", fields...)
		return
	}
	logger.Info("LSP process tree release", fields...)
}

// dispatchMessage 根据消息类型将其派发到对应处理器。
func (t *transport) dispatchMessage(payload json.RawMessage) error {
	envelope, err := protocol.DecodeEnvelope(payload)
	if err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(envelope.Method) == "":
		return t.handleResponse(payload)
	case normalizeID(envelope.ID) == "":
		return t.handleNotification(payload)
	default:
		t.spawnResponder(envelope)
		return nil
	}
}

// spawnResponder 为服务端主动请求启动受 transport 管理的响应 goroutine。
// 它在启动前登记 WaitGroup，Close 可等待响应写入完成；若已关闭则直接丢弃，避免关停阶段新增后台写入。
func (t *transport) spawnResponder(envelope protocol.Envelope) {
	t.responderMu.Lock()
	defer t.responderMu.Unlock()
	if t.responderAdmissionClosed || t.closed.Load() {
		return
	}
	t.responderWG.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("LSP responder panic", "panic", fmt.Sprint(r))
			}
		}()
		t.respondToServerRequest(envelope)
	})
}

// sealResponderAdmission 与 spawnResponder 共用互斥闸门，确保 Wait 开始后不会再登记 responder。
func (t *transport) sealResponderAdmission() bool {
	t.responderMu.Lock()
	defer t.responderMu.Unlock()
	first := !t.responderAdmissionClosed
	t.responderAdmissionClosed = true
	t.closed.Store(true)
	return first
}

// handleResponse 将收到的响应分发给对应的 pending 等待通道。
func (t *transport) handleResponse(payload json.RawMessage) error {
	response, err := protocol.DecodeResponse(payload)
	if err != nil {
		return fmt.Errorf("%w; response_shape=%s", err, jsonRPCShapeSummary(payload))
	}
	result := t.removePending(normalizeID(response.ID))
	if result == nil {
		return nil
	}
	if response.Error != nil {
		result <- pendingResult{err: &responseError{
			Code:    response.Error.Code,
			Message: response.Error.Message,
			Data:    platformshared.CloneRawMessage(response.Error.Data),
		}}
		return nil
	}
	result <- pendingResult{result: platformshared.CloneRawMessage(response.Result)}
	return nil
}

// handleNotification 将收到的通知转发给注册的通知处理器。
func (t *transport) handleNotification(payload json.RawMessage) error {
	if t.notificationHandler != nil {
		err := protocol.DispatchNotification(payload, t.notificationHandler)
		if err != nil && !errors.Is(err, protocol.ErrUnsupportedNotification) {
			return err
		}
		if err == nil {
			return nil
		}
	}
	if t.serverNotificationHandler == nil {
		return nil
	}
	envelope, err := protocol.DecodeEnvelope(payload)
	if err != nil {
		return err
	}
	err = t.serverNotificationHandler(
		platformshared.NonNilContext(t.actorCtx),
		envelope.Method,
		envelope.Params,
		func(ctx context.Context, method string, params any) error {
			return t.notify(ctx, method, params)
		},
	)
	if errors.Is(err, ErrMethodNotSupported) {
		return nil
	}
	return err
}

// respondToServerRequest 执行服务端请求并写回响应。
func (t *transport) respondToServerRequest(request protocol.Envelope) {
	result, err := t.serverRequestResult(platformshared.NonNilContext(t.actorCtx), request.Method, request.Params)
	message, err := buildServerResponse(request.ID, result, err)
	if err != nil {
		t.stopWithError(err)
		return
	}
	if err := t.writeMessage(message); err != nil && !errors.Is(err, ErrTransportClosed) {
		t.stopWithError(err)
	}
}

// serverRequestResult 调用 requestHandler 或默认兼容处理器返回结果。
func (t *transport) serverRequestResult(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if t.requestHandler != nil {
		result, err := t.requestHandler(ctx, method, params)
		if err == nil || !errors.Is(err, ErrMethodNotSupported) {
			return result, err
		}
	}
	return defaultServerRequestResult(method, params)
}

// buildServerResponse 将服务端请求结果或错误封装为 JSON-RPC 响应。
func buildServerResponse(id json.RawMessage, result any, err error) (any, error) {
	if err == nil {
		return protocol.BuildSuccessResponse(id, result)
	}
	if errors.Is(err, ErrMethodNotSupported) {
		return protocol.BuildErrorResponse(id, jsonRPCMethodNotFound, err.Error(), nil)
	}
	return protocol.BuildErrorResponse(id, jsonRPCInternalError, err.Error(), nil)
}

// 以下声明集中维护多语言 LSP transport 允许自动 ACK 的服务端主动请求。
// 未列入集合的方法必须返回 ErrMethodNotSupported，由上层映射为 JSON-RPC MethodNotFound，
// 避免未知服务端请求被静默确认。

// 服务端主动请求中可用空 struct 结果 ACK 的方法集合。
const (
	LSPCompatMethodClientRegisterCapability     = "client/registerCapability"
	LSPCompatMethodClientUnregisterCapability   = "client/unregisterCapability"
	LSPCompatMethodWindowWorkDoneProgressCreate = "window/workDoneProgress/create"
)

// workspace/*/refresh 系列请求同样只需要空 struct 结果。
const (
	LSPCompatMethodWorkspaceSemanticTokensRefresh = "workspace/semanticTokens/refresh"
	LSPCompatMethodWorkspaceCodeLensRefresh       = "workspace/codeLens/refresh"
	LSPCompatMethodWorkspaceInlayHintRefresh      = "workspace/inlayHint/refresh"
	LSPCompatMethodWorkspaceDiagnosticRefresh     = "workspace/diagnostic/refresh"
)

// workspace/configuration 需要返回与请求 items 等长的空配置数组。
const LSPCompatMethodWorkspaceConfiguration = "workspace/configuration"

func isLSPCompatEmptyStructMethod(method string) bool {
	switch method {
	case LSPCompatMethodClientRegisterCapability,
		LSPCompatMethodClientUnregisterCapability,
		LSPCompatMethodWindowWorkDoneProgressCreate,
		LSPCompatMethodWorkspaceSemanticTokensRefresh,
		LSPCompatMethodWorkspaceCodeLensRefresh,
		LSPCompatMethodWorkspaceInlayHintRefresh,
		LSPCompatMethodWorkspaceDiagnosticRefresh:
		return true
	default:
		return false
	}
}

// dispatchCompatServerRequest 根据兼容方法表处理服务端主动请求。
// 命中兼容表时记录稳定事件供诊断统计；未命中的方法返回 ErrMethodNotSupported，不做静默 ACK。
func dispatchCompatServerRequest(method string, params json.RawMessage) (any, error) {
	if isLSPCompatEmptyStructMethod(method) {
		pkglogger.Get().Info("LSP compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "empty_struct",
		)
		return struct{}{}, nil
	}
	if method == LSPCompatMethodWorkspaceConfiguration {
		pkglogger.Get().Info("LSP compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "workspace_configuration",
		)
		return emptyConfigurationResult(params), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrMethodNotSupported, method)
}

// defaultServerRequestResult 是 transport 回答服务端主动请求的默认入口。
func defaultServerRequestResult(method string, params json.RawMessage) (any, error) {
	return dispatchCompatServerRequest(method, params)
}

// emptyConfigurationResult 为 workspace/configuration 请求返回空配置列表。
func emptyConfigurationResult(params json.RawMessage) []any {
	var request struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(params, &request); err != nil || len(request.Items) == 0 {
		return []any{}
	}
	return make([]any, len(request.Items))
}

// addPending 注册 pending 请求 channel，transport 已关闭时返回错误。
func (t *transport) addPending(key string, result chan pendingResult) error {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	if t.closed.Load() {
		return ErrTransportClosed
	}
	t.pending[key] = result
	return nil
}

// removePending 移除并返回 key 对应的 pending channel。
func (t *transport) removePending(key string) chan pendingResult {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	result := t.pending[key]
	delete(t.pending, key)
	return result
}

// clearPending 关闭所有 pending channel 并写入错误，用于 transport 关闭时清理。
func (t *transport) clearPending(err error) {
	t.pendingMu.Lock()
	pending := t.pending
	t.pending = map[string]chan pendingResult{}
	t.pendingMu.Unlock()
	for _, result := range pending {
		select {
		case result <- pendingResult{err: err}:
		default:
		}
	}
}

func (t *transport) requestPreflight(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.checkFatalChildCrash()
}

func (t *transport) afterRequestWrite(key string) error {
	if err := t.checkFatalChildCrash(); err != nil {
		t.removePending(key)
		return err
	}
	return nil
}

func (t *transport) completeRequestOutcome(outcome pendingResult) (json.RawMessage, error) {
	if err := t.checkFatalChildCrash(); err != nil {
		return nil, err
	}
	return platformshared.CloneRawMessage(outcome.result), outcome.err
}

func (t *transport) checkFatalChildCrash() error {
	if t == nil || t.closed.Load() || t.stderr == nil {
		return nil
	}
	err := fatalChildCrashError(t.stderr.String())
	if err == nil {
		return nil
	}
	t.stopWithError(err)
	return err
}

func fatalChildCrashError(stderr string) error {
	trimmed := strings.TrimSpace(stderr)
	lower := strings.ToLower(trimmed)
	if !strings.Contains(lower, "fatal error:") ||
		!strings.Contains(lower, "heap out of memory") {
		return nil
	}
	return fmt.Errorf("%w: TypeScript child server crash: %s", ErrTransportClosed, trimmed)
}
