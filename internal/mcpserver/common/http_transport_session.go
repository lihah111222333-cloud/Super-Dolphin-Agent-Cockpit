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
	errHTTPMCPSessionRequired  = errors.New("Mcp-Session-Id header is required")
	errHTTPMCPSessionUnknown   = errors.New("Mcp-Session-Id does not identify an active session")
	errHTTPMCPTaskUnauthorized = errors.New("task session authentication failed")
)

// localHTTPRequestAuthentication 是 local preflight 的脱敏结果；原始 bearer 不进入该结构。
type localHTTPRequestAuthentication struct {
	task             bool
	credentialDigest SessionCredentialDigest
	identity         TaskSessionIdentity
}

// httpMCPSession 隔离单个 Streamable HTTP 客户端的请求 ID 与取消函数。
type httpMCPSession struct {
	mu               sync.Mutex
	inFlight         map[jsonRPCIDKey]*inFlightToolCall
	lastAccess       time.Time
	closed           bool
	initialized      bool
	principal        CallerPrincipal
	principalOn      bool
	taskAuthOn       bool
	credentialDigest SessionCredentialDigest
	taskIdentity     TaskSessionIdentity
}

// handleInitializeRequest 完成握手后创建 session；失败握手不得占用 session。
func (h *HTTPServer) handleInitializeRequest(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	h.handleInitializeRequestWithAuthentication(w, r, req, localHTTPRequestAuthentication{})
}

// handleInitializeRequestWithAuthentication 在握手成功后冻结 task digest/identity 或 operator principal。
func (h *HTTPServer) handleInitializeRequestWithAuthentication(
	w http.ResponseWriter,
	r *http.Request,
	req jsonRPCRequest,
	authentication localHTTPRequestAuthentication,
) {
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
	binding, status, err := h.authenticationForNewSession(authentication)
	if err != nil {
		if status == http.StatusUnauthorized {
			w.WriteHeader(status)
			return
		}
		writeHTTPJSONError(w, http.StatusServiceUnavailable, req.ID, codeInternal, "session caller principal issuance failed")
		return
	}
	sessionID, err := h.createSessionWithAuthentication(binding)
	if err != nil {
		writeHTTPJSONError(w, http.StatusServiceUnavailable, req.ID, codeInternal, err.Error())
		return
	}
	w.Header().Set(httpMCPHeaderSessionID, sessionID)
	writeHTTPResponse(w, resp)
}

// authenticationForNewSession 将 profile 模式投影为一次性冻结的 session 认证绑定。
func (h *HTTPServer) authenticationForNewSession(
	authentication localHTTPRequestAuthentication,
) (httpSessionAuthentication, int, error) {
	if h.localProfile == nil {
		return httpSessionAuthentication{}, 0, nil
	}
	if h.localProfile.usesTaskAuthentication() {
		if err := authentication.validateTask(); err != nil {
			return httpSessionAuthentication{}, http.StatusUnauthorized, err
		}
		return httpSessionAuthentication{
			taskAuthOn:       true,
			credentialDigest: authentication.credentialDigest,
			taskIdentity:     authentication.identity,
		}, 0, nil
	}
	principal, err := h.localProfile.issueCallerPrincipal()
	return httpSessionAuthentication{principal: principal, principalOn: !principal.IsZero()}, 0, err
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

// httpSessionAuthentication 是创建 session 时一次性冻结的 transport 身份事实。
type httpSessionAuthentication struct {
	principal        CallerPrincipal
	principalOn      bool
	taskAuthOn       bool
	credentialDigest SessionCredentialDigest
	taskIdentity     TaskSessionIdentity
}

// Validate 校验 session 认证绑定只携带当前模式允许的字段。
func (a httpSessionAuthentication) Validate() error {
	if a.taskAuthOn {
		return a.validateTask()
	}
	if a.principalOn {
		return a.principal.Validate()
	}
	if !a.principal.IsZero() {
		return errors.New("HTTP MCP session principal flag is inconsistent")
	}
	return nil
}

// validateTask 校验 task session 不混入 operator principal，并携带完整 digest/identity。
func (a httpSessionAuthentication) validateTask() error {
	if a.principalOn || !a.principal.IsZero() || a.credentialDigest == (SessionCredentialDigest{}) {
		return errors.New("HTTP MCP task session authentication binding is invalid")
	}
	return a.taskIdentity.Validate()
}

// validateTask 复验 preflight 结果，防止 task profile 落入 operator/legacy 创建路径。
func (a localHTTPRequestAuthentication) validateTask() error {
	if !a.task || a.credentialDigest == (SessionCredentialDigest{}) {
		return errHTTPMCPTaskUnauthorized
	}
	return a.identity.Validate()
}

// createSession 保留 legacy/operator 测试入口，并转入单一 session 创建实现。
func (h *HTTPServer) createSession(principals ...CallerPrincipal) (string, error) {
	if len(principals) > 1 {
		return "", errors.New("HTTP MCP session accepts at most one caller principal")
	}
	var binding httpSessionAuthentication
	if len(principals) == 1 {
		binding.principal = principals[0]
		binding.principalOn = !principals[0].IsZero()
	}
	return h.createSessionWithAuthentication(binding)
}

// createSessionWithAuthentication 创建不可预测的 session ID，并冻结认证模式与身份快照。
func (h *HTTPServer) createSessionWithAuthentication(binding httpSessionAuthentication) (string, error) {
	if err := binding.Validate(); err != nil {
		return "", fmt.Errorf("bind HTTP MCP session authentication: %w", err)
	}
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
		inFlight:         make(map[jsonRPCIDKey]*inFlightToolCall),
		lastAccess:       time.Now(),
		principal:        binding.principal,
		principalOn:      binding.principalOn,
		taskAuthOn:       binding.taskAuthOn,
		credentialDigest: binding.credentialDigest,
		taskIdentity:     binding.taskIdentity,
	}
	return sessionID, nil
}

// bindCallerPrincipal 从不可变 session 快照派生请求 context；task 模式按 principal、
// attribution 固定顺序绑定，任一步失败都返回原始 context。
func (s *httpMCPSession) bindCallerPrincipal(ctx context.Context) (context.Context, error) {
	s.mu.Lock()
	principalOn := s.principalOn
	principal := s.principal
	taskAuthOn := s.taskAuthOn
	taskIdentity := s.taskIdentity
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ctx, errors.New("HTTP MCP session is closed")
	}
	if taskAuthOn {
		return BindTaskSessionIdentity(ctx, taskIdentity)
	}
	if !principalOn {
		return ctx, nil
	}
	return BindCallerPrincipal(ctx, principal)
}

// validateCallerPrincipal 在生命周期状态变化前检查 session 快照，阻止过期身份解锁本地会话。
func (s *httpMCPSession) validateCallerPrincipal() error {
	s.mu.Lock()
	principalOn := s.principalOn
	principal := s.principal
	taskAuthOn := s.taskAuthOn
	taskIdentity := s.taskIdentity
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("HTTP MCP session is closed")
	}
	if taskAuthOn {
		return taskIdentity.Validate()
	}
	if !principalOn {
		return nil
	}
	return principal.Validate()
}

// requireSession 解析并验证后续请求携带的 session header。
func (h *HTTPServer) requireSession(r *http.Request) (*httpMCPSession, int, error) {
	return h.requireSessionWithAuthentication(r, localHTTPRequestAuthentication{})
}

// requireSessionWithAuthentication 在 registry/session 锁外完成的认证结果与冻结绑定逐字段比较，
// 通过后才刷新访问时间。
func (h *HTTPServer) requireSessionWithAuthentication(
	r *http.Request,
	authentication localHTTPRequestAuthentication,
) (*httpMCPSession, int, error) {
	sessionID := strings.TrimSpace(r.Header.Get(httpMCPHeaderSessionID))
	if sessionID == "" {
		return nil, http.StatusBadRequest, errHTTPMCPSessionRequired
	}
	h.sessionsMu.RLock()
	session := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if session == nil {
		return nil, http.StatusNotFound, errHTTPMCPSessionUnknown
	}
	if h.localProfile != nil && h.localProfile.usesTaskAuthentication() {
		if err := session.authorizeTaskRequest(authentication, true); err != nil {
			return nil, http.StatusUnauthorized, errHTTPMCPTaskUnauthorized
		}
		return session, 0, nil
	}
	if session.touch(time.Now()) {
		return session, 0, nil
	}
	return nil, http.StatusNotFound, errHTTPMCPSessionUnknown
}

// authorizeTaskRequest 原子比较 digest 和完整身份对；只有匹配时才可选 touch。
func (s *httpMCPSession) authorizeTaskRequest(authentication localHTTPRequestAuthentication, touch bool) error {
	if err := authentication.validateTask(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.taskAuthOn || s.credentialDigest != authentication.credentialDigest ||
		!s.taskIdentity.Equal(authentication.identity) {
		return errHTTPMCPTaskUnauthorized
	}
	if touch {
		s.lastAccess = time.Now()
	}
	return nil
}

// handleDeleteSession 终止一个 session，并取消其全部进行中工具调用。
func (h *HTTPServer) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	h.handleDeleteSessionWithAuthentication(w, r, localHTTPRequestAuthentication{})
}

// handleDeleteSessionWithAuthentication 先复验 task session 绑定，再从 registry 原子摘除。
func (h *HTTPServer) handleDeleteSessionWithAuthentication(
	w http.ResponseWriter,
	r *http.Request,
	authentication localHTTPRequestAuthentication,
) {
	sessionID := strings.TrimSpace(r.Header.Get(httpMCPHeaderSessionID))
	if sessionID == "" {
		writeHTTPJSONError(w, http.StatusBadRequest, nil, codeInvalidReq, errHTTPMCPSessionRequired.Error())
		return
	}
	h.sessionsMu.RLock()
	session := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if session == nil {
		writeHTTPJSONError(w, http.StatusNotFound, nil, codeInvalidReq, errHTTPMCPSessionUnknown.Error())
		return
	}
	if h.localProfile != nil && h.localProfile.usesTaskAuthentication() {
		if err := session.authorizeTaskRequest(authentication, false); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	h.sessionsMu.Lock()
	if h.sessions[sessionID] == session {
		delete(h.sessions, sessionID)
	} else {
		session = nil
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
