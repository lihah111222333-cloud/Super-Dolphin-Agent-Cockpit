package rpc

import (
	"context"
	"encoding/json"
	"maps"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

const controlRPCAddrEnv = "GO_AGENT_CTL_RPC_ADDR"

type Server struct {
	logger        *pkglogger.Logger
	addr          string
	methods       handler.Map
	traceRecorder TraceRecorder

	mu         sync.RWMutex
	active     map[*jrpc2.Server]string
	activeUIWS int
	onConnects []func(*jrpc2.Server)
}

type rpcRequestTracker struct {
	logger *pkglogger.Logger

	mu      sync.Mutex
	pending map[string]rpcPendingRequest
}

type rpcPendingRequest struct {
	ID            string
	Method        string
	ThreadID      string
	ParamsPreview string
	StartedAt     time.Time
}

func newRPCRequestTracker(logger *pkglogger.Logger) *rpcRequestTracker {
	if logger == nil {
		return nil
	}
	return &rpcRequestTracker{
		logger:  logger,
		pending: map[string]rpcPendingRequest{},
	}
}

// LogRequest 处理日志请求。
func (t *rpcRequestTracker) LogRequest(_ context.Context, req *jrpc2.Request) {
	if t == nil || req == nil || req.IsNotification() {
		return
	}
	id := strings.TrimSpace(req.ID())
	if id == "" {
		return
	}
	params := req.ParamString()
	t.mu.Lock()
	t.pending[id] = rpcPendingRequest{
		ID:            id,
		Method:        strings.TrimSpace(req.Method()),
		ThreadID:      rpcRequestThreadID(params),
		ParamsPreview: rpcParamPreview(params),
		StartedAt:     time.Now(),
	}
	t.mu.Unlock()
}

// LogResponse 处理日志响应。
func (t *rpcRequestTracker) LogResponse(_ context.Context, rsp *jrpc2.Response) {
	if t == nil || rsp == nil {
		return
	}
	meta, ok := t.takePendingResponse(strings.TrimSpace(rsp.ID()))
	if !ok || t.logger == nil {
		return
	}
	rpcErr := rsp.Error()
	if rpcErr == nil {
		return
	}
	t.logger.Warn(rpcFailureLogMessage(rpcErr.Message), rpcFailureLogArgs(meta, rpcErr)...)
}

func (t *rpcRequestTracker) takePendingResponse(id string) (rpcPendingRequest, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return rpcPendingRequest{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	meta, ok := t.pending[id]
	if ok {
		delete(t.pending, id)
	}
	return meta, ok
}

func rpcFailureLogArgs(meta rpcPendingRequest, rpcErr *jrpc2.Error) []any {
	args := []any{
		"method", meta.Method,
		"request_id", meta.ID,
		"duration_ms", time.Since(meta.StartedAt).Milliseconds(),
		"error_code", int(rpcErr.Code),
		"error", strings.TrimSpace(rpcErr.Message),
	}
	if meta.ThreadID != "" {
		args = append(args, "thread_id", meta.ThreadID)
	}
	if meta.ParamsPreview != "" {
		args = append(args, "params_preview", meta.ParamsPreview)
	}
	return args
}

func rpcFailureLogMessage(raw string) string {
	switch message := strings.TrimSpace(raw); {
	case isRPCTimeoutMessage(message):
		return "rpc request timed out"
	case isRPCCanceledMessage(message):
		return "rpc request canceled"
	default:
		return "rpc request failed"
	}
}

func (t *rpcRequestTracker) logConnectionExit(err error) {
	if t == nil || t.logger == nil || err == nil {
		return
	}
	pending := t.snapshotPending(time.Now())
	if len(pending) == 0 {
		t.logger.Warn("rpc connection exited", "error", err)
		return
	}
	t.logger.Warn(
		"rpc connection exited with pending requests",
		"error", err,
		"pending_count", len(pending),
		"pending", pending,
	)
}

// snapshotPending 处理快照待处理。
func (t *rpcRequestTracker) snapshotPending(now time.Time) []map[string]any {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	items := make([]rpcPendingRequest, 0, len(t.pending))
	for _, current := range t.pending {
		items = append(items, current)
	}
	t.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	out := make([]map[string]any, 0, len(items))
	for _, current := range items {
		summary := map[string]any{
			"id":          current.ID,
			"method":      current.Method,
			"duration_ms": now.Sub(current.StartedAt).Milliseconds(),
		}
		if current.ThreadID != "" {
			summary["thread_id"] = current.ThreadID
		}
		if current.ParamsPreview != "" {
			summary["params_preview"] = current.ParamsPreview
		}
		out = append(out, summary)
	}
	return out
}

// rpcRequestThreadID 处理RPC请求线程ID。
func rpcRequestThreadID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// json.RawMessage: justified -- Wails bridge layer; extracts thread_id from
	// an unknown RPC param shape without requiring a typed struct.
	var params map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return ""
	}
	for _, key := range []string{"threadId", "threadID", "thread_id"} {
		value := params[key]
		if len(value) == 0 {
			continue
		}
		var threadID string
		if err := json.Unmarshal(value, &threadID); err == nil && strings.TrimSpace(threadID) != "" {
			return strings.TrimSpace(threadID)
		}
	}
	return ""
}

func rpcParamPreview(raw string) string {
	raw = strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if raw == "" {
		return ""
	}
	return truncateRPCText(raw, 160)
}

func truncateRPCText(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	if limit <= 1 {
		return raw[:limit]
	}
	return raw[:limit-1] + "…"
}

func isRPCTimeoutMessage(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(value, "deadline exceeded") ||
		strings.Contains(value, "timed out") ||
		strings.Contains(value, "timeout")
}

func isRPCCanceledMessage(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(value, "context canceled") ||
		strings.Contains(value, "cancelled") ||
		strings.Contains(value, "canceled")
}

// NewServer 创建服务端。
func NewServer(p Params) *Server {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Server{
		logger:        logger,
		addr:          p.Config.RPCAddr,
		methods:       handler.Map{},
		traceRecorder: p.TraceRecorder,
		active:        make(map[*jrpc2.Server]string),
	}
}

// Register 注册平台RPC。
func (s *Server) Register(handlerMaps ...handler.Map) {
	for _, current := range handlerMaps {
		maps.Copy(s.methods, current)
	}
}

// Dispatch executes a registered handler locally without using the network.
// It is used by the Wails binding layer to bridge CallAPI requests.
// json.RawMessage: justified -- Wails bridge layer; params/result are dynamic JSON
// whose shape is determined by the dispatched method at runtime.
// Dispatch 派发平台RPC。
func (s *Server) Dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	ctx = platformshared.NonNilContext(ctx)
	var err error
	ctx, _, err = pkglogger.WithChildTraceSpan(ctx)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	if err := s.recordDispatchTrace(ctx, method, params, startedAt, "backend.rpc.dispatch.start", "start", 0, TraceStatusOK, nil); err != nil {
		s.logTraceRecordError(ctx, method, "start", err)
	}

	var rpcLog jrpc2.RPCLogger
	if tracker := newRPCRequestTracker(s.logger); tracker != nil {
		rpcLog = tracker
	}

	local := jrpcserver.NewLocal(s.methods, &jrpcserver.LocalOptions{
		Server: prepareServerOptions(rpcLog, &jrpc2.ServerOptions{NewContext: func() context.Context { return ctx }}),
	})
	defer local.Close()

	var callParams any
	if len(params) != 0 {
		callParams = append(json.RawMessage(nil), params...)
	}

	var result json.RawMessage
	if err := local.Client.CallResult(ctx, method, callParams, &result); err != nil {
		if recordErr := s.recordDispatchTrace(ctx, method, params, startedAt, "backend.rpc.dispatch.failed", "failed", time.Since(startedAt), TraceStatusError, err); recordErr != nil {
			s.logTraceRecordError(ctx, method, "failed", recordErr)
		}
		return nil, err
	}
	duration := time.Since(startedAt)
	if err := s.recordDispatchTrace(ctx, method, params, startedAt, "backend.rpc.dispatch.done", "done", duration, rpcTraceStatus(method, duration), nil); err != nil {
		s.logTraceRecordError(ctx, method, "done", err)
	}
	return append(json.RawMessage(nil), result...), nil
}

func (s *Server) logTraceRecordError(ctx context.Context, method string, phase string, err error) {
	logger := s.logger
	if logger == nil {
		logger = pkglogger.FromContext(ctx)
	}
	logger.Warn("rpc dispatch trace record failed", "phase", phase, "method", method, "error", err)
}

// recordDispatchTrace 记录dispatchtrace。
func (s *Server) recordDispatchTrace(ctx context.Context, method string, params json.RawMessage, startedAt time.Time, kind string, phase string, duration time.Duration, status TraceStatus, dispatchErr error) error {
	if s == nil || s.traceRecorder == nil || !s.traceRecorder.Enabled() {
		return nil
	}
	metadata := map[string]any{
		"param_bytes": len(params),
	}
	if keys := rpcParamKeys(params); len(keys) > 0 {
		metadata["param_keys"] = keys
	}
	record := TraceRecord{
		Timestamp:    startedAt,
		TraceID:      pkglogger.TraceIDFromContext(ctx),
		SpanID:       pkglogger.SpanIDFromContext(ctx),
		ParentSpanID: pkglogger.ParentSpanIDFromContext(ctx),
		Kind:         kind,
		Phase:        phase,
		Method:       strings.TrimSpace(method),
		DurationMS:   duration.Milliseconds(),
		Status:       status,
		Code:         TraceCodeAnchor{File: "internal/platform/rpc/server.go", Function: "(*Server).Dispatch", Line: 270},
		Metadata:     metadata,
	}
	if dispatchErr != nil {
		record.Error = strings.TrimSpace(dispatchErr.Error())
	}
	return s.traceRecorder.RecordTrace(ctx, record)
}

func rpcParamKeys(raw json.RawMessage) []string {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}

func rpcTraceStatus(method string, duration time.Duration) TraceStatus {
	if duration > rpcSlowThreshold(method) {
		return TraceStatusSlow
	}
	return TraceStatusOK
}

func rpcSlowThreshold(method string) time.Duration {
	switch {
	case strings.TrimSpace(method) == "thread/start":
		return 1000 * time.Millisecond
	case strings.HasPrefix(strings.TrimSpace(method), "ui/"):
		return 300 * time.Millisecond
	default:
		return 500 * time.Millisecond
	}
}

// NotifyAll 处理notifyall。
func (s *Server) NotifyAll(ctx context.Context, bridge *PushBridge, method string, params any) {
	if bridge == nil {
		return
	}
	ctx = platformshared.NonNilContext(ctx)
	for _, current := range s.snapshotActive() {
		if err := bridge.NotifyClient(ctx, current, method, params); err != nil {
			s.logger.Warn("rpc push notify failed", "method", method, "error", err)
		}
	}
}

// Run 启动平台RPC后台流程。
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	activeAddr := listener.Addr().String()
	_ = os.Setenv(controlRPCAddrEnv, activeAddr)
	s.logger.Info("rpc server listening", "addr", activeAddr)
	err = s.acceptLoop(ctx, jrpcserver.NetAccepter(listener, channel.Line))
	if err != nil && !isExpectedCloseErr(err) {
		return err
	}
	return nil
}

func (s *Server) acceptLoop(ctx context.Context, accepter jrpcserver.Accepter) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		ch, err := accepter.Accept(ctx)
		if err != nil {
			return err
		}
		wg.Add(1)
		runtimesafe.SafeGo(ctx, s.logger, "rpc.serveConn", func(context.Context) {
			s.serveConn(ctx, ch, &wg)
		})
	}
}

// serveConn 处理serveconn。
func (s *Server) serveConn(ctx context.Context, ch channel.Channel, wg *sync.WaitGroup) {
	defer wg.Done()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var rpcLog jrpc2.RPCLogger
	tracker := newRPCRequestTracker(s.logger)
	if tracker != nil {
		rpcLog = tracker
	}
	opts := prepareServerOptions(rpcLog, nil)
	srv := jrpc2.NewServer(s.methods, opts).Start(ch)
	s.addActive(srv, dto.PeerKindTool)
	defer s.removeActive(srv)
	s.notifyConnected(srv)

	runtimesafe.SafeGo(connCtx, s.logger, "rpc.serveConn.cancelWatcher", func(context.Context) {
		<-connCtx.Done()
		srv.Stop()
	})

	stat := srv.WaitStatus()
	if stat.Err != nil && !isExpectedCloseErr(stat.Err) {
		if tracker != nil {
			tracker.logConnectionExit(stat.Err)
		} else if s.logger != nil {
			s.logger.Warn("rpc connection exited", "error", stat.Err)
		}
	}
}

func (s *Server) addActive(srv *jrpc2.Server, peerKind string) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[srv] = peerKind
}

func (s *Server) removeActive(srv *jrpc2.Server) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, srv)
}

func (s *Server) reserveUIWebSocketSlot() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeUIWS >= wailsWSMaxActiveConnections {
		return jrpc2.Errorf(
			jrpc2.Code(CodeInvalidState),
			"wails websocket connection limit reached: max %d active UI websocket connections",
			wailsWSMaxActiveConnections,
		)
	}
	s.activeUIWS++
	return nil
}

func (s *Server) releaseUIWebSocketSlot() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeUIWS <= 0 {
		logger := s.logger
		if logger == nil {
			logger = pkglogger.Get()
		}
		logger.Error("rpc: UI websocket slot released without a reservation")
		return
	}
	s.activeUIWS--
}

// OnConnect 处理onconnect。
func (s *Server) OnConnect(fn func(*jrpc2.Server)) {
	if s == nil || fn == nil {
		return
	}
	for _, current := range s.addOnConnect(fn) {
		fn(current)
	}
}

// OnConnectUI 处理onconnectUI。
func (s *Server) OnConnectUI(fn func(*jrpc2.Server)) {
	if s == nil || fn == nil {
		return
	}
	s.OnConnect(func(current *jrpc2.Server) {
		if s.PeerKind(current) == dto.PeerKindUI {
			fn(current)
		}
	})
}

func (s *Server) notifyConnected(srv *jrpc2.Server) {
	for _, hook := range s.snapshotOnConnects() {
		hook(srv)
	}
}

// PeerKind 处理peerkind。
func (s *Server) PeerKind(srv *jrpc2.Server) string {
	if s == nil || srv == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active[srv]
}

func (s *Server) snapshotActive() []*jrpc2.Server {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*jrpc2.Server, 0, len(s.active))
	for current := range s.active {
		out = append(out, current)
	}
	return out
}

func (s *Server) snapshotOnConnects() []func(*jrpc2.Server) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]func(*jrpc2.Server){}, s.onConnects...)
}

func (s *Server) addOnConnect(fn func(*jrpc2.Server)) []*jrpc2.Server {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onConnects = append(s.onConnects, fn)
	out := make([]*jrpc2.Server, 0, len(s.active))
	for current := range s.active {
		out = append(out, current)
	}
	return out
}

func prepareServerOptions(rpcLog jrpc2.RPCLogger, opts *jrpc2.ServerOptions) *jrpc2.ServerOptions {
	if opts == nil {
		return &jrpc2.ServerOptions{
			AllowPush: true,
			RPCLog:    rpcLog,
		}
	}
	dup := *opts
	dup.AllowPush = true
	if dup.RPCLog == nil {
		dup.RPCLog = rpcLog
	}
	return &dup
}
