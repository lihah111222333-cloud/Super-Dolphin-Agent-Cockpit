package thread

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
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

type sessionLifecyclePort struct {
	service Service
}

// NewSessionLifecyclePort 将 thread.Service 收窄为 session lifecycle 端口。
// 该 adapter 只做字段映射和可变输入复制，真实启动、恢复和 fork 仍由 thread service 负责。
func NewSessionLifecyclePort(service Service) contract.SessionLifecyclePort {
	return sessionLifecyclePort{service: service}
}

// StartSession 将 contract 启动 DTO 转为 thread.StartRequest，并把 thread 结果投影回 session port。
func (p sessionLifecyclePort) StartSession(ctx context.Context, req contract.SessionStartRequest) (contract.SessionStartResult, error) {
	got, err := p.service.Start(ctx, startRequestFromSession(req))
	if err != nil {
		return contract.SessionStartResult{}, err
	}
	return sessionStartResultFromStart(got), nil
}

// ResumeSession 恢复指定 thread 对应的 provider session，空 threadID 会立即报错。
// RPC 兼容字段会原样传给 Service.Resume，避免 port 迁移丢掉覆盖项。
func (p sessionLifecyclePort) ResumeSession(ctx context.Context, req contract.SessionResumeRequest) (contract.SessionStartResult, error) {
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	if req.ThreadID == "" {
		return contract.SessionStartResult{}, fmt.Errorf("session lifecycle: thread id is required")
	}
	got, err := p.service.Resume(ctx, resumeRequestFromSession(req))
	if err != nil {
		return contract.SessionStartResult{}, err
	}
	return sessionStartResultFromResume(got), nil
}

// ForkSession 基于已有 thread 创建 fork，并保留 RPC 响应需要的 fork 元数据。
func (p sessionLifecyclePort) ForkSession(ctx context.Context, threadID string) (contract.SessionForkResult, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return contract.SessionForkResult{}, fmt.Errorf("session lifecycle: fork source thread id is required")
	}
	got, err := p.service.Fork(ctx, threadID)
	if err != nil {
		return contract.SessionForkResult{}, err
	}
	return sessionForkResultFromFork(got), nil
}

// ArchiveSession 归档指定 thread；空 threadID 视为调用方错误并阻断。
func (p sessionLifecyclePort) ArchiveSession(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return fmt.Errorf("session lifecycle: archive thread id is required")
	}
	return p.service.Archive(ctx, threadID)
}

type sessionStatusPort struct {
	service Service
}

type serviceSessionPorts struct {
	contract.SessionLifecyclePort
	contract.SessionStatusPort
}

// NewSessionPorts 将 thread.Service 聚合成跨模块 session 端口。
// 生产 RPC 入口应依赖该聚合端口逐步迁移，避免继续扩散完整 thread.Service。
func NewSessionPorts(service Service) contract.SessionPorts {
	return serviceSessionPorts{
		SessionLifecyclePort: NewSessionLifecyclePort(service),
		SessionStatusPort:    NewSessionStatusPort(service),
	}
}

var _ contract.SessionPorts = serviceSessionPorts{}

// NewSessionStatusPort 将 thread.Service 收窄为 session read/status 端口。
// adapter 只投影列表和消息读取结果，不改变 thread 模块的状态来源。
func NewSessionStatusPort(service Service) contract.SessionStatusPort {
	return sessionStatusPort{service: service}
}

// ListSessions 读取 thread 列表并投影为 session port 的稳定摘要字段。
func (p sessionStatusPort) ListSessions(ctx context.Context) ([]contract.SessionThreadSummary, error) {
	refs, err := p.service.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contract.SessionThreadSummary, 0, len(refs))
	for _, ref := range refs {
		out = append(out, sessionThreadRefFromThread(ref))
	}
	return out, nil
}

// ReadMessages 透传 thread 消息分页读取，保持现有消息 DTO 与分页语义不变。
func (p sessionStatusPort) ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return dto.ThreadMessagesResult{}, fmt.Errorf("session status: read messages thread id is required")
	}
	return p.service.ReadMessages(ctx, threadID, limit, before)
}

// startRequestFromSession 显式映射 session 启动字段，并深拷贝所有可变 slice/map。
// 这里是字段漂移守卫覆盖的核心路径，新增 StartRequest 字段必须同步映射或写明豁免。
func startRequestFromSession(req contract.SessionStartRequest) StartRequest {
	return StartRequest{
		Provider:                     req.Provider,
		AgentID:                      req.AgentID,
		ParentAgentID:                req.ParentAgentID,
		AgentType:                    req.AgentType,
		AgentMemoryScope:             req.AgentMemoryScope,
		CWD:                          req.CWD,
		Model:                        req.Model,
		ModelProvider:                req.ModelProvider,
		Name:                         req.Name,
		Prompt:                       req.Prompt,
		BaseInstructions:             req.BaseInstructions,
		BaseInstructionBlocks:        cloneSessionBaseInstructionBlocks(req.BaseInstructionBlocks),
		DeveloperInstructions:        req.DeveloperInstructions,
		ApprovalPolicy:               req.ApprovalPolicy,
		Sandbox:                      clone.RawMessage(req.Sandbox),
		Summary:                      req.Summary,
		Effort:                       req.Effort,
		Personality:                  req.Personality,
		Language:                     req.Language,
		GitRoot:                      req.GitRoot,
		IsWorktree:                   req.IsWorktree,
		ToolSurfaceMode:              req.ToolSurfaceMode,
		EnabledTools:                 clone.Strings(req.EnabledTools),
		AdditionalWorkingDirectories: clone.Strings(req.AdditionalWorkingDirectories),
		MCPSnapshot:                  cloneSessionMCPSnapshot(req.MCPSnapshot),
		SessionFlags:                 cloneSessionBoolMap(req.SessionFlags),
		Config:                       clone.RuntimeConfigMap(req.Config),
		LaunchSkillNames:             clone.Strings(req.LaunchSkillNames),
		LaunchSkillRefs:              append([]dto.SkillRef(nil), req.LaunchSkillRefs...),
		ForceLaunchSkills:            req.ForceLaunchSkills,
		AgentKey:                     req.AgentKey,
		PromptKey:                    req.PromptKey,
		OwnerThreadID:                req.OwnerThreadID,
		LaunchIntentID:               req.LaunchIntentID,
		DeferSpawn:                   req.DeferSpawn,
	}
}

func resumeRequestFromSession(req contract.SessionResumeRequest) ResumeRequest {
	return ResumeRequest{
		ThreadID: req.ThreadID,
		Path:     req.Path,
		CWD:      req.CWD,
		Model:    req.Model,
		Provider: req.Provider,
	}
}

func sessionStartResultFromStart(got StartResult) contract.SessionStartResult {
	return contract.SessionStartResult{
		ThreadID:        got.ThreadID,
		AgentID:         got.AgentID,
		SessionID:       got.SessionID,
		Status:          got.Status,
		Model:           got.Model,
		Provider:        got.Provider,
		ModelProvider:   got.ModelProvider,
		CWD:             got.CWD,
		ApprovalPolicy:  got.ApprovalPolicy,
		AgentKey:        got.AgentKey,
		AgentTitle:      got.AgentTitle,
		PromptKey:       got.PromptKey,
		PromptVersionID: got.PromptVersionID,
		PromptKeyStale:  got.PromptKeyStale,
		PendingLaunch:   got.PendingLaunch,
	}
}

func sessionStartResultFromResume(got ResumeResult) contract.SessionStartResult {
	return contract.SessionStartResult{
		ThreadID:  got.ThreadID,
		SessionID: got.SessionID,
		Status:    got.Status,
		Model:     got.Model,
		CWD:       got.CWD,
	}
}

func sessionForkResultFromFork(got ForkResult) contract.SessionForkResult {
	return contract.SessionForkResult{
		NewThreadID:  got.NewThreadID,
		ForkedFrom:   got.ForkedFrom,
		KickoffState: string(got.KickoffState),
	}
}

func sessionThreadRefFromThread(ref Ref) contract.SessionThreadSummary {
	return contract.SessionThreadSummary{
		ID:               ref.ID,
		Name:             ref.Name,
		AgentID:          ref.AgentID,
		Status:           ref.Status,
		CreatedAt:        ref.CreatedAt,
		UpdatedAt:        ref.UpdatedAt,
		Provider:         ref.Provider,
		ProviderThreadID: ref.ProviderThreadID,
		SessionID:        ref.SessionID,
		CWD:              ref.CWD,
		Model:            ref.Model,
		Port:             ref.Port,
	}
}

func cloneSessionBaseInstructionBlocks(in []contract.BaseInstructionBlock) []contract.BaseInstructionBlock {
	if len(in) == 0 {
		return nil
	}
	out := append([]contract.BaseInstructionBlock(nil), in...)
	for index := range out {
		out[index].EnableWhen = append([]byte(nil), out[index].EnableWhen...)
	}
	return out
}

func cloneSessionMCPSnapshot(in contract.MCPSnapshot) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:                  clone.Strings(in.Servers),
		Tools:                    clone.Strings(in.Tools),
		Instructions:             clone.StringMap(in.Instructions),
		ServerConfigs:            cloneSessionMCPServerConfigs(in.ServerConfigs),
		InstructionsDeltaEnabled: in.InstructionsDeltaEnabled,
		InstructionAttachments:   append([]contract.MCPAttachmentRef(nil), in.InstructionAttachments...),
	}
}

func cloneSessionMCPServerConfigs(in map[string]contract.MCPServerConfig) map[string]contract.MCPServerConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]contract.MCPServerConfig, len(in))
	for name, cfg := range in {
		out[name] = contract.MCPServerConfig{
			Transport: cfg.Transport,
			URL:       cfg.URL,
			Headers:   clone.StringMap(cfg.Headers),
			Command:   cfg.Command,
			Args:      clone.Strings(cfg.Args),
			Env:       clone.StringMap(cfg.Env),
			Enabled:   cloneSessionBoolPtr(cfg.Enabled),
		}
	}
	return out
}

func cloneSessionBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneSessionBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	maps.Copy(out, in)
	return out
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
