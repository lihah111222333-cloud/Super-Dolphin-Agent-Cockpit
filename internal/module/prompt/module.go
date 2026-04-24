package prompt

import (
	"log/slog"
	"sort"
	"strings"

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
		NewCompositeNativeSkillDetector,
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

const SkillInjectionPortGroupTag = `group:"skill_injection_ports"`

type compositeNativeSkillDetector struct {
	ports []contract.SkillInjectionPort
}

// DetectNativeSkills unions every registered port's detected skill names.
// P22 P4 fail-closed contract: if any port returns contract.ErrMissingCWD
// (meaning cwd was required but missing), the composite propagates that
// error instead of silently falling back to "no native skills" — the
// caller must know the input was incomplete. Non-ErrMissingCWD errors are
// treated as port-local failures and are also propagated so the caller
// can decide whether to proceed with a partial result (policy lives at
// the call site, not here).
func (d compositeNativeSkillDetector) DetectNativeSkills(cwd string) ([]string, error) {
	if len(d.ports) == 0 {
		return nil, nil
	}
	cwd = strings.TrimSpace(cwd)
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, port := range d.ports {
		if port == nil {
			continue
		}
		names, err := port.DetectNativeSkills(cwd)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, strings.TrimSpace(name))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out, nil
}

type NewCompositeNativeSkillDetectorParams struct {
	fx.In
	Ports []contract.SkillInjectionPort `group:"skill_injection_ports"`
}

func NewCompositeNativeSkillDetector(p NewCompositeNativeSkillDetectorParams) NativeSkillDetector {
	filtered := make([]contract.SkillInjectionPort, 0, len(p.Ports))
	for _, port := range p.Ports {
		if port == nil {
			continue
		}
		filtered = append(filtered, port)
	}
	return compositeNativeSkillDetector{ports: filtered}
}

type skillCatalogProviderDeps struct {
	fx.In
	Cfg      *Config
	Skills   skillpkg.Service    `optional:"true"`
	Detector NativeSkillDetector `optional:"true"`
}

func NewSkillCatalogProviderFx(deps skillCatalogProviderDeps) SkillCatalogProvider {
	if deps.Cfg == nil {
		return NewSkillCatalogProviderWithApproval(deps.Skills, deps.Detector, deps.Skills, 0)
	}
	charBudget := deps.Cfg.SkillCatalogTokenBudget * 4
	return NewSkillCatalogProviderWithOptionsAndApproval(
		deps.Skills,
		deps.Detector,
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
	Skills    skillpkg.Service `optional:"true"`
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
