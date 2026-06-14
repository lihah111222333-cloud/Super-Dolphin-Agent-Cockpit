package provider

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// RawProviderEvent is a driver-originated event before translation.
type RawProviderEvent struct {
	EventType string
	Data      any
}

// Type 返回事件分发用的类型编号。
func (RawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }

// BusRawProviderEvent wraps a raw provider event for event-bus publication.
type BusRawProviderEvent struct {
	Event RawProviderEvent
}

// Type 返回事件分发用的类型编号。
func (BusRawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }
