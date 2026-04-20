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
		// P20.1 Phase 10: 聚合 provider 侧 SkillInjectionPort group → NativeSkillDetector。
		NewCompositeNativeSkillDetector,
		// P20.1 Phase 10: 按 cfg + 可选 skill.Service 构造 SkillCatalogProvider。
		NewSkillCatalogProviderFx,
		// p20.1 bug fix: 恢复 prompts/list|write|delete 宿主 RPC。
		NewPromptHandlers,
	),
	// P20.1 Phase 10: 灰度注册 SkillCatalogProvider 到 dynamic section registrar。
	// flag=false 或 skill.Service 缺席时 no-op，section 渲染为空 → 回滚兼容。
	fx.Invoke(RegisterSkillCatalogProviderIfEnabled),
)
