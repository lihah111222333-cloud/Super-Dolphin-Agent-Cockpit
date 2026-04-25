package orchestration

import (
	"context"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

// ensureRuntimeForPersistedAgent repairs the gap after mcp-orch restarts:
// the UI can still list persisted agent threads, while this process's
// in-memory runtime map is empty. For remote Codex agents, the persisted
// provider binding is enough to route the next turn back to the same thread.
func (s *service) ensureRuntimeForPersistedAgent(ctx context.Context, agentID string) {
	agentID = strings.TrimSpace(agentID)
	if s == nil || agentID == "" || s.launcher == nil || s.agentBindings == nil {
		return
	}
	if !launcherSupportsPersistedRuntimeRehydrate(s.launcher) {
		return
	}
	if s.hasRuntimeAgent(agentID) {
		return
	}
	agent, reason, err := s.buildRuntimeFromPersistedBinding(ctx, agentID)
	if err != nil {
		if !platformdb.IsNotFound(err) {
			loggerOrDefault(s.logger).Warn("orchestration: persisted runtime rehydrate failed",
				"agent_id", agentID,
				"reason", reason,
				"error", err)
		}
		return
	}
	if agent == nil {
		loggerOrDefault(s.logger).Warn("orchestration: persisted runtime rehydrate skipped",
			"agent_id", agentID,
			"reason", reason)
		return
	}
	s.mu.Lock()
	if _, err := lookupAgentByIDLocked(s.agents, agentID); err == nil {
		s.mu.Unlock()
		return
	}
	s.agents[agent.id] = agent
	s.mu.Unlock()
	loggerOrDefault(s.logger).Warn("orchestration: rehydrated missing runtime from persisted binding",
		"agent_id", agent.id,
		"provider", agent.provider,
		"thread_id", agent.threadID,
		"remote_thread_id", agent.remoteThreadID,
		"cwd", agent.cwd)
}

func (s *service) hasRuntimeAgent(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := lookupAgentByIDLocked(s.agents, agentID)
	return err == nil
}

func (s *service) buildRuntimeFromPersistedBinding(ctx context.Context, agentID string) (*agentRuntime, string, error) {
	binding, err := s.agentBindings.GetByAgentID(ctx, agentID)
	if err != nil {
		return nil, "binding_lookup_failed", err
	}
	if binding == nil {
		return nil, "binding_missing", nil
	}
	if binding.Archived {
		return nil, "binding_archived", nil
	}
	provider := strings.ToLower(strings.TrimSpace(binding.Provider))
	if provider == "" {
		provider = "codex"
	}
	if provider != "codex" {
		return nil, "unsupported_provider", nil
	}
	remoteThreadID := strings.TrimSpace(platformshared.FirstNonEmpty(binding.CodexThreadID, binding.ProviderThreadID))
	if remoteThreadID == "" {
		return nil, "provider_thread_missing", nil
	}
	thread, err := s.persistedThreadForBinding(ctx, agentID, remoteThreadID)
	if err != nil && !platformdb.IsNotFound(err) {
		return nil, "thread_lookup_failed", err
	}
	if thread != nil {
		state := persistedThreadAgentState(*thread)
		if state == agentdto.StateStopped || state == agentdto.StateFailed {
			return nil, "persisted_thread_not_active", nil
		}
	}
	now := persistedRuntimeTime(binding, thread)
	agent := &agentRuntime{
		id:              agentID,
		name:            persistedRuntimeName(agentID, thread),
		cwd:             persistedRuntimeCWD(binding, thread),
		provider:        provider,
		providerSource:  "persisted-binding",
		runtimeProvider: provider,
		runtimePort:     persistedRuntimePort(thread),
		portSource:      "persisted-thread",
		state:           agentdto.StateIdle,
		threadID:        remoteThreadID,
		remoteThreadID:  remoteThreadID,
		remoteAgentID:   agentID,
		startedAt:       now,
		updatedAt:       now,
		launchSeq:       1,
		queue:           &SubmissionQueue{},
	}
	agent.sm = platformstatemachine.New(s.machineCfg, func() string {
		return agent.state
	}, func(next string) {
		agent.state = next
	})
	return agent, "", nil
}

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
		if t := persistedThreadTime(thread.UpdatedAt, thread.CreatedAt); !t.IsZero() {
			return t
		}
	}
	if binding != nil {
		for _, value := range []int64{binding.UpdatedAt, binding.CreatedAt} {
			if value > 0 {
				return time.Unix(value, 0)
			}
		}
	}
	return time.Now()
}
