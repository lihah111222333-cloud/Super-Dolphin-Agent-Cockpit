package unified

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type Client struct {
	registry *Registry
	sessions *SessionManager
	logger   *slog.Logger
	tracer   *observability.Service
}

// NewClient 创建统一 provider client。
// registry 负责 provider 解析，sessions 负责会话登记；tracer 使用默认 nil 配置。
func NewClient(registry *Registry, sessions *SessionManager, logger *slog.Logger) *Client {
	return newClient(registry, sessions, logger, nil)
}

func newClient(registry *Registry, sessions *SessionManager, logger *slog.Logger, tracer *observability.Service) *Client {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Client{registry: registry, sessions: sessions, logger: logger, tracer: tracer}
}

// StartSession 解析 provider driver 并启动新会话。
// 成功后会包装 session、记录 trace，并按 agentID 注册到 SessionManager。
func (c *Client) StartSession(
	ctx context.Context,
	req dto.StartSessionRequest,
) (contract.Session, error) {
	return c.open(ctx, "starting", req.Provider, req.AgentID, func(driver contract.Driver) (contract.Session, error) {
		return driver.StartSession(ctx, req)
	})
}

// ResumeSession 解析 provider driver 并恢复既有会话。
// 恢复路径与新建路径共用 open，确保日志、trace 和会话登记行为一致。
func (c *Client) ResumeSession(
	ctx context.Context,
	req dto.ResumeSessionRequest,
) (contract.Session, error) {
	return c.open(ctx, "resuming", req.Provider, req.AgentID, func(driver contract.Driver) (contract.Session, error) {
		return driver.ResumeSession(ctx, req)
	})
}

func (c *Client) open(
	ctx context.Context,
	action string,
	provider string,
	agentID string,
	run func(contract.Driver) (contract.Session, error),
) (contract.Session, error) {
	driver, err := c.registry.Resolve(provider)
	if err != nil {
		return nil, err
	}
	c.logger.Info(action+" session", "provider", strings.TrimSpace(provider), "agent_id", strings.TrimSpace(agentID))
	started := time.Now()
	session, err := run(driver)
	c.recordProviderTrace(ctx, providerSessionEvent("provider.session.acquire", provider, agentID, "", time.Since(started), err))
	if err != nil {
		return nil, err
	}
	session = c.wrapSession(provider, session)
	c.recordProviderTrace(ctx, providerSessionEvent("provider.session.ready", provider, agentID, session.ThreadID(), time.Since(started), nil))
	if c.sessions != nil {
		c.sessions.Register(agentID, session)
	}
	return session, nil
}
