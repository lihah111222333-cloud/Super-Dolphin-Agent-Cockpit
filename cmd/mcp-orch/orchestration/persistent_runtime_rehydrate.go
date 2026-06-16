package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

// ensureRuntimeForPersistedAgent repairs the gap after mcp-orch restarts:
// the UI can still list persisted agent threads, while this process's
// in-memory runtime map is empty. For remote Codex agents, the persisted
// provider binding is enough to route the next turn back to the same thread.
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
	loggerOrDefault(s.logger).Warn("orchestration: rehydrated missing runtime from persisted binding",
		"agent_id", agent.id,
		"provider", agent.provider,
		"thread_id", agent.threadID,
		"remote_thread_id", agent.remoteThreadID,
		"cwd", agent.cwd)
}

// canRehydratePersistedRuntime 判断rehydratepersisted运行时是否可用。
func (s *service) canRehydratePersistedRuntime(agentID string) bool {
	if s == nil {
		return false
	}
	if agentID == "" {
		return false
	}
	if s.launcher == nil {
		return false
	}
	if s.agentBindings == nil {
		return false
	}
	if !launcherSupportsPersistedRuntimeRehydrate(s.launcher) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := lookupAgentByIDLocked(s.agents, agent.id); err == nil {
		return false
	}
	s.agents[agent.id] = agent
	return true
}

func (s *service) hasRuntimeAgent(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := lookupAgentByIDLocked(s.agents, agentID)
	return err == nil
}

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
	return s.newPersistedRuntimeAgent(agentID, source, thread), "", nil
}

type persistedRuntimeSource struct {
	binding        *PersistedBinding
	provider       string
	remoteThreadID string
}

// loadPersistedRuntimeSource 加载persisted运行时source。
func (s *service) loadPersistedRuntimeSource(ctx context.Context, agentID string) (persistedRuntimeSource, string, error) {
	binding, err := s.agentBindings.GetByAgentID(ctx, agentID)
	if err != nil {
		return persistedRuntimeSource{}, "binding_lookup_failed", err
	}
	if binding == nil {
		return persistedRuntimeSource{}, "binding_missing", nil
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
	return strings.TrimSpace(platformshared.FirstNonEmpty(binding.CodexThreadID, binding.ProviderThreadID))
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

func (s *service) newPersistedRuntimeAgent(agentID string, source persistedRuntimeSource, thread *PersistedThread) *agentRuntime {
	now := persistedRuntimeTime(source.binding, thread)
	agent := &agentRuntime{
		id:              agentID,
		name:            persistedRuntimeName(agentID, thread),
		cwd:             persistedRuntimeCWD(source.binding, thread),
		provider:        source.provider,
		providerSource:  "persisted-binding",
		runtimeProvider: source.provider,
		runtimePort:     persistedRuntimePort(thread),
		portSource:      "persisted-thread",
		state:           agentdto.StateIdle,
		threadID:        source.remoteThreadID,
		remoteThreadID:  source.remoteThreadID,
		remoteAgentID:   agentID,
		startedAt:       now,
		updatedAt:       now,
		launchSeq:       1,
		queue:           &SubmissionQueue{},
	}
	agent.sm = platformstatemachine.New(s.machineCfg, func() string {
		return string(agent.state)
	}, func(next string) {
		agent.state = agentdto.AgentState(next)
	})
	return agent
}

// persistedThreadForBinding 为binding处理persisted线程。
func (s *service) persistedThreadForBinding(ctx context.Context, agentID, remoteThreadID string) (*PersistedThread, error) {
	if s.agentThreads == nil {
		return nil, platformdb.ErrNotFound
	}
	if thread, err := s.agentThreads.GetByThreadID(ctx, remoteThreadID); err == nil && thread != nil {
		return thread, nil
	} else if err != nil && !platformdb.IsNotFound(err) {
		return nil, err
	}
	if thread, err := s.agentThreads.GetByThreadID(ctx, agentID); err == nil && thread != nil {
		return thread, nil
	} else if err != nil {
		return nil, err
	}
	return nil, platformdb.ErrNotFound
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
