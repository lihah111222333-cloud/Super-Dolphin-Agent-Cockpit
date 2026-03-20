package provider

// RawProviderEvent is a driver-originated event before translation.
type RawProviderEvent struct {
	Type string
	Data any
}

// EventTranslator translates raw driver events into typed events.
type EventTranslator func(raw RawProviderEvent, publish func(ev any))
