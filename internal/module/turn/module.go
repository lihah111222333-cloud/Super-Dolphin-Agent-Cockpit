package turn

import (
	"context"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndTurnContext,
			// p20.2 step 1: Skill.Service is optional, contract.Contract is also optional, etc.
			// (Original tag rationale preserved below.)
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewOrchestrationTurnStarter,
			fx.ParamTags("", "", `optional:"true"`),
		),
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
		// P0b Step 2: trajectory collector. observation.Contract is
		// optional so deployments without observation still wire turn
		// successfully; the collector tolerates a nil contract.
		fx.Annotate(
			NewTrajectoryCollector,
			fx.ParamTags(`optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewTrajectorySubscribers,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`),
		),
		// P0b Step 3: skill evaluator. stateless / pure function; no
		// external dependencies, so plain fx.Provide is sufficient.
		// The interface adapter mirrors the Redactor/Extractor pattern:
		// NewDefaultEvaluator returns *DefaultEvaluator, but NewDefaultExtractor
		// wants the Evaluator interface — fx does not auto-cast.
		NewDefaultEvaluator,
		func(e *DefaultEvaluator) Evaluator { return e },
		// P0b Step 4: redactor + extractor + extractor runner.
		// - Redactor is exposed as the interface so other modules can
		//   substitute a stub in tests.
		// - DefaultExtractor takes contract.DreamExecutor and
		//   skillcandidate.Store as fx-optional; either being nil short-
		//   circuits Extract with a metric bump rather than a hard fail.
		//   This keeps deployments without P0b wiring buildable.
		// - The extractor is also exposed via the Extractor interface
		//   so the runner can be unit-tested without spinning the
		//   full dependency graph.
		// - The runner is registered into `group:"runners"` so its
		//   lifecycle is owned by the root run.Group supervisor (same
		//   pattern as SweeperRunner / ApprovalCleanupRunner).
		func() Redactor { return NewDefaultRedactor() },
		fx.Annotate(
			NewDefaultExtractor,
			fx.ParamTags(`optional:"true"`, `optional:"true"`, "", "", ""),
		),
		func(e *DefaultExtractor) Extractor { return e },
		NewExtractorRunner,
		fx.Annotate(
			func(r *ExtractorRunner) contract.Runner { return r },
			fx.ResultTags(`group:"runners"`),
		),
	),
	fx.Invoke(registerTurnServiceLifecycle),
)

// registerTurnServiceLifecycle wires the turn Service into fx.Lifecycle so
// its Shutdown hook is called on app stop. Shutdown is discovered via a
// private shutdowner interface assertion so the public Service contract
// stays unchanged.
func registerTurnServiceLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	type shutdowner interface{ Shutdown() }
	sd, ok := svc.(shutdowner)
	if !ok {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			sd.Shutdown()
			return nil
		},
	})
}
