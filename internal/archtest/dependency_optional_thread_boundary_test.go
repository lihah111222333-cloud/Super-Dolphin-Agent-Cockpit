package archtest

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

func threadOptionalDependencyBudgets() map[optionalDependencyBudgetKey]int {
	return map[optionalDependencyBudgetKey]int{
		{owner: "internal/module/thread", category: optionalDependencyAbsence}: 1,
		{owner: "internal/module/thread", category: optionalAdjunct}:           8,
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
		"internal/module/thread/module.go:optional_tag:Store":                                      threadAdjunct("internal/module/thread/module.go", "store port adapters preserve Fx closure while service methods fail-fast when missing"),
		"internal/module/thread/module.go:optional_tag:Catalog":                                    threadAdjunct("internal/module/thread/module.go", "catalog injection is a prompt assembly adjunct covered by runtime catalog construction"),
		"internal/module/thread/module.go:optional_tag:Registrar":                                  threadAdjunct("internal/module/thread/module.go", "thread prompt registration tolerates nil registrar as no-op registration boundary"),
		"internal/module/thread/module.go:optional_tag:PromptStore":                                threadAdjunct("internal/module/thread/module.go", "runtime prompt catalog can be built with nil store for test and desktop fallback surfaces"),
		"internal/module/thread/module.go:optional_tag:Builtin":                                    threadAdjunct("internal/module/thread/module.go", "runtime prompt catalog treats builtin prompt registry as adjunct input"),
		"internal/module/thread/module.go:optional_tag:PromptCatalog":                              threadAdjunct("internal/module/thread/module.go", "registerThreadPromptProviders synthesizes catalog from PromptStore/Builtin when omitted"),
	}
}
