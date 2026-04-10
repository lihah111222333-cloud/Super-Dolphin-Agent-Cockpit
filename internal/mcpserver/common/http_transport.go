package common

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// HTTPServer exposes the MCP JSON-RPC protocol over Streamable HTTP (POST /mcp).
// Multiple Claude CLI instances can connect to the same endpoint concurrently,
// eliminating the need for per-agent stdio sidecar processes.
type HTTPServer struct {
	name     string
	version  string
	tools    ToolProvider
	server   *http.Server
	listener net.Listener
}

// NewHTTPServer creates an MCP server that speaks Streamable HTTP transport.
func NewHTTPServer(name, version string, tools ToolProvider) *HTTPServer {
	if strings.TrimSpace(name) == "" {
		name = "mcp-server"
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &HTTPServer{name: name, version: version, tools: tools}
}

// Start binds to listenAddr (use "127.0.0.1:0" for dynamic port) and begins
// serving. Returns the actual address (including port) on success.
func (h *HTTPServer) Start(ctx context.Context, listenAddr string) (string, error) {
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", fmt.Errorf("mcp http: listen %s: %w", listenAddr, err)
	}
	h.listener = ln
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.handleMCP)

	h.server = &http.Server{Handler: mux}
	go func() {
		if err := h.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			pkglogger.Warn("mcp http: serve error", "server", h.name, "error", err)
		}
	}()

	pkglogger.Info("mcp http: started", "server", h.name, "addr", addr)
	return addr, nil
}

// Stop gracefully shuts down the HTTP server.
func (h *HTTPServer) Stop(ctx context.Context) error {
	if h.server == nil {
		return nil
	}
	pkglogger.Info("mcp http: stopping", "server", h.name)
	return h.server.Shutdown(ctx)
}

func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		writeJSONError(w, nil, codeParseError, "read body failed")
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, nil, codeParseError, "parse error")
		return
	}

	resp, _ := h.dispatch(r.Context(), req)
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

func (h *HTTPServer) dispatch(ctx context.Context, req jsonRPCRequest) (*jsonRPCResponse, bool) {
	if strings.TrimSpace(req.JSONRPC) != "2.0" {
		return errorResponse(req.ID, codeInvalidReq, "jsonrpc must be 2.0"), false
	}
	switch req.Method {
	case "initialize":
		return h.handleInitialize(req), false
	case "notifications/initialized":
		return nil, false
	case "ping", "shutdown":
		return maybeResult(req.ID, map[string]any{}), false
	case "exit":
		return nil, true
	case "tools/list":
		return h.handleToolsList(ctx, req), false
	case "tools/call":
		return h.handleToolsCall(ctx, req), false
	default:
		if !hasRequestID(req.ID) {
			return nil, false
		}
		return errorResponse(req.ID, codeMethodMissing, "method not found"), false
	}
}

func (h *HTTPServer) handleInitialize(req jsonRPCRequest) *jsonRPCResponse {
	var params initializeParams
	if err := platformshared.DecodeInput(req.Params, &params); err != nil {
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
			"name":    h.name,
			"version": h.version,
		},
	}
	return maybeResult(req.ID, result)
}

func (h *HTTPServer) handleToolsList(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	if h.tools == nil {
		return maybeResult(req.ID, map[string]any{"tools": []MCPTool{}})
	}
	tools, err := h.tools.ListTools(ctx)
	if err != nil {
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	return maybeResult(req.ID, map[string]any{"tools": tools})
}

func (h *HTTPServer) handleToolsCall(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	var params toolCallParams
	if err := platformshared.DecodeInput(req.Params, &params); err != nil {
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	pkglogger.Info("mcp http: tools/call begin",
		"server", h.name, "tool", params.Name,
		"req_id", string(req.ID))
	start := time.Now()

	if h.tools == nil {
		return errorResponse(req.ID, codeInternal, "tool provider not configured")
	}
	value, err := h.tools.CallTool(ctx, strings.TrimSpace(params.Name), params.Arguments)
	elapsed := time.Since(start)
	if err != nil {
		pkglogger.Warn("mcp http: tools/call error",
			"server", h.name, "tool", params.Name,
			"elapsed", elapsed, "error", err)
		return errorResponse(req.ID, codeToolCall, err.Error())
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return errorResponse(req.ID, codeInternal, err.Error())
	}
	if elapsed > 3*time.Second {
		pkglogger.Warn("mcp http: tools/call slow",
			"server", h.name, "tool", params.Name, "elapsed", elapsed)
	}
	pkglogger.Info("mcp http: tools/call done",
		"server", h.name, "tool", params.Name,
		"elapsed", elapsed, "result_len", len(raw))
	return maybeResult(req.ID, map[string]any{
		"content": []textContent{{Type: "text", Text: string(raw)}},
	})
}

func writeJSONError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	resp := errorResponse(id, code, message)
	_ = json.NewEncoder(w).Encode(resp)
}
