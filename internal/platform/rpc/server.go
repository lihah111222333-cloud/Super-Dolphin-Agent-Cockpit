package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const controlRPCAddrEnv = "GO_AGENT_CTL_RPC_ADDR"

// Server 管理本地 control RPC listener、WebSocket UI 连接和本地 Dispatch。
// active map 记录当前 jrpc2 server 的 peer kind，供审批恢复和广播选择目标。
type Server struct {
	logger        *pkglogger.Logger
	addr          string
	methods       handler.Map
	traceRecorder TraceRecorder
	authToken     string

	mu         sync.RWMutex
	active     map[*jrpc2.Server]string
	activeUIWS int
	onConnects []func(*jrpc2.Server)
}

// rpcRequestTracker 跟踪单连接内未完成请求，用于连接异常退出时输出诊断。
type rpcRequestTracker struct {
	logger *pkglogger.Logger

	mu      sync.Mutex
	pending map[string]rpcPendingRequest
}

// rpcPendingRequest 是一个等待响应的 RPC 请求摘要，用于连接异常退出日志。
type rpcPendingRequest struct {
	ID            string    // jrpc2 request id。
	Method        string    // RPC method。
	ThreadID      string    // 从 params 提取的 thread id。
	ParamsSummary string    // 不含原始内容的参数安全摘要。
	StartedAt     time.Time // 请求开始时间。
}

// newRPCRequestTracker 在 logger 可用时创建请求跟踪器。
func newRPCRequestTracker(logger *pkglogger.Logger) *rpcRequestTracker {
	if logger == nil {
		return nil
	}
	return &rpcRequestTracker{
		logger:  logger,
		pending: map[string]rpcPendingRequest{},
	}
}

// LogRequest 记录非 notification 请求的开始时间和参数摘要。
func (t *rpcRequestTracker) LogRequest(_ context.Context, req *jrpc2.Request) {
	if t == nil || req == nil || req.IsNotification() {
		return
	}
	id := strings.TrimSpace(req.ID())
	if id == "" {
		return
	}
	method := strings.TrimSpace(req.Method())
	params := rpcRequestParamsRaw(req)
	t.mu.Lock()
	t.pending[id] = rpcPendingRequest{
		ID:            id,
		Method:        method,
		ThreadID:      rpcRequestThreadID(params),
		ParamsSummary: SafeRPCLogSummary(method, params),
		StartedAt:     time.Now(),
	}
	t.mu.Unlock()
}

// LogResponse 在响应携带 RPC 错误时输出带请求上下文的告警。
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

// takePendingResponse 取出并删除对应响应 ID 的 pending 请求。
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

// rpcFailureLogArgs 组装 RPC 失败日志字段。
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
	if meta.ParamsSummary != "" {
		args = append(args, "params_summary", meta.ParamsSummary)
	}
	return args
}

// rpcFailureLogMessage 根据错误文本选择稳定日志消息。
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

// logConnectionExit 在连接异常退出时记录未完成请求，帮助定位悬挂调用。
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

// snapshotPending 返回按开始时间排序的 pending 请求摘要。
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
		if current.ParamsSummary != "" {
			summary["params_summary"] = current.ParamsSummary
		}
		out = append(out, summary)
	}
	return out
}

// rpcRequestParamsRaw 从 jrpc2 请求中复制 params 原文；调用方只能把它交给安全摘要或 ID 提取。
func rpcRequestParamsRaw(req *jrpc2.Request) string {
	if req == nil || !req.HasParams() {
		return ""
	}
	var params json.RawMessage
	if err := req.UnmarshalParams(&params); err != nil {
		return ""
	}
	return string(params)
}

// rpcRequestThreadID 从未知形态 params 中提取 threadID。
func rpcRequestThreadID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Wails 桥接层的 params 形态由 method 决定，这里只读取 thread_id 相关字段，
	// 不要求每个方法先定义强类型结构。
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

// isRPCTimeoutMessage 判断错误文本是否表示超时。
func isRPCTimeoutMessage(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(value, "deadline exceeded") ||
		strings.Contains(value, "timed out") ||
		strings.Contains(value, "timeout")
}

// isRPCCanceledMessage 判断错误文本是否表示取消。
func isRPCCanceledMessage(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(value, "context canceled") ||
		strings.Contains(value, "cancelled") ||
		strings.Contains(value, "canceled")
}

// NewServer 创建 RPC server，初始化 handler 表和活跃连接索引。
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

// Register 合并注册一组或多组 RPC handler。
func (s *Server) Register(handlerMaps ...handler.Map) {
	for _, current := range handlerMaps {
		maps.Copy(s.methods, current)
	}
}

// Dispatch 在本进程内执行已注册 handler，不经过网络连接。
// Wails binding 用它桥接 CallAPI；params/result 使用动态 JSON，由目标 method 决定结构。
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

// logTraceRecordError 把 trace 记录失败降级为告警，不影响 RPC 返回。
func (s *Server) logTraceRecordError(ctx context.Context, method string, phase string, err error) {
	logger := s.logger
	if logger == nil {
		logger = pkglogger.FromContext(ctx)
	}
	logger.Warn("rpc dispatch trace record failed", "phase", phase, "method", method, "error", err)
}

// recordDispatchTrace 记录本地 Dispatch 的 start/done/failed trace。
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

// rpcParamKeys 提取 JSON object params 的 key 列表，用于 trace metadata。
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

// rpcTraceStatus 根据方法和耗时判断 trace 状态是否为 slow。
func rpcTraceStatus(method string, duration time.Duration) TraceStatus {
	if duration > rpcSlowThreshold(method) {
		return TraceStatusSlow
	}
	return TraceStatusOK
}

// rpcSlowThreshold 返回不同方法类别的慢请求阈值。
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

// NotifyAll 向所有活跃 RPC 客户端广播通知。
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

// Run 启动 loopback TCP control RPC listener，并把实际监听地址写入环境变量。
func (s *Server) Run(ctx context.Context) error {
	if err := validateControlRPCAddr(s.addr); err != nil {
		return err
	}
	inheritedCanonicalSessionToken := inheritedCanonicalControlRPCSessionToken()
	if err := requireRPCReadyFileInheritedSessionToken(inheritedCanonicalSessionToken); err != nil {
		return err
	}
	authToken, err := ensureControlRPCSessionToken()
	if err != nil {
		return err
	}
	s.setControlRPCAuthToken(authToken)
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	activeAddr := listener.Addr().String()
	_ = os.Setenv(controlRPCAddrEnv, activeAddr)
	if err := maybePublishRPCReadyFile(activeAddr, inheritedCanonicalSessionToken); err != nil {
		return err
	}
	s.logger.Info("rpc server listening", "addr", activeAddr)
	err = s.acceptLoop(ctx, jrpcserver.NetAccepter(listener, channel.Line))
	if err != nil && !isExpectedCloseErr(err) {
		return err
	}
	return nil
}

// validateControlRPCAddr 强制 control RPC 只绑定 loopback 地址。
func validateControlRPCAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ErrInvalidParams(fmt.Sprintf("control rpc addr must be loopback: %v", err))
	}
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return nil
	default:
		return ErrInvalidParams(fmt.Sprintf("control rpc addr must be loopback, got %q", addr))
	}
}

// acceptLoop 接收 control RPC 连接，并为每条连接启动受 runtimesafe 保护的服务 goroutine。
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

// serveConn 绑定单条 TCP control RPC 连接的认证、活跃状态和退出诊断。
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
	var srv *jrpc2.Server
	auth := newControlRPCConnectionAuth(s.controlRPCAuthToken())
	assigner := controlRPCAuthAssigner{base: s.methods, auth: auth}
	srv = jrpc2.NewServer(assigner, opts).Start(ch)
	auth.setOnAuthenticated(func() {
		s.addActive(srv, dto.PeerKindTool)
		s.notifyConnected(srv)
	})
	defer s.removeActive(srv)

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

// addActive 记录活跃 jrpc2 server 及其 peer kind。
func (s *Server) addActive(srv *jrpc2.Server, peerKind string) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[srv] = peerKind
}

// removeActive 移除活跃 jrpc2 server。
func (s *Server) removeActive(srv *jrpc2.Server) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, srv)
}

// reserveUIWebSocketSlot 为 UI WebSocket 连接预留并发槽位。
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

// releaseUIWebSocketSlot 释放 UI WebSocket 并发槽位，重复释放返回显式生命周期错误。
func (s *Server) releaseUIWebSocketSlot() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeUIWS <= 0 {
		return ErrInvalidState("rpc UI websocket slot released without a reservation")
	}
	s.activeUIWS--
	return nil
}

// OnConnect 注册连接建立回调，并立即回放当前活跃连接。
func (s *Server) OnConnect(fn func(*jrpc2.Server)) {
	if s == nil || fn == nil {
		return
	}
	for _, current := range s.addOnConnect(fn) {
		fn(current)
	}
}

// OnConnectUI 注册只针对 UI peer 的连接建立回调。
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

// notifyConnected 调用当前所有连接回调。
func (s *Server) notifyConnected(srv *jrpc2.Server) {
	for _, hook := range s.snapshotOnConnects() {
		hook(srv)
	}
}

// PeerKind 返回活跃连接的 peer kind。
func (s *Server) PeerKind(srv *jrpc2.Server) string {
	if s == nil || srv == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active[srv]
}

// snapshotActive 返回活跃 server 快照，避免调用方持锁做 RPC 操作。
func (s *Server) snapshotActive() []*jrpc2.Server {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*jrpc2.Server, 0, len(s.active))
	for current := range s.active {
		out = append(out, current)
	}
	return out
}

// snapshotOnConnects 返回连接回调快照，避免回调执行时持有 Server 锁。
func (s *Server) snapshotOnConnects() []func(*jrpc2.Server) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]func(*jrpc2.Server){}, s.onConnects...)
}

// addOnConnect 注册回调并返回当前活跃连接快照供立即回放。
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

// prepareServerOptions 复制或创建 jrpc2 ServerOptions，并确保允许 push。
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
