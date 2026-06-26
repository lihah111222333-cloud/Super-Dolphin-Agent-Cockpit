package provider

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// RawProviderEvent 是 provider driver 原始事件，尚未转换成统一业务事件。
type RawProviderEvent struct {
	EventType string
	Data      any
}

// Type 返回 provider raw event 分发用的类型编号。
func (RawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }

// BusRawProviderEvent 将原始 provider 事件包装为事件总线载荷。
type BusRawProviderEvent struct {
	Event RawProviderEvent
}

// Type 返回 provider raw event 总线载荷的类型编号。
func (BusRawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }
