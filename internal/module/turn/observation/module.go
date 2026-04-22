package observation

import (
	"context"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// Module wires the observation layer into the core Fx tree.
//
// Guarantees:
//   - Provides *Memory as both *Memory (for tests / direct introspection)
//     and Contract (the read/write facade consumers depend on).
//   - fx.Invoke(RegisterSubscribers) installs bus subscribers that push
//     canonical facts into the Memory; subscribers never read back.
//   - Shutdown cancels every subscription via the returned cancel func.
//
// The module is intentionally single-purpose: it does not import turn /
// tracker packages, so turn.Service cannot accidentally grow a reverse
// dependency on observation. P3 collector and P0b extractor consume the
// Contract that this module provides.
var Module = fx.Module("module.turn.observation",
	fx.Provide(
		NewMemory,
		func(m *Memory) Contract { return m },
	),
	fx.Invoke(RegisterSubscribers),
)

// SubscribersParams is the fx.In bundle for RegisterSubscribers. Logger is
// optional: subscribers fall back to pkglogger.Get() when missing.
type SubscribersParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher
	Contract   Contract
	Logger     *pkglogger.Logger `optional:"true"`
}

// RegisterSubscribers attaches the observation bus subscribers to the Fx
// lifecycle. Nothing subscribes until OnStart fires, and OnStop guarantees
// every subscription is cancelled before the bus dispatcher is closed.
func RegisterSubscribers(p SubscribersParams) {
	var cancel context.CancelFunc = func() {}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancel = Subscribe(p.Dispatcher, p.Contract, p.Logger)
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			cancel = func() {}
			return nil
		},
	})
}
