package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	transportReadyTimeout        = 30 * time.Second
	transportShutdownGracePeriod = 3 * time.Second
	transportKillWaitTimeout     = 5 * time.Second
	transportStderrLimitBytes    = 8 * 1024
	transportConnectRetryDelay   = 150 * time.Millisecond
	transportConnectRetryMaxWait = time.Second
	transportHealthPingTimeout   = time.Second
)

var errConnectRetryPending = errors.New("codexapp: connect retry pending")

type transport struct {
	serverURL  string
	local      bool
	ws         *websocket.Conn
	process    *localProcess
	processErr error
	stateMu    sync.RWMutex
	writeMu    sync.Mutex
	pending    sync.Map
	nextID     atomic.Int64
	looping    atomic.Bool
	closing    atomic.Bool
	closed     atomic.Bool
	codexHome  atomic.Value
}

type transportCallError struct {
	method         string
	writeAttempted bool
	err            error
}

func newTransportCallError(method string, writeAttempted bool, err error) error {
	if err == nil {
		return nil
	}
	return &transportCallError{method: strings.TrimSpace(method), writeAttempted: writeAttempted, err: err}
}

// Error 保留底层 transport 错误文案，避免改变现有 shouldReconnect 分类。
func (e *transportCallError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

// Unwrap 暴露底层 websocket/context/RPC 错误，供 errors.Is/As 继续工作。
func (e *transportCallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func transportWriteAttempted(err error) bool {
	var callErr *transportCallError
	return errors.As(err, &callErr) && callErr.writeAttempted
}

func newTransport(ctx context.Context, serverURL string) (*transport, error) {
	startupCtx, cancel := withTimeout(shared.NonNilContext(ctx), transportReadyTimeout)
	defer cancel()
	t := &transport{serverURL: normalizeServerURL(serverURL)}
	if t.serverURL == "" {
		if err := t.spawnLocal(startupCtx); err != nil {
			return nil, err
		}
	}
	if err := t.establish(startupCtx); err != nil {
		_ = t.Kill()
		return nil, err
	}
	return t, nil
}

// Call 通过 JSON-RPC 向 Codex app 发送请求并等待同 ID 响应。
// pending 表必须在返回前清理；写入前会剥离内部字段，避免 provider 侧 strict schema 拒绝。
func (t *transport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	id := t.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	pc := &pendingCall{done: make(chan struct{})}
	t.pending.Store(key, pc)
	defer t.pending.Delete(key)
	if err := t.writeJSON(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: sanitizeProviderPayload(method, params)}); err != nil {
		return nil, newTransportCallError(method, true, err)
	}
	select {
	case <-callCtx.Done():
		return nil, newTransportCallError(method, true, callCtx.Err())
	case <-pc.done:
		if pc.err != nil {
			return nil, newTransportCallError(method, true, pc.err)
		}
		return pc.result, pc.err
	}
}

// Notify 发送无响应 JSON-RPC 通知到 Codex app。
// 与 Call 一样会清理内部字段，但不会登记 pending call，也不会等待远端确认。
func (t *transport) Notify(method string, params any) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}
	return t.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: sanitizeProviderPayload(method, params)})
}

// sanitizeProviderPayload 移除 Go 侧内部追踪字段后再交给 python proxy。
// approval/respond 保留 requestId 兼容桥接协议，其余未知内部字段必须剥离以通过 Pydantic 严格校验。
func sanitizeProviderPayload(method string, payload any) any {
	if payload == nil {
		return nil
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	safe := make(map[string]any, len(m))
	preserveApprovalRequestID := strings.TrimSpace(method) == "approval/respond"
	for k, v := range m {
		// 内部追踪键不是 provider API 的一部分，发送前必须剥离以避免严格校验失败。
		switch k {
		case "request_id":
			if preserveApprovalRequestID {
				if _, exists := safe["requestId"]; !exists {
					safe["requestId"] = v
				}
			}
			continue
		case "requestId":
			if preserveApprovalRequestID {
				safe["requestId"] = v
			}
			continue
		case "uiType", "uiText", "uiCommand", "uiFiles", "uiExitCode", "internal", "worker":
			continue
		}
		safe[k] = v
	}
	return safe
}

// ReadLoop 持续读取 WebSocket 消息并交给 session handler 分发。
// 同一 transport 只允许一个读循环运行，退出时释放 looping 标记以支持恢复后的重新建立。
func (t *transport) ReadLoop(ctx context.Context, handler any) {
	if !t.looping.CompareAndSwap(false, true) {
		return
	}
	defer t.looping.Store(false)
	for t.readLoopStep(ctx, handler) {
	}
}

// Close 关闭 Codex app transport 并执行优雅清理。
func (t *transport) Close() error { return t.shutdownTransport(true) }

// Kill 强制终止底层进程或连接。
func (t *transport) Kill() error { return t.shutdownTransport(false) }

// Running 返回底层进程或连接是否仍在运行。
func (t *transport) Running() bool {
	if t.closed.Load() || t.currentWS() == nil {
		return false
	}
	return !t.local || t.processRunning()
}

// CheckHealth 对当前 transport 做轻量健康检查。
// 本地进程异常优先返回进程错误；WebSocket 可用时发送 ping，确保恢复逻辑能区分连接和进程故障。
func (t *transport) CheckHealth(ctx context.Context) error {
	if t == nil {
		return errors.New("codexapp: transport unavailable")
	}
	if !t.Running() {
		if err := t.localProcessFailure(); err != nil {
			return err
		}
		return errors.New("codexapp: transport not running")
	}
	pingCtx, cancel := withTimeout(ctx, transportHealthPingTimeout)
	defer cancel()
	if err := shared.CheckCtx(pingCtx); err != nil {
		return err
	}
	deadline := time.Now().Add(transportHealthPingTimeout)
	if ctxDeadline, ok := pingCtx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	ws, err := t.currentWSOrErr()
	if err != nil {
		return err
	}
	if err := ws.WriteControl(websocket.PingMessage, []byte("health"), deadline); err != nil {
		return err
	}
	return shared.CheckCtx(pingCtx)
}

// InitializeCodexHome 返回 initialize 握手记录的 Codex home。
// 未完成握手或 transport 为空时返回空字符串，由上层决定是否视为缺失配置。
func (t *transport) InitializeCodexHome() string {
	if t == nil {
		return ""
	}
	value, _ := t.codexHome.Load().(string)
	return strings.TrimSpace(value)
}

// reconnect 在恢复流程中重建 WebSocket，并在本地进程已退出时重新拉起 app-server。
// 调用方通过 recoveryMu 串行化该路径，因此这里可以先清 closed 标记再关闭旧 socket。
func (t *transport) reconnect(ctx context.Context) error {
	if t == nil {
		return errors.New("codexapp: transport unavailable")
	}
	if t.closing.Load() {
		return errSessionClosing
	}
	// 只有 attemptRecovery 持有 recoveryMu 后才会调用 reconnect，因此可安全清除 closed 标记。
	// 这样空闲断开或网络失败后的 WebSocket 才能重新建立。
	t.closed.Store(false)
	t.closeSocket()
	if t.local && !t.processRunning() {
		if err := t.spawnLocal(ctx); err != nil {
			return err
		}
	}
	return t.establish(ctx)
}

func (t *transport) establish(ctx context.Context) error {
	ctx = shared.NonNilContext(ctx)
	if err := t.connect(ctx); err != nil {
		return err
	}
	return t.initialize(ctx)
}

// connect 按退避节奏重试 WebSocket 连接，直到连接成功、进程失败或上下文取消。
// 临时连接失败会包装为 errConnectRetryPending，真实进程错误必须立即冒泡。
func (t *transport) connect(ctx context.Context) error {
	retryDelay := transportConnectRetryDelay
	for {
		connected, err := t.connectAttempt(ctx)
		if err != nil && !errors.Is(err, errConnectRetryPending) {
			return err
		}
		if connected {
			return nil
		}
		if err := t.waitForConnectRetry(ctx, retryDelay); err != nil {
			return err
		}
		retryDelay = nextConnectRetryDelay(retryDelay)
	}
}

// connectAttempt 执行一次 WebSocket 连接尝试并同步检查本地进程状态。
// 如果 app-server 仍在启动或 socket 暂不可达，返回可重试标记；进程退出则返回最终错误。
func (t *transport) connectAttempt(ctx context.Context) (bool, error) {
	if err := t.localProcessReady(); err != nil {
		if procErr := t.localProcessFailure(); procErr != nil {
			return false, procErr
		}
		return false, fmt.Errorf("%w: %w", errConnectRetryPending, err)
	}
	if err := t.connectOnce(ctx); err != nil {
		if procErr := t.localProcessFailure(); procErr != nil {
			return false, procErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, fmt.Errorf("%w: %w", errConnectRetryPending, err)
	}
	return true, nil
}

func (t *transport) waitForConnectRetry(ctx context.Context, delay time.Duration) error {
	select {
	case <-ctx.Done():
		if procErr := t.localProcessFailure(); procErr != nil {
			return procErr
		}
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func nextConnectRetryDelay(delay time.Duration) time.Duration {
	if delay < transportConnectRetryMaxWait {
		delay *= 2
		if delay > transportConnectRetryMaxWait {
			return transportConnectRetryMaxWait
		}
	}
	return delay
}

func (t *transport) localProcessFailure() error {
	if !t.local {
		return nil
	}
	return t.processFailure()
}

func (t *transport) readLoopStep(ctx context.Context, handler any) bool {
	if err := shared.CheckCtx(ctx); err != nil {
		return false
	}
	if t.closed.Load() {
		return false
	}
	ws := t.currentWS()
	if ws == nil {
		return t.endReadLoop(ctx, handler, ws, nil, "connection unavailable")
	}
	_, data, err := ws.ReadMessage()
	if err != nil {
		return t.endReadLoop(ctx, handler, ws, err, err.Error())
	}
	return t.dispatchReadMessage(ctx, data, handler)
}

// transportServer 把 *transport 适配为池使用的 SpawnedServer。
// 字段刻意收窄到 URL、进程存活和有序关闭，避免池层依赖 transport 内部细节。
type transportServer struct{ t *transport }

func wrapTransport(t *transport) SpawnedServer { return &transportServer{t: t} }

// ServerURL 返回已启动 Codex app 服务地址。
func (s *transportServer) ServerURL() string {
	if s == nil || s.t == nil {
		return ""
	}
	s.t.stateMu.RLock()
	defer s.t.stateMu.RUnlock()
	return s.t.serverURL
}

// Close 按池接口关闭底层 transport，保持 graceful shutdown 语义。
// nil 接收者视为已关闭，便于池释放路径保持幂等。
func (s *transportServer) Close(ctx context.Context) error {
	if s == nil || s.t == nil {
		return nil
	}
	ctx = nonNilContext(ctx)
	done := make(chan error, 1)
	safego.Go(ctx, nil, "codexapp.transportServer.close", func(context.Context) { done <- s.t.shutdownTransport(true) })
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Alive 返回池中 app-server 进程是否仍在运行。
// 这里只看本地进程状态，连接健康由 session/transport 的恢复路径进一步确认。
func (s *transportServer) Alive() bool { return s != nil && s.t != nil && s.t.processRunning() }

// DiagnoseExit 返回进程 stderr 尾部和已记录的退出错误，供池退避日志使用。
// 该方法不触发额外清理，只读取当前快照，避免诊断路径改变进程生命周期。
func (s *transportServer) DiagnoseExit() (string, error) {
	if s == nil || s.t == nil {
		return "", nil
	}
	err := s.t.processFailure()
	var tail string
	if proc := s.t.currentProcess(); proc != nil {
		tail = proc.stderrSummary()
	}
	return tail, err
}

var _ SpawnedServer = (*transportServer)(nil)
