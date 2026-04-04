package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

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

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Server struct {
	name      string
	version   string
	transport *StdioTransport
	tools     ToolProvider
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

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type readResult struct {
	payload json.RawMessage
	err     error
}

func NewServer(name, version string, transport *StdioTransport, tools ToolProvider) *Server {
	if strings.TrimSpace(name) == "" {
		name = "mcp-server"
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &Server{name: name, version: version, transport: transport, tools: tools}
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	results := make(chan readResult, 1)
	go s.readLoop(results)
	for {
		select {
		case <-ctx.Done():
			_ = s.transport.Close()
			return nil
		case result, ok := <-results:
			if !ok {
				return nil
			}
			if shouldStop(result.err) {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			exit, err := s.handleMessage(ctx, result.payload)
			if err != nil {
				return err
			}
			if exit {
				return nil
			}
		}
	}
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

func (s *Server) handleToolsCall(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	var params toolCallParams
	if err := platformshared.DecodeInput(req.Params, &params); err != nil {
		err = fmt.Errorf("decode params: %w", err)
		return errorResponse(req.ID, codeInvalidParams, err.Error())
	}
	result, err := s.callTool(ctx, params.Name, params.Arguments)
	if err != nil {
		return errorResponse(req.ID, codeToolCall, err.Error())
	}
	return maybeResult(req.ID, map[string]any{
		"content": []textContent{{Type: "text", Text: result}},
	})
}

func (s *Server) listTools(ctx context.Context) ([]MCPTool, error) {
	if s.tools == nil {
		return nil, nil
	}
	return s.tools.ListTools(ctx)
}

func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if s.tools == nil {
		return "", errors.New("tool provider is not configured")
	}
	value, err := s.tools.CallTool(ctx, strings.TrimSpace(name), args)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s *Server) reply(resp *jsonRPCResponse) error {
	if resp == nil {
		return nil
	}
	return s.transport.WriteMessage(resp)
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
