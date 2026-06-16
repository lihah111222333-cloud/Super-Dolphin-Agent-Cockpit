package personalization

import (
	"fmt"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type promptProviderParams struct {
	fx.In

	Registry contract.DynamicSectionRegistrar `optional:"true"`
	Provider *PromptProvider                  `optional:"true"`
}

var Module = fx.Module("personalization",
	fx.Provide(
		NewService,
		NewHandlers,
		NewPromptProvider,
	),
	fx.Invoke(registerPromptProvider),
)

func registerPromptProvider(p promptProviderParams) error {
	if p.Registry == nil || p.Provider == nil {
		return nil
	}
	if err := p.Registry.RegisterDynamicProvider(p.Provider); err != nil {
		return fmt.Errorf("personalization: register prompt provider: %w", err)
	}
	return nil
}
