package eventsurface

import (
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func agentLaunchedPayload(ev agentdto.AgentLaunched) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "cwd", ev.CWD)
	setString(payload, "model", ev.Model)
	return payload
}

func agentStoppedPayload(ev agentdto.AgentStopped) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "reason", ev.Reason)
	return payload
}

func agentRecoveringPayload(ev agentdto.AgentRecovering) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "reason", ev.Reason)
	if ev.Attempt > 0 {
		payload["attempt"] = ev.Attempt
	}
	return payload
}

func agentFailedPayload(ev agentdto.AgentFailed) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "error", ev.Error)
	if ev.Recoverable {
		payload["recoverable"] = true
	}
	return payload
}

func agentRuntimeReportedPayload(ev agentdto.AgentRuntimeReported) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "provider", ev.Provider)
	if ev.Port > 0 {
		payload["port"] = ev.Port
	}
	return payload
}
