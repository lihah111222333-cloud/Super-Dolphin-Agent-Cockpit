package memory

import (
	"context"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type memoryHookParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher     `optional:"true"`
	Hooks      *MemoryLifecycleHooks `optional:"true"`
}

var Module = fx.Module("memory",
	fx.Provide(
		NewConfig,
		NewService,
		NewAgentMemoryManager,
		NewMemoryRuleEngine,
		NewRulesProvider,
		NewMemoryLifecycleHooks,
		NewMemoryExtractor,
	),
	fx.Invoke(registerLifecycle, registerPromptProvider, registerMemoryHooks),
)

func registerLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return svc.EnsureRoot(ctx)
		},
	})
}

func registerMemoryHooks(p memoryHookParams) {
	if p.Dispatcher == nil || p.Hooks == nil || !p.Hooks.enabled {
		return
	}
	var cancels []context.CancelFunc
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = []context.CancelFunc{
				bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
					p.Hooks.onThreadStart(context.Background(), ev)
				}, pkglogger.Get()),
				bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
					p.Hooks.onTurnEnd(context.Background(), ev)
				}, pkglogger.Get()),
			}
			return nil
		},
		OnStop: func(context.Context) error {
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			return nil
		},
	})
}
