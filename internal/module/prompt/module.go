package prompt

import "go.uber.org/fx"

var Module = fx.Module("prompt",
	fx.Provide(
		NewConfig,
		NewService,
		AsPromptRegistry,
		AsPromptAssemblyService,
		AsDynamicSectionRegistrar,
		AsSectionInvalidator,
		registerPromptHandlers,
	),
)

const SkillInjectionPortGroupTag = `group:"skill_injection_ports"`
