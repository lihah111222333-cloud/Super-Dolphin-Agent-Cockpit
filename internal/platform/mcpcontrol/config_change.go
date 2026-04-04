package mcpcontrol

import (
	"context"
	"encoding/json"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

const (
	configTopicAgent  = "config/agent"
	configTopicThread = "config/thread"
)

func publishConfigChanged(
	notifier contract.ToolNotifier,
	versions configVersionSource,
	logger *pkglogger.Logger,
	topic string,
	payload map[string]any,
) {
	if notifier == nil || versions == nil || strings.TrimSpace(topic) == "" {
		return
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logger.Warn("mcp config change marshal failed", "topic", topic, "err", err)
		return
	}
	configVersion := versions.advanceConfigVersion()
	if err := notifier.NotifyConfigChanged(context.Background(), topic, configChangeSelectorScope(payload), configVersion, raw); err != nil {
		logger.Warn("mcp config change notify failed", "topic", topic, "config_version", configVersion, "err", err)
	}
}

func configChangeSelectorScope(payload map[string]any) *dto.SelectorScope {
	scope := normalizeSelectorScope(&dto.SelectorScope{
		AgentID:  configChangePayloadString(payload, "agentId", "agent_id"),
		ThreadID: configChangePayloadString(payload, "threadId", "thread_id"),
	})
	if scope == (dto.SelectorScope{}) {
		return nil
	}
	return &scope
}

func configChangePayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

func configChangeScope(topic string) string {
	switch strings.TrimSpace(topic) {
	case configTopicAgent:
		return dto.ScopeAgentRuntime
	case configTopicThread:
		return dto.ScopeThreadBinding
	default:
		return ""
	}
}

func agentStateChangedPayload(ev agentdto.StateChanged) map[string]any {
	payload := baseConfigPayload("agent/state/changed", ev.AgentSessionHeader)
	setPayloadString(payload, "oldState", ev.OldState)
	setPayloadString(payload, "newState", ev.NewState)
	setPayloadString(payload, "trigger", ev.Trigger)
	return payload
}

func agentLaunchedPayload(ev agentdto.AgentLaunched) map[string]any {
	payload := baseConfigPayload("agent/launched", ev.AgentSessionHeader)
	setPayloadString(payload, "cwd", ev.CWD)
	setPayloadString(payload, "model", ev.Model)
	return payload
}

func agentStoppedPayload(ev agentdto.AgentStopped) map[string]any {
	payload := baseConfigPayload("agent/stopped", ev.AgentSessionHeader)
	setPayloadString(payload, "reason", ev.Reason)
	return payload
}

func agentRecoveringPayload(ev agentdto.AgentRecovering) map[string]any {
	payload := baseConfigPayload("agent/recovering", ev.AgentSessionHeader)
	setPayloadString(payload, "reason", ev.Reason)
	if ev.Attempt > 0 {
		payload["attempt"] = ev.Attempt
	}
	return payload
}

func agentFailedPayload(ev agentdto.AgentFailed) map[string]any {
	payload := baseConfigPayload("agent/failed", ev.AgentSessionHeader)
	setPayloadString(payload, "error", ev.Error)
	if ev.Recoverable {
		payload["recoverable"] = true
	}
	return payload
}

func agentRuntimeReportedPayload(ev agentdto.AgentRuntimeReported) map[string]any {
	payload := baseConfigPayload("agent/runtime/reported", ev.AgentSessionHeader)
	setPayloadString(payload, "provider", ev.Provider)
	if ev.Port > 0 {
		payload["port"] = ev.Port
	}
	return payload
}

func threadStartedPayload(ev threaddto.Started) map[string]any {
	payload := baseConfigPayload("thread/started", configPayloadHeader(ev.AgentID, ev.ThreadID))
	setPayloadString(payload, "provider", ev.Provider)
	setPayloadString(payload, "providerThreadId", ev.ProviderThreadID)
	setPayloadString(payload, "cwd", ev.CWD)
	setPayloadString(payload, "model", ev.Model)
	return payload
}

func threadStoppedPayload(ev threaddto.Stopped) map[string]any {
	payload := baseConfigPayload("thread/stopped", configPayloadHeader(ev.AgentID, ev.ThreadID))
	setPayloadString(payload, "status", ev.Status)
	setPayloadString(payload, "reason", ev.Reason)
	return payload
}

func setPayloadString(payload map[string]any, key, value string) {
	if payload == nil {
		return
	}
	if text := strings.TrimSpace(value); text != "" {
		payload[key] = text
	}
}
