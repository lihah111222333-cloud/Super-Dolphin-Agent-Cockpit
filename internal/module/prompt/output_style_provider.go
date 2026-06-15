package prompt

import (
	"context"
	"strings"
)

var _ DynamicSectionProvider = OutputStyleProvider{}

type OutputStyleProvider struct{}

// SectionName 处理section名称。
func (OutputStyleProvider) SectionName() string {
	return DynamicSectionOutputStyle
}

func hasRenderableOutputStyle(cfg *OutputStyleConfig) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.Name) != "" ||
		strings.TrimSpace(cfg.Description) != "" ||
		strings.TrimSpace(cfg.Prompt) != ""
}

// Resolve 解析prompt。
func (OutputStyleProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	cfg := input.BuildCtx.OutputStyleConfig
	if !hasRenderableOutputStyle(cfg) {
		return nil, nil
	}
	name := strings.TrimSpace(cfg.Name)
	description := strings.TrimSpace(cfg.Description)
	prompt := strings.TrimSpace(cfg.Prompt)
	lines := []string{"# Output Style"}
	if name != "" {
		lines[0] += ": " + name
	}
	if description != "" {
		lines = append(lines, description)
	}
	if prompt != "" && prompt != description {
		lines = append(lines, prompt)
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return nil, nil
	}
	return &text, nil
}

const numericLengthAnchorsSectionText = `Length limits:
- Keep text between tool calls to 25 words or fewer.
- Keep the final response to 100 words or fewer unless the task truly needs more detail.`

var _ DynamicSectionProvider = NumericLengthAnchorsProvider{}

type NumericLengthAnchorsProvider struct{}

func (NumericLengthAnchorsProvider) SectionName() string {
	return DynamicSectionNumericLengthAnchors
}

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
