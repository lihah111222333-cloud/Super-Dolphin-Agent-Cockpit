package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/historyjsonl"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type driverRegistry interface {
	Resolve(provider string) (contract.Driver, error)
	Names() []string
}

type sessionResolver struct {
	threadStore   contract.SessionThreadLookup
	bindingStore  contract.SessionBindingLookup
	bindingWriter contract.SessionBindingUpserter
	registry      driverRegistry
	sessions      *SessionManager
}

var _ contract.SessionResolver = (*sessionResolver)(nil)

type autoResumeThreadState struct {
	runtimeConfig  map[string]any
	promptSnapshot contract.PromptAssemblySnapshot
}

// NewSessionResolver 创建会话解析器。
func NewSessionResolver(p sessionResolverParams) contract.SessionResolver {
	return &sessionResolver{
		threadStore:   p.ThreadStore,
		bindingStore:  p.BindingStore,
		bindingWriter: p.BindingWriter,
		registry:      p.Registry,
		sessions:      p.Sessions,
	}
}

// ResolveSession 根据 threadID 解析当前可用 session，先复用内存会话，再尝试从持久化绑定恢复。
func (r *sessionResolver) ResolveSession(ctx context.Context, threadID string) (contract.Session, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("resolve session: thread id is required")
	}
	if r.sessions == nil {
		return nil, fmt.Errorf("resolve session: session manager is not configured")
	}
	if session, ok := r.tryExistingSession(threadID); ok {
		return session, nil
	}
	session, errs := r.tryCreateSession(ctx, threadID)
	if session != nil {
		return session, nil
	}
	return nil, r.resolveLookupError(threadID, errs)
}

// tryExistingSession 处理调用方直接传 agent_id 的最快路径，只查询内存 SessionManager。
func (r *sessionResolver) tryExistingSession(threadID string) (contract.Session, bool) {
	// 调用方直接传 agent_id 时优先复用内存 session，避免触发持久化恢复路径。
	session, err := r.sessions.Get(threadID)
	return session, err == nil
}

// tryCreateSession 在直接 agent_id 命中失败后，通过线程索引或 provider 绑定恢复活跃 session。
// 这里的 create 只表示重建内存引用，不会创建全新的 provider 线程。
func (r *sessionResolver) tryCreateSession(ctx context.Context, threadID string) (contract.Session, []error) {
	errs := make([]error, 0, 2)
	if session, err := r.resolveThreadSession(ctx, threadID); err == nil {
		return session, nil
	} else if !contract.IsNotFound(err) {
		errs = append(errs, err)
	}
	if session, err := r.resolveProviderThreadSession(ctx, threadID); err == nil {
		return session, nil
	} else if !contract.IsNotFound(err) && !errors.Is(err, contract.ErrSessionNotFound) {
		errs = append(errs, err)
	}
	return nil, errs
}

// resolveLookupError 合并 session 查找过程中的非 NotFound 错误，缺少后端时直接 fail-fast。
func (r *sessionResolver) resolveLookupError(threadID string, errs []error) error {
	if r.threadStore == nil && r.bindingStore == nil {
		return fmt.Errorf("resolve session: no thread lookup backend is configured")
	}
	if len(errs) > 0 {
		return fmt.Errorf("resolve session: thread %q: %w", threadID, errors.Join(errs...))
	}
	return fmt.Errorf("resolve session: thread %q not found", threadID)
}

// resolveThreadSession 通过公共 thread 记录找到 agent，并在内存缺失时用绑定执行 auto-resume。
func (r *sessionResolver) resolveThreadSession(ctx context.Context, threadID string) (contract.Session, error) {
	if r.threadStore == nil {
		return nil, contract.ErrNotFound
	}
	ref, err := r.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, contract.ErrNotFound
	}
	if err := rejectAutoResumeLifecycle(ref); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(ref.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("resolve session: thread %q has no agent id", threadID)
	}
	if session, err := r.sessions.Get(agentID); err == nil {
		return session, nil
	}
	// 内存 session 缺失通常来自应用重启，需要通过 binding 找回 provider 线程并执行 auto-resume。
	if r.bindingStore == nil {
		return nil, contract.ErrSessionNotFound
	}
	binding, err := r.bindingStore.GetByAgentID(ctx, agentID)
	if err != nil {
		if !contract.IsNotFound(err) {
			return nil, err
		}
		return nil, contract.ErrSessionNotFound
	}
	return r.autoResumeSession(ctx, binding, autoResumeThreadState{
		runtimeConfig:  ref.RuntimeConfig,
		promptSnapshot: ref.PromptSnapshot,
	}, ref.ThreadID, threadID)
}

// resolveProviderThreadSession 按 provider thread ID 反查绑定，支持重启后从 provider 侧线程恢复。
func (r *sessionResolver) resolveProviderThreadSession(ctx context.Context, threadID string) (contract.Session, error) {
	if r.bindingStore == nil {
		return nil, contract.ErrNotFound
	}
	var errs []error
	for _, provider := range r.providerNames() {
		binding, err := r.bindingStore.GetByProviderThread(ctx, provider, threadID)
		if err != nil {
			if !contract.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("provider %q: %w", provider, err))
			}
			continue
		}
		if err := r.rejectBindingAutoResumeLifecycle(ctx, binding); err != nil {
			return nil, err
		}
		agentID := strings.TrimSpace(binding.AgentID)
		if agentID == "" {
			errs = append(errs, fmt.Errorf("resolve session: provider %q thread %q has no agent id", provider, threadID))
			continue
		}
		if session, err := r.sessions.Get(agentID); err == nil {
			return session, nil
		}
		// 内存未命中时从持久化 binding 恢复 provider session。
		threadState, err := r.lookupAutoResumeThreadState(ctx, binding)
		if err != nil {
			return nil, err
		}
		return r.autoResumeSession(ctx, binding, threadState)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, contract.ErrNotFound
}

// autoResumeSession 根据持久化绑定重建运行时 session。
// 这是应用重启后的关键恢复路径：数据库仍有线程 UUID，但内存 SessionManager 已为空。
func (r *sessionResolver) autoResumeSession(ctx context.Context, binding *contract.SessionBinding, threadState autoResumeThreadState, publicThreadID ...string) (contract.Session, error) {
	plan, err := r.buildAutoResumePlan(binding, threadState.runtimeConfig, threadState.promptSnapshot, publicThreadID...)
	if err != nil {
		return nil, err
	}
	req, err := resolveAutoResumeSessionIdentity(ctx, plan.driver, plan.req)
	if err != nil {
		pkglogger.Warn("resolve session: auto-resume identity resolution failed",
			"agent_id", binding.AgentID, "error", err)
		return nil, fmt.Errorf("resolve session: auto-resume failed: %w", err)
	}
	logAutoResumeStart(binding, req)
	session, err := plan.driver.ResumeSession(ctx, req)
	if err != nil {
		pkglogger.Warn("resolve session: auto-resume failed",
			"agent_id", binding.AgentID, "error", err)
		return nil, fmt.Errorf("resolve session: auto-resume failed: %w", err)
	}
	if err := r.backfillAutoResumeCodexIdentity(ctx, binding, req, session); err != nil {
		_ = session.ForceStop()
		return nil, fmt.Errorf("resolve session: auto-resume backfill codex identity: %w", err)
	}
	r.sessions.Register(binding.AgentID, session)
	pkglogger.Info("resolve session: auto-resume succeeded",
		"agent_id", binding.AgentID,
		"thread_id", session.ThreadID(),
	)
	return session, nil
}

// lookupAutoResumeThreadState 从线程记录中读取 auto-resume 配置和 prompt 快照。
// 只有 NotFound 表示候选线程不存在可继续查找，其它存储或解码错误都要阻断恢复。
func (r *sessionResolver) lookupAutoResumeThreadState(ctx context.Context, binding *contract.SessionBinding) (autoResumeThreadState, error) {
	if r == nil || r.threadStore == nil || binding == nil {
		return autoResumeThreadState{}, nil
	}
	for _, candidate := range []string{binding.CodexThreadID, binding.AgentID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		ref, err := r.threadStore.GetByThreadID(ctx, candidate)
		if err != nil {
			if contract.IsNotFound(err) {
				continue
			}
			return autoResumeThreadState{}, fmt.Errorf("resolve session: runtime config lookup thread %q: %w", candidate, err)
		}
		if ref == nil {
			continue
		}
		return autoResumeThreadState{
			runtimeConfig:  clone.RuntimeConfigMap(ref.RuntimeConfig),
			promptSnapshot: ref.PromptSnapshot,
		}, nil
	}
	return autoResumeThreadState{}, nil
}

// rejectBindingAutoResumeLifecycle 在按 binding 恢复前检查线程状态，阻止 stopped 或 archived 会话被重新拉起。
func (r *sessionResolver) rejectBindingAutoResumeLifecycle(ctx context.Context, binding *contract.SessionBinding) error {
	if r == nil || r.threadStore == nil || binding == nil {
		return nil
	}
	for _, candidate := range []string{binding.CodexThreadID, binding.AgentID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		ref, err := r.threadStore.GetByThreadID(ctx, candidate)
		if err != nil {
			if contract.IsNotFound(err) {
				continue
			}
			return err
		}
		if err := rejectAutoResumeLifecycle(ref); err != nil {
			return err
		}
	}
	return nil
}

// rejectAutoResumeLifecycle 校验单条线程引用是否允许 auto-resume。
func rejectAutoResumeLifecycle(ref *contract.SessionThreadRef) error {
	if ref == nil {
		return nil
	}
	switch status := strings.TrimSpace(ref.Status); status {
	case "stopped", "archived":
		return fmt.Errorf("resolve session: thread %q is %s", strings.TrimSpace(ref.ThreadID), status)
	default:
		return nil
	}
}

// autoResumeBindingCWD 提取恢复 session 所需工作目录，缺失时直接返回错误阻断恢复。
func autoResumeBindingCWD(binding *contract.SessionBinding) (string, error) {
	if binding == nil {
		return "", contract.ErrSessionNotFound
	}
	cwd := strings.TrimSpace(binding.Cwd)
	if cwd == "" || cwd == "." {
		return "", fmt.Errorf("resolve session: auto-resume cwd is required for agent %q", strings.TrimSpace(binding.AgentID))
	}
	return cwd, nil
}

// recoverableAutoResumeProviderThreadID 选择可被历史文件验证的 provider thread ID，避免恢复到错误线程。
func recoverableAutoResumeProviderThreadID(binding *contract.SessionBinding) (string, error) {
	if binding == nil {
		return "", contract.ErrSessionNotFound
	}
	var lastErr error
	for _, candidate := range []string{binding.ProviderThreadID, binding.SessionUUID} {
		providerThreadID := strings.TrimSpace(candidate)
		if !identifier.LooksLikeUUID(providerThreadID) {
			continue
		}
		if _, err := historyjsonl.ExistingProviderPath(historyjsonl.ReadRequest{
			Provider:         binding.Provider,
			RolloutPath:      binding.RolloutPath,
			ThreadID:         binding.CodexThreadID,
			ProviderThreadID: providerThreadID,
			SessionUUID:      providerThreadID,
			CodexHome:        binding.CodexHome,
		}); err != nil {
			lastErr = err
			continue
		}
		return providerThreadID, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", contract.ErrSessionNotFound
}

// providerNames 返回去重后的 provider 查找顺序，registry 缺失时保留 codex 和 claude 的兼容顺序。
func (r *sessionResolver) providerNames() []string {
	names := []string(nil)
	if r.registry != nil {
		names = append(names, r.registry.Names()...)
	}
	if len(names) == 0 {
		names = []string{"codex", "claude"}
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name := normalizeProviderName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
