package prompt

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
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
		newPromptClassifier,
		newClassifierFastPathFunc,
		newClassifierPruneCandidatesFunc,
		newClassifierMaxCandidatesFunc,
		newMatchWhenEvaluator,
	),
)

// newPromptClassifier reads env-driven classifier config at fx wire-up and
// returns the resulting Classifier. Disabled/missing-binary both yield
// NoopClassifier so downstream consumers (thread router) can always depend
// on a non-nil value and skip feature detection on the hot path.
func newPromptClassifier() contract.PromptClassifier {
	return classifier.NewService(classifier.NewConfigFromEnv())
}

func newClassifierFastPathFunc() contract.ClassifierFastPathFunc {
	return classifier.FastPath
}

func newClassifierPruneCandidatesFunc() contract.ClassifierPruneCandidatesFunc {
	return classifier.PruneCandidates
}

func newClassifierMaxCandidatesFunc() contract.ClassifierMaxCandidatesFunc {
	return classifier.MaxCandidatesFromEnv
}

func newMatchWhenEvaluator() contract.MatchWhenEvaluator {
	return EvaluateMatchWhen
}

// ServiceFxParams resolves optional dependencies needed to surface the
// user-configurable LSP prompt hint in the start system prompt.
type ServiceFxParams struct {
	fx.In
	Cfg             *Config
	Logger          *slog.Logger                          `optional:"true"`
	Prefs           uipreference.Store                    `optional:"true"`
	SharedFiles     sharedfilestore.Reader                `optional:"true"`
	SkillStore      contract.SkillNativeReplacementSource `optional:"true"`
	DisabledToolsFn DisabledBuiltinToolsFn                `optional:"true"`
}

// NewServiceFx is the fx-facing constructor that wires the preference store,
// shared-file reader, skill library store, and disabled-tools function into
// the prompt Service.
func NewServiceFx(p ServiceFxParams) Service {
	opts := []ServiceOption{
		WithPromptHintSources(p.Prefs, p.SharedFiles),
		WithSkillStore(p.SkillStore),
	}
	if p.DisabledToolsFn != nil {
		opts = append(opts, WithDisabledBuiltinToolsFn(p.DisabledToolsFn))
	}
	return NewService(p.Cfg, p.Logger, opts...)
}
