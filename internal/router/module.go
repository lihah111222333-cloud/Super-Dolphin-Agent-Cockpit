package router

import "go.uber.org/fx"

// Module wires the router backend. Default = RuleRouter; swap by overriding
// the Backend provider (e.g. in an integration-test fx harness or a build
// variant that enables HaikuRouter).
var Module = fx.Module("router",
	fx.Provide(
		func() Backend { return NewRuleRouter() },
	),
)
