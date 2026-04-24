package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	jsonRPCCodeParseError   = -32700
	jsonRPCCodeInvalidReq   = -32600
	jsonRPCCodeMethodMiss   = -32601
	jsonRPCCodeInvalidParam = -32602
	jsonRPCCodeInternal     = -32603
	proxyMaxBodyBytes       = 1 << 20
	proxyToolCallTimeout    = 30 * time.Second
)

type proxyJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type proxyJSONRPCResponse struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      any                `json:"id"`
	Result  any                `json:"result,omitempty"`
	Error   *proxyJSONRPCError `json:"error,omitempty"`
}

type proxyJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type proxyToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (h *Handler) ServeProxy(ln net.Listener) error {
	if h == nil {
		return errors.New("toolbridge: nil handler")
	}
	if ln == nil {
		return errors.New("toolbridge: nil listener")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/", h.handleProxyRequest)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	err := server.Serve(ln)
	if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (h *Handler) handleProxyRequest(w http.ResponseWriter, r *http.Request) {
	h.debug("proxy: incoming request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, proxyMaxBodyBytes)
	defer r.Body.Close()

	req, err := decodeProxyJSONRPCRequest(r)
	if err != nil {
		writeJSONRPCError(w, nil, proxyDecodeErrorCode(err), err.Error())
		return
	}
	if strings.TrimSpace(req.JSONRPC) != "2.0" {
		writeJSONRPCError(w, req.ID, jsonRPCCodeInvalidReq, "jsonrpc must be 2.0")
		return
	}
	family, agentID, err := extractPathParts(r.URL.Path)
	if err != nil {
		writeJSONRPCError(w, req.ID, jsonRPCCodeInvalidParam, err.Error())
		return
	}
	switch req.Method {
	case "initialize":
		writeJSONRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "proxy", "version": "1.0.0"},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		h.handleProxyToolsList(w, r.Context(), req.ID, family)
	case "tools/call":
		h.handleProxyToolCall(w, r.Context(), req, family, agentID)
	default:
		writeJSONRPCError(w, req.ID, jsonRPCCodeMethodMiss, "method not found")
	}
}

func decodeProxyJSONRPCRequest(r *http.Request) (proxyJSONRPCRequest, error) {
	var req proxyJSONRPCRequest
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		return proxyJSONRPCRequest{}, err
	}
	return req, nil
}

func proxyDecodeErrorCode(err error) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return jsonRPCCodeInvalidParam
	}
	return jsonRPCCodeParseError
}

func decodeProxyToolCallParams(raw json.RawMessage) (proxyToolCallParams, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return proxyToolCallParams{}, errors.New("tool call params must be a non-null object")
	}
	var params proxyToolCallParams
	if err := platformshared.DecodeInput(json.RawMessage(trimmed), &params); err != nil {
		return proxyToolCallParams{}, err
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return proxyToolCallParams{}, errors.New("tool name is required")
	}
	trimmedArgs := bytes.TrimSpace(params.Arguments)
	if len(trimmedArgs) == 0 || bytes.Equal(trimmedArgs, []byte("null")) {
		params.Arguments = json.RawMessage(`{}`)
	}
	return params, nil
}

func validateProxyToolFamily(family, toolName string) error {
	clientKind := familyToClientKind(family)
	if clientKind == "" {
		return errors.New("unsupported family")
	}
	if classifyTool(toolName) != clientKind {
		return fmt.Errorf("tool %q does not match proxy family %q", toolName, family)
	}
	return nil
}

func (h *Handler) handleProxyToolsList(w http.ResponseWriter, ctx context.Context, id any, family string) {
	clientKind := familyToClientKind(family)
	if clientKind == "" {
		writeJSONRPCError(w, id, jsonRPCCodeInvalidParam, "unsupported family")
		return
	}
	tools, err := h.listPeerTools(ctx, clientKind)
	if err != nil {
		writeJSONRPCError(w, id, jsonRPCCodeInternal, err.Error())
		return
	}
	writeJSONRPCResult(w, id, map[string]any{"tools": tools})
}

func (h *Handler) handleProxyToolCall(w http.ResponseWriter, ctx context.Context, req proxyJSONRPCRequest, family, agentID string) {
	params, err := decodeProxyToolCallParams(req.Params)
	if err != nil {
		writeJSONRPCError(w, req.ID, jsonRPCCodeInvalidParam, err.Error())
		return
	}
	if err := validateProxyToolFamily(family, params.Name); err != nil {
		writeJSONRPCError(w, req.ID, jsonRPCCodeInvalidParam, err.Error())
		return
	}

	callCtx, cancel := platformconfig.WithPeerTimeout(ctx, proxyToolCallTimeout)
	defer cancel()

	result, err := h.routeToolCall(callCtx, ToolCallRequest{
		Name:       params.Name,
		Arguments:  params.Arguments,
		AgentID:    strings.TrimSpace(agentID),
		ThreadID:   h.lookupProxyThreadID(callCtx, agentID),
		CallID:     callIDFromJSONRPCID(req.ID),
		ClientKind: familyToClientKind(family),
	})
	if err != nil {
		writeJSONRPCError(w, req.ID, proxyToolCallErrorCode(err), err.Error())
		return
	}
	if result == nil {
		result = &ToolCallResult{Success: true}
	}
	writeJSONRPCResult(w, req.ID, map[string]any{
		"content": toMCPContent(result.ContentItems),
		"isError": !result.Success,
	})
}

func proxyToolCallErrorCode(err error) int {
	switch {
	case errors.Is(err, contract.ErrThreadRuntimeRequired), errors.Is(err, contract.ErrPersistentSubagentRuntimeRequired):
		return jsonRPCCodeInvalidParam
	default:
		return jsonRPCCodeInternal
	}
}

func (h *Handler) lookupProxyThreadID(ctx context.Context, agentID string) string {
	if h == nil || h.bindingStore == nil {
		return ""
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	threadID, err := h.bindingStore.GetThreadByAgent(ctx, agentID)
	if err != nil {
		h.warn("toolbridge: proxy thread lookup failed", "agent_id", agentID, "error", err)
		return ""
	}
	return strings.TrimSpace(threadID)
}

func callIDFromJSONRPCID(id any) string {
	if id == nil {
		return ""
	}
	return fmt.Sprintf("%v", id)
}

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	if id == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeProxyJSON(w, proxyJSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeProxyJSON(w, proxyJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &proxyJSONRPCError{Code: code, Message: strings.TrimSpace(message)},
	})
}

func writeProxyJSON(w http.ResponseWriter, resp proxyJSONRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func toMCPContent(items []ToolCallContentItem) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	content := make([]map[string]any, 0, len(items))
	for _, item := range items {
		itemType := strings.TrimSpace(item.Type)
		if itemType == "" || itemType == "inputText" {
			itemType = "text"
		}
		content = append(content, map[string]any{
			"type": itemType,
			"text": item.Text,
		})
	}
	return content
}

func familyToClientKind(family string) string {
	switch strings.TrimSpace(family) {
	case mcpdto.ClientKindLSP:
		return mcpdto.ClientKindLSP
	case mcpdto.ClientKindOrch:
		return mcpdto.ClientKindOrch
	case mcpdto.ClientKindIDA:
		return mcpdto.ClientKindIDA
	default:
		return ""
	}
}

func extractPathParts(path string) (string, string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "mcp" {
		return "", "", errors.New("invalid proxy path")
	}
	family := strings.TrimSpace(parts[1])
	agentID := strings.TrimSpace(parts[2])
	if family == "" || agentID == "" {
		return "", "", errors.New("invalid proxy path")
	}
	return family, agentID, nil
}
