package archtest

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

func threadOptionalDependencyBudgets() map[optionalDependencyBudgetKey]int {
	return map[optionalDependencyBudgetKey]int{
		{owner: "internal/module/thread", category: optionalDependencyAbsence}: 1,
		{owner: "internal/module/thread", category: optionalAdjunct}:           6,
	}
}

func registeredOptionalDependencyThreadClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	dependency := func(name string, profile contract.DependencyProfile, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: optionalDependencyAbsence, dependency: name, profile: profile, owner: owner, evidence: evidence}
	}
	threadAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/module/thread", path+": "+evidence)
	}
	threadDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/module/thread", path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/module/thread/lifecycle.go:typed_unsupported:thread.bind_session_generation":     threadDependency("internal/module/thread/lifecycle.go", "thread.bind_session_generation", contract.DependencyProfileDesktopHost, "BindSessionGeneration propagates MissingDependencyModeError for profiles without session-generation binding"),
		"internal/module/thread/module.go:optional_tag:NewServiceWithPromptAssemblyAndSharedFiles": threadAdjunct("internal/module/thread/module.go", "fx.Annotate(NewServiceWithPromptAssemblyAndSharedFiles, fx.ParamTags(... optional:\"true\" ...)) inventories optional constructor adjuncts"),
		"internal/module/thread/module.go:optional_tag:NewThreadHandlers":                          threadAdjunct("internal/module/thread/module.go", "fx.Annotate(NewThreadHandlers, fx.ParamTags(... optional:\"true\" ...)) inventories optional constructor adjuncts"),
		"internal/app/storeadapter/thread/prompt.go:optional_tag:Store":                            threadAdjunct("internal/app/storeadapter/thread/prompt.go", "prompt Store inputs are optional so builtin-only runtime catalogs remain a legal read-only capability"),
		"internal/app/storeadapter/thread/prompt.go:optional_tag:Builtin":                          threadAdjunct("internal/app/storeadapter/thread/prompt.go", "builtin registry is an optional catalog source composed independently of prompt persistence"),
		"internal/app/storeadapter/thread/prompt.go:optional_tag:Registrar":                        threadAdjunct("internal/app/storeadapter/thread/prompt.go", "dynamic section registration is a no-op when prompt assembly is absent"),
		"internal/app/storeadapter/thread/prompt.go:optional_tag:Catalog":                          threadAdjunct("internal/app/storeadapter/thread/prompt.go", "catalog is conditional on prompt sources; RegisterProviders fails fast when a registrar exists without a catalog"),
	}
}
