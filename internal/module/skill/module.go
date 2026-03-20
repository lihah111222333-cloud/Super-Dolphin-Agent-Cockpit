package skill

import "go.uber.org/fx"

var Module = fx.Module("skill",
	fx.Provide(NewService),
	fx.Provide(NewSkillHandlers),
)
