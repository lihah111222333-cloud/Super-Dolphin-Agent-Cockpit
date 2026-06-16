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

type contextKey string

const CwdContextKey = contextKey("mcp_cwd")

var ErrMissingContextCWD = errors.New("strict context enforcement: missing tool scope CWD")
var ErrMissingWorkspaceRoots = errors.New("strict context enforcement: missing workspace roots")

// WorkspaceRootFromContextStrict 从上下文strict处理工作区根目录。
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

// WorkspaceRootsFromContextStrict 从上下文strict处理工作区根目录。
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

// WorkspaceRootForPathFromContextStrict 从上下文strict处理工作区根目录路径。
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

// WorkspaceRootFromContext 从上下文处理工作区根目录。
func WorkspaceRootFromContext(ctx context.Context, fallback string) string {
	if scope, ok := ToolScopeFromContext(ctx); ok && scope.CWD != "" {
		return scope.CWD
	}
	if cwd, ok := ctx.Value(CwdContextKey).(string); ok && cwd != "" {
		return NormalizeToolScope(ToolScope{CWD: cwd}).CWD
	}
	return fallback
}

func pathWithinRoot(root, target string) bool {
	return platformshared.ContainsPath(root, target)
}

const (
	codeParseError    = -32700
	codeInvalidReq    = -32600
	codeMethodMissing = -32601
	codeInvalidParams = -32602
	codeInternal      = -32603
	codeToolCall      = -32000
)

type ToolProvider interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// MCPTool is re-exported from the canonical DTO definition so that
// ToolProvider and all mcpserver/common internal code can reference
// it by short name without breaking the public API surface.
type MCPTool = mcpdto.MCPTool

type Server struct {
	name      string
	version   string
	transport *StdioTransport
	tools     ToolProvider
	ready     chan struct{} // closed when Run enters its read loop
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion,omitempty"`
}

type toolResultTextProvider interface {
	ToolResultText() string
}

type readResult struct {
	payload json.RawMessage
	err     error
}

// NewServer 创建服务端。
func NewServer(name, version string, transport *StdioTransport, tools ToolProvider) *Server {
	if strings.TrimSpace(name) == "" {
		name = "mcp-server"
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &Server{name: name, version: version, transport: transport, tools: tools, ready: make(chan struct{})}
}

// Ready returns a channel that is closed once the stdio server has entered
// its read loop and is ready to accept JSON-RPC messages. Bootstrap runners
// wait on this channel to guarantee that the local MCP tool-execution
// surface is available before connecting to the control-plane jrpc2.
// Ready 判断MCP 服务是否可用。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Run 启动MCP 服务后台流程。
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

// handleToolsCall 处理工具call。
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
		if value == nil {
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

func (s *Server) listTools(ctx context.Context) ([]MCPTool, error) {
	if s.tools == nil {
		return nil, nil
	}
	return s.tools.ListTools(ctx)
}

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

func hasRequestID(id json.RawMessage) bool {
	return len(bytes.TrimSpace(id)) != 0
}

func responseID(id json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), trimmed...)
}

func shouldStop(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
}
