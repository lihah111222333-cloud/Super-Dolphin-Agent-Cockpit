package orchestration

import "go.uber.org/fx"

var Module = fx.Module("orchestration",
	fx.Provide(
		NewService,
		func(s *service) Service { return s },
		fx.Annotate(NewRunnerActor, fx.ResultTags(`group:"runners"`)),
	),
)
