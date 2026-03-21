package provider

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// RawProviderEvent is a driver-originated event before translation.
type RawProviderEvent struct {
	EventType string
	Data      any
}

func (RawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }

// BusRawProviderEvent wraps a raw provider event for event-bus publication.
type BusRawProviderEvent struct {
	Event RawProviderEvent
}

func (BusRawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }

// EventTranslator translates raw driver events into typed events.
type EventTranslator func(raw RawProviderEvent, publish func(ev any))
