package prompt

import (
	"context"
	"fmt"
)

var _ DynamicSectionProvider = FRCProvider{}

type FRCProvider struct{}

func (FRCProvider) SectionName() string {
	return DynamicSectionFRC
}

func (FRCProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	cfg := input.BuildCtx.FRCConfig
	if !cfg.EnabledForModel(input.BuildCtx.Model) {
		return nil, nil
	}
	text := fmt.Sprintf(
		"Old tool results will be automatically cleared from context to free up space.\nThe %d most recent results are always kept.",
		cfg.KeepRecentCount(),
	)
	return &text, nil
}
