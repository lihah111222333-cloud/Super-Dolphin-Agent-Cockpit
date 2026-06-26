package eventsurface

import (
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

// agentLaunchedPayload 将 agent 启动事件转成 UI 通知载荷。
// 字段名沿用前端 wire 命名，空值通过 setString 省略以兼容旧客户端。
func agentLaunchedPayload(ev agentdto.AgentLaunched) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "cwd", ev.CWD)
	setString(payload, "model", ev.Model)
	setString(payload, "name", ev.Name)
	setString(payload, "provider", ev.Provider)
	return payload
}

// agentStoppedPayload 将 agent 停止原因补到会话基础载荷。
func agentStoppedPayload(ev agentdto.AgentStopped) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "reason", ev.Reason)
	return payload
}

// agentRecoveringPayload 描述 agent 恢复尝试，attempt 仅在有有效次数时输出。
func agentRecoveringPayload(ev agentdto.AgentRecovering) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "reason", ev.Reason)
	if ev.Attempt > 0 {
		payload["attempt"] = ev.Attempt
	}
	return payload
}

// agentFailedPayload 描述 agent 失败事件，recoverable 只在 true 时进入 wire 载荷。
func agentFailedPayload(ev agentdto.AgentFailed) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "error", ev.Error)
	if ev.Recoverable {
		payload["recoverable"] = true
	}
	return payload
}

// agentRuntimeReportedPayload 发布 agent runtime 上报结果，端口为正数时才暴露给 UI。
func agentRuntimeReportedPayload(ev agentdto.AgentRuntimeReported) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "provider", ev.Provider)
	if ev.Port > 0 {
		payload["port"] = ev.Port
	}
	return payload
}
