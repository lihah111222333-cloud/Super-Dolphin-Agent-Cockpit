package rpc

import (
	"context"
	"time"
)

// TraceStatus 是 RPC trace 事件的状态枚举，会写入 observability 记录。
type TraceStatus string

// RPC trace 状态枚举。
const (
	TraceStatusOK    TraceStatus = "ok"
	TraceStatusSlow  TraceStatus = "slow"
	TraceStatusError TraceStatus = "error"
)

// TraceCodeAnchor 标记 trace 事件对应的代码位置，供诊断 UI 定位后端入口。
type TraceCodeAnchor struct {
	File     string // 源文件路径。
	Function string // 函数或方法名。
	Line     int    // 入口附近行号。
}

// TraceRecord 是 RPC 层写入 observability 的跨模块 DTO。
// 字段对齐 observability.TraceEvent，避免 RPC 包直接依赖具体事件结构。
type TraceRecord struct {
	Timestamp    time.Time       // 事件时间。
	TraceID      string          // trace 标识。
	SpanID       string          // 当前 span 标识。
	ParentSpanID string          // 父 span 标识。
	Kind         string          // 事件类别。
	Phase        string          // start/done/failed 等阶段。
	Method       string          // RPC method。
	DurationMS   int64           // 阶段耗时毫秒。
	Status       TraceStatus     // 事件状态。
	Error        string          // 失败原因，成功时为空。
	Code         TraceCodeAnchor // 代码锚点。
	Metadata     map[string]any  // 附加诊断字段。
}

// TraceRecorder 是 RPC 层依赖的最小 trace 写入接口。
// 该接口隔离 observability 实现，便于测试和模块化装配。
type TraceRecorder interface {
	Enabled() bool
	RecordTrace(context.Context, TraceRecord) error
}
