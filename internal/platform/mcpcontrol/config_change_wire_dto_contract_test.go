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
		})
	})
	t.Run("agent runtime reported", func(t *testing.T) {
		archtest.AssertWireDTOMapperConsumesProducerFields(t, agentRuntimeReportedPayload, []archtest.WireDTOMapperExemption{
			configMapperExemption("timestamp", "agent runtime producer -> MCP config notification", "config changes are ordered by fanout version", "agentRuntimeReportedPayload", "internal/platform/mcpcontrol"),
		})
	})
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
