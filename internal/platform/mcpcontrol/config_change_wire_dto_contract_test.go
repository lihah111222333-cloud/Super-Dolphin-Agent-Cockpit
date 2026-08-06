package mcpcontrol

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

// TestConfigChangeWireDTOMapperConsumesProducerFields 逐字段驱动配置变更通知的真实 mapper。
func TestConfigChangeWireDTOMapperConsumesProducerFields(t *testing.T) {
	t.Parallel()

	t.Run("thread started", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, threadStartedPayload, []archtest.WireDTOMapperExemption{
			configMapperExemption("timestamp", "thread Started producer -> MCP config notification", "config changes are ordered by fanout version", "threadStartedPayload", "internal/platform/mcpcontrol"),
			configMapperExemption("name", "thread Started producer -> MCP config notification", "thread name changes use the dedicated metadata projection", "threadStartedPayload + config selector projection", "internal/platform/mcpcontrol"),
			configMapperExemption("pending_launch", "thread Started producer -> MCP config notification", "pending launch is not part of MCP server selection", "threadStartedPayload + config selector projection", "internal/platform/mcpcontrol"),
			configMapperExemption("board", "thread Started producer -> MCP config notification", "B1 board view is outside the MCP server selection payload", "threadStartedPayload omits B1 board projection by owner boundary", "internal/platform/mcpcontrol"),
		}, configMapperProjections(
			configMapperProjection("thread_id", "threadId"), configMapperProjection("agent_id", "agentId"),
			configMapperProjection("provider", "provider"), configMapperProjection("provider_thread_id", "providerThreadId"),
			configMapperProjection("cwd", "cwd"), configMapperProjection("model", "model"),
		))
	})
	t.Run("agent runtime reported", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, agentRuntimeReportedPayload, []archtest.WireDTOMapperExemption{
			configMapperExemption("timestamp", "agent runtime producer -> MCP config notification", "config changes are ordered by fanout version", "agentRuntimeReportedPayload", "internal/platform/mcpcontrol"),
		}, configMapperProjections(
			configMapperProjection("thread_id", "threadId"), configMapperProjection("agent_id", "agentId"),
			configMapperProjection("session_id", "sessionId"), configMapperProjection("port", "port"), configMapperProjection("provider", "provider"),
		))
	})
}

func configMapperProjections(values ...archtest.WireDTOMapperProjection) []archtest.WireDTOMapperProjection {
	return values
}

func configMapperProjection(field, consumerKey string) archtest.WireDTOMapperProjection {
	return archtest.WireDTOMapperProjection{Field: field, ConsumerKey: consumerKey}
}

func configMapperExemption(field, direction, reason, evidence, owner string) archtest.WireDTOMapperExemption {
	return archtest.WireDTOMapperExemption{
		Field:     field,
		Direction: direction,
		Reason:    reason,
		Evidence:  evidence,
		Owner:     owner,
	}
}
