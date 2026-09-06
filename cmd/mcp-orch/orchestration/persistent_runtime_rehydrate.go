package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/reportstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	platformstatemachine "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/statemachine"
)

var errPersistedBindingIdentity = errors.New("persisted binding identity is invalid")
var errPersistedThreadIdentity = errors.New("persisted thread identity is invalid")

// ensureRuntimeForPersistedAgent 在 mcp-orch 重启后补建内存 runtime。
// UI 仍能看到持久化 agent 线程，但当前进程的 runtime map 为空；远端 Codex agent 可依靠持久化 provider 绑定继续路由下一轮 turn。
func (s *service) ensureRuntimeForPersistedAgent(ctx context.Context, agentID string) {
	agentID = strings.TrimSpace(agentID)
	if !s.canRehydratePersistedRuntime(agentID) {
		return
	}
	agent, reason, err := s.buildRuntimeFromPersistedBinding(ctx, agentID)
	if err != nil {
		s.warnPersistedRuntimeRehydrateError(agentID, reason, err)
		return
	}
	if agent == nil {
		loggerOrDefault(s.logger).Warn("orchestration: persisted runtime rehydrate skipped",
			"agent_id", agentID,
			"reason", reason)
		return
	}
	if !s.addRehydratedRuntimeAgent(agent) {
		return
	}
	if err := s.activatePersistedRuntime(ctx, agent); err != nil {
		s.registry.removeRehydratedRuntimeAgent(agent)
		s.warnPersistedRuntimeRehydrateError(agentID, "provider_runtime_activation_failed", err)
		return
	}
	// provider resume 期间可能会回放旧通知；先完成真实 session activation，再建立
	// terminal head generation，避免在 provider 尚未接管时把旧事件与新 runtime 混在一起。
	if err := s.BindSessionGeneration(ctx, agent.id, agent.launchSeq); err != nil {
		s.registry.removeRehydratedRuntimeAgent(agent)
		s.warnPersistedRuntimeRehydrateError(agentID, "session_generation_activation_failed", err)
		return
	}
	loggerOrDefault(s.logger).Warn("orchestration: rehydrated missing runtime from persisted binding",
		"agent_id", agent.id,
		"provider", agent.provider,
		"thread_id", agent.threadID,
		"remote_thread_id", agent.remoteThreadID,
		"cwd", agent.cwd)
}

type persistedRuntimeActivator interface {
	ActivatePersistedRuntime(context.Context, PersistedRuntimeActivation) error
}

func (s *service) activatePersistedRuntime(ctx context.Context, agent *agentRuntime) error {
	activator, ok := s.lifecycle.launcher.(persistedRuntimeActivator)
	if !ok {
		return nil
	}
	if agent == nil {
		return errors.New("persisted runtime agent is required for activation")
	}
	return activator.ActivatePersistedRuntime(ctx, PersistedRuntimeActivation{
		AgentID: agent.id, ThreadID: agent.remoteThreadID, Provider: agent.provider, CWD: agent.cwd,
	})
}

// canRehydratePersistedRuntime 判断当前 agent 是否可以从持久化绑定重建 runtime。
// 只有支持重建的 launcher、binding store 和缺失内存 runtime 同时满足时才继续。
func (s *service) canRehydratePersistedRuntime(agentID string) bool {
	if s == nil {
		return false
	}
	if agentID == "" {
		return false
	}
	if s.lifecycle.launcher == nil {
		return false
	}
	if s.lifecycle.agentBindings == nil {
		return false
	}
	if !launcherSupportsPersistedRuntimeRehydrate(s.lifecycle.launcher) {
		return false
	}
	return !s.hasRuntimeAgent(agentID)
}

func (s *service) warnPersistedRuntimeRehydrateError(agentID, reason string, err error) {
	loggerOrDefault(s.logger).Warn("orchestration: persisted runtime rehydrate "+persistedRuntimeRehydrateLogLevel(err),
		"agent_id", agentID,
		"reason", reason,
		"error", err)
}

func persistedRuntimeRehydrateLogLevel(err error) string {
	if platformdb.IsNotFound(err) {
		return "skipped"
	}
	return "failed"
}

func (s *service) addRehydratedRuntimeAgent(agent *agentRuntime) bool {
	return s.registry.addRehydratedRuntimeAgent(agent)
}

func (s *service) hasRuntimeAgent(agentID string) bool {
	return s.registry.hasRuntimeAgent(agentID)
}

// buildRuntimeFromPersistedBinding 从绑定、线程和 report 文件重建可继续提交 turn 的 runtime。
func (s *service) buildRuntimeFromPersistedBinding(ctx context.Context, agentID string) (*agentRuntime, string, error) {
	source, reason, err := s.loadPersistedRuntimeSource(ctx, agentID)
	if err != nil {
		return nil, reason, err
	}
	if reason != "" {
		return nil, reason, nil
	}
	thread, reason, err := s.activePersistedThreadForBinding(ctx, agentID, source.remoteThreadID)
	if err != nil {
		return nil, reason, err
	}
	if reason != "" {
		return nil, reason, nil
	}
	if err := validatePersistedRuntimeThread(source, thread); err != nil {
		return nil, "thread_identity_invalid", err
	}
	agent, err := s.newPersistedRuntimeAgent(agentID, source, thread)
	if err != nil {
		return nil, "report_lookup_failed", err
	}
	return agent, "", nil
}

type persistedRuntimeSource struct {
	binding        *PersistedBinding
	provider       string
	remoteThreadID string
}

// loadPersistedRuntimeSource 加载persisted运行时source。
func (s *service) loadPersistedRuntimeSource(ctx context.Context, agentID string) (persistedRuntimeSource, string, error) {
	binding, err := s.lifecycle.agentBindings.GetByAgentID(ctx, agentID)
	if err != nil {
		return persistedRuntimeSource{}, "binding_lookup_failed", err
	}
	if binding == nil {
		return persistedRuntimeSource{}, "binding_missing", nil
	}
	if err := validatePersistedBindingIdentity(binding, agentID); err != nil {
		return persistedRuntimeSource{}, "binding_identity_invalid", err
	}
	if binding.Archived {
		return persistedRuntimeSource{}, "binding_archived", nil
	}
	provider := persistedBindingProvider(binding)
	if provider == "" {
		return persistedRuntimeSource{}, "provider_missing", fmt.Errorf("persisted binding provider is required for agent %q", agentID)
	}
	if provider != "codex" {
		return persistedRuntimeSource{}, "unsupported_provider", nil
	}
	remoteThreadID := persistedBindingRemoteThreadID(binding)
	if remoteThreadID == "" {
		return persistedRuntimeSource{}, "provider_thread_missing", nil
	}
	return persistedRuntimeSource{
		binding:        binding,
		provider:       provider,
		remoteThreadID: remoteThreadID,
	}, "", nil
}

func persistedBindingProvider(binding *PersistedBinding) string {
	if binding == nil {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(binding.Provider))
	return provider
}

func persistedBindingRemoteThreadID(binding *PersistedBinding) string {
	if binding == nil {
		return ""
	}
	// provider_thread_id is the provider-native identity and must win when it
	// is present. CodexThreadID is the public/legacy identity and is only a
	// compatibility fallback for rows written before provider_thread_id was
	// populated.
	if providerThreadID := strings.TrimSpace(binding.ProviderThreadID); providerThreadID != "" {
		return providerThreadID
	}
	return strings.TrimSpace(binding.CodexThreadID)
}

// validatePersistedBindingIdentity rejects malformed durable binding tuples
// before they can be used to rehydrate a provider runtime. The public thread
// may be absent for provider-only legacy rows, but an empty pair is never a
// usable identity and Codex recovery metadata must remain internally paired.
func validatePersistedBindingIdentity(binding *PersistedBinding, requestedAgentID string) error {
	if binding == nil {
		return fmt.Errorf("%w: binding is required", errPersistedBindingIdentity)
	}
	agentID := strings.TrimSpace(requestedAgentID)
	bindingAgentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" || bindingAgentID == "" || bindingAgentID != agentID {
		return fmt.Errorf("%w: agent id mismatch (requested=%q persisted=%q)", errPersistedBindingIdentity, agentID, bindingAgentID)
	}
	provider := strings.ToLower(strings.TrimSpace(binding.Provider))
	if err := validatePersistedBindingProvider(provider, agentID); err != nil {
		return err
	}
	if err := validatePersistedBindingThreads(binding, bindingAgentID, agentID); err != nil {
		return err
	}
	return validatePersistedBindingIdentityFields(binding, provider, agentID)
}

func validatePersistedBindingProvider(provider, agentID string) error {
	if provider == "" {
		return fmt.Errorf("%w: provider is required for agent %q", errPersistedBindingIdentity, agentID)
	}
	if provider != "codex" && provider != "claude" {
		return fmt.Errorf("%w: unsupported provider %q for agent %q", errPersistedBindingIdentity, provider, agentID)
	}
	return nil
}

func validatePersistedBindingThreads(binding *PersistedBinding, bindingAgentID, agentID string) error {
	providerThreadID := strings.TrimSpace(binding.ProviderThreadID)
	publicThreadID := strings.TrimSpace(binding.CodexThreadID)
	if providerThreadID == "" && publicThreadID == "" {
		return fmt.Errorf("%w: provider and public thread ids are empty for agent %q", errPersistedBindingIdentity, agentID)
	}
	// agent_id was used as a provider_thread_id placeholder by an old writer;
	// it is not a provider identity and must not be selected for recovery.
	if providerThreadID == bindingAgentID {
		return fmt.Errorf("%w: provider thread id is an agent placeholder for agent %q", errPersistedBindingIdentity, agentID)
	}
	return nil
}

func validatePersistedBindingIdentityFields(binding *PersistedBinding, provider, agentID string) error {
	if provider == "codex" {
		codexHome := strings.TrimSpace(binding.CodexHome)
		recoveryHome := strings.TrimSpace(binding.ProviderRecoveryHome)
		if codexHome != "" && recoveryHome != "" && codexHome != recoveryHome {
			return fmt.Errorf("%w: codex recovery home drift for agent %q", errPersistedBindingIdentity, agentID)
		}
		return nil
	}
	if strings.TrimSpace(binding.CodexHome) != "" ||
		strings.TrimSpace(binding.ProviderRecoveryHome) != "" ||
		strings.TrimSpace(binding.CodexInstanceKey) != "" ||
		strings.TrimSpace(binding.CodexModelProvider) != "" {
		return fmt.Errorf("%w: Codex identity fields drifted onto provider %q for agent %q", errPersistedBindingIdentity, provider, agentID)
	}
	return nil
}

func (s *service) activePersistedThreadForBinding(ctx context.Context, agentID, remoteThreadID string) (*PersistedThread, string, error) {
	thread, err := s.persistedThreadForBinding(ctx, agentID, remoteThreadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, "thread_not_found", err
		}
		return nil, "thread_lookup_failed", err
	}
	if persistedThreadInactive(thread) {
		return nil, "persisted_thread_not_active", nil
	}
	return thread, "", nil
}

func persistedThreadInactive(thread *PersistedThread) bool {
	if thread == nil {
		return false
	}
	state := persistedThreadAgentState(*thread)
	return state == string(agentdto.StateStopped) || state == string(agentdto.StateFailed)
}

// newPersistedRuntimeAgent 创建缺失的内存态 runtime，并把 persisted report seq 带回水位。
func (s *service) newPersistedRuntimeAgent(agentID string, source persistedRuntimeSource, thread *PersistedThread) (*agentRuntime, error) {
	now := persistedRuntimeTime(source.binding, thread)
	report, err := persistedRuntimeReport(agentID, source, thread)
	if err != nil {
		return nil, err
	}
	agent := &agentRuntime{
		id:                  agentID,
		name:                persistedRuntimeName(agentID, thread),
		cwd:                 persistedRuntimeCWD(source.binding, thread),
		provider:            source.provider,
		providerSource:      "persisted-binding",
		runtimeProvider:     source.provider,
		runtimePort:         persistedRuntimePort(thread),
		portSource:          "persisted-thread",
		state:               agentdto.StateIdle,
		threadID:            source.remoteThreadID,
		remoteThreadID:      source.remoteThreadID,
		remoteAgentID:       agentID,
		lastReport:          report.Report,
		lastReportSeq:       report.ReportSeq,
		lastReportUpdatedAt: report.UpdatedAt,
		startedAt:           now,
		updatedAt:           now,
		launchSeq:           1,
		queue:               &SubmissionQueue{},
	}
	agent.sm = platformstatemachine.New(s.lifecycle.machineCfg, func() string {
		return string(agent.state)
	}, func(next string) {
		agent.state = agentdto.AgentState(next)
	})
	return agent, nil
}

func persistedRuntimeReport(agentID string, source persistedRuntimeSource, thread *PersistedThread) (reportstore.PersistedRecord, error) {
	record := reportstore.Record{
		AgentID: agentID,
		Name:    persistedRuntimeName(agentID, thread),
		Cwd:     persistedRuntimeCWD(source.binding, thread),
	}
	report, err := reportstore.ReadPersistedRecord(record)
	if err != nil {
		if strings.TrimSpace(record.Cwd) == "" || errors.Is(err, reportstore.ErrNotFound) {
			return reportstore.PersistedRecord{}, nil
		}
		return reportstore.PersistedRecord{}, err
	}
	return report, nil
}

// persistedThreadForBinding 根据 provider thread 或 agentID 找到持久化线程。
// provider thread 优先，兼容旧数据时再回退到 agentID。
func (s *service) persistedThreadForBinding(ctx context.Context, agentID, remoteThreadID string) (*PersistedThread, error) {
	if s.lifecycle.agentThreads == nil {
		return nil, platformdb.ErrNotFound
	}
	agentID = strings.TrimSpace(agentID)
	remoteThreadID = strings.TrimSpace(remoteThreadID)
	if thread, found, err := exactPersistedThreadForBinding(ctx, s.lifecycle.agentThreads, agentID, remoteThreadID); err != nil || found {
		return thread, err
	}
	if thread, found, err := exactPersistedThreadForBinding(ctx, s.lifecycle.agentThreads, agentID, agentID); err != nil || found {
		return thread, err
	}
	return persistedThreadByAgentIDFromList(ctx, s.lifecycle.agentThreads, agentID, remoteThreadID)
}

func exactPersistedThreadForBinding(ctx context.Context, store AgentThreadStore, agentID, expectedThreadID string) (*PersistedThread, bool, error) {
	if strings.TrimSpace(expectedThreadID) == "" {
		return nil, false, nil
	}
	thread, err := store.GetByThreadID(ctx, expectedThreadID)
	if err != nil {
		if isPersistedThreadNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if thread == nil {
		return nil, false, nil
	}
	if err := validatePersistedThreadIdentity(thread, agentID, expectedThreadID); err != nil {
		return nil, false, err
	}
	return thread, true, nil
}

// persistedThreadByAgentIDFromList is the explicit provider/public split
// fallback: the list projection carries the binding owner even when the
// thread table is keyed only by public Codex id.
func persistedThreadByAgentIDFromList(ctx context.Context, store AgentThreadStore, agentID, remoteThreadID string) (*PersistedThread, error) {
	threads, err := store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var match *PersistedThread
	for _, candidate := range threads {
		candidateAgentID := strings.TrimSpace(candidate.AgentID)
		candidateThreadID := strings.TrimSpace(candidate.ThreadID)
		if candidateThreadID == remoteThreadID && candidateAgentID != agentID {
			return nil, fmt.Errorf("%w: provider thread %q belongs to agent %q, requested %q", errPersistedThreadIdentity, remoteThreadID, candidateAgentID, agentID)
		}
		if candidateAgentID != agentID {
			continue
		}
		if err := validatePersistedThreadIdentity(&candidate, agentID, ""); err != nil {
			return nil, err
		}
		if match != nil {
			return nil, fmt.Errorf("%w: agent %q has multiple persisted threads", errPersistedThreadIdentity, agentID)
		}
		thread := candidate
		match = &thread
	}
	if match == nil {
		return nil, platformdb.ErrNotFound
	}
	return match, nil
}

func isPersistedThreadNotFound(err error) bool {
	return errors.Is(err, errAgentNotFound) || platformdb.IsNotFound(err)
}

// validatePersistedThreadIdentity confirms that a recovered thread still
// belongs to the requested agent and has a concrete durable thread id. When
// expectedThreadID is set, exact provider-thread lookup must not drift to a
// different row; list fallback intentionally leaves it empty for public ids.
func validatePersistedThreadIdentity(thread *PersistedThread, agentID, expectedThreadID string) error {
	if thread == nil {
		return fmt.Errorf("%w: persisted thread is missing", errPersistedThreadIdentity)
	}
	agentID = strings.TrimSpace(agentID)
	actualAgentID := strings.TrimSpace(thread.AgentID)
	if agentID == "" || actualAgentID == "" || actualAgentID != agentID {
		return fmt.Errorf("%w: agent id mismatch (requested=%q persisted=%q)", errPersistedThreadIdentity, agentID, actualAgentID)
	}
	threadID := strings.TrimSpace(thread.ThreadID)
	if threadID == "" {
		return fmt.Errorf("%w: thread id is empty for agent %q", errPersistedThreadIdentity, agentID)
	}
	if expectedThreadID = strings.TrimSpace(expectedThreadID); expectedThreadID != "" && threadID != expectedThreadID {
		return fmt.Errorf("%w: thread id mismatch (expected=%q persisted=%q)", errPersistedThreadIdentity, expectedThreadID, threadID)
	}
	return nil
}

// validatePersistedRuntimeThread compares the thread projection with the
// binding identity before constructing an in-memory runtime. A non-empty CWD
// is an execution identity, not display metadata, so drift is rejected.
func validatePersistedRuntimeThread(source persistedRuntimeSource, thread *PersistedThread) error {
	if source.binding == nil {
		return fmt.Errorf("%w: binding is missing", errPersistedThreadIdentity)
	}
	if err := validatePersistedThreadIdentity(thread, source.binding.AgentID, ""); err != nil {
		return err
	}
	bindingCWD := strings.TrimSpace(source.binding.Cwd)
	threadCWD := strings.TrimSpace(thread.Cwd)
	if bindingCWD != "" && threadCWD != "" && bindingCWD != threadCWD {
		return fmt.Errorf("%w: cwd drift for agent %q", errPersistedThreadIdentity, strings.TrimSpace(source.binding.AgentID))
	}
	threadID := strings.TrimSpace(thread.ThreadID)
	remoteThreadID := strings.TrimSpace(source.remoteThreadID)
	publicThreadID := strings.TrimSpace(source.binding.CodexThreadID)
	if threadID != remoteThreadID && (publicThreadID == "" || threadID != publicThreadID) {
		return fmt.Errorf("%w: thread id %q is not bound to provider %q or public %q", errPersistedThreadIdentity, threadID, remoteThreadID, publicThreadID)
	}
	return nil
}

type persistedRuntimeRehydrateLauncher interface {
	SupportsPersistedRuntimeRehydrate() bool
}

func launcherSupportsPersistedRuntimeRehydrate(launcher AgentLauncher) bool {
	supports, ok := launcher.(persistedRuntimeRehydrateLauncher)
	return ok && supports.SupportsPersistedRuntimeRehydrate()
}

func persistedRuntimeName(agentID string, thread *PersistedThread) string {
	if thread == nil {
		return agentID
	}
	return strings.TrimSpace(platformshared.FirstNonEmpty(thread.Name, thread.Prompt, agentID))
}

func persistedRuntimeCWD(binding *PersistedBinding, thread *PersistedThread) string {
	if thread != nil && strings.TrimSpace(thread.Cwd) != "" {
		return strings.TrimSpace(thread.Cwd)
	}
	if binding != nil {
		return strings.TrimSpace(binding.Cwd)
	}
	return ""
}

func persistedRuntimePort(thread *PersistedThread) int {
	if thread == nil {
		return 0
	}
	return int(thread.Port)
}

func persistedRuntimeTime(binding *PersistedBinding, thread *PersistedThread) time.Time {
	if thread != nil {
		if t := contract.NormalizeUnixTime(thread.UpdatedAt, thread.CreatedAt); !t.IsZero() {
			return t
		}
	}
	if binding != nil {
		if t := contract.NormalizeUnixTime(binding.UpdatedAt, binding.CreatedAt); !t.IsZero() {
			return t
		}
	}
	return time.Now()
}
