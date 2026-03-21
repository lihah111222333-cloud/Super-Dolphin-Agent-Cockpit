package orchestration

import (
	"context"
	"errors"
	"strings"
)

func (s *service) UpdateRuntime(ctx context.Context, report RuntimeReport) error {
	agentID := strings.TrimSpace(report.AgentID)
	provider := strings.TrimSpace(report.Provider)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	if report.Port <= 0 && provider == "" {
		return errors.New("runtime report must include port or provider")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return err
	}
	applyRuntimeReportLocked(agent, report.Port, provider)
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	s.publishAgentRuntimeReported(agent)
	return nil
}

func (s *service) snapshotLocked(_ context.Context, agent *agentRuntime) AgentSnapshot {
	port, portSource := snapshotPort(agent)
	provider, providerSource := snapshotProvider(agent)
	return AgentSnapshot{
		ID:             agent.id,
		Name:           agent.name,
		ParentID:       agent.parentID,
		Port:           port,
		PortSource:     portSource,
		ThreadID:       agent.threadID,
		ActiveTurnID:   agent.activeTurnID,
		Cwd:            agent.cwd,
		State:          agent.state,
		Provider:       provider,
		ProviderSource: providerSource,
		LastReport:     agent.lastReport,
		UpdatedAt:      agent.updatedAt,
	}
}

func applyRuntimeReportLocked(agent *agentRuntime, port int, provider string) {
	if port > 0 {
		agent.runtimePort = port
		agent.portSource = "runtime"
	}
	if provider != "" {
		agent.runtimeProvider = provider
		agent.providerSource = "runtime"
	}
}

func resetRuntimeStateLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.runtimePort = 0
	agent.runtimeProvider = ""
}

func snapshotPort(agent *agentRuntime) (int, string) {
	if agent != nil && agent.runtimePort > 0 {
		return agent.runtimePort, "runtime"
	}
	if agent == nil {
		return 0, ""
	}
	return agent.port, agent.portSource
}

func snapshotProvider(agent *agentRuntime) (string, string) {
	if agent != nil && strings.TrimSpace(agent.runtimeProvider) != "" {
		return agent.runtimeProvider, "runtime"
	}
	if agent == nil {
		return "", ""
	}
	return agent.provider, agent.providerSource
}
