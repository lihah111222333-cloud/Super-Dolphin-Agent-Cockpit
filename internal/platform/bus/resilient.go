package bus

import (
	"context"
	"log/slog"

	"github.com/kelindar/event"
)

func ResilientSubscribe[T event.Event](dispatcher *event.Dispatcher, fn func(T), logger *slog.Logger) context.CancelFunc {
	if dispatcher == nil || fn == nil {
		return func() {}
	}
	log := logger
	if log == nil {
		log = slog.Default()
	}
	return event.Subscribe(dispatcher, func(ev T) {
		if recovered := recoverCall(func() { fn(ev) }); recovered != nil {
			log.Error("handler panic", "type", eventTypeName(ev), "error", recovered)
		}
	})
}

func recoverCall(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}
