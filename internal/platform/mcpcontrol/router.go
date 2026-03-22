package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func (r *ToolRegistry) NotifyConfigChanged(ctx context.Context, topic string, configVersion int64, payload json.RawMessage) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return errInvalidParams("mcp config topic is required")
	}
	return r.notifyTargets(ctx, r.snapshotTargets(r.bySubscription, topic), dto.MethodConfigChanged, dto.ConfigChangedNotify{
		Selector: dto.Selector{
			Subscription: topic,
		},
		ConfigVersion: configVersion,
		Payload:       append(json.RawMessage(nil), payload...),
	})
}
