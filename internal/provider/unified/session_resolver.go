package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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

// AgentSessionResolver 是 provider/unified 内部可信路径使用的 agent ID 解析端口。
// 不放入 contract.SessionResolver，避免外部 RPC 或公共 thread 路径把 agent id 当 thread id。
type AgentSessionResolver interface {
	ResolveSessionByAgentID(ctx context.Context, agentID string) (contract.Session, error)
}

var _ AgentSessionResolver = (*sessionResolver)(nil)

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

// ResolveSession 根据公开 threadID 或 provider threadID 解析当前可用 session。
func (r *sessionResolver) ResolveSession(ctx context.Context, threadID string) (contract.Session, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("resolve session: thread id is required")
	}
	if r.sessions == nil {
		return nil, fmt.Errorf("resolve session: session manager is not configured")
	}
	session, errs := r.tryCreateSession(ctx, threadID)
	if session != nil {
		return session, nil
	}
	return nil, r.resolveLookupError(threadID, errs)
}

// ResolveSessionByAgentID 仅供已持有可信 agent ID 的内部调用方复用内存 session。
func (r *sessionResolver) ResolveSessionByAgentID(_ context.Context, agentID string) (contract.Session, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("resolve session by agent id: agent id is required")
	}
	if r.sessions == nil {
		return nil, fmt.Errorf("resolve session by agent id: session manager is not configured")
	}
	return r.sessions.Get(agentID)
}

// tryCreateSession 通过线程索引或 provider 绑定恢复活跃 session。
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
	// The in-memory and auto-resume paths both apply the structural snapshot
	// boundary.  The provider-owner lookup is deferred until the active fast
	// path below, avoiding a duplicate binding read on cold auto-resume (whose
	// plan performs the owner check before calling the driver).
	if err := validateExistingPromptSnapshot(ref.PromptSnapshot); err != nil {
		return nil, fmt.Errorf("resolve session: thread %q prompt snapshot: %w", threadID, err)
	}
	agentID := strings.TrimSpace(ref.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("resolve session: thread %q has no agent id", threadID)
	}
	if session, err := r.sessions.Get(agentID); err == nil {
		if err := r.validateExistingThreadPromptSnapshotOwner(ctx, ref); err != nil {
			return nil, fmt.Errorf("resolve session: thread %q prompt snapshot: %w", threadID, err)
		}
		return session, nil
	}
	// 内存 session 缺失通常来自应用重启，需要通过 binding 找回 provider 线程并执行 auto-resume。
	binding, err := r.lookupAutoResumeBindingByAgentID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return r.autoResumeSession(ctx, binding, autoResumeThreadState{
		runtimeConfig:  ref.RuntimeConfig,
		promptSnapshot: ref.PromptSnapshot,
	}, ref.ThreadID, threadID)
}

// validateExistingThreadPromptSnapshotOwner 在活跃 session 快路径校验 prompt 快照的 provider owner。
// SessionThreadRef 保持 provider-neutral；配置了 binding backend 时必须从权威 binding 读取 owner，不能跳过校验。
// validateExistingThreadPromptSnapshotOwner applies the provider-owner gate even
// on the public-thread active-session fast path.  SessionThreadRef deliberately
// stays provider-neutral, so the binding store is the authoritative owner
// source whenever it is configured.  A configured binding backend that cannot
// resolve an owner is an assembly/data error, not a reason to skip validation.
func (r *sessionResolver) validateExistingThreadPromptSnapshotOwner(ctx context.Context, ref *contract.SessionThreadRef) error {
	if ref == nil || promptSnapshotBlank(ref.PromptSnapshot) {
		return nil
	}
	if r != nil && r.bindingStore != nil {
		agentID := strings.TrimSpace(ref.AgentID)
		if agentID == "" {
			return errors.New("prompt snapshot owner binding requires agent id")
		}
		binding, err := r.bindingStore.GetByAgentID(ctx, agentID)
		if err != nil {
			if contract.IsNotFound(err) {
				return fmt.Errorf("prompt snapshot owner binding for agent %q is missing", agentID)
			}
			return fmt.Errorf("prompt snapshot owner binding lookup for agent %q: %w", agentID, err)
		}
		if binding == nil || strings.TrimSpace(binding.Provider) == "" {
			return fmt.Errorf("prompt snapshot owner binding for agent %q has no provider", agentID)
		}
		return contract.ValidateResumePromptSnapshotForProvider(ref.PromptSnapshot, binding.Provider)
	}
	return validateExistingPromptSnapshot(ref.PromptSnapshot)
}

// lookupAutoResumeBindingByAgentID 读取 public-thread 路径需要的 binding 并执行统一生命周期门禁。
func (r *sessionResolver) lookupAutoResumeBindingByAgentID(ctx context.Context, agentID string) (*contract.SessionBinding, error) {
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
	if err := r.rejectBindingAutoResumeLifecycle(ctx, binding); err != nil {
		return nil, err
	}
	return binding, nil
}

// resolveProviderThreadSession 按 provider thread ID 反查绑定，支持重启后从 provider 侧线程恢复。
func (r *sessionResolver) resolveProviderThreadSession(ctx context.Context, threadID string) (contract.Session, error) {
	if r.bindingStore == nil {
		return nil, contract.ErrNotFound
	}
	var errs []error
	for _, provider := range r.providerNames() {
		session, continueLookup, err := r.resolveProviderThreadCandidate(ctx, provider, threadID)
		if !continueLookup {
			return session, err
		}
		if err != nil && !contract.IsNotFound(err) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, contract.ErrNotFound
}

// resolveProviderThreadCandidate 处理单个 provider 的 thread 绑定查找。
// 返回 continueLookup=true 表示当前 provider 未命中或仅有可聚合错误，应继续尝试其他 owner。
func (r *sessionResolver) resolveProviderThreadCandidate(ctx context.Context, provider, threadID string) (contract.Session, bool, error) {
	binding, err := r.bindingStore.GetByProviderThread(ctx, provider, threadID)
	if err != nil {
		if contract.IsNotFound(err) {
			return nil, true, err
		}
		return nil, true, fmt.Errorf("provider %q: %w", provider, err)
	}
	if err := r.rejectBindingAutoResumeLifecycle(ctx, binding); err != nil {
		return nil, false, err
	}
	// A provider-thread lookup can succeed even when the public-thread lookup
	// above did not.  Validate every persisted projection before returning a
	// live or auto-resumed session, so legacy memory snapshots cannot bypass the
	// normal public-thread guard.
	if err := r.validateProviderThreadPromptSnapshot(ctx, binding, provider, threadID); err != nil {
		return nil, false, err
	}
	agentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return nil, true, fmt.Errorf("resolve session: provider %q thread %q has no agent id", provider, threadID)
	}
	if session, err := r.sessions.Get(agentID); err == nil {
		return session, false, nil
	}
	// 内存未命中时从持久化 binding 恢复 provider session。
	threadState, err := r.lookupAutoResumeThreadState(ctx, binding)
	if err != nil {
		return nil, false, err
	}
	session, err := r.autoResumeSession(ctx, binding, threadState)
	return session, false, err
}

// autoResumeSession 根据持久化绑定重建运行时 session。
// 这是应用重启后的关键恢复路径：数据库仍有线程 UUID，但内存 SessionManager 已为空。
func (r *sessionResolver) autoResumeSession(ctx context.Context, binding *contract.SessionBinding, threadState autoResumeThreadState, publicThreadID ...string) (contract.Session, error) {
	plan, err := r.buildAutoResumePlan(binding, threadState.runtimeConfig, threadState.promptSnapshot, publicThreadID...)
	if err != nil {
		return nil, err
	}
	resumeCtx := context.WithoutCancel(ctx)
	resumeIdentity := resumeCoordinationIdentity(plan.req.Provider, plan.req.ProviderThreadID)
	return r.sessions.resumeSession(ctx, binding.AgentID, resumeIdentity, false, func() (contract.Session, error) {
		return r.runAutoResumeSession(resumeCtx, binding, plan)
	})
}

// runAutoResumeSession 是 singleflight owner 执行的实际恢复流程。
func (r *sessionResolver) runAutoResumeSession(ctx context.Context, binding *contract.SessionBinding, plan autoResumePlan) (contract.Session, error) {
	req, err := resolveAutoResumeSessionIdentity(ctx, plan.driver, plan.req)
	if err != nil {
		pkglogger.Warn("resolve session: auto-resume identity resolution failed",
			"agent_id", binding.AgentID, "error", err)
		return nil, fmt.Errorf("resolve session: auto-resume failed: %w", err)
	}
	if err := validateAutoResumePromptSnapshot(req.PromptSnapshot, plan.req.Provider); err != nil {
		pkglogger.Warn("resolve session: auto-resume prompt snapshot validation failed",
			"agent_id", binding.AgentID, "error", err)
		return nil, fmt.Errorf("resolve session: auto-resume failed: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(req.Provider), strings.TrimSpace(plan.req.Provider)) {
		err := fmt.Errorf("resolve session: auto-resume provider owner mismatch: request=%q binding=%q", strings.TrimSpace(req.Provider), strings.TrimSpace(plan.req.Provider))
		pkglogger.Warn("resolve session: auto-resume provider validation failed",
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

// validateProviderThreadPromptSnapshot 在 provider-thread fallback 返回 session 前关闭 prompt owner 边界。
// provider thread 可能不同于 public thread，因此需要检查所有持久化别名，阻断旧快照旁路。
// validateProviderThreadPromptSnapshot closes the provider-thread fallback
// boundary before an already-live session is returned.  The fallback may be
// addressed by a provider thread ID rather than the public thread ID, so
// probe all persisted aliases and validate every projection that exists.
func (r *sessionResolver) validateProviderThreadPromptSnapshot(ctx context.Context, binding *contract.SessionBinding, provider, providerThreadID string) error {
	if r == nil || r.threadStore == nil || binding == nil {
		return nil
	}
	seen := make(map[string]struct{}, 3)
	for _, candidate := range []string{providerThreadID, binding.CodexThreadID, binding.AgentID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if err := r.validateProviderThreadPromptSnapshotCandidate(ctx, provider, candidate); err != nil && !contract.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// validateProviderThreadPromptSnapshotCandidate 校验 provider-thread fallback 的单个持久化别名。
// 缺失或空快照表示该别名没有可恢复 prompt；非 NotFound/owner 错误必须直接阻断。
func (r *sessionResolver) validateProviderThreadPromptSnapshotCandidate(ctx context.Context, provider, candidate string) error {
	ref, err := r.threadStore.GetByThreadID(ctx, candidate)
	if err != nil {
		if contract.IsNotFound(err) {
			return err
		}
		return fmt.Errorf("resolve session: provider thread %q prompt snapshot lookup: %w", candidate, err)
	}
	if ref == nil || promptSnapshotBlank(ref.PromptSnapshot) {
		return nil
	}
	if err := validateAutoResumePromptSnapshot(ref.PromptSnapshot, provider); err != nil {
		return fmt.Errorf("resolve session: provider thread %q prompt snapshot: %w", candidate, err)
	}
	return nil
}

// rejectBindingAutoResumeLifecycle 在按 binding 恢复前检查线程状态，阻止 stopped 或 archived 会话被重新拉起。
func (r *sessionResolver) rejectBindingAutoResumeLifecycle(ctx context.Context, binding *contract.SessionBinding) error {
	if binding == nil {
		return nil
	}
	if binding.Archived {
		return fmt.Errorf("resolve session: binding for agent %q is archived", strings.TrimSpace(binding.AgentID))
	}
	if r == nil || r.threadStore == nil {
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
	result, err := providerrecovery.Resolve(providerRecoveryRequestFromSessionBinding(binding))
	if err != nil {
		return "", err
	}
	return result.ProviderThreadID, nil
}

// providerRecoveryRequestFromSessionBinding 将 unified binding 映射到唯一 recovery port。
func providerRecoveryRequestFromSessionBinding(binding *contract.SessionBinding) providerrecovery.Request {
	return providerrecovery.Request{
		Provider:         binding.Provider,
		RolloutPath:      binding.RolloutPath,
		ProviderThreadID: binding.ProviderThreadID,
		SessionUUID:      binding.SessionUUID,
		CodexHome:        providerRecoveryCodexHome(binding.CodexHome, binding.ProviderRecoveryHome),
		ClaudeHome:       binding.ProviderRecoveryHome,
	}
}

func providerRecoveryCodexHome(codexHome, recoveryHome string) string {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome != "" {
		return codexHome
	}
	return strings.TrimSpace(recoveryHome)
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
