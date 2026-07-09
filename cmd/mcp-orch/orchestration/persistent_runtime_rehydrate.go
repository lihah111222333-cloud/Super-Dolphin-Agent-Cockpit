package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/reportstore"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

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
	loggerOrDefault(s.logger).Warn("orchestration: rehydrated missing runtime from persisted binding",
		"agent_id", agent.id,
		"provider", agent.provider,
		"thread_id", agent.threadID,
		"remote_thread_id", agent.remoteThreadID,
		"cwd", agent.cwd)
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
	return s.agentRegistry().addRehydratedRuntimeAgent(agent)
}

func (s *service) hasRuntimeAgent(agentID string) bool {
	return s.agentRegistry().hasRuntimeAgent(agentID)
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
	agent.sm = platformstatemachine.New(s.machineCfg, func() string {
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
