package prompt

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var Module = fx.Module("prompt",
	fx.Provide(
		NewConfig,
		NewServiceFx,
		AsPromptRegistry,
		AsPromptAssemblyService,
		AsDynamicSectionRegistrar,
		AsSectionInvalidator,
		registerPromptHandlers,
		NewSkillCatalogProviderFx,
		newPromptClassifier,
	),
	fx.Invoke(RegisterSkillCatalogProviderIfEnabled),
)

// newPromptClassifier reads env-driven classifier config at fx wire-up and
// returns the resulting Classifier. Disabled/missing-binary both yield
// NoopClassifier so downstream consumers (thread router) can always depend
// on a non-nil value and skip feature detection on the hot path.
func newPromptClassifier() classifier.Classifier {
	return classifier.NewService(classifier.NewConfigFromEnv())
}

// ServiceFxParams resolves optional dependencies needed to surface the
// user-configurable LSP prompt hint in the start system prompt.
type ServiceFxParams struct {
	fx.In
	Cfg         *Config
	Logger      *slog.Logger           `optional:"true"`
	Prefs       uipreference.Store     `optional:"true"`
	SharedFiles sharedfilestore.Reader `optional:"true"`
}

// NewServiceFx is the fx-facing constructor that wires the preference store
// and shared-file reader into the prompt Service so the configured LSP prompt
// hint actually reaches the assembled system prompt.
func NewServiceFx(p ServiceFxParams) Service {
	return NewService(p.Cfg, p.Logger, WithPromptHintSources(p.Prefs, p.SharedFiles))
}

type skillCatalogProviderDeps struct {
	fx.In
	Cfg    *Config
	Skills skillpkg.Service `optional:"true"`
}

func NewSkillCatalogProviderFx(deps skillCatalogProviderDeps) SkillCatalogProvider {
	if deps.Cfg == nil {
		return NewSkillCatalogProviderWithApproval(deps.Skills, nil, deps.Skills, 0)
	}
	charBudget := deps.Cfg.SkillCatalogTokenBudget * 4
	return NewSkillCatalogProviderWithOptionsAndApproval(
		deps.Skills,
		nil,
		deps.Skills,
		charBudget,
		SkillCatalogOptions{EmitMetaInstructions: deps.Cfg.EmitSkillCatalogMetaInstructions},
	)
}

type registerSkillCatalogDeps struct {
	fx.In
	Cfg       *Config
	Registrar contract.DynamicSectionRegistrar
	Provider  SkillCatalogProvider
	Skills    skillpkg.SkillCatalogSource `optional:"true"`
}

func RegisterSkillCatalogProviderIfEnabled(deps registerSkillCatalogDeps) error {
	logger := pkglogger.Get()
	if deps.Cfg == nil || !deps.Cfg.EnableSkillProgressiveDisclosure {
		logger.Info("skill_catalog_provider.skipped",
			"reason", "disabled",
			"flag", "ENABLE_SKILL_PROGRESSIVE_DISCLOSURE")
		return nil
	}
	if deps.Skills == nil {
		logger.Warn("skill_catalog_provider.skipped",
			"reason", "skill_service_unavailable",
			"hint", "fx optional injection returned nil; progressive disclosure stays inert")
		return nil
	}
	if deps.Registrar == nil {
		logger.Error("skill_catalog_provider.skipped",
			"reason", "registrar_nil")
		return nil
	}
	if err := deps.Registrar.RegisterDynamicProvider(deps.Provider); err != nil {
		logger.Error("skill_catalog_provider.register_failed",
			"error", err.Error())
		return err
	}
	logger.Info("skill_catalog_provider.registered",
		"token_budget", deps.Cfg.SkillCatalogTokenBudget,
		"emit_meta_instructions", deps.Cfg.EmitSkillCatalogMetaInstructions)
	return nil
}
