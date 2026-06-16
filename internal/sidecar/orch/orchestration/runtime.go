package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// UpdateRuntime 更新运行时。
func (s *service) UpdateRuntime(ctx context.Context, report RuntimeReport) error {
	agentID := strings.TrimSpace(report.AgentID)
	provider := normalizeRuntimeProvider(report.Provider)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	// Compatibility contract: port<=0 means "not provided", not "clear".
	// TestUpdateRuntimeZeroPortDoesNotClearRuntimePort locks this behavior.
	if !shouldUpdatePort(report.Port) && !shouldUpdateProvider(provider) {
		return errors.New("runtime report must include port or provider")
	}
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		// provider fail-fast：runtime 上报的 provider 必须命中 isKnownRuntimeProvider
		// 白名单。原先 silent Warn + 放行会让非法值落到 agent.runtimeProvider，
		// snapshot 里以 "runtime-unverified" 暴露；P23 README §默认值安全要求
		// 默认值不背锅，错就拒。
		//
		// provider fail-fast: runtime-reported provider must hit the known
		// allow-list. The previous silent-Warn-then-pass behavior leaked
		// unknown values into snapshots tagged "runtime-unverified"; P23
		// README §default-safety requires rejecting unknown inputs instead.
		if shouldUpdateProvider(provider) && !isKnownRuntimeProvider(provider) {
			return fmt.Errorf(
				"runtime 上报 provider 非法：%q，必须是 claude 或 codex (invalid runtime provider %q: must be claude or codex)",
				provider, provider,
			)
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

// snapshotLocked 处理快照locked。
func (s *service) snapshotLocked(_ context.Context, agent *agentRuntime) AgentSnapshot {
	port, portSource := snapshotPort(agent)
	provider, providerSource := snapshotProvider(agent)
	threadID := strings.TrimSpace(agent.threadID)
	if remoteThreadID := strings.TrimSpace(agent.remoteThreadID); remoteThreadID != "" {
		threadID = remoteThreadID
	}
	persistedAgentID := strings.TrimSpace(agent.remoteAgentID)
	if persistedAgentID == "" {
		persistedAgentID = agent.id
	}
	launchID := strings.TrimSpace(agent.requestedAgentID)
	if launchID == persistedAgentID || launchID == agent.id {
		launchID = ""
	}
	createdAt := agent.startedAt
	if createdAt.IsZero() {
		createdAt = agent.updatedAt
	}
	return AgentSnapshot{
		ID:             agent.id,
		AgentID:        persistedAgentID,
		LaunchID:       launchID,
		Name:           agent.name,
		ParentID:       agent.parentID,
		Port:           port,
		PortSource:     portSource,
		PID:            processPID(agent.cmd),
		ThreadID:       threadID,
		ActiveTurnID:   agent.activeTurnID,
		Cwd:            agent.cwd,
		State:          string(agent.state),
		Provider:       provider,
		ProviderSource: providerSource,
		LastReport:     normalizeDisplayReportText(agent.lastReport),
		CreatedAt:      createdAt,
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
	agent.remoteThreadID, agent.pendingLaunchThreadID = "", ""
	agent.pendingLaunchThreadAt, agent.remoteAgentID = time.Time{}, ""
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

// snapshotPort 处理快照port。
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

// snapshotProvider 处理快照provider。
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
