package prompt

import (
	"log/slog"

	"go.uber.org/fx"

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
	),
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
