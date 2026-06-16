package mcpcontrol

import (
	"context"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

const (
	configTopicAgent  = "config/agent"
	configTopicThread = "config/thread"
)

// registerConfigChangeSubscriptions is the P22 P2 boundary for
// `internal/platform/mcpcontrol/config_change.go`: the bus callback body
// contains no NotifyConfigChanged call and no `context.Background()` —
// only a worker Enqueue. Marshal + advanceConfigVersion + Notify all run
// on the configFanoutWorker goroutine under its own cancellable ctx.
// registerConfigChangeSubscriptions 注册配置changesubscriptions。
func registerConfigChangeSubscriptions(
	dispatcher *event.Dispatcher,
	worker *configFanoutWorker,
	logger *pkglogger.Logger,
) []context.CancelFunc {
	if dispatcher == nil || worker == nil {
		return nil
	}
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
			worker.Enqueue(configTopicAgent, agentStateChangedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
			worker.Enqueue(configTopicAgent, agentLaunchedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentStopped) {
			worker.Enqueue(configTopicAgent, agentStoppedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentRecovering) {
			worker.Enqueue(configTopicAgent, agentRecoveringPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentFailed) {
			worker.Enqueue(configTopicAgent, agentFailedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentRuntimeReported) {
			worker.Enqueue(configTopicAgent, agentRuntimeReportedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Started) {
			worker.Enqueue(configTopicThread, threadStartedPayload(ev))
		}, logger),
		platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
			worker.Enqueue(configTopicThread, threadStoppedPayload(ev))
		}, logger),
	}
}

func configChangeSelectorScope(payload map[string]any) *dto.SelectorScope {
	scope := shared.NormalizeSelectorScope(&dto.SelectorScope{
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
