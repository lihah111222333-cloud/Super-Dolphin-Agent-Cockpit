package bus

import (
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/kelindar/event"
)

func ResilientSubscribe[T event.Event](dispatcher *event.Dispatcher, fn func(T), logger *pkglogger.Logger) context.CancelFunc {
	if dispatcher == nil || fn == nil {
		return func() {}
	}
	log := logger
	if log == nil {
		log = pkglogger.Get()
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
