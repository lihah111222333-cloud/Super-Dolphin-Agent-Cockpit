package app

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// sharedFileAdapterModule provides the UISharedFilesChanged emitter adapter
// so that the low-level store.sharedfile does not directly import the dispatcher
// or contract layers, preserving strict DDD dependency isolation.
func sharedFileAdapterModule() fx.Option {
	return fx.Provide(provideUISharedFilesChangedEmitter)
}

func provideUISharedFilesChangedEmitter(dispatcher *event.Dispatcher) func(uidto.UISharedFilesChanged) {
	if dispatcher == nil {
		return func(uidto.UISharedFilesChanged) {}
	}
	return contract.NewEmitter[uidto.UISharedFilesChanged](dispatcher)
}
