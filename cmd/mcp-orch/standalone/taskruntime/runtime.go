// Package taskruntime bundles the standalone task-only HTTP runtime.
//
// The owner is deliberately local and non-authoritative: it owns only an
// in-memory credential registry and a task-scoped HTTP listener. It does not
// open storage, launch provider processes, or integrate with managed CLI
// runtime; taskhttp freezes the explicitly supplied tool definitions once.
package taskruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskcredential"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskhttp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

var (
	// ErrConfigInvalid indicates missing or malformed runtime construction input.
	ErrConfigInvalid = errors.New("task runtime config is invalid")
	// ErrNotStarted indicates that credential issuance preceded listener startup.
	ErrNotStarted = errors.New("task runtime is not started")
	// ErrAlreadyStarted indicates that Start was called more than once.
	ErrAlreadyStarted = errors.New("task runtime is already started")
	// ErrClosed indicates that the owner has been closed and can no longer serve.
	ErrClosed = errors.New("task runtime is closed")
)

// Config is the complete explicit construction input for a task runtime.
type Config struct {
	WorkspaceRoot    string
	BaseToolProvider common.ToolProvider
	AllowedTools     []string
	Logger           *pkglogger.Runtime
	IdentityResolver taskprincipal.IdentityChainReader
	Name             string
	Version          string
}

// Endpoint identifies the task-only HTTP endpoint. It intentionally has no
// bearer/token field; credentials are returned only through IssuedCredential.
type Endpoint struct {
	URL        string `json:"url"`
	InstanceID string `json:"instance_id"`
}

// String renders only the public endpoint URL and never credential material.
// String 仅返回公开 endpoint URL，绝不携带 credential 材料。
func (e Endpoint) String() string { return e.URL }

// MarshalJSON keeps the public endpoint shape explicit and token-free.
// MarshalJSON 固定 endpoint 的无 token JSON 形状，防止凭据字段漂移。
func (e Endpoint) MarshalJSON() ([]byte, error) {
	type endpointJSON struct {
		URL        string `json:"url"`
		InstanceID string `json:"instance_id"`
	}
	return json.Marshal(endpointJSON{URL: e.URL, InstanceID: e.InstanceID})
}

// TaskSessionIdentity is the identity pair accepted by Activate.
type TaskSessionIdentity = common.TaskSessionIdentity

// TaskRuntimeOwner is the local, non-authoritative task runtime owner.
type TaskRuntimeOwner struct {
	mu       sync.Mutex
	server   *taskhttp.Server
	registry *taskcredential.Registry

	started  bool
	closed   bool
	endpoint Endpoint
	// listener is the sole listener owned by this runtime after Start succeeds.
	// Keeping the identity lets a racing duplicate Start avoid closing the
	// already-owned listener while still closing a fresh rejected listener.
	listener  net.Listener
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	serveErr  chan error
	closeDone chan struct{}
}

// Owner is retained as a concise alias for TaskRuntimeOwner.
type Owner = TaskRuntimeOwner

var _ interface{ Close() error } = (*TaskRuntimeOwner)(nil)

// New constructs all task-only dependencies. Identity resolution is strictly
// through taskprincipal and current-identity verification; no default tools
// or operator authentication are introduced.
// New 构造 task-only 全部依赖；身份解析只经 taskprincipal/current 校验，拒绝默认工具和 operator 认证。
func New(config Config) (*TaskRuntimeOwner, error) {
	resolver, err := taskprincipal.New(config.IdentityResolver)
	if err != nil {
		return nil, fmt.Errorf("construct task principal resolver: %w", err)
	}
	verifier, err := taskcredential.NewResolverCurrentIdentityVerifier(resolver)
	if err != nil {
		return nil, fmt.Errorf("construct task identity verifier: %w", err)
	}
	registry, err := taskcredential.New(verifier)
	if err != nil {
		return nil, fmt.Errorf("construct task credential registry: %w", err)
	}
	allowedTools := config.AllowedTools
	if allowedTools != nil {
		allowedTools = append([]string{}, allowedTools...)
	}
	server, err := taskhttp.NewServer(context.Background(), config.BaseToolProvider, taskhttp.ServerConfig{
		WorkspaceRoot: config.WorkspaceRoot,
		Authenticator: registry,
		Name:          config.Name,
		Version:       config.Version,
		AllowedTools:  allowedTools,
		Logger:        config.Logger,
	})
	if err != nil {
		_ = registry.Close()
		return nil, fmt.Errorf("construct task HTTP server: %w", err)
	}
	return &TaskRuntimeOwner{server: server, registry: registry, closeDone: make(chan struct{})}, nil
}

// NewTaskRuntimeOwner is the descriptive constructor alias for New.
// NewTaskRuntimeOwner 是 New 的语义化构造别名。
func NewTaskRuntimeOwner(config Config) (*TaskRuntimeOwner, error) { return New(config) }

// Start binds a loopback listener and starts the bounded task HTTP server.
// The optional argument is either an int port (0 means an ephemeral port) or
// an already-bound net.Listener. A listener must be TCP on 127.0.0.1.
// Start 绑定 loopback listener 并启动有界 task HTTP 服务；重复启动不会破坏已拥有的 listener。
func (o *TaskRuntimeOwner) Start(ctx context.Context, target ...any) (Endpoint, error) {
	if o == nil {
		return Endpoint{}, ErrConfigInvalid
	}
	if err := validateStartContext(ctx); err != nil {
		return Endpoint{}, err
	}
	listener, err := resolveListener(target)
	if err != nil {
		return Endpoint{}, err
	}
	if err := validateLoopbackListener(listener); err != nil {
		_ = listener.Close()
		return Endpoint{}, err
	}

	return o.startResolved(ctx, listener)
}

func validateStartContext(ctx context.Context) error {
	if ctx == nil {
		return ErrConfigInvalid
	}
	return ctx.Err()
}

// startResolved 在线性化 owner 状态后启动唯一 listener worker，并在所有失败分支关闭候选 listener。
func (o *TaskRuntimeOwner) startResolved(ctx context.Context, listener net.Listener) (Endpoint, error) {
	o.mu.Lock()
	if o.server == nil || o.registry == nil {
		o.mu.Unlock()
		_ = listener.Close()
		return Endpoint{}, ErrConfigInvalid
	}
	if o.closed {
		o.mu.Unlock()
		_ = listener.Close()
		return Endpoint{}, ErrClosed
	}
	if o.started {
		owned := sameListener(listener, o.listener)
		o.mu.Unlock()
		if !owned {
			_ = listener.Close()
		}
		return Endpoint{}, ErrAlreadyStarted
	}
	instanceID, err := newInstanceID()
	if err != nil {
		o.mu.Unlock()
		_ = listener.Close()
		return Endpoint{}, err
	}
	address := listener.Addr().(*net.TCPAddr)
	endpoint := Endpoint{URL: (&url.URL{Scheme: "http", Host: address.String(), Path: "/mcp"}).String(), InstanceID: instanceID}
	serveCtx, cancel := context.WithCancel(ctx)
	o.started, o.endpoint, o.listener, o.cancel = true, endpoint, listener, cancel
	o.serveErr = make(chan error, 1)
	server, serveErr := o.server, o.serveErr
	o.workers.Go(func() {
		err := server.ServeLocal(serveCtx, listener)
		serveErr <- err
	})
	o.mu.Unlock()
	return endpoint, nil
}

// IssuePending issues a one-shot pending credential and returns this runtime's
// endpoint URL. Pending credentials cannot authenticate until Activate succeeds.
// IssuePending 签发一次性 pending credential 并返回本 runtime endpoint；激活前不能认证。
func (o *TaskRuntimeOwner) IssuePending(ctx context.Context, leaseID string, expiresAt time.Time) (*taskcredential.IssuedCredential, string, error) {
	if o == nil {
		return nil, "", ErrConfigInvalid
	}
	o.mu.Lock()
	if o.registry == nil {
		o.mu.Unlock()
		return nil, "", ErrConfigInvalid
	}
	if o.closed {
		o.mu.Unlock()
		return nil, "", ErrClosed
	}
	if !o.started {
		o.mu.Unlock()
		return nil, "", ErrNotStarted
	}
	endpointURL := o.endpoint.URL
	registry := o.registry
	o.mu.Unlock()
	credential, err := registry.IssuePending(ctx, leaseID, expiresAt)
	if err != nil {
		return nil, "", err
	}
	return credential, endpointURL, nil
}

// Activate delegates the pending-to-active transition to the credential owner.
// Activate 将 pending 到 active 的迁移委托给唯一 credential owner，并受 runtime 生命周期约束。
func (o *TaskRuntimeOwner) Activate(ctx context.Context, leaseID string, identity TaskSessionIdentity) error {
	if o == nil {
		return ErrConfigInvalid
	}
	o.mu.Lock()
	if o.registry == nil {
		o.mu.Unlock()
		return ErrConfigInvalid
	}
	if o.closed {
		o.mu.Unlock()
		return ErrClosed
	}
	if !o.started {
		o.mu.Unlock()
		return ErrNotStarted
	}
	registry := o.registry
	o.mu.Unlock()
	return registry.Activate(ctx, leaseID, identity)
}

// Revoke delegates credential revocation to the registry.
// Revoke 委托 registry 撤销 credential；未启动或已关闭的 runtime 直接拒绝。
func (o *TaskRuntimeOwner) Revoke(ctx context.Context, leaseID string) error {
	if o == nil {
		return ErrConfigInvalid
	}
	o.mu.Lock()
	if o.registry == nil {
		o.mu.Unlock()
		return ErrConfigInvalid
	}
	if o.closed {
		o.mu.Unlock()
		return ErrClosed
	}
	if !o.started {
		o.mu.Unlock()
		return ErrNotStarted
	}
	registry := o.registry
	o.mu.Unlock()
	return registry.Revoke(ctx, leaseID)
}

// Close cancels the HTTP lifecycle and revokes all credentials. It is idempotent.
// Close 取消 HTTP 生命周期并撤销全部 credential；重复调用保持幂等且等待首次关闭完成。
func (o *TaskRuntimeOwner) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if o.closed {
		closeDone := o.closeDone
		o.mu.Unlock()
		if closeDone != nil {
			<-closeDone
		}
		return nil
	}
	o.closed = true
	cancel, serveErr, registry := o.cancel, o.serveErr, o.registry
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	o.workers.Wait()
	var serving error
	if serveErr != nil {
		serving = <-serveErr
		if errors.Is(serving, net.ErrClosed) {
			serving = nil
		}
	}
	err := errors.Join(registry.Close(), serving)
	o.mu.Lock()
	if o.closeDone != nil {
		close(o.closeDone)
		o.closeDone = nil
	}
	o.mu.Unlock()
	return err
}

// resolveListener 将显式端口或已绑定 listener 解析为单一 TCP 监听对象。
func resolveListener(target []any) (net.Listener, error) {
	if len(target) > 1 {
		return nil, fmt.Errorf("%w: one port or listener is required", ErrConfigInvalid)
	}
	if len(target) == 1 {
		switch value := target[0].(type) {
		case net.Listener:
			if interfaceIsNil(value) {
				return nil, fmt.Errorf("%w: listener is nil", ErrConfigInvalid)
			}
			return value, nil
		case int:
			if value < 0 || value > 65535 {
				return nil, fmt.Errorf("%w: port is invalid", ErrConfigInvalid)
			}
			return net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(value))
		default:
			return nil, fmt.Errorf("%w: target must be port or listener", ErrConfigInvalid)
		}
	}
	return net.Listen("tcp4", "127.0.0.1:0")
}

func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// sameListener compares listener identity without assuming that every custom
// net.Listener implementation is comparable. Production listeners are
// pointers, while tests may provide value implementations with slice/map
// fields; a non-comparable value can never be the owner identity match.
func sameListener(left, right net.Listener) bool {
	if interfaceIsNil(left) || interfaceIsNil(right) {
		return false
	}
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() || !leftValue.Type().Comparable() {
		return false
	}
	return leftValue.Interface() == rightValue.Interface()
}

// validateLoopbackListener 强制 listener 为 127.0.0.1 的有效 TCP 端点，拒绝外部暴露。
func validateLoopbackListener(listener net.Listener) error {
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address == nil || address.IP == nil || !address.IP.Equal(net.IPv4(127, 0, 0, 1)) || address.Port < 0 || address.Port > 65535 {
		return errors.New("task runtime listener must be bound to 127.0.0.1")
	}
	return nil
}

func newInstanceID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate task runtime instance id: %w", err)
	}
	return "task-" + strings.ToLower(hex.EncodeToString(raw[:])), nil
}
