package common

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const maxHTTPBodyBytes = 10 * 1024 * 1024

// HTTPServerOption 配置 legacy Streamable HTTP MCP transport。
// Legacy: HTTP MCP transport 仅保留给旧调用方；当前工具执行路径使用 stdio MCP sidecar Server。
type HTTPServerOption func(*HTTPServer)

// WithBearerToken configures bearer-token authentication for the deprecated
// Streamable HTTP MCP transport.
// Legacy: HTTP MCP transport 仅保留给旧调用方；当前工具执行路径使用 stdio MCP sidecar Server。
// WithBearerToken 设置bearer令牌。
func WithBearerToken(token string) HTTPServerOption {
	return func(h *HTTPServer) {
		h.bearerToken = strings.TrimSpace(token)
	}
}

// WithHTTPToolErrorClassifier 安装 legacy HTTP tools/call 的 sidecar 本地错误分类器。
func WithHTTPToolErrorClassifier(classifier ToolErrorClassifier) HTTPServerOption {
	return func(h *HTTPServer) {
		h.toolErrorClassifier = classifier
	}
}

// HTTPServer 通过 legacy Streamable HTTP 暴露 MCP JSON-RPC 协议（POST /mcp）。
// 多个旧 Claude CLI 实例可共用同一 endpoint；当前工具执行路径应使用 stdio sidecar Server。
// Legacy: HTTP MCP transport 仅保留给旧调用方；当前工具执行路径使用 stdio MCP sidecar Server。
type HTTPServer struct {
	name                string
	version             string
	tools               ToolProvider
	server              *http.Server
	bearerToken         string
	toolErrorClassifier ToolErrorClassifier
}

// NewHTTPServer 创建使用 Streamable HTTP transport 的 legacy MCP server。
// Legacy: HTTP MCP transport 仅保留给旧调用方；当前工具执行路径使用 stdio MCP sidecar Server。
// NewHTTPServer 创建 legacy HTTP MCP 服务端，并应用可选鉴权配置。
func NewHTTPServer(name, version string, tools ToolProvider, opts ...HTTPServerOption) *HTTPServer {
	if strings.TrimSpace(name) == "" {
		name = "mcp-server"
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	h := &HTTPServer{name: name, version: version, tools: tools}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// Start binds to listenAddr (use "127.0.0.1:0" for dynamic port) and begins
// serving. Returns the actual address (including port) on success.
// Legacy: HTTP MCP transport 仅保留给旧调用方；当前工具执行路径使用 stdio MCP sidecar Server。
// Start 绑定 HTTP 监听地址并启动 /mcp endpoint，返回实际监听地址。
func (h *HTTPServer) Start(ctx context.Context, listenAddr string) (string, error) {
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", fmt.Errorf("mcp http: listen %s: %w", listenAddr, err)
	}
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.handleMCP)

	h.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	var serveWG sync.WaitGroup
	serveWG.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("mcp http: recovered serve panic",
					"server", h.name, "panic", rec)
			}
		}()
		if err := h.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			pkglogger.Warn("mcp http: serve error", "server", h.name, "error", err)
		}
	})

	pkglogger.Info("mcp http: started", "server", h.name, "addr", addr)
	return addr, nil
}

// Stop gracefully shuts down the HTTP server.
// Legacy: HTTP MCP transport 仅保留给旧调用方；当前工具执行路径使用 stdio MCP sidecar Server。
// Stop 优雅关闭 legacy HTTP server；未启动时直接返回 nil。
func (h *HTTPServer) Stop(ctx context.Context) error {
	if h.server == nil {
		return nil
	}
	pkglogger.Info("mcp http: stopping", "server", h.name)
	return h.server.Shutdown(ctx)
}

// handleMCP 处理单个 HTTP JSON-RPC 请求；notification 返回 202 且不写响应体。
func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	defer r.Body.Close()
	body, err := readLimitedHTTPBody(r.Body)
	if err != nil {
		writeJSONError(w, nil, codeParseError, err.Error())
		return
	}

	req, errorCode, message := decodeJSONRPCRequest(body)
	if errorCode != 0 {
		writeJSONError(w, nil, errorCode, message)
		return
	}

	resp := h.dispatch(r.Context(), req)
	if resp == nil {
		// Notification — no response needed, return 202 Accepted.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		pkglogger.Warn("mcp http: write response failed",
			"server", h.name, "error", err)
	}
}

// readLimitedHTTPBody 读取 HTTP JSON-RPC body，并用 limit+1 检测避免截断后继续解析。
func readLimitedHTTPBody(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxHTTPBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body failed: %w", err)
	}
	if len(raw) > maxHTTPBodyBytes {
		return nil, fmt.Errorf("request body exceeds %d bytes", maxHTTPBodyBytes)
	}
	return raw, nil
}

// authorized 校验可选 bearer token；未配置 token 时允许 legacy 本地调用通过。
func (h *HTTPServer) authorized(r *http.Request) bool {
	token := strings.TrimSpace(h.bearerToken)
	if token == "" {
		// legacy 兼容路径：未配置 bearerToken 时允许请求通过。
		// 第一道防线由 http_runner.go 的外层 bearerToken 检查承担；此处仅处理
		// 直接构造 HTTPServer（不经 http_runner.go）的旧测试路径。
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// dispatch 派发MCP 服务。
func (h *HTTPServer) dispatch(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	if err := validateJSONRPCID(req.ID); err != nil {
		return errorResponse(nil, codeInvalidReq, err.Error())
	}
	if strings.TrimSpace(req.JSONRPC) != "2.0" {
		return errorResponse(req.ID, codeInvalidReq, "jsonrpc must be 2.0")
	}
	switch req.Method {
	case "initialize":
		return h.handleInitialize(req)
	case "notifications/initialized", "exit":
		return nil
	case "ping", "shutdown":
		return maybeResult(req.ID, map[string]any{})
	case "tools/list":
		return h.handleToolsList(ctx, req)
	case "tools/call":
		return h.handleToolsCall(ctx, req)
	default:
		if !hasRequestID(req.ID) {
			return nil
		}
		return errorResponse(req.ID, codeMethodMissing, "method not found")
	}
}

// handleInitialize 返回 HTTP transport 的 MCP serverInfo 和工具能力。
func (h *HTTPServer) handleInitialize(req jsonRPCRequest) *jsonRPCResponse {
	var params initializeParams
	if err := platformshared.DecodeInput(req.Params, &params); err != nil {
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	version, err := mcpwire.NegotiateProtocolVersion(params.ProtocolVersion)
	if err != nil {
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	result := map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    h.name,
			"version": h.version,
		},
	}
	return maybeResult(req.ID, result)
}

// handleToolsList 返回 legacy HTTP transport 可见的工具列表，未注入 provider 时 fail-fast。
func (h *HTTPServer) handleToolsList(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	if h.tools == nil {
		return errorResponse(req.ID, codeInternal, ErrToolProviderUnavailable.Error())
	}
	tools, err := h.tools.ListTools(ctx)
	if err != nil {
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	if err := validateToolsList(tools); err != nil {
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	return maybeResult(req.ID, map[string]any{"tools": tools})
}

// handleToolsCall 执行 legacy HTTP tools/call，并复用 stdio 路径的 scope、日志和错误 envelope。
func (h *HTTPServer) handleToolsCall(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	params, err := DecodeToolCallParams(req.Params)
	if err != nil {
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	scope := params.Scope(h.name)
	ctx = WithToolScope(ctx, scope)
	requestPayload := logToolCallRequestPayload("http", h.name, req.ID, params, scope)
	beginAttrs := []any{
		"server", h.name, "tool", params.Name, "deprecated", true,
		"agent_id", scope.AgentID, "thread_id", scope.ThreadID,
		"call_id", scope.CallID, "cwd", scope.CWD,
		"req_id", string(req.ID), "raw_args_len", len(params.Arguments),
	}
	beginAttrs = append(beginAttrs, toolPayloadAttrs("request_payload", requestPayload)...)
	pkglogger.Info("mcp http: tools/call begin", beginAttrs...)
	start := time.Now()

	value, err := callToolSafely(ctx, h.tools, strings.TrimSpace(params.Name), params.Arguments)
	elapsed := time.Since(start)
	if err != nil {
		errorPayload := logToolCallResultPayload("http", h.name, params.Name, req.ID, scope, nil, err)
		errorAttrs := []any{
			"server", h.name, "tool", params.Name, "deprecated", true,
			"elapsed", elapsed, "error", err,
		}
		errorAttrs = append(errorAttrs, toolPayloadAttrs("error_payload", errorPayload)...)
		pkglogger.Warn("mcp http: tools/call error", errorAttrs...)
		if isNilToolResult(value) {
			value = NewToolErrorEnvelopeWithClassifier(params.Name, "", err, nil, h.toolErrorClassifier)
		}
	}
	resp, raw, err := toolCallResultResponse(req.ID, value)
	if err != nil {
		resultPayload := logToolCallResultPayload("http", h.name, params.Name, req.ID, scope, nil, err)
		attrs := []any{"server", h.name, "tool", params.Name, "deprecated", true, "error", err}
		attrs = append(attrs, toolPayloadAttrs("result_payload", resultPayload)...)
		pkglogger.Warn("mcp http: tools/call result encode error", attrs...)
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	resultPayload := logToolCallResultPayload("http", h.name, params.Name, req.ID, scope, raw, nil)
	if elapsed > 3*time.Second {
		pkglogger.Warn("mcp http: tools/call slow",
			"server", h.name, "tool", params.Name, "elapsed", elapsed)
	}
	doneAttrs := []any{
		"server", h.name, "tool", params.Name, "deprecated", true,
		"elapsed", elapsed, "result_len", len(raw),
	}
	doneAttrs = append(doneAttrs, toolPayloadAttrs("result_payload", resultPayload)...)
	pkglogger.Info("mcp http: tools/call done", doneAttrs...)
	return resp
}

// writeJSONError 以 JSON-RPC error envelope 写出 HTTP 错误响应。
func writeJSONError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	resp := errorResponse(id, code, message)
	_ = json.NewEncoder(w).Encode(resp)
}
