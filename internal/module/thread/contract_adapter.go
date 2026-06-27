package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type serviceThreadListerAdapter struct {
	svc Service
}

// NewThreadLister 将 thread.Service 暴露为 contract.ThreadLister。
// svc 为空时返回 nil，允许上层按可选依赖装配而不制造空 adapter。
func NewThreadLister(svc Service) contract.ThreadLister {
	if svc == nil {
		return nil
	}
	return &serviceThreadListerAdapter{svc: svc}
}

// List 将 thread.Ref 投影为跨模块 contract.ThreadRef。
// adapter 不回查额外状态，避免 prompt/cron 等调用方触发 thread 模块的副作用。
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

// NewThreadConfigReader 将 thread.Service 暴露为线程配置读取器。
// 同一个 adapter 也实现 runtime config 读取接口；svc 为空时返回 nil 以支持可选装配。
func NewThreadConfigReader(svc Service) contract.ThreadConfigReader {
	if svc == nil {
		return nil
	}
	return &serviceConfigReaderAdapter{svc: svc}
}

// NewThreadRuntimeConfigReader 返回与 NewThreadConfigReader 相同的 runtime config adapter。
// 它只读 thread 模块状态，不负责启动或恢复 provider session。
func NewThreadRuntimeConfigReader(svc Service) contract.ThreadRuntimeConfigReader {
	if svc == nil {
		return nil
	}
	return &serviceConfigReaderAdapter{svc: svc}
}

// GetConfig 透传 Service.GetConfig，用于跨模块读取线程可见配置。
func (a *serviceConfigReaderAdapter) GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error) {
	return a.svc.GetConfig(ctx, threadID)
}

// ReadRuntimeConfig 读取单个线程的 runtime config。
// 底层 Service 未实现该窄接口时返回 nil，表示装配中没有该能力而不是空配置持久化。
func (a *serviceConfigReaderAdapter) ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	type runtimeReader interface {
		ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
	}
	if reader, ok := a.svc.(runtimeReader); ok {
		return reader.ReadRuntimeConfig(ctx, threadID)
	}
	return nil, nil
}

// ReadRuntimeConfigs 批量读取线程 runtime config。
// Service 不支持批量接口时返回 nil，调用方需要按能力存在与否选择路径。
func (a *serviceConfigReaderAdapter) ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error) {
	type runtimeReader interface {
		ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error)
	}
	if reader, ok := a.svc.(runtimeReader); ok {
		return reader.ReadRuntimeConfigs(ctx, threadIDs)
	}
	return nil, nil
}

// ReadThreadStateRuntimeConfig 只读取 thread store 中的离线 runtime 状态。
// 它不会访问 provider session，适合在 prompt 构建等只需要持久化边界的路径调用。
func (a *serviceConfigReaderAdapter) ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	type runtimeReader interface {
		ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
	}
	if reader, ok := a.svc.(runtimeReader); ok {
		return reader.ReadThreadStateRuntimeConfig(ctx, threadID)
	}
	return nil, nil
}

// CronStarterAdapter 将完整 thread.Service 收窄为 cron 模块需要的启动接口。
// 该 adapter 是跨模块边界，避免 cron 直接依赖 thread 的完整生命周期 API。
type CronStarterAdapter struct {
	svc Service
}

// NewCronStarterAdapter 构造 cron 启动 adapter。
// 调用方必须传入非 nil service；这里不做兜底，错误应在装配阶段暴露。
func NewCronStarterAdapter(svc Service) *CronStarterAdapter {
	return &CronStarterAdapter{svc: svc}
}

var _ contract.CronThreadStarter = (*CronStarterAdapter)(nil)

// CronStartThread 将 cron 的窄启动请求转换为 StartRequest。
// 返回值只暴露 cron 后续追踪需要的 thread/agent 身份，避免泄露 thread 模块内部响应结构。
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
	binding, err := s.providerThreadNameBindingRecord(ctx, agentID)
	if err != nil {
		return "", err
	}
	return historyTargetIDRecord(binding, threadID), nil
}

func (s *service) providerThreadNameBindingRecord(ctx context.Context, agentID string) (*threadBindingRecord, error) {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil, errors.New("thread/name/set: binding store is not configured")
	}
	binding, err := store.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("thread/name/set: provider binding lookup failed: %w", err)
	}
	return binding, nil
}
