package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

type serviceThreadListerAdapter struct {
	svc Service
}

// NewThreadLister returns a contract.ThreadLister backed by the given Service.
// Returns nil when svc is nil so callers can safely wire optional deps.
// NewThreadLister 创建线程lister。
func NewThreadLister(svc Service) contract.ThreadLister {
	if svc == nil {
		return nil
	}
	return &serviceThreadListerAdapter{svc: svc}
}

// List 列出线程。
func (a *serviceThreadListerAdapter) List(ctx context.Context) ([]contract.ThreadRef, error) {
	refs, err := a.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contract.ThreadRef, len(refs))
	for i, r := range refs {
		out[i] = contract.ThreadRef{
			ID:        r.ID,
			Name:      r.Name,
			AgentID:   r.AgentID,
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

type serviceConfigReaderAdapter struct {
	svc Service
}

// NewThreadConfigReader returns a contract.ThreadConfigReader backed by the
// given Service. The returned adapter also satisfies
// contract.ThreadRuntimeConfigReader. Returns nil when svc is nil.
// NewThreadConfigReader 创建线程配置读取器。
func NewThreadConfigReader(svc Service) contract.ThreadConfigReader {
	if svc == nil {
		return nil
	}
	return &serviceConfigReaderAdapter{svc: svc}
}

// NewThreadRuntimeConfigReader returns a contract.ThreadRuntimeConfigReader
// backed by the same adapter as NewThreadConfigReader.
// NewThreadRuntimeConfigReader 创建线程运行时配置读取器。
func NewThreadRuntimeConfigReader(svc Service) contract.ThreadRuntimeConfigReader {
	if svc == nil {
		return nil
	}
	return &serviceConfigReaderAdapter{svc: svc}
}

// GetConfig 读取配置。
func (a *serviceConfigReaderAdapter) GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error) {
	return a.svc.GetConfig(ctx, threadID)
}

// ReadRuntimeConfig 读取运行时配置。
func (a *serviceConfigReaderAdapter) ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	type runtimeReader interface {
		ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
	}
	if reader, ok := a.svc.(runtimeReader); ok {
		return reader.ReadRuntimeConfig(ctx, threadID)
	}
	return nil, nil
}

// ReadRuntimeConfigs 读取运行时配置。
func (a *serviceConfigReaderAdapter) ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error) {
	type runtimeReader interface {
		ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error)
	}
	if reader, ok := a.svc.(runtimeReader); ok {
		return reader.ReadRuntimeConfigs(ctx, threadIDs)
	}
	return nil, nil
}

// ReadThreadStateRuntimeConfig 读取线程状态运行时配置。
func (a *serviceConfigReaderAdapter) ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	type runtimeReader interface {
		ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
	}
	if reader, ok := a.svc.(runtimeReader); ok {
		return reader.ReadThreadStateRuntimeConfig(ctx, threadID)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// CronStarterAdapter (was cron_adapter.go)
// ---------------------------------------------------------------------------

// CronStarterAdapter wraps the full thread.Service into the narrow
// contract.CronThreadStarter interface consumed by the cron module.
type CronStarterAdapter struct {
	svc Service
}

// NewCronStarterAdapter creates an adapter. svc must not be nil.
// NewCronStarterAdapter 创建cronstarter适配器。
func NewCronStarterAdapter(svc Service) *CronStarterAdapter {
	return &CronStarterAdapter{svc: svc}
}

var _ contract.CronThreadStarter = (*CronStarterAdapter)(nil)

// CronStartThread translates the narrow cron request into a full
// thread.StartRequest, delegates to Service.Start, and projects the
// result back into the narrow cron result.
// CronStartThread 处理cron起点线程。
func (a *CronStarterAdapter) CronStartThread(ctx context.Context, req contract.CronStartThreadRequest) (contract.CronStartThreadResult, error) {
	res, err := a.svc.Start(ctx, StartRequest{
		Provider: req.Provider,
		CWD:      req.CWD,
		Model:    req.Model,
		Name:     req.Name,
		Config:   req.Config,
	})
	if err != nil {
		return contract.CronStartThreadResult{}, err
	}
	return contract.CronStartThreadResult{
		ThreadID: res.ThreadID,
		AgentID:  res.AgentID,
	}, nil
}

type providerThreadNameSetter interface {
	SetThreadName(ctx context.Context, threadID, name string) error
}

// syncProviderThreadName 把本地线程名同步到当前活跃的 provider session。
// 只有 provider 支持重命名且 binding 能定位目标线程时才会调用远端，失败会阻断本次改名。
func (s *service) syncProviderThreadName(ctx context.Context, threadID, agentID, name string) error {
	session, active, err := s.activeProviderThreadNameSession(agentID)
	if err != nil || !active {
		return err
	}
	syncer, ok := session.(providerThreadNameSetter)
	if !ok {
		return nil
	}
	targetID, err := s.providerThreadNameTargetID(ctx, threadID, agentID)
	if err != nil {
		return err
	}
	if err := syncer.SetThreadName(ctx, targetID, name); err != nil {
		return fmt.Errorf("thread/name/set: provider rename failed: %w", err)
	}
	return nil
}

// activeProviderThreadNameSession 查找 agent 当前绑定的 provider session。
// 会区分“没有活跃 session”和真正的 session 管理器错误，避免把后台未启动状态当成失败。
func (s *service) activeProviderThreadNameSession(agentID string) (contract.Session, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.sessions == nil || agentID == "" {
		return nil, false, nil
	}
	session, err := s.sessions.GetSession(agentID)
	switch {
	case err == nil && session != nil:
		return session, true, nil
	case err == nil:
		return nil, false, nil
	case errors.Is(err, contract.ErrSessionNotFound):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("thread/name/set: provider session lookup failed: %w", err)
	}
}

func (s *service) providerThreadNameTargetID(ctx context.Context, threadID, agentID string) (string, error) {
	binding, err := s.providerThreadNameBinding(ctx, agentID)
	if err != nil {
		return "", err
	}
	return historyTargetID(binding, threadID), nil
}

func (s *service) providerThreadNameBinding(ctx context.Context, agentID string) (*bindingstore.Binding, error) {
	if s == nil || s.bindingStore == nil {
		return nil, errors.New("thread/name/set: binding store is not configured")
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("thread/name/set: provider binding lookup failed: %w", err)
	}
	return binding, nil
}
