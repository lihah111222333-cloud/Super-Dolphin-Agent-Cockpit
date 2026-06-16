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

	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/gorilla/websocket"
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
		return nil, err
	}
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case <-pc.done:
		return pc.result, pc.err
	}
}

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

// sanitizeProviderPayload filters out internal-only Go side tracking fields
// before the payload is dispatched to the python proxy, satisfying strict Pydantic extra='forbid' validation.
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
		// Strip internal tracking keys that are not part of the provider's API.
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

func (t *transport) InitializeCodexHome() string {
	if t == nil {
		return ""
	}
	value, _ := t.codexHome.Load().(string)
	return strings.TrimSpace(value)
}

func (t *transport) reconnect(ctx context.Context) error {
	if t == nil {
		return errors.New("codexapp: transport unavailable")
	}
	if t.closing.Load() {
		return errSessionClosing
	}
	// Reset the closed flag so the reconnection can proceed. This is safe
	// because reconnect is only called from attemptRecovery which serializes
	// via recoveryMu. If the transport was closed due to an idle disconnect
	// or network failure, we need to clear the flag to re-establish the WS.
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

// transportServer bridges *transport to the SpawnedServer interface the pool
// uses. Fields are intentionally narrow: the pool only cares about the
// WebSocket URL, process liveness, and orderly shutdown.
type transportServer struct {
	t *transport
}

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

func (s *transportServer) Close(_ context.Context) error {
	if s == nil || s.t == nil {
		return nil
	}
	return s.t.shutdownTransport(true)
}

func (s *transportServer) Alive() bool {
	if s == nil || s.t == nil {
		return false
	}
	return s.t.processRunning()
}

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
