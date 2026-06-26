package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// contextKey 是 MCP server 写入 context 的私有 key 类型，避免与外部包 key 冲突。
type contextKey string

// CwdContextKey 保存工具调用的可信工作目录，旧路径仍会从这里读取。
const CwdContextKey = contextKey("mcp_cwd")

// ErrMissingContextCWD 表示严格上下文模式下没有可信 CWD。
var ErrMissingContextCWD = errors.New("strict context enforcement: missing tool scope CWD")

// ErrMissingWorkspaceRoots 表示严格上下文模式下没有可用 workspace roots。
var ErrMissingWorkspaceRoots = errors.New("strict context enforcement: missing workspace roots")

// WorkspaceRootFromContextStrict 从可信 tool scope 或旧 CWD context 读取当前工作区根。
// 严格模式缺少 CWD 时返回 ErrMissingContextCWD，而不是使用进程 cwd 兜底。
func WorkspaceRootFromContextStrict(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingContextCWD
	}
	if scope, ok := ToolScopeFromContext(ctx); ok && scope.CWD != "" {
		return scope.CWD, nil
	}
	if cwd, ok := ctx.Value(CwdContextKey).(string); ok && cwd != "" {
		return NormalizeToolScope(ToolScope{CWD: cwd}).CWD, nil
	}
	return "", ErrMissingContextCWD
}

// WorkspaceRootsFromContextStrict 读取允许访问的 workspace roots。
// 缺少可信 roots 时返回 ErrMissingWorkspaceRoots，避免工具默认扩大文件访问面。
func WorkspaceRootsFromContextStrict(ctx context.Context) ([]string, error) {
	if ctx == nil {
		return nil, ErrMissingWorkspaceRoots
	}
	if scope, ok := ToolScopeFromContext(ctx); ok && len(scope.WorkspaceRoots) > 0 {
		return append([]string(nil), scope.WorkspaceRoots...), nil
	}
	if cwd, ok := ctx.Value(CwdContextKey).(string); ok && strings.TrimSpace(cwd) != "" {
		roots := NormalizeToolScope(ToolScope{CWD: cwd}).WorkspaceRoots
		if len(roots) > 0 {
			return roots, nil
		}
	}
	return nil, ErrMissingWorkspaceRoots
}

// WorkspaceRootForPathFromContextStrict 为目标绝对路径选择最具体的允许 workspace root。
// 相对或空 target 使用主 root；越界绝对路径直接报错。
func WorkspaceRootForPathFromContextStrict(ctx context.Context, target string) (string, error) {
	roots, err := WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return "", err
	}
	target = strings.TrimSpace(target)
	if target == "" || !filepath.IsAbs(target) {
		return roots[0], nil
	}
	requested := filepath.Clean(target)
	best := ""
	for _, root := range roots {
		if pathWithinRoot(root, requested) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return "", fmt.Errorf("strict context enforcement: requested path %s is outside allowed workspace roots [%s]", requested, strings.Join(roots, ", "))
	}
	return best, nil
}

// WorkspaceRootFromContext 读取可信 CWD，旧调用方可传 fallback 保持兼容。
func WorkspaceRootFromContext(ctx context.Context, fallback string) string {
	if scope, ok := ToolScopeFromContext(ctx); ok && scope.CWD != "" {
		return scope.CWD
	}
	if cwd, ok := ctx.Value(CwdContextKey).(string); ok && cwd != "" {
		return NormalizeToolScope(ToolScope{CWD: cwd}).CWD
	}
	return fallback
}

// pathWithinRoot 判断 target 是否位于 root 内，委托共享路径工具处理大小写和分隔符差异。
func pathWithinRoot(root, target string) bool {
	return platformshared.ContainsPath(root, target)
}

// JSON-RPC 错误码常量，保持 MCP stdio 和 legacy HTTP transport 返回一致。
const (
	codeParseError    = -32700
	codeInvalidReq    = -32600
	codeMethodMissing = -32601
	codeInvalidParams = -32602
	codeInternal      = -32603
	codeToolCall      = -32000
)

// ToolProvider 是 mcpserver/common 调用具体工具实现的最小接口。
type ToolProvider interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// MCPTool 复用 canonical DTO 定义，让 common 包内部和 ToolProvider 使用同一工具描述类型。
type MCPTool = mcpdto.MCPTool

// Server 实现 stdio MCP JSON-RPC 服务端，负责初始化、工具列表和工具调用分发。
type Server struct {
	name      string
	version   string
	transport *StdioTransport
	tools     ToolProvider
	ready     chan struct{} // closed when Run enters its read loop
}

// jsonRPCRequest 是 stdio/HTTP 共用的 JSON-RPC 请求结构。
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse 是 stdio/HTTP 共用的 JSON-RPC 响应结构。
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError 是 JSON-RPC error 对象，只承载稳定 code 和可读 message。
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// initializeParams 保存 initialize 请求里当前只需要协商的 protocolVersion。
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion,omitempty"`
}

// textContent 是 MCP content 文本块的内部结构。
type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolResultTextProvider 是旧工具结果的纯文本渲染接口，保留给历史实现兼容。
type toolResultTextProvider interface {
	ToolResultText() string
}

// readResult 是 readLoop 向 Run 主循环发送的一次读结果。
type readResult struct {
	payload json.RawMessage
	err     error
}

// NewServer 创建 stdio MCP 服务端，并补齐空 name/version 的开发默认值。
func NewServer(name, version string, transport *StdioTransport, tools ToolProvider) *Server {
	if strings.TrimSpace(name) == "" {
		name = "mcp-server"
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &Server{name: name, version: version, transport: transport, tools: tools, ready: make(chan struct{})}
}

// Ready 返回在 stdio server 进入读循环后关闭的通道。
// Bootstrap runner 会等待它，确保本地 MCP 工具面已可用后再连接控制平面。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Run 启动 stdio JSON-RPC 主循环；ctx 取消或 transport 关闭都会结束循环。
func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pkglogger.Info("mcp: server run started", "server", s.name)
	results := make(chan readResult, 1)
	s.startReadLoop(results)
	// Signal that the stdio server is ready to accept messages.
	// Bootstrap runners block on this before connecting to the control plane.
	close(s.ready)
	for {
		select {
		case <-ctx.Done():
			pkglogger.Warn("mcp: server run stopping (ctx done)",
				"server", s.name, "error", ctx.Err())
			_ = s.transport.Close()
			return nil
		case result, ok := <-results:
			if !ok {
				pkglogger.Warn("mcp: server run stopping (read channel closed)",
					"server", s.name)
				return nil
			}
			if shouldStop(result.err) {
				pkglogger.Warn("mcp: server run stopping (EOF/pipe closed)",
					"server", s.name, "error", result.err)
				return nil
			}
			if result.err != nil {
				pkglogger.Warn("mcp: server run stopping (read error)",
					"server", s.name, "error", result.err)
				return result.err
			}
			exit, err := s.handleMessage(ctx, result.payload)
			if err != nil {
				pkglogger.Warn("mcp: server run stopping (handle error)",
					"server", s.name, "error", err)
				return err
			}
			if exit {
				pkglogger.Warn("mcp: server run stopping (exit requested)",
					"server", s.name)
				return nil
			}
		}
	}
}

// startReadLoop 启动单独 goroutine 读取 transport，panic 会被记录而不是击穿 Run。
func (s *Server) startReadLoop(results chan<- readResult) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("mcp: recovered server readLoop panic",
					"server", s.name, "panic", rec)
			}
		}()
		s.readLoop(results)
	}()
}

// readLoop 持续读取 transport 消息并把错误也送回主循环，由 Run 统一决定是否退出。
func (s *Server) readLoop(results chan<- readResult) {
	defer close(results)
	for {
		payload, err := s.transport.ReadMessage()
		results <- readResult{payload: payload, err: err}
		if err != nil {
			return
		}
	}
}

// handleMessage 解析单条 JSON-RPC 消息并写回响应，exit=true 表示收到退出通知。
func (s *Server) handleMessage(ctx context.Context, payload json.RawMessage) (bool, error) {
	var req jsonRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return false, s.reply(errorResponse(nil, codeParseError, "parse error"))
	}
	resp, exit := s.dispatch(ctx, req)
	if resp == nil {
		return exit, nil
	}
	return exit, s.reply(resp)
}

// dispatch 派发MCP 服务。
func (s *Server) dispatch(ctx context.Context, req jsonRPCRequest) (*jsonRPCResponse, bool) {
	if strings.TrimSpace(req.JSONRPC) != "2.0" {
		return errorResponse(req.ID, codeInvalidReq, "jsonrpc must be 2.0"), false
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req), false
	case "notifications/initialized":
		return nil, false
	case "ping", "shutdown":
		return maybeResult(req.ID, map[string]any{}), false
	case "exit":
		return nil, true
	case "tools/list":
		return s.handleToolsList(ctx, req), false
	case "tools/call":
		return s.handleToolsCall(ctx, req), false
	default:
		if !hasRequestID(req.ID) {
			return nil, false
		}
		return errorResponse(req.ID, codeMethodMissing, "method not found"), false
	}
}

// handleInitialize 返回 MCP serverInfo 和工具能力，缺省协议版本保持与客户端兼容。
func (s *Server) handleInitialize(req jsonRPCRequest) *jsonRPCResponse {
	var params initializeParams
	if err := platformshared.DecodeInput(req.Params, &params); err != nil {
		err = fmt.Errorf("decode params: %w", err)
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	version := strings.TrimSpace(params.ProtocolVersion)
	if version == "" {
		version = "2024-11-05"
	}
	result := map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
	}
	return maybeResult(req.ID, result)
}

// handleToolsList 调用 ToolProvider 列出工具，provider 错误会变成 JSON-RPC internal error。
func (s *Server) handleToolsList(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	var params map[string]any
	if err := platformshared.DecodeInput(req.Params, &params); err != nil {
		err = fmt.Errorf("decode params: %w", err)
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	tools, err := s.listTools(ctx)
	if err != nil {
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	return maybeResult(req.ID, map[string]any{"tools": tools})
}

// handleToolsCall 解码 tools/call、注入可信 scope，并把 handler 错误包装为工具错误 envelope。
func (s *Server) handleToolsCall(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	params, err := DecodeToolCallParams(req.Params)
	if err != nil {
		err = fmt.Errorf("decode params: %w", err)
		pkglogger.Warn("mcp: tools/call decode error",
			"server", s.name, "error", err)
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	scope := params.Scope(s.name)
	ctx = WithToolScope(ctx, scope)
	requestPayload := logToolCallRequestPayload("stdio", s.name, req.ID, params, scope)
	beginAttrs := []any{
		"server", s.name, "tool", params.Name,
		"agent_id", scope.AgentID, "thread_id", scope.ThreadID,
		"call_id", scope.CallID, "cwd", scope.CWD,
		"req_id", string(req.ID), "raw_args_len", len(params.Arguments),
	}
	beginAttrs = append(beginAttrs, toolPayloadAttrs("request_payload", requestPayload)...)
	pkglogger.Info("mcp: tools/call begin", beginAttrs...)
	start := time.Now()

	value, err := callToolSafely(ctx, s.tools, strings.TrimSpace(params.Name), params.Arguments)
	elapsed := time.Since(start)
	if err != nil {
		errorPayload := logToolCallResultPayload("stdio", s.name, params.Name, req.ID, scope, nil, err)
		errorAttrs := []any{
			"server", s.name, "tool", params.Name,
			"elapsed", elapsed, "error", err,
		}
		errorAttrs = append(errorAttrs, toolPayloadAttrs("error_payload", errorPayload)...)
		pkglogger.Warn("mcp: tools/call error", errorAttrs...)
		if isNilToolResult(value) {
			value = NewToolErrorEnvelope(params.Name, err)
		}
	}
	resp, raw, err := toolCallResultResponse(req.ID, value)
	if err != nil {
		resultPayload := logToolCallResultPayload("stdio", s.name, params.Name, req.ID, scope, nil, err)
		attrs := []any{"server", s.name, "tool", params.Name, "error", err}
		attrs = append(attrs, toolPayloadAttrs("result_payload", resultPayload)...)
		pkglogger.Warn("mcp: tools/call result encode error", attrs...)
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	resultPayload := logToolCallResultPayload("stdio", s.name, params.Name, req.ID, scope, raw, nil)
	if elapsed > 3*time.Second {
		pkglogger.Warn("mcp: tools/call slow",
			"server", s.name, "tool", params.Name, "elapsed", elapsed)
	}
	doneAttrs := []any{
		"server", s.name, "tool", params.Name,
		"elapsed", elapsed, "result_len", len(raw),
	}
	doneAttrs = append(doneAttrs, toolPayloadAttrs("result_payload", resultPayload)...)
	pkglogger.Info("mcp: tools/call done", doneAttrs...)
	return resp
}

// listTools 在 provider 未注入时返回空列表，避免 tools/list 因可选 peer 缺失而 panic。
func (s *Server) listTools(ctx context.Context) ([]MCPTool, error) {
	if s.tools == nil {
		return nil, nil
	}
	return s.tools.ListTools(ctx)
}

// reply 写出 JSON-RPC 响应；nil 响应用于 notification，不会触碰 transport。
func (s *Server) reply(resp *jsonRPCResponse) error {
	if resp == nil {
		return nil
	}
	if err := s.transport.WriteMessage(resp); err != nil {
		pkglogger.Warn("mcp: reply write failed",
			"server", s.name, "resp_id", string(resp.ID),
			"has_error", resp.Error != nil, "error", err)
		return err
	}
	return nil
}

// maybeResult 只为带 ID 的请求构造响应，无 ID notification 按 JSON-RPC 规则不回复。
func maybeResult(id json.RawMessage, result any) *jsonRPCResponse {
	if !hasRequestID(id) {
		return nil
	}
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      responseID(id),
		Result:  result,
	}
}

// errorResponse 构造 JSON-RPC error 响应，空 ID 会规范化为 null。
func errorResponse(id json.RawMessage, code int, message string) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      responseID(id),
		Error: &jsonRPCError{
			Code:    code,
			Message: strings.TrimSpace(message),
		},
	}
}

// callToolSafely 调用工具 provider，并把 panic 转换成稳定的 coded tool error。
func callToolSafely(ctx context.Context, provider ToolProvider, name string, args json.RawMessage) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = NewPanicToolError(recovered)
			value = nil
		}
	}()
	if provider == nil {
		return nil, NewCodedToolError("lsp_unavailable", errors.New("tool provider is not configured"), true, "Retry after the MCP server finishes tool registration.")
	}
	return provider.CallTool(ctx, name, args)
}

// toolCallResultResponse 同时生成结构化 MCP result 和原始 JSON，用于响应与载荷日志复用。
func toolCallResultResponse(id json.RawMessage, value any) (*jsonRPCResponse, []byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	envelope, err := BuildToolCallResult(value)
	if err != nil {
		return nil, nil, err
	}
	return maybeResult(id, envelope), raw, nil
}

// hasRequestID 判断请求是否需要响应，空白 ID 视为 notification。
func hasRequestID(id json.RawMessage) bool {
	return len(bytes.TrimSpace(id)) != 0
}

// responseID 复制并规范化响应 ID，避免复用调用方的底层 buffer。
func responseID(id json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), trimmed...)
}

// shouldStop 判断 transport EOF/pipe 关闭是否表示服务端应正常退出。
func shouldStop(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
}
