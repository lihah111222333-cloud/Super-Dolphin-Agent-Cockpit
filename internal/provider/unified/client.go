package unified

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Client 是 provider 统一启动和恢复入口。
// 它通过 Registry 解析具体 driver，并在成功后把 session 注册到 SessionManager。
type Client struct {
	registry         *Registry
	sessions         *SessionManager
	logger           *slog.Logger
	tracer           *observability.Service
	traceSpanCounter providershared.TraceSpanCounter
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
	return c.open(ctx, "starting", req.Provider, req.AgentID, "", false, func(driver contract.Driver) (contract.Session, error) {
		return driver.StartSession(ctx, req)
	})
}

// ResumeSession 解析 provider driver 并恢复既有会话。
// 恢复路径与新建路径共用 open，确保日志、trace 和会话登记行为一致。
func (c *Client) ResumeSession(
	ctx context.Context,
	req dto.ResumeSessionRequest,
) (contract.Session, error) {
	resumeCtx := context.WithoutCancel(ctx)
	return c.open(ctx, "resuming", req.Provider, req.AgentID, resumeCoordinationIdentity(req.Provider, req.ProviderThreadID), true, func(driver contract.Driver) (contract.Session, error) {
		return driver.ResumeSession(resumeCtx, req)
	})
}

// open 统一 provider 新建和恢复会话的 driver 解析、日志和 pending 登记。
// pending=true 时会话必须等上层持久化成功后才对外可见。
func (c *Client) open(
	ctx context.Context,
	action string,
	provider string,
	agentID string,
	resumeIdentity string,
	pending bool,
	run func(contract.Driver) (contract.Session, error),
) (contract.Session, error) {
	driver, err := c.registry.Resolve(provider)
	if err != nil {
		return nil, err
	}
	runOpen := func() (contract.Session, error) {
		return c.runOpen(ctx, action, provider, agentID, driver, run)
	}
	if pending && c.sessions != nil {
		return c.sessions.resumeSession(ctx, agentID, resumeIdentity, true, runOpen)
	}
	session, err := runOpen()
	if err != nil {
		return nil, err
	}
	if c.sessions != nil {
		c.sessions.Register(agentID, session)
	}
	return session, nil
}

// runOpen 执行一次 provider session acquire，并在成功后统一包装 trace session。
func (c *Client) runOpen(
	ctx context.Context,
	action string,
	provider string,
	agentID string,
	driver contract.Driver,
	run func(contract.Driver) (contract.Session, error),
) (contract.Session, error) {
	c.logger.Info(action+" session", "provider", strings.TrimSpace(provider), "agent_id", strings.TrimSpace(agentID))
	started := time.Now()
	session, err := run(driver)
	c.recordProviderTrace(ctx, providerSessionEvent("provider.session.acquire", provider, agentID, "", time.Since(started), err))
	if err != nil {
		return nil, err
	}
	session = c.wrapSession(provider, session)
	c.recordProviderTrace(ctx, providerSessionEvent("provider.session.ready", provider, agentID, session.ThreadID(), time.Since(started), nil))
	return session, nil
}
