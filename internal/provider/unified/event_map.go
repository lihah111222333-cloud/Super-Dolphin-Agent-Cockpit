package unified

import (
	"log/slog"
	"sync"

	"github.com/kelindar/event"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// EventDispatcher manages raw driver events and republishes translated typed events.
type EventDispatcher struct {
	mu          sync.RWMutex
	translators []dto.EventTranslator
	bus         *event.Dispatcher
	logger      *slog.Logger
}

func NewEventDispatcher(bus *event.Dispatcher, logger *slog.Logger) *EventDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventDispatcher{
		bus:    bus,
		logger: logger,
	}
}

// Register registers one event translator from a driver.
func (d *EventDispatcher) Register(t dto.EventTranslator) {
	if t == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.translators = append(d.translators, t)
}

// Dispatch sends one raw driver event through all registered translators.
func (d *EventDispatcher) Dispatch(raw dto.RawProviderEvent) {
	d.mu.RLock()
	translators := make([]dto.EventTranslator, len(d.translators))
	copy(translators, d.translators)
	d.mu.RUnlock()

	for _, translator := range translators {
		translator(raw, func(ev any) {
			typedEv, ok := ev.(event.Event)
			if !ok {
				d.logger.Warn(
					"event translator produced non-typed event",
					"raw_type", raw.Type,
					"event", ev,
				)
				return
			}
			if d.bus == nil {
				return
			}
			event.Publish(d.bus, typedEv)
		})
	}
}
