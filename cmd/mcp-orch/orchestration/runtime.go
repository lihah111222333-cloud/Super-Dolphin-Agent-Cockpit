package orchestration

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

func (s *service) UpdateRuntime(ctx context.Context, report RuntimeReport) error {
	agentID := strings.TrimSpace(report.AgentID)
	provider := normalizeRuntimeProvider(report.Provider)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	// TODO: add explicit clear semantics for runtime port updates instead of
	// treating port<=0 as "not provided" for backward compatibility.
	if !shouldUpdatePort(report.Port) && !shouldUpdateProvider(provider) {
		return errors.New("runtime report must include port or provider")
	}
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if shouldUpdateProvider(provider) && !isKnownRuntimeProvider(provider) {
			loggerOrDefault(s.logger).Warn("orchestration: unknown runtime provider", "agent_id", agent.id, "provider", provider)
		}
		beforePort, beforePortSource := snapshotPort(agent)
		beforeProvider, beforeProviderSource := snapshotProvider(agent)
		applyRuntimeReportLocked(agent, report.Port, provider)
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		afterPort, afterPortSource := snapshotPort(agent)
		afterProvider, afterProviderSource := snapshotProvider(agent)
		if runtimeSnapshotChanged(
			beforePort,
			beforePortSource,
			beforeProvider,
			beforeProviderSource,
			afterPort,
			afterPortSource,
			afterProvider,
			afterProviderSource,
		) {
			s.publishAgentRuntimeReported(agent)
		}
		return nil
	})
}

func (s *service) snapshotLocked(_ context.Context, agent *agentRuntime) AgentSnapshot {
	port, portSource := snapshotPort(agent)
	provider, providerSource := snapshotProvider(agent)
	threadID := strings.TrimSpace(agent.threadID)
	if remoteThreadID := strings.TrimSpace(agent.remoteThreadID); remoteThreadID != "" {
		threadID = remoteThreadID
	}
	return AgentSnapshot{
		ID:             agent.id,
		Name:           agent.name,
		ParentID:       agent.parentID,
		Port:           port,
		PortSource:     portSource,
		PID:            processPID(agent.cmd),
		ThreadID:       threadID,
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
	if shouldUpdatePort(port) {
		agent.runtimePort = port
		agent.portSource = "runtime"
	}
	if shouldUpdateProvider(provider) {
		agent.runtimeProvider = provider
		agent.providerSource = runtimeProviderSource(provider)
	}
}

func processPID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func shouldUpdatePort(port int) bool {
	return port > 0
}

func shouldUpdateProvider(provider string) bool {
	return provider != ""
}

func runtimeSnapshotChanged(
	beforePort int,
	beforePortSource string,
	beforeProvider string,
	beforeProviderSource string,
	afterPort int,
	afterPortSource string,
	afterProvider string,
	afterProviderSource string,
) bool {
	return beforePort != afterPort ||
		beforePortSource != afterPortSource ||
		beforeProvider != afterProvider ||
		beforeProviderSource != afterProviderSource
}

func runtimeProviderSource(provider string) string {
	// Keep forward compatibility for new providers while exposing that the
	// runtime-reported value has not been verified against the known registry.
	if isKnownRuntimeProvider(provider) {
		return "runtime"
	}
	return "runtime-unverified"
}

func normalizeRuntimeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func isKnownRuntimeProvider(provider string) bool {
	switch provider {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func resetRuntimeStateLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.runtimePort = 0
	agent.runtimeProvider = ""
	agent.remoteThreadID = ""
	agent.remoteAgentID = ""
}

// clearAgentLifecycleErrorLocked zeroes the per-lifecycle error + stop
// intent fields on agent. It is the single write-site for these two
// fields outside the state machine, used when we start a fresh launch
// cycle or resolve a prior stop intent. Caller must hold s.mu.
func clearAgentLifecycleErrorLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.lastError = ""
	agent.stopRequested = false
}

// clearAgentStopReasonLocked clears the free-form stop reason note.
// Intentionally separated from clearAgentLifecycleErrorLocked because
// restart paths preserve the reason for audit while new-agent paths
// clear it. Caller must hold s.mu.
func clearAgentStopReasonLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.stopReason = ""
}

// clearAgentTurnStateLocked zeroes all fields tied to a single turn
// instance (active turn id / provider thread id / exit timestamp).
// Called from launch-prepare and interrupt-recovery paths.
// Caller must hold s.mu.
func clearAgentTurnStateLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.exitedAt = nil
}

func snapshotPort(agent *agentRuntime) (int, string) {
	if agent != nil && agent.runtimePort > 0 {
		source := strings.TrimSpace(agent.portSource)
		if source == "" || source == "inferred" {
			source = "runtime"
		}
		return agent.runtimePort, source
	}
	if agent == nil {
		return 0, ""
	}
	return agent.port, agent.portSource
}

func snapshotProvider(agent *agentRuntime) (string, string) {
	if agent != nil && strings.TrimSpace(agent.runtimeProvider) != "" {
		source := strings.TrimSpace(agent.providerSource)
		if source == "" || source == "inferred" {
			source = "runtime"
		}
		return agent.runtimeProvider, source
	}
	if agent == nil {
		return "", ""
	}
	return agent.provider, agent.providerSource
}
