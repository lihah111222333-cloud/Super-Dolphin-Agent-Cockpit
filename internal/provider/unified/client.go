package unified

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Client is the provider-agnostic session facade used by application modules.
type Client struct {
	registry *Registry
	sessions *SessionManager
	logger   *pkglogger.Logger
	tracer   *observability.Service
}

// NewClient 创建客户端。
func NewClient(registry *Registry, sessions *SessionManager, logger *pkglogger.Logger) *Client {
	return newClient(registry, sessions, logger, nil)
}

func newClient(registry *Registry, sessions *SessionManager, logger *pkglogger.Logger, tracer *observability.Service) *Client {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Client{registry: registry, sessions: sessions, logger: logger, tracer: tracer}
}

// StartSession 启动会话。
func (c *Client) StartSession(
	ctx context.Context,
	req dto.StartSessionRequest,
) (contract.Session, error) {
	return c.open(ctx, "starting", req.Provider, req.AgentID, func(driver contract.Driver) (contract.Session, error) {
		return driver.StartSession(ctx, req)
	})
}

// ResumeSession 处理恢复会话。
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
