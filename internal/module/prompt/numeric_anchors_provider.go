package prompt

import (
	"context"
	"strings"
)

const numericLengthAnchorsSectionText = `Length limits:
- Keep text between tool calls to 25 words or fewer.
- Keep the final response to 100 words or fewer unless the task truly needs more detail.`

var _ DynamicSectionProvider = NumericLengthAnchorsProvider{}

type NumericLengthAnchorsProvider struct{}

// SectionName 处理section名称。
func (NumericLengthAnchorsProvider) SectionName() string {
	return DynamicSectionNumericLengthAnchors
}

// Resolve 解析prompt。
func (NumericLengthAnchorsProvider) Resolve(context.Context, SectionContext) (*string, error) {
	if !numericLengthAnchorsEnabled() {
		return nil, nil
	}
	text := numericLengthAnchorsSectionText
	return &text, nil
}

func numericLengthAnchorsEnabled() bool {
	return strings.EqualFold(promptUserType(), "ant")
}
