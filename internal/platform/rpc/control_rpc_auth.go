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

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

const (
	controlRPCSessionTokenEnv       = "GO_AGENT_CTL_SESSION_TOKEN"
	legacyControlRPCSessionTokenEnv = "GO_AGENT_MCP_SESSION_TOKEN"
)

type controlRPCAuthAssigner struct {
	base jrpc2.Assigner
	auth *controlRPCConnectionAuth
}

// Assign wraps TCP control-plane handlers with the per-connection registration gate.
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

func (a controlRPCAuthAssigner) Names() []string {
	namer, ok := a.base.(jrpc2.Namer)
	if !ok {
		return nil
	}
	return namer.Names()
}

type controlRPCConnectionAuth struct {
	expected string

	mu            sync.Mutex
	authenticated bool
}

func newControlRPCConnectionAuth(expected string) *controlRPCConnectionAuth {
	return &controlRPCConnectionAuth{expected: strings.TrimSpace(expected)}
}

// authorize requires ctl/register with the shared session token before other TCP RPC methods run.
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

func (a *controlRPCConnectionAuth) markAuthenticated() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authenticated = true
}

func (a *controlRPCConnectionAuth) isAuthenticated() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authenticated
}

// ensureControlRPCSessionToken preserves an inherited token or publishes a new one for local sidecars.
func ensureControlRPCSessionToken() (string, error) {
	if token := controlRPCSessionTokenFromEnv(); token != "" {
		return token, nil
	}
	token, err := newControlRPCSessionToken()
	if err != nil {
		return "", err
	}
	if err := os.Setenv(controlRPCSessionTokenEnv, token); err != nil {
		return "", fmt.Errorf("set %s: %w", controlRPCSessionTokenEnv, err)
	}
	return token, nil
}

func controlRPCSessionTokenFromEnv() string {
	for _, key := range []string{controlRPCSessionTokenEnv, legacyControlRPCSessionTokenEnv} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	return ""
}

func newControlRPCSessionToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate control rpc session token: %w", err)
	}
	return "sd-" + hex.EncodeToString(raw[:]), nil
}

func (s *Server) setControlRPCAuthToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authToken = strings.TrimSpace(token)
}

func (s *Server) controlRPCAuthToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authToken
}
