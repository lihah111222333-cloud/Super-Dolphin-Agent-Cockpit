package common

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	httpMCPHeaderSessionID       = "Mcp-Session-Id"
	maxHTTPMCPSessions           = 1024
	maxHTTPInFlightToolCalls     = 64
	httpMCPSessionRandomByteSize = 24
)

var (
	errHTTPMCPSessionRequired = errors.New("Mcp-Session-Id header is required")
	errHTTPMCPSessionUnknown  = errors.New("Mcp-Session-Id does not identify an active session")
)

// httpMCPSession 隔离单个 Streamable HTTP 客户端的请求 ID 与取消函数。
type httpMCPSession struct {
	mu         sync.Mutex
	inFlight   map[jsonRPCIDKey]*inFlightToolCall
	lastAccess time.Time
	closed     bool
}

// handleInitializeRequest 完成握手后创建 session；失败握手不得占用 session。
func (h *HTTPServer) handleInitializeRequest(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	if strings.TrimSpace(r.Header.Get(httpMCPHeaderSessionID)) != "" {
		writeHTTPJSONError(w, http.StatusBadRequest, req.ID, codeInvalidReq, "initialize must not include Mcp-Session-Id")
		return
	}
	if !hasRequestID(req.ID) {
		writeHTTPJSONError(w, http.StatusBadRequest, nil, codeInvalidReq, "initialize requires a request id")
		return
	}
	resp := h.handleInitialize(req)
	if resp == nil || resp.Error != nil {
		writeHTTPResponse(w, resp)
		return
	}
	sessionID, err := h.createSession()
	if err != nil {
		writeHTTPJSONError(w, http.StatusServiceUnavailable, req.ID, codeInternal, err.Error())
		return
	}
	w.Header().Set(httpMCPHeaderSessionID, sessionID)
	writeHTTPResponse(w, resp)
}

// writeHTTPResponse 写出单个 JSON-RPC HTTP 响应。
func writeHTTPResponse(w http.ResponseWriter, resp *jsonRPCResponse) {
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// createSession 创建不可预测的 session ID，并对 session 总数设硬上限。
func (h *HTTPServer) createSession() (string, error) {
	raw := make([]byte, httpMCPSessionRandomByteSize)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate HTTP MCP session ID: %w", err)
	}
	sessionID := base64.RawURLEncoding.EncodeToString(raw)
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	if len(h.sessions) >= maxHTTPMCPSessions {
		return "", fmt.Errorf("too many active HTTP MCP sessions (limit %d)", maxHTTPMCPSessions)
	}
	if _, exists := h.sessions[sessionID]; exists {
		return "", errors.New("generated duplicate HTTP MCP session ID")
	}
	h.sessions[sessionID] = &httpMCPSession{
		inFlight:   make(map[jsonRPCIDKey]*inFlightToolCall),
		lastAccess: time.Now(),
	}
	return sessionID, nil
}

// requireSession 解析并验证后续请求携带的 session header。
func (h *HTTPServer) requireSession(r *http.Request) (*httpMCPSession, int, error) {
	sessionID := strings.TrimSpace(r.Header.Get(httpMCPHeaderSessionID))
	if sessionID == "" {
		return nil, http.StatusBadRequest, errHTTPMCPSessionRequired
	}
	h.sessionsMu.RLock()
	session := h.sessions[sessionID]
	if session != nil && session.touch(time.Now()) {
		h.sessionsMu.RUnlock()
		return session, 0, nil
	}
	h.sessionsMu.RUnlock()
	return nil, http.StatusNotFound, errHTTPMCPSessionUnknown
}

// handleDeleteSession 终止一个 session，并取消其全部进行中工具调用。
func (h *HTTPServer) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.Header.Get(httpMCPHeaderSessionID))
	if sessionID == "" {
		writeHTTPJSONError(w, http.StatusBadRequest, nil, codeInvalidReq, errHTTPMCPSessionRequired.Error())
		return
	}
	h.sessionsMu.Lock()
	session := h.sessions[sessionID]
	if session != nil {
		delete(h.sessions, sessionID)
	}
	h.sessionsMu.Unlock()
	if session == nil {
		writeHTTPJSONError(w, http.StatusNotFound, nil, codeInvalidReq, errHTTPMCPSessionUnknown.Error())
		return
	}
	session.terminate()
	w.WriteHeader(http.StatusNoContent)
}

// terminateAllSessions 从 registry 摘除并取消全部 session。
func (h *HTTPServer) terminateAllSessions() {
	h.sessionsMu.Lock()
	sessions := make([]*httpMCPSession, 0, len(h.sessions))
	for id, session := range h.sessions {
		sessions = append(sessions, session)
		delete(h.sessions, id)
	}
	h.sessionsMu.Unlock()
	for _, session := range sessions {
		session.terminate()
	}
}

// reapIdleSessions 摘除超过 idle TTL 且没有进行中调用的 session。
func (h *HTTPServer) reapIdleSessions(now time.Time) {
	h.sessionsMu.Lock()
	sessions := make([]*httpMCPSession, 0)
	for id, session := range h.sessions {
		if !session.isIdleAt(now, h.sessionIdleTTL) {
			continue
		}
		delete(h.sessions, id)
		sessions = append(sessions, session)
	}
	h.sessionsMu.Unlock()
	for _, session := range sessions {
		session.terminate()
	}
}

// handleSessionToolCall 在 session 内登记请求 ID，并在完成后精确释放。
func (h *HTTPServer) handleSessionToolCall(
	ctx context.Context,
	session *httpMCPSession,
	req jsonRPCRequest,
) *jsonRPCResponse {
	if !hasRequestID(req.ID) {
		return h.handleToolsCall(ctx, req)
	}
	callCtx, finish, rejection := session.beginToolCall(ctx, req.ID)
	if rejection != nil {
		return rejection
	}
	defer finish()
	return h.handleToolsCall(callCtx, req)
}

// beginToolCall 为一个可取消工具调用建立子 context。
func (s *httpMCPSession) beginToolCall(
	ctx context.Context,
	id json.RawMessage,
) (context.Context, func(), *jsonRPCResponse) {
	key, err := makeJSONRPCIDKey(id)
	if err != nil {
		return nil, func() {}, errorResponse(nil, codeInvalidReq, err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, func() {}, errorResponse(id, codeInvalidReq, "HTTP MCP session is closed")
	}
	if _, duplicate := s.inFlight[key]; duplicate {
		return nil, func() {}, errorResponse(id, codeInvalidReq, "duplicate active request id")
	}
	if len(s.inFlight) >= maxHTTPInFlightToolCalls {
		return nil, func() {}, errorResponse(
			id,
			codeInternal,
			fmt.Sprintf("too many active tools/call requests in session (limit %d)", maxHTTPInFlightToolCalls),
		)
	}
	s.lastAccess = time.Now()
	callCtx, cancel := context.WithCancel(ctx)
	call := &inFlightToolCall{cancel: cancel}
	s.inFlight[key] = call
	finish := func() {
		cancel()
		s.mu.Lock()
		if s.inFlight[key] == call {
			delete(s.inFlight, key)
		}
		s.mu.Unlock()
	}
	return callCtx, finish, nil
}

// touch 刷新活跃 session 的最后访问时间，已关闭 session 不再复活。
func (s *httpMCPSession) touch(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.lastAccess = now
	return true
}

// isIdleAt 仅在没有进行中调用且最后访问超过 TTL 时允许回收。
func (s *httpMCPSession) isIdleAt(now time.Time, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && len(s.inFlight) == 0 && !s.lastAccess.After(now.Add(-ttl))
}

// cancelRequest 仅取消当前 session 中匹配协议类型和值的请求 ID。
func (s *httpMCPSession) cancelRequest(raw json.RawMessage) {
	var params cancelledParams
	if err := platformshared.DecodeInput(raw, &params); err != nil {
		return
	}
	key, err := makeJSONRPCIDKey(params.RequestID)
	if err != nil {
		return
	}
	s.mu.Lock()
	call := s.inFlight[key]
	s.mu.Unlock()
	if call != nil {
		call.cancel()
	}
}

// terminate 原子关闭 session，再在锁外取消全部调用。
func (s *httpMCPSession) terminate() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancels := make([]context.CancelFunc, 0, len(s.inFlight))
	for _, call := range s.inFlight {
		cancels = append(cancels, call.cancel)
	}
	s.mu.Unlock()
	cancelToolCalls(cancels)
}
