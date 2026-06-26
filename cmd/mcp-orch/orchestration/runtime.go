package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// UpdateRuntime 接收 runtime 上报的端口和 provider，并在 snapshot 可见字段变化时发布事件。
// provider 必须命中白名单，避免未知值静默写入 agent 运行态。
func (s *service) UpdateRuntime(ctx context.Context, report RuntimeReport) error {
	agentID := strings.TrimSpace(report.AgentID)
	provider := normalizeRuntimeProvider(report.Provider)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	// port<=0 表示 runtime 未提供端口，不代表清空已有端口；调用方不能用空报文抹掉快照。
	if !shouldUpdatePort(report.Port) && !shouldUpdateProvider(provider) {
		return errors.New("runtime report must include port or provider")
	}
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		// runtime 上报 provider 必须命中白名单；未知值直接拒绝，避免落入快照后被误认为可信来源。
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

// snapshotLocked 在持锁状态下把内存 runtime 投影成对外 AgentSnapshot。
// 远端 launcher 返回的 thread/agent id 优先于本地占位 id。
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

// applyRuntimeReportLocked 在持锁状态下应用 runtime 上报的端口和 provider。
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

// processPID 安全读取 exec.Cmd 的 pid；进程未启动或已清空时返回 0。
func processPID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

// shouldUpdatePort 判断 runtime report 是否携带有效端口。
func shouldUpdatePort(port int) bool {
	return port > 0
}

// shouldUpdateProvider 判断 runtime report 是否携带 provider。
func shouldUpdateProvider(provider string) bool {
	return provider != ""
}

// runtimeSnapshotChanged 判断 runtime 可见端口/provider 或其来源是否发生变化。
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

// runtimeProviderSource 返回 provider 来源标记。
// 未知 provider 只允许旧快照兼容路径显示为 unverified；新上报路径会在 UpdateRuntime 阻断。
func runtimeProviderSource(provider string) string {
	if isKnownRuntimeProvider(provider) {
		return "runtime"
	}
	return "runtime-unverified"
}

// normalizeRuntimeProvider 清理 provider 名称，供白名单校验和快照输出复用。
func normalizeRuntimeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// isKnownRuntimeProvider 判断 runtime provider 是否在当前支持列表内。
func isKnownRuntimeProvider(provider string) bool {
	switch provider {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

// resetRuntimeStateLocked 清空远端 runtime 派生字段，调用方必须持有 service 锁。
func resetRuntimeStateLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.runtimePort = 0
	agent.runtimeProvider = ""
	agent.remoteThreadID, agent.pendingLaunchThreadID = "", ""
	agent.pendingLaunchThreadAt, agent.remoteAgentID = time.Time{}, ""
}

// clearAgentLifecycleErrorLocked 清空单个启动周期内的错误和停止意图字段。
// 这是状态机外少数允许改这两个字段的位置；调用方必须已持有 service 锁。
func clearAgentLifecycleErrorLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.lastError = ""
	agent.stopRequested = false
}

// clearAgentStopReasonLocked 清空自由文本 stop reason。
// 它和 lifecycle error 分离，是因为 restart 路径会保留 reason 供审计，新建路径才清掉。
func clearAgentStopReasonLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.stopReason = ""
}

// clearAgentTurnStateLocked 清空绑定到单个 turn 实例的字段。
// 启动准备和中断恢复会调用它；调用方必须持有 service 锁，避免与事件处理并发写。
func clearAgentTurnStateLocked(agent *agentRuntime) {
	if agent == nil {
		return
	}
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.exitedAt = nil
}

// snapshotPort 选择 snapshot 中展示的端口，runtime 上报值优先于启动配置值。
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

// snapshotProvider 选择 snapshot 中展示的 provider，runtime 上报值优先于启动配置值。
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
