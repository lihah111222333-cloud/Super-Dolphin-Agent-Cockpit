package prompt

import "context"

const summarizeToolResultsSectionText = `When working with tool results, write down any important information you might need later in your response, as the original tool result may be cleared later.`

var _ DynamicSectionProvider = SummarizeToolResultsProvider{}

type SummarizeToolResultsProvider struct{}

func (SummarizeToolResultsProvider) SectionName() string {
	return DynamicSectionSummarizeToolResults
}

func (SummarizeToolResultsProvider) Resolve(context.Context, SectionContext) (*string, error) {
	text := summarizeToolResultsSectionText
	return &text, nil
}
