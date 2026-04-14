package prompt

import "context"

var (
	_ DynamicSectionProvider = FRCStubProvider{}
	_ DynamicSectionProvider = NumericLengthAnchorsStubProvider{}
	_ DynamicSectionProvider = TokenBudgetStubProvider{}
	_ DynamicSectionProvider = BriefStubProvider{}
	_ DynamicSectionProvider = AntModelOverrideStubProvider{}
)

// FRCStubProvider reserves the frc section for future function result clearing
// and microcompact guidance that reclaims stale tool-result context.
// Claude reference: the FRC / CACHED_MICROCOMPACT branch inside
// getSystemPrompt().
type FRCStubProvider struct{}

func (FRCStubProvider) SectionName() string {
	return DynamicSectionFRC
}

func (FRCStubProvider) Resolve(context.Context, SectionContext) (*string, error) {
	return nil, nil
}

// NumericLengthAnchorsStubProvider reserves the numeric_length_anchors section
// for future ant-only length-anchor instructions that constrain response size.
// Claude reference: the ant-only numeric_length_anchors branch in
// getSystemPrompt().
type NumericLengthAnchorsStubProvider struct{}

func (NumericLengthAnchorsStubProvider) SectionName() string {
	return DynamicSectionNumericLengthAnchors
}

func (NumericLengthAnchorsStubProvider) Resolve(context.Context, SectionContext) (*string, error) {
	return nil, nil
}

// TokenBudgetStubProvider reserves the token_budget section for future token
// target and auto-continue budget guidance.
// Claude reference: the TOKEN_BUDGET-gated branch in getSystemPrompt().
type TokenBudgetStubProvider struct{}

func (TokenBudgetStubProvider) SectionName() string {
	return DynamicSectionTokenBudget
}

func (TokenBudgetStubProvider) Resolve(context.Context, SectionContext) (*string, error) {
	return nil, nil
}

// BriefStubProvider reserves the brief section for future KAIROS recap,
// proactive dedupe, and brief-mode prompt guidance.
// Claude reference: the KAIROS / KAIROS_BRIEF branch in getSystemPrompt().
type BriefStubProvider struct{}

func (BriefStubProvider) SectionName() string {
	return DynamicSectionBrief
}

func (BriefStubProvider) Resolve(context.Context, SectionContext) (*string, error) {
	return nil, nil
}

// AntModelOverrideStubProvider reserves the ant_model_override section for
// future ant-family system-prompt overrides.
// Claude reference: the ant-only model override branch in getSystemPrompt().
type AntModelOverrideStubProvider struct{}

func (AntModelOverrideStubProvider) SectionName() string {
	return DynamicSectionAntModelOverride
}

func (AntModelOverrideStubProvider) Resolve(context.Context, SectionContext) (*string, error) {
	return nil, nil
}
