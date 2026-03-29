package unified

import (
	"context"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type Client struct {
	registry *Registry
	sessions *SessionManager
	logger   *slog.Logger
}

func NewClient(registry *Registry, sessions *SessionManager, logger *slog.Logger) *Client {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Client{
		registry: registry,
		sessions: sessions,
		logger:   logger,
	}
}

func (c *Client) StartSession(
	ctx context.Context,
	req dto.StartSessionRequest,
) (contract.Session, error) {
	return c.open(ctx, "starting", req.Provider, req.AgentID, func(driver contract.Driver) (contract.Session, error) {
		return driver.StartSession(ctx, req)
	})
}

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
	session, err := run(driver)
	if err != nil {
		return nil, err
	}
	if c.sessions != nil {
		c.sessions.Register(agentID, session)
	}
	return session, nil
}
