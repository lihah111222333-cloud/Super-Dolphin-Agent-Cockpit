package toolbridge

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"
)

const (
	jsonRPCCodeParseError    = -32700
	jsonRPCCodeInvalidReq    = -32600
	jsonRPCCodeMethodMiss    = -32601
	jsonRPCCodeInvalidParam  = -32602
	jsonRPCCodeInternal      = -32603
	proxyMaxBodyBytes        = 1 << 20
	proxyToolCallTimeout     = 30 * time.Second
	proxyAuthorizationHeader = "Authorization"
)

// proxyJSONRPCRequest 是 toolbridge HTTP proxy 接收的 JSON-RPC 请求外壳。
type proxyJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// proxyJSONRPCResponse 是 toolbridge HTTP proxy 返回的 JSON-RPC 响应外壳。
type proxyJSONRPCResponse struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      any                `json:"id"`
	Result  any                `json:"result,omitempty"`
	Error   *proxyJSONRPCError `json:"error,omitempty"`
}

// proxyJSONRPCError 是 JSON-RPC error 字段的最小结构。
type proxyJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// proxyToolCallParams 是 proxy tools/call 的严格参数结构。
type proxyToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ServeProxy 在给定 listener 上启动 toolbridge HTTP JSON-RPC proxy。
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

// newProxyAuthToken 创建本地 proxy bearer token。
func newProxyAuthToken() string {
	return platformshared.NewID("toolbridge_proxy")
}

// authorizeProxyRequest 校验 proxy Authorization header；空 token 仅用于旧测试路径。
func (h *Handler) authorizeProxyRequest(r *http.Request) bool {
	if h == nil {
		return false
	}
	token := strings.TrimSpace(h.proxyAuthToken)
	if token == "" {
		return true
	}
	want := "Bearer " + token
	got := strings.TrimSpace(r.Header.Get(proxyAuthorizationHeader))
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// writeProxyUnauthorized 返回 bearer 认证失败响应。
func writeProxyUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="toolbridge"`)
	http.Error(w, "toolbridge proxy authorization required", http.StatusUnauthorized)
}

// handleProxyRequest 处理 /mcp/{family}/{agentID} JSON-RPC 请求。
// 未知 method fail-closed 返回 method not found，避免静默 ACK。
func (h *Handler) handleProxyRequest(w http.ResponseWriter, r *http.Request) {
	h.debug("proxy: incoming request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
	if !h.authorizeProxyRequest(r) {
		writeProxyUnauthorized(w)
		return
	}
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
	case ProxyMethodInitialize:
		writeJSONRPCResult(w, req.ID, map[string]any{
			"protocolVersion": ProxyProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    ProxyServerInfoName,
				"version": ProxyServerInfoVersion,
			},
		})
	case ProxyNotificationMethod:
		w.WriteHeader(http.StatusAccepted)
	case ProxyMethodToolsList:
		h.handleProxyToolsList(w, r.Context(), req.ID, family)
	case ProxyMethodToolsCall:
		h.handleProxyToolCall(w, r.Context(), req, family, agentID)
	default:
		// 未知 method 必须显式返回 method-not-found，避免客户端误以为请求已被处理。
		writeJSONRPCError(w, req.ID, jsonRPCCodeMethodMiss, "method not found")
	}
}

// decodeProxyJSONRPCRequest 读取单个 JSON-RPC 请求，保留数字 id 的原始精度。
func decodeProxyJSONRPCRequest(r *http.Request) (proxyJSONRPCRequest, error) {
	var req proxyJSONRPCRequest
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		return proxyJSONRPCRequest{}, err
	}
	return req, nil
}

// proxyDecodeErrorCode 将 HTTP body 解码错误映射为 JSON-RPC error code。
func proxyDecodeErrorCode(err error) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return jsonRPCCodeInvalidParam
	}
	return jsonRPCCodeParseError
}

// decodeProxyToolCallParams 解码 proxy tools/call 参数，空 arguments 规范化为对象。
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

// validateProxyToolFamily 确保工具名和 URL family 匹配，防止跨 peer family 调用。
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

// handleProxyToolsList 返回指定 family 的工具列表。
func (h *Handler) handleProxyToolsList(w http.ResponseWriter, ctx context.Context, id any, family string) {
	clientKind := familyToClientKind(family)
	if clientKind == "" {
		writeJSONRPCError(w, id, jsonRPCCodeInvalidParam, "unsupported family")
		return
	}
	if clientKind == mcpdto.ClientKindOrch {
		h.handleProxyOrchToolsList(w, ctx, id)
		return
	}
	tools, err := h.listPeerTools(ctx, clientKind)
	if err != nil {
		writeJSONRPCError(w, id, jsonRPCCodeInternal, err.Error())
		return
	}
	writeJSONRPCResult(w, id, map[string]any{"tools": filterProxyPeerReservedHostTools(tools)})
}

// filterProxyPeerReservedHostTools 从 peer 列表中移除 host-only 保留工具。
func filterProxyPeerReservedHostTools(tools []mcpdto.MCPTool) []mcpdto.MCPTool {
	if len(tools) == 0 {
		return tools
	}
	out := make([]mcpdto.MCPTool, 0, len(tools))
	for _, tool := range tools {
		if isReservedHostOnlyToolName(strings.TrimSpace(tool.Name)) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// handleProxyOrchToolsList 合并 host-direct 与 orchestration peer 工具列表。
func (h *Handler) handleProxyOrchToolsList(w http.ResponseWriter, ctx context.Context, id any) {
	var hostTools []mcpdto.MCPTool
	if h != nil && h.hostTools != nil {
		hostTools = h.hostTools.ListHostTools()
	}
	seen := make(map[string]string, len(hostTools))
	tools := h.appendMCPToolsWithShadowWarning(nil, seen, "host", hostTools)
	peerTools, err := h.listPeerTools(ctx, mcpdto.ClientKindOrch)
	if err != nil {
		if len(tools) == 0 {
			writeJSONRPCError(w, id, jsonRPCCodeInternal, err.Error())
			return
		}
		h.warn("toolbridge proxy tools/list peer degraded", "client_kind", mcpdto.ClientKindOrch, "error", err)
		writeJSONRPCResult(w, id, map[string]any{"tools": tools})
		return
	}
	tools = h.appendMCPToolsWithShadowWarning(tools, seen, mcpdto.ClientKindOrch, peerTools)
	writeJSONRPCResult(w, id, map[string]any{"tools": tools})
}

// handleProxyToolCall 解码 proxy tools/call，并发布工具生命周期事件。
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

	threadID, err := h.resolveProxyThreadID(callCtx, agentID, family)
	if err != nil {
		writeJSONRPCError(w, req.ID, jsonRPCCodeInvalidParam, err.Error())
		return
	}
	callReq := ToolCallRequest{
		Name:       params.Name,
		Arguments:  params.Arguments,
		AgentID:    strings.TrimSpace(agentID),
		ThreadID:   threadID,
		CallID:     callIDFromJSONRPCID(req.ID),
		ClientKind: familyToClientKind(family),
	}
	started := time.Now()
	h.publishProxyToolCallBegin(callReq, started)
	result, err := h.routeToolCall(callCtx, callReq)
	if err != nil {
		h.publishProxyToolCallEnd(callReq, started, nil, err)
		writeJSONRPCError(w, req.ID, proxyToolCallErrorCode(err), err.Error())
		return
	}
	if result == nil {
		result = &ToolCallResult{Success: true}
	}
	h.publishProxyToolCallEnd(callReq, started, result, nil)
	payload := map[string]any{
		"content": toMCPContent(result.ContentItems),
		"isError": !result.Success,
	}
	if len(result.StructuredContent) > 0 {
		payload["structuredContent"] = json.RawMessage(append([]byte(nil), result.StructuredContent...))
	}
	writeJSONRPCResult(w, req.ID, payload)
}

// publishProxyToolCallBegin 发布 proxy 入口的工具开始事件。
func (h *Handler) publishProxyToolCallBegin(req ToolCallRequest, started time.Time) {
	if h == nil || h.dispatcher == nil {
		return
	}
	if strings.TrimSpace(req.ThreadID) == "" {
		return
	}
	event.Publish(h.dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader:   proxyToolCallHeader(req, started),
		ArgumentsPreview: strings.TrimSpace(string(req.Arguments)),
	})
}

// publishProxyToolCallEnd 发布 proxy 入口的工具结束事件。
func (h *Handler) publishProxyToolCallEnd(req ToolCallRequest, started time.Time, result *ToolCallResult, callErr error) {
	if h == nil || h.dispatcher == nil {
		return
	}
	if strings.TrimSpace(req.ThreadID) == "" {
		return
	}
	header := proxyToolCallHeader(req, time.Now())
	success := callErr == nil && (result == nil || result.Success)
	ev := tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Result:         proxyToolResultPreview(result),
		ElapsedMS:      time.Since(started).Milliseconds(),
	}
	if callErr != nil {
		ev.Error = callErr.Error()
	}
	event.Publish(h.dispatcher, ev)
}

// proxyToolCallHeader 构造 proxy tool lifecycle 事件头。
func proxyToolCallHeader(req ToolCallRequest, ts time.Time) shareddto.ToolCallHeader {
	return shareddto.ToolCallHeader{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: ts},
					ThreadID:    strings.TrimSpace(req.ThreadID),
				},
				AgentID: strings.TrimSpace(req.AgentID),
			},
		},
		CallID:   strings.TrimSpace(req.CallID),
		ToolName: strings.TrimSpace(req.Name),
	}
}

// proxyToolResultPreview 生成 tool end 事件中的结果预览。
func proxyToolResultPreview(result *ToolCallResult) string {
	if result == nil {
		return ""
	}
	if raw := bytes.TrimSpace(result.StructuredContent); len(raw) != 0 {
		return string(raw)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return string(raw)
}

// proxyToolCallErrorCode 将 tool call 错误映射为 JSON-RPC error code。
func proxyToolCallErrorCode(err error) int {
	switch {
	case errors.Is(err, contract.ErrThreadRuntimeRequired),
		errors.Is(err, contract.ErrPersistentSubagentRuntimeRequired),
		errors.Is(err, contract.ErrPersistentSubagentFlagRequired):
		return jsonRPCCodeInvalidParam
	default:
		return jsonRPCCodeInternal
	}
}

// resolveProxyThreadID 通过 agentID 解析 proxy 调用所属 threadID。
// LSP family 在无 binding store 时允许使用 agentID，其他 family 必须绑定到真实 thread。
func (h *Handler) resolveProxyThreadID(ctx context.Context, agentID, family string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", errors.New("toolbridge: proxy agent id is required")
	}
	if h == nil || h.bindingStore == nil {
		if familyToClientKind(family) == mcpdto.ClientKindLSP {
			return agentID, nil
		}
		return "", nil
	}
	threadID, err := h.bindingStore.GetThreadByAgent(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("toolbridge: resolve proxy thread for agent %q: %w", agentID, err)
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", fmt.Errorf("toolbridge: resolve proxy thread for agent %q: empty thread id", agentID)
	}
	return threadID, nil
}

// callIDFromJSONRPCID 将 JSON-RPC id 转成工具 callID。
func callIDFromJSONRPCID(id any) string {
	if id == nil {
		return ""
	}
	return fmt.Sprintf("%v", id)
}

// writeJSONRPCResult 写入 JSON-RPC success 响应；notification 不带 body。
func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	if id == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeProxyJSON(w, proxyJSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

// writeJSONRPCError 写入 JSON-RPC error 响应。
func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeProxyJSON(w, proxyJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &proxyJSONRPCError{Code: code, Message: strings.TrimSpace(message)},
	})
}

// writeProxyJSON 统一写入 proxy JSON 响应。
func writeProxyJSON(w http.ResponseWriter, resp proxyJSONRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// toMCPContent 将内部 content item 转为 MCP text content。
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

// familyToClientKind 校验并返回 URL family 对应的 MCP client kind。
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

// extractPathParts 从 /mcp/{family}/{agentID} 路径中提取 family 和 agentID。
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
