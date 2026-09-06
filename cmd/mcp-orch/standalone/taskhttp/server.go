package taskhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// ErrServerConfigInvalid 表示 task-only HTTP server 缺少显式依赖或配置。
var ErrServerConfigInvalid = errors.New("task HTTP server config is invalid")

// ErrListenerInvalid 表示 task-only server 收到了非 loopback TCP listener。
var ErrListenerInvalid = errors.New("task HTTP listener is invalid")

// ServerConfig 是 task-only HTTP server 的完整显式构造配置。
type ServerConfig struct {
	WorkspaceRoot string
	Authenticator common.TaskSessionAuthenticator
	Name          string
	Version       string
	AllowedTools  []string
	Logger        *pkglogger.Runtime
}

// Server 只暴露由调用方 listener 驱动的 task HTTP 阻塞生命周期。
type Server struct {
	local *common.HTTPServer
}

// NewServer 构造 task session 认证模式的本地 MCP HTTP server，不注册任何隐式工具。
func NewServer(
	ctx context.Context,
	base common.ToolProvider,
	config ServerConfig,
) (*Server, error) {
	if err := validateServerConfig(config); err != nil {
		return nil, err
	}
	exact, err := NewExactToolProvider(ctx, base, config.AllowedTools)
	if err != nil {
		return nil, err
	}
	local, err := common.NewLocalHTTPServer(
		config.Name,
		config.Version,
		exact,
		common.LocalHTTPProfile{
			WorkspaceRoot:            config.WorkspaceRoot,
			TaskSessionAuthenticator: config.Authenticator,
		},
		config.Logger,
	)
	if err != nil {
		return nil, fmt.Errorf("construct task HTTP server: %w", err)
	}
	return &Server{local: local}, nil
}

// ServeLocal 在调用方已绑定的 listener 上阻塞运行，ctx 取消后等待 HTTP 生命周期退出。
func (s *Server) ServeLocal(ctx context.Context, listener net.Listener) error {
	if s == nil || s.local == nil {
		return errors.New("task HTTP server is required")
	}
	if ctx == nil {
		return errors.New("task HTTP serve context is required")
	}
	if interfaceIsNil(listener) {
		return errors.New("task HTTP listener is required")
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address == nil || address.IP == nil || !address.IP.IsLoopback() {
		return fmt.Errorf("%w: loopback TCP is required", ErrListenerInvalid)
	}
	return s.local.ServeLocal(ctx, listener)
}

// validateServerConfig 拒绝隐式名称、日志 owner、认证方式或 allowlist。
func validateServerConfig(config ServerConfig) error {
	if strings.TrimSpace(config.WorkspaceRoot) != config.WorkspaceRoot || !filepath.IsAbs(config.WorkspaceRoot) {
		return fmt.Errorf("%w: workspace root must be a canonical absolute path", ErrServerConfigInvalid)
	}
	if strings.TrimSpace(config.Name) != config.Name || config.Name == "" {
		return fmt.Errorf("%w: name must be canonical nonempty text", ErrServerConfigInvalid)
	}
	if strings.TrimSpace(config.Version) != config.Version || config.Version == "" {
		return fmt.Errorf("%w: version must be canonical nonempty text", ErrServerConfigInvalid)
	}
	if interfaceIsNil(config.Authenticator) {
		return fmt.Errorf("%w: authenticator is required", ErrServerConfigInvalid)
	}
	if config.Logger == nil {
		return fmt.Errorf("%w: logger is required", ErrServerConfigInvalid)
	}
	if config.AllowedTools == nil {
		return fmt.Errorf("%w: allowed tools must be explicit", ErrServerConfigInvalid)
	}
	return nil
}
