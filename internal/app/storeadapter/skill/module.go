package skilladapter

import "go.uber.org/fx"

// Module 提供 skill 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.skill",
	fx.Provide(
		provideSkillMutationAuditStore,
		provideSkillToolPersistence,
	),
)
