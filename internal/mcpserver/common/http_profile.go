package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// validateInitializeProfile 校验客户端版本格式，未知安全版本由响应协商为本 profile 支持版本。
func (h *HTTPServer) validateInitializeProfile(w http.ResponseWriter, req jsonRPCRequest) bool {
	if h.localProfile == nil {
		return true
	}
	var params initializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeHTTPJSONError(w, http.StatusBadRequest, req.ID, codeInvalidParams, "invalid initialize parameters")
		return false
	}
	if err := mcpwire.ValidateProtocolVersion(params.ProtocolVersion); err != nil {
		writeHTTPJSONError(w, http.StatusBadRequest, req.ID, codeInvalidParams, err.Error())
		return false
	}
	if err := validateLocalInitializeIdentity(params); err != nil {
		writeHTTPJSONError(w, http.StatusBadRequest, req.ID, codeInvalidParams, err.Error())
		return false
	}
	return true
}

// validateLocalInitializeIdentity 验证标准必填对象，不将客户端名称当作授权主体。
func validateLocalInitializeIdentity(params initializeParams) error {
	if !localJSONObject(params.Capabilities) || !localJSONObject(params.ClientInfo) {
		return errors.New("initialize requires capabilities and clientInfo objects")
	}
	var info map[string]json.RawMessage
	if err := json.Unmarshal(params.ClientInfo, &info); err != nil {
		return errors.New("invalid initialize clientInfo")
	}
	for _, field := range []string{"name", "version"} {
		var value string
		if err := json.Unmarshal(info[field], &value); err != nil || strings.TrimSpace(value) == "" {
			return errors.New("clientInfo requires nonempty name and version strings")
		}
	}
	if len(params.Meta) != 0 && !localJSONObject(params.Meta) {
		return errors.New("initialize _meta must be an object")
	}
	return nil
}

// localJSONObject 区分缺失、null、数组和对象，不以空默认值补齐握手。
func localJSONObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{' && json.Valid(raw)
}

// dispatchLocalLifecycle 将初始化屏障限定在单个连接会话；legacy 行为不变。
func (h *HTTPServer) dispatchLocalLifecycle(session *httpMCPSession, req jsonRPCRequest) (bool, *jsonRPCResponse) {
	if h.localProfile == nil {
		return false, nil
	}
	if req.Method == "notifications/initialized" {
		if err := session.validateCallerPrincipal(); err != nil {
			return true, maybeLocalLifecycleError(req, codeInvalidReq, "session caller principal is invalid")
		}
		return true, session.acceptLocalInitialized(req)
	}
	if req.Method == "ping" {
		return false, nil
	}
	session.mu.Lock()
	ready := session.initialized && !session.closed
	session.mu.Unlock()
	if !ready {
		return true, maybeLocalLifecycleError(req, codeInvalidReq, "session requires notifications/initialized before operations")
	}
	if req.Method == "shutdown" || req.Method == "exit" {
		return true, maybeLocalLifecycleError(req, codeMethodMissing, "method not found")
	}
	return false, nil
}

// acceptLocalInitialized 只接受通知形态，重复通知幂等且不能复活关闭会话。
func (s *httpMCPSession) acceptLocalInitialized(req jsonRPCRequest) *jsonRPCResponse {
	if hasRequestID(req.ID) {
		return errorResponse(req.ID, codeInvalidReq, "initialized must be a notification")
	}
	if len(req.Params) != 0 && !localJSONObject(req.Params) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.initialized = true
	}
	return nil
}

// maybeLocalLifecycleError 通知不产生 JSON-RPC 响应，也不执行请求副作用。
func maybeLocalLifecycleError(req jsonRPCRequest, code int, message string) *jsonRPCResponse {
	if !hasRequestID(req.ID) {
		return nil
	}
	return errorResponse(req.ID, code, message)
}

// negotiateHTTPProtocolVersion 独立 profile 只承诺当前版本；legacy 保留原支持集合。
func (h *HTTPServer) negotiateHTTPProtocolVersion(requested string) (string, error) {
	if h.localProfile == nil {
		return mcpwire.NegotiateProtocolVersion(requested)
	}
	if err := mcpwire.ValidateProtocolVersion(requested); err != nil {
		return "", err
	}
	return mcpwire.LatestProtocolVersion, nil
}

// resolveHTTPToolScope 独立 profile 的 scope 来自服务配置和已验证 task 归因，不来自模型参数。
func (h *HTTPServer) resolveHTTPToolScope(ctx context.Context, params ToolCallParams) (ToolScope, error) {
	if h.localProfile == nil {
		return params.Scope(h.name), nil
	}
	if params.MetaAgentID != "" || params.MetaThreadID != "" || params.MetaCallID != "" || params.MetaCWD != "" || len(params.MetaWorkspaceRoots)+len(params.MetaWorkspaceRootsSnake) != 0 {
		return ToolScope{}, errors.New("client metadata cannot supply standalone caller scope")
	}
	scope := ToolScope{CWD: h.localProfile.WorkspaceRoot, WorkspaceRoots: []string{h.localProfile.WorkspaceRoot}, Family: h.name}
	if h.localProfile.usesTaskAuthentication() {
		attribution, err := RequireCallerAttribution(ctx)
		if err != nil {
			return ToolScope{}, fmt.Errorf("task caller attribution required: %w", err)
		}
		scope.AgentID = attribution.TaskID
	}
	return scope, nil
}

// LocalHTTPProfile 冻结单用户本机 MCP 服务的接入边界，不代表 unified_v2 已通过。
// bearer 验证认证的是服务 operator；客户端 metadata 不能自证 child principal。
type LocalHTTPProfile struct {
	WorkspaceRoot string
	BearerToken   string
	// TaskSessionAuthenticator 独占启用 task session 模式；该模式不配置 operator
	// BearerToken、CallerPrincipal 或 PrincipalIssuer。
	TaskSessionAuthenticator TaskSessionAuthenticator
	// CallerPrincipal 是可选的 transport-owned caller 快照；零值明确关闭 principal
	// 绑定以兼容旧 local HTTP 调用方，非零值在使用前必须校验。
	CallerPrincipal CallerPrincipal
	// PrincipalIssuer 为每个 initialize 签发新的 session 快照；它与
	// CallerPrincipal 固定快照互斥，standalone production 使用此入口。
	PrincipalIssuer CallerPrincipalIssuer
}

// NewLocalHTTPServer 显式启用本机多会话 profile，复用同一 MCP dispatcher 和工具结果链。
func NewLocalHTTPServer(name, version string, provider ToolProvider, profile LocalHTTPProfile, logger *pkglogger.Runtime) (*HTTPServer, error) {
	if err := validateLocalHTTPServerInputs(name, version, provider, profile, logger); err != nil {
		return nil, err
	}
	options := []HTTPServerOption{WithHTTPLoggerRuntime(logger)}
	if profile.usesOperatorAuthentication() {
		options = append(options, WithBearerToken(profile.BearerToken))
	}
	h := NewHTTPServer(name, version, provider, options...)
	h.localProfile = &profile
	h.requestSlots = make(chan struct{}, 64)
	return h, nil
}

// validateLocalHTTPServerInputs 校验 server 基础依赖及 caller principal 单一来源。
func validateLocalHTTPServerInputs(name, version string, provider ToolProvider, profile LocalHTTPProfile, logger *pkglogger.Runtime) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(version) == "" || provider == nil || logger == nil {
		return errors.New("local MCP server requires name, version, provider and logger")
	}
	if !filepath.IsAbs(profile.WorkspaceRoot) {
		return errors.New("local MCP profile requires an absolute workspace")
	}
	return validateLocalHTTPAuthenticationMode(profile)
}

// validateLocalHTTPAuthenticationMode 强制 operator 与 task authenticator 二选一，禁止 endpoint fallback。
func validateLocalHTTPAuthenticationMode(profile LocalHTTPProfile) error {
	if profile.TaskSessionAuthenticator != nil && taskSessionAuthenticatorIsNil(profile.TaskSessionAuthenticator) {
		return errors.New("local MCP task session authenticator must not be typed nil")
	}
	hasTaskAuthenticator := !taskSessionAuthenticatorIsNil(profile.TaskSessionAuthenticator)
	hasOperatorCredential := profile.BearerToken != ""
	if hasTaskAuthenticator == hasOperatorCredential {
		return errors.New("local MCP profile requires exactly one authentication mode")
	}
	if hasTaskAuthenticator {
		if !profile.CallerPrincipal.IsZero() || !callerPrincipalIssuerIsNil(profile.PrincipalIssuer) {
			return errors.New("local MCP task authentication cannot configure operator principal sources")
		}
		if err := profile.TaskSessionAuthenticator.Validate(); err != nil {
			return fmt.Errorf("local MCP task session authenticator: %w", err)
		}
		return nil
	}
	if strings.TrimSpace(profile.BearerToken) != profile.BearerToken {
		return errors.New("local MCP operator credential must be canonical nonempty text")
	}
	return validateLocalHTTPPrincipalSource(profile)
}

// usesOperatorAuthentication 判断 profile 是否启用固定 operator bearer 模式。
func (p LocalHTTPProfile) usesOperatorAuthentication() bool {
	return p.BearerToken != "" && taskSessionAuthenticatorIsNil(p.TaskSessionAuthenticator)
}

// usesTaskAuthentication 判断 profile 是否启用逐请求 task session 认证模式。
func (p LocalHTTPProfile) usesTaskAuthentication() bool {
	return p.BearerToken == "" && !taskSessionAuthenticatorIsNil(p.TaskSessionAuthenticator)
}

// validateLocalHTTPPrincipalSource 拒绝双重来源并校验配置快照或 issuer。
func validateLocalHTTPPrincipalSource(profile LocalHTTPProfile) error {
	hasIssuer := !callerPrincipalIssuerIsNil(profile.PrincipalIssuer)
	if !profile.CallerPrincipal.IsZero() && hasIssuer {
		return errors.New("local MCP profile accepts one caller principal source")
	}
	if hasIssuer {
		if err := profile.PrincipalIssuer.Validate(); err != nil {
			return fmt.Errorf("local MCP profile principal issuer: %w", err)
		}
		return nil
	}
	if err := profile.CallerPrincipal.Validate(); err != nil && !profile.CallerPrincipal.IsZero() {
		return fmt.Errorf("local MCP profile caller principal: %w", err)
	}
	return nil
}

// issueCallerPrincipal 在 initialize 边界签发或复制 session principal。
func (p LocalHTTPProfile) issueCallerPrincipal() (CallerPrincipal, error) {
	if !callerPrincipalIssuerIsNil(p.PrincipalIssuer) {
		principal, err := p.PrincipalIssuer.Issue()
		if err != nil {
			return CallerPrincipal{}, err
		}
		if err := principal.Validate(); err != nil {
			return CallerPrincipal{}, err
		}
		return principal, nil
	}
	return p.CallerPrincipal, nil
}

// localPreflight 在读取 body 或分配 session 之前验证 header 和 Origin。
func (h *HTTPServer) localPreflight(w http.ResponseWriter, r *http.Request) (localHTTPRequestAuthentication, bool) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return localHTTPRequestAuthentication{}, false
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "POST, GET, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return localHTTPRequestAuthentication{}, false
	}
	if !h.validateLocalScalarHeaders(w, r) {
		return localHTTPRequestAuthentication{}, false
	}
	authentication, err := h.authenticateLocalRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return localHTTPRequestAuthentication{}, false
	}
	if r.Header.Get(httpMCPHeaderSessionID) != "" && r.Header.Get("MCP-Protocol-Version") != mcpwire.LatestProtocolVersion {
		http.Error(w, "MCP protocol version mismatch", http.StatusBadRequest)
		return localHTTPRequestAuthentication{}, false
	}
	if !validateLocalMethodHeaders(w, r) {
		return localHTTPRequestAuthentication{}, false
	}
	return authentication, true
}

// authenticateLocalRequest 在任何 session 锁外完成 operator 比对或 task authenticator 调用。
func (h *HTTPServer) authenticateLocalRequest(r *http.Request) (localHTTPRequestAuthentication, error) {
	if h.localProfile.usesOperatorAuthentication() {
		if !h.authorized(r) {
			return localHTTPRequestAuthentication{}, errors.New("operator authentication failed")
		}
		return localHTTPRequestAuthentication{}, nil
	}
	digest, err := digestBearerAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		return localHTTPRequestAuthentication{}, errors.New("task session authentication failed")
	}
	identity, err := h.localProfile.TaskSessionAuthenticator.Authenticate(r.Context(), digest)
	if err != nil {
		return localHTTPRequestAuthentication{}, errors.New("task session authentication failed")
	}
	if err := identity.Validate(); err != nil {
		return localHTTPRequestAuthentication{}, errors.New("task session authentication failed")
	}
	return localHTTPRequestAuthentication{task: true, credentialDigest: digest, identity: identity}, nil
}

// digestBearerAuthorization 严格解析单个 Authorization Bearer 值并立即丢弃原始 credential。
func digestBearerAuthorization(header string) (SessionCredentialDigest, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || strings.TrimSpace(header) != header {
		return SessionCredentialDigest{}, errors.New("invalid bearer authorization")
	}
	return DigestSessionCredential(strings.TrimPrefix(header, prefix))
}

// validateLocalScalarHeaders 对认证、版本和来源拒绝重复与显式空值。
func (h *HTTPServer) validateLocalScalarHeaders(w http.ResponseWriter, r *http.Request) bool {
	for _, name := range []string{"Authorization", "Origin", httpMCPHeaderSessionID, "MCP-Protocol-Version", "Last-Event-ID", "Content-Type"} {
		values := r.Header.Values(name)
		if len(values) > 1 || (len(values) == 1 && strings.TrimSpace(values[0]) == "") {
			http.Error(w, "duplicate or empty scalar header", http.StatusBadRequest)
			return false
		}
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameHTTPOrigin(origin, r.Host) {
		http.Error(w, "origin rejected", http.StatusForbidden)
		return false
	}
	return true
}

// sameHTTPOrigin 只接受当前 endpoint 的同源 Origin；缺失 Origin 的 native CLI 仍须 bearer 认证。
func sameHTTPOrigin(value, host string) bool {
	origin, err := url.Parse(value)
	if err != nil {
		return false
	}
	return origin.Scheme == "http" && origin.Host == host && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == ""
}

// validateLocalMethodHeaders 拒绝混用 resume header，遵守 JSON/SSE 接收声明。
func validateLocalMethodHeaders(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Last-Event-ID") != "" {
		http.Error(w, "SSE replay is not enabled for this profile", http.StatusBadRequest)
		return false
	}
	if r.Method == http.MethodDelete {
		return true
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "application/json is required", http.StatusUnsupportedMediaType)
		return false
	}
	if !acceptsHTTPMedia(r.Header.Values("Accept"), "application/json") || !acceptsHTTPMedia(r.Header.Values("Accept"), "text/event-stream") {
		http.Error(w, "Accept must include JSON and SSE", http.StatusNotAcceptable)
		return false
	}
	return true
}

// acceptsHTTPMedia 解析带 q 值的媒体范围，不把 q=0 或字符串子串误当支持。
func acceptsHTTPMedia(headers []string, expected string) bool {
	for _, header := range headers {
		for part := range strings.SplitSeq(header, ",") {
			media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil || media != expected {
				continue
			}
			quality := 1.0
			if raw, exists := params["q"]; exists {
				quality, err = strconv.ParseFloat(raw, 64)
			}
			if err == nil && quality > 0 && quality <= 1 {
				return true
			}
		}
	}
	return false
}

// handleLocalMCP 以进程级 slot 和 body 上限约束单实例多客户端请求。
func (h *HTTPServer) handleLocalMCP(w http.ResponseWriter, r *http.Request) {
	select {
	case h.requestSlots <- struct{}{}:
		defer func() { <-h.requestSlots }()
	default:
		http.Error(w, "MCP request capacity exceeded", http.StatusServiceUnavailable)
		return
	}
	authentication, ok := h.localPreflight(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxStdioMessageBytes)
	h.handleAuthorizedMCPWithAuthentication(w, r, authentication)
}
