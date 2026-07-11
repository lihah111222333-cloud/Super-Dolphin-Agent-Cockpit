package rpc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const (
	// controlRPCSessionTokenEnv 是当前控制 RPC 会话令牌环境变量。
	controlRPCSessionTokenEnv       = "GO_AGENT_CTL_SESSION_TOKEN"
	legacyControlRPCSessionTokenEnv = "GO_AGENT_MCP_SESSION_TOKEN"
)

// controlRPCAuthAssigner 为 TCP control-plane handler 增加连接级注册认证。
type controlRPCAuthAssigner struct {
	base jrpc2.Assigner
	auth *controlRPCConnectionAuth
}

// Assign 为 TCP control-plane handler 包装连接级注册认证门禁。
// 只有 ctl/register 携带正确 session token 后，同一连接上的其他方法才会继续执行。
func (a controlRPCAuthAssigner) Assign(ctx context.Context, method string) jrpc2.Handler {
	if a.base == nil {
		return nil
	}
	next := a.base.Assign(ctx, method)
	if next == nil {
		return nil
	}
	return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
		if a.auth == nil {
			return nil, rpcError(CodeInvalidState, "control rpc auth is not configured")
		}
		if err := a.auth.authorize(method, req); err != nil {
			return nil, err
		}
		resp, err := next(ctx, req)
		if err == nil && strings.TrimSpace(method) == dto.MethodRegister {
			a.auth.markAuthenticated()
		}
		return resp, err
	})
}

// Names 透传底层 assigner 的方法名列表，保持 jrpc2 introspection 行为。
func (a controlRPCAuthAssigner) Names() []string {
	namer, ok := a.base.(jrpc2.Namer)
	if !ok {
		return nil
	}
	return namer.Names()
}

// controlRPCConnectionAuth 记录单条控制 RPC 连接的认证状态。
type controlRPCConnectionAuth struct {
	expected string
	onAuth   func()

	mu            sync.Mutex
	authenticated bool
	notifyOnce    sync.Once
}

// newControlRPCConnectionAuth 创建连接认证状态，expected 为空会让 register fail-fast。
func newControlRPCConnectionAuth(expected string, onAuthenticated ...func()) *controlRPCConnectionAuth {
	auth := &controlRPCConnectionAuth{expected: strings.TrimSpace(expected)}
	if len(onAuthenticated) > 0 {
		auth.setOnAuthenticated(onAuthenticated[0])
	}
	return auth
}

// authorize 要求同一连接先用共享 session token 完成 ctl/register。
func (a *controlRPCConnectionAuth) authorize(method string, req *jrpc2.Request) error {
	method = strings.TrimSpace(method)
	if method == dto.MethodRegister {
		return a.authorizeRegister(req)
	}
	if a.isAuthenticated() {
		return nil
	}
	return rpcError(CodeInvalidState, "control rpc unauthorized: register with a valid session token first")
}

// authorizeRegister 校验 register 请求中的 session token。
func (a *controlRPCConnectionAuth) authorizeRegister(req *jrpc2.Request) error {
	if a.expected == "" {
		return rpcError(CodeInvalidState, "control rpc session token is not configured")
	}
	var payload struct {
		SessionToken string `json:"session_token"`
	}
	if err := req.UnmarshalParams(&payload); err != nil {
		return rpcError(CodeInvalidParams, "invalid register params: "+err.Error())
	}
	token := strings.TrimSpace(payload.SessionToken)
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.expected)) != 1 {
		return rpcError(CodeInvalidState, "control rpc unauthorized: invalid session token")
	}
	return nil
}

// markAuthenticated 在 register 成功后标记当前连接已认证。
func (a *controlRPCConnectionAuth) markAuthenticated() {
	a.mu.Lock()
	a.authenticated = true
	a.mu.Unlock()
	a.notifyAuthenticated()
}

// setOnAuthenticated 绑定认证成功回调；若连接已经完成认证，则立即补发一次。
func (a *controlRPCConnectionAuth) setOnAuthenticated(fn func()) {
	a.mu.Lock()
	a.onAuth = fn
	authenticated := a.authenticated
	a.mu.Unlock()
	if authenticated {
		a.notifyAuthenticated()
	}
}

// notifyAuthenticated 一次性通知认证完成；没有回调时不消耗 once，允许稍后绑定。
func (a *controlRPCConnectionAuth) notifyAuthenticated() {
	a.mu.Lock()
	fn := a.onAuth
	a.mu.Unlock()
	if fn == nil {
		return
	}
	a.notifyOnce.Do(fn)
}

// isAuthenticated 读取当前连接认证状态。
func (a *controlRPCConnectionAuth) isAuthenticated() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authenticated
}

// ensureControlRPCSessionToken 复用继承令牌，缺失时生成并发布给本地 sidecar。
func ensureControlRPCSessionToken() (string, error) {
	if token := controlRPCSessionTokenFromEnv(); token != "" {
		return token, nil
	}
	token, err := newControlRPCSessionToken()
	if err != nil {
		return "", err
	}
	if err := os.Setenv(controlRPCSessionTokenEnv, token); err != nil {
		return "", ErrInvalidState(fmt.Sprintf("set %s: %v", controlRPCSessionTokenEnv, err))
	}
	return token, nil
}

// controlRPCSessionTokenFromEnv 按新旧环境变量顺序读取控制 RPC 会话令牌。
func controlRPCSessionTokenFromEnv() string {
	for _, key := range []string{controlRPCSessionTokenEnv, legacyControlRPCSessionTokenEnv} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	return ""
}

// newControlRPCSessionToken 生成带前缀的随机会话令牌。
func newControlRPCSessionToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", ErrInvalidState(fmt.Sprintf("generate control rpc session token: %v", err))
	}
	return "sd-" + hex.EncodeToString(raw[:]), nil
}

// setControlRPCAuthToken 写入服务端当前控制 RPC 认证令牌。
func (s *Server) setControlRPCAuthToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authToken = strings.TrimSpace(token)
}

// controlRPCAuthToken 读取服务端当前控制 RPC 认证令牌。
func (s *Server) controlRPCAuthToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authToken
}
