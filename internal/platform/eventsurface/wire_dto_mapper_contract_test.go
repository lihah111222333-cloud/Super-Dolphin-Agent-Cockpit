package eventsurface

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

// TestWireDTOMapperContractConsumesProducerFields 逐字段驱动 eventsurface 真实 mapper。
func TestWireDTOMapperContractConsumesProducerFields(t *testing.T) {
	t.Parallel()

	t.Run("thread started", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, threadStartedPayload, []archtest.WireDTOMapperExemption{
			mapperExemption("timestamp", "thread Started producer -> UI notification", "thread started wire payload intentionally omits event ordering time", "threadStartedPayload", "internal/platform/eventsurface"),
			mapperExemption("name", "thread Started producer -> UI notification", "thread name is projected by the dedicated update surface", "threadStartedPayload + thread update projection", "internal/platform/eventsurface"),
			mapperExemption("pending_launch", "thread Started producer -> UI notification", "pending launch remains internal lazy-start state", "threadStartedPayload + launch projection", "internal/platform/eventsurface"),
			mapperExemption("board", "thread Started producer -> UI notification", "Agent Board truth is consumed by uistate and projected through the dedicated ui/state/patch surface", "applyThreadStarted + refreshThreadPatchLocked", "internal/module/uistate"),
		}, mapperProjections(
			mapperProjection("thread_id", "threadId"), mapperProjection("thread_id", "providerThreadId"), mapperProjection("agent_id", "agentId"),
			mapperProjection("provider", "provider"), mapperProjection("provider_thread_id", "providerThreadId"),
			mapperProjection("cwd", "cwd"), mapperProjection("model", "model"),
		))
	})
	t.Run("turn output delta", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, turnOutputDeltaPayload, []archtest.WireDTOMapperExemption{
			mapperExemption("timestamp", "turn delta producer -> UI notification", "stream ordering follows patch sequence instead of producer time", "turnOutputDeltaPayload", "internal/platform/eventsurface"),
		}, mapperProjections(
			mapperProjection("thread_id", "threadId"), mapperProjection("agent_id", "agentId"),
			mapperProjection("turn_id", "turnId"), mapperProjection("item_id", "itemId"),
			mapperProjection("stream", "stream"), mapperProjection("delta", "delta"),
		))
	})
	t.Run("tool call end", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, toolCallEndPayload, []archtest.WireDTOMapperExemption{
			mapperExemption("timestamp", "tool call end producer -> UI notification", "tool timeline ordering follows patch sequence instead of producer time", "toolCallEndPayload", "internal/platform/eventsurface"),
		}, mapperProjections(
			mapperProjection("thread_id", "threadId"), mapperProjection("agent_id", "agentId"),
			mapperProjection("turn_id", "turnId"), mapperProjection("call_id", "callId"),
			mapperProjection("tool_name", "toolName"), mapperProjection("tool_name", "item", toolNameItemProjection),
			mapperProjection("success", "success"), mapperProjection("error", "error"), mapperProjection("result", "result"),
			mapperProjection("persisted_path", "persistedPath"), mapperProjection("persist_failed", "persistFailed"),
			mapperProjection("persist_error", "persistError"), mapperProjection("truncated", "truncated"),
			mapperProjection("original_size", "originalSize"), mapperProjection("elapsed_ms", "elapsedMs"),
		))
	})
	t.Run("agent runtime reported", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, agentRuntimeReportedPayload, []archtest.WireDTOMapperExemption{
			mapperExemption("timestamp", "agent runtime producer -> UI notification", "runtime reports expose the current snapshot rather than event time", "agentRuntimeReportedPayload", "internal/platform/eventsurface"),
		}, mapperProjections(
			mapperProjection("thread_id", "threadId"), mapperProjection("agent_id", "agentId"),
			mapperProjection("session_id", "sessionId"), mapperProjection("port", "port"), mapperProjection("provider", "provider"),
		))
	})
}

func mapperProjections(values ...archtest.WireDTOMapperProjection) []archtest.WireDTOMapperProjection {
	return values
}

func mapperProjection(field, consumerKey string, transforms ...func(any, any) any) archtest.WireDTOMapperProjection {
	projection := archtest.WireDTOMapperProjection{Field: field, ConsumerKey: consumerKey}
	if len(transforms) == 1 {
		projection.Transform = transforms[0]
	}
	return projection
}

func toolNameItemProjection(_ any, sentinel any) any {
	return map[string]any{"kind": "tool", "type": "tool", "toolName": sentinel}
}

func mapperExemption(field, direction, reason, evidence, owner string) archtest.WireDTOMapperExemption {
	return archtest.WireDTOMapperExemption{
		Field:     field,
		Direction: direction,
		Reason:    reason,
		Evidence:  evidence,
		Owner:     owner,
	}
}
