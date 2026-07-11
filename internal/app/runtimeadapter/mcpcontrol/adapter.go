package mcpcontroladapter

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/systemlog"
	"go.uber.org/fx"
)

// Module 将 MCP 控制面日志端口接到 systemlog store。
var Module = fx.Module("mcpcontroladapter",
	fx.Provide(provideMCPControlSystemLogSink),
)

type mcpControlSystemLogSink struct {
	store systemlog.Store
}

var _ mcpcontrol.SystemLogSink = mcpControlSystemLogSink{}

// provideMCPControlSystemLogSink 把 MCP 控制面日志端口接到 systemlog store。
// store 缺失说明 app 装配已损坏，必须 fail-fast，避免 ctl/log 静默绕过持久化。
func provideMCPControlSystemLogSink(store systemlog.Store) (mcpcontrol.SystemLogSink, error) {
	if store == nil {
		return nil, fmt.Errorf("mcp control system log sink requires systemlog store")
	}
	return mcpControlSystemLogSink{store: store}, nil
}

// InsertSystemLog 将平台层 DTO 转换为 store 输入，保持 mcpcontrol 不依赖 store 包。
func (s mcpControlSystemLogSink) InsertSystemLog(ctx context.Context, entry mcpcontrol.SystemLogEntry) error {
	return s.store.Insert(ctx, systemlog.InsertParams{
		Level:        entry.Level,
		Logger:       entry.Logger,
		Message:      entry.Message,
		Raw:          entry.Raw,
		Source:       entry.Source,
		Component:    entry.Component,
		AgentID:      entry.AgentID,
		ThreadID:     entry.ThreadID,
		TraceID:      entry.TraceID,
		SpanID:       entry.SpanID,
		ParentSpanID: entry.ParentSpanID,
		EventType:    entry.EventType,
		ToolName:     entry.ToolName,
		DurationMs:   entry.DurationMs,
		Extra:        entry.Extra,
	})
}
