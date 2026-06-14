package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
