package mcpcontrol

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

const (
	configTopicAgent  = "config/agent"
	configTopicThread = "config/thread"
)

func registerConfigChangeSubscriptions(
	dispatcher *event.Dispatcher,
	notifier contract.ToolNotifier,
	versions configVersionSource,
	logger *slog.Logger,
) []context.CancelFunc {
	if dispatcher == nil || notifier == nil || versions == nil {
		return nil
	}
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
			publishConfigChanged(notifier, versions, logger, configTopicAgent, agentStateChangedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
			publishConfigChanged(notifier, versions, logger, configTopicAgent, agentLaunchedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentStopped) {
			publishConfigChanged(notifier, versions, logger, configTopicAgent, agentStoppedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentRecovering) {
			publishConfigChanged(notifier, versions, logger, configTopicAgent, agentRecoveringPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentFailed) {
			publishConfigChanged(notifier, versions, logger, configTopicAgent, agentFailedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentRuntimeReported) {
			publishConfigChanged(notifier, versions, logger, configTopicAgent, agentRuntimeReportedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Started) {
			publishConfigChanged(notifier, versions, logger, configTopicThread, threadStartedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
			publishConfigChanged(notifier, versions, logger, configTopicThread, threadStoppedPayload(ev))
		}, logger),
	}
}

func publishConfigChanged(
	notifier contract.ToolNotifier,
	versions configVersionSource,
	logger *slog.Logger,
	topic string,
	payload map[string]any,
) {
	if notifier == nil || versions == nil || strings.TrimSpace(topic) == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
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
	payload := agentSessionPayload(ev.AgentSessionHeader)
	payload["event"] = "agent/state/changed"
	setPayloadString(payload, "oldState", ev.OldState)
	setPayloadString(payload, "newState", ev.NewState)
	setPayloadString(payload, "trigger", ev.Trigger)
	return payload
}

func agentLaunchedPayload(ev agentdto.AgentLaunched) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	payload["event"] = "agent/launched"
	setPayloadString(payload, "cwd", ev.CWD)
	setPayloadString(payload, "model", ev.Model)
	return payload
}

func agentStoppedPayload(ev agentdto.AgentStopped) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	payload["event"] = "agent/stopped"
	setPayloadString(payload, "reason", ev.Reason)
	return payload
}

func agentRecoveringPayload(ev agentdto.AgentRecovering) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	payload["event"] = "agent/recovering"
	setPayloadString(payload, "reason", ev.Reason)
	if ev.Attempt > 0 {
		payload["attempt"] = ev.Attempt
	}
	return payload
}

func agentFailedPayload(ev agentdto.AgentFailed) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	payload["event"] = "agent/failed"
	setPayloadString(payload, "error", ev.Error)
	if ev.Recoverable {
		payload["recoverable"] = true
	}
	return payload
}

func agentRuntimeReportedPayload(ev agentdto.AgentRuntimeReported) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	payload["event"] = "agent/runtime/reported"
	setPayloadString(payload, "provider", ev.Provider)
	if ev.Port > 0 {
		payload["port"] = ev.Port
	}
	return payload
}

func threadStartedPayload(ev threaddto.Started) map[string]any {
	payload := map[string]any{
		"event":    "thread/started",
		"threadId": strings.TrimSpace(ev.ThreadID),
	}
	setPayloadString(payload, "agentId", ev.AgentID)
	setPayloadString(payload, "provider", ev.Provider)
	setPayloadString(payload, "providerThreadId", ev.ProviderThreadID)
	setPayloadString(payload, "cwd", ev.CWD)
	setPayloadString(payload, "model", ev.Model)
	return payload
}

func threadStoppedPayload(ev threaddto.Stopped) map[string]any {
	payload := map[string]any{
		"event":    "thread/stopped",
		"threadId": strings.TrimSpace(ev.ThreadID),
	}
	setPayloadString(payload, "agentId", ev.AgentID)
	setPayloadString(payload, "status", ev.Status)
	setPayloadString(payload, "reason", ev.Reason)
	return payload
}

func agentSessionPayload(header shareddto.AgentSessionHeader) map[string]any {
	payload := map[string]any{}
	setPayloadString(payload, "threadId", header.ThreadID)
	setPayloadString(payload, "agentId", header.AgentID)
	setPayloadString(payload, "sessionId", header.SessionID)
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
