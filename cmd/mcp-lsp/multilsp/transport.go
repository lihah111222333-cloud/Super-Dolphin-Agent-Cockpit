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
	"slices"
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

// ServerRequestHandler 处理 LSP 服务端主动发起的请求。
type ServerRequestHandler func(context.Context, string, json.RawMessage) (any, error)

// transportOptions 配置 transport 启动参数。
type transportOptions struct {
	Binary, Dir         string
	Args, Env           []string
	NotificationHandler protocol.NotificationHandler
	RequestHandler      ServerRequestHandler
}

type processTreeOwner interface {
	Terminate() error
	Release() error
	RSSBytes() (uint64, error)
	PrepareShutdown() error
}

type transportCloseAttempt struct {
	done   chan struct{}
	result error
}

// transport 封装 LSP 子进程的 stdin/stdout 通信，管理 pending 请求和响应派发。
type transport struct {
	cmd                 *exec.Cmd
	processTree         processTreeOwner
	stdin               io.WriteCloser
	stdinMu             sync.Mutex
	stdout              *bufio.Reader
	stderr              *limitedBuffer
	notificationHandler protocol.NotificationHandler
	requestHandler      ServerRequestHandler
	writeMu             sync.Mutex
	pendingMu           sync.Mutex
	pending             map[string]chan pendingResult
	nextID              atomic.Int64
	closed              atomic.Bool
	closeMu             sync.Mutex
	closeAttempt        *transportCloseAttempt
	closeComplete       bool
	closeResult         error
	done                chan struct{}
	doneMu              sync.Mutex
	doneErr             error
	terminationMu       sync.Mutex
	terminationInFlight bool
	terminationDone     chan struct{}
	terminationComplete bool
	terminationErr      error
	treeReleaseMu       sync.Mutex
	treeReleased        bool
	actorCtx            context.Context
	cancelActors        context.CancelFunc

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
	return newTransportWithStarter(options, hiddenexec.StartProcessTree)
}

// newTransportWithStarter 启动并绑定 LSP transport 的 exact process-tree owner。
func newTransportWithStarter(
	options transportOptions,
	starter func(*exec.Cmd) (*hiddenexec.ProcessTree, error),
) (*transport, error) {
	cmd, processTree, stdin, stdout, stderr, err := startTransportWithStarter(options, starter)
	if err != nil {
		return nil, errors.Join(err, cleanupFailedProcessTree(processTree))
	}
	actorCtx, cancelActors := context.WithCancel(context.Background())
	t := &transport{
		cmd:                 cmd,
		processTree:         processTree,
		stdin:               stdin,
		stdout:              bufio.NewReader(stdout),
		stderr:              stderr,
		notificationHandler: options.NotificationHandler,
		requestHandler:      options.RequestHandler,
		pending:             map[string]chan pendingResult{},
		done:                make(chan struct{}),
		actorCtx:            actorCtx,
		cancelActors:        cancelActors,
	}
	safego.Go(actorCtx, nil, "mcp-lsp.transport.wait", func(context.Context) {
		t.wait()
	})
	safego.Go(actorCtx, nil, "mcp-lsp.transport.read-loop", func(context.Context) {
		t.readLoop()
	})
	return t, nil
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
	ctx = platformshared.NonNilContext(ctx)
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, defaultRequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
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
	select {
	case outcome := <-result:
		return platformshared.CloneRawMessage(outcome.result), outcome.err
	case <-ctx.Done():
		t.removePending(key)
		t.abortBlockedWrite(ctx.Err())
		return nil, ctx.Err()
	}
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
	return t.writeMessageContext(ctx, notification)
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
		return err
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
	if t.notificationHandler == nil {
		return nil
	}
	err := protocol.DispatchNotification(payload, t.notificationHandler)
	if errors.Is(err, protocol.ErrUnsupportedNotification) {
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

// lspCompatEmptyStructMethods 是 transport 会以 struct{}{} ACK 的完整方法表。
// 新增兼容方法必须先落到这里，避免分散在 transport 分支里形成隐式放行。
var lspCompatEmptyStructMethods = []string{
	LSPCompatMethodClientRegisterCapability,
	LSPCompatMethodClientUnregisterCapability,
	LSPCompatMethodWindowWorkDoneProgressCreate,
	LSPCompatMethodWorkspaceSemanticTokensRefresh,
	LSPCompatMethodWorkspaceCodeLensRefresh,
	LSPCompatMethodWorkspaceInlayHintRefresh,
	LSPCompatMethodWorkspaceDiagnosticRefresh,
}

func isLSPCompatEmptyStructMethod(method string) bool {
	return slices.Contains(lspCompatEmptyStructMethods, method)
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
