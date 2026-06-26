package prompt

import (
	"context"
	"strings"
)

var _ DynamicSectionProvider = OutputStyleProvider{}

// OutputStyleProvider 把用户配置的输出风格追加到动态 prompt 中。
type OutputStyleProvider struct{}

// SectionName 返回输出风格动态 section 的注册名。
func (OutputStyleProvider) SectionName() string {
	return DynamicSectionOutputStyle
}

// hasRenderableOutputStyle 判断配置是否包含可展示内容，避免生成只有标题的空 section。
func hasRenderableOutputStyle(cfg *OutputStyleConfig) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.Name) != "" ||
		strings.TrimSpace(cfg.Description) != "" ||
		strings.TrimSpace(cfg.Prompt) != ""
}

// Resolve 渲染输出风格 section；当配置为空时返回 nil 让组装器跳过该 section。
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

// numericLengthAnchorsSectionText 是 ant 内部场景下用于压短回复的硬提示。
const numericLengthAnchorsSectionText = `Length limits:
- Keep text between tool calls to 25 words or fewer.
- Keep the final response to 100 words or fewer unless the task truly needs more detail.`

var _ DynamicSectionProvider = NumericLengthAnchorsProvider{}

// NumericLengthAnchorsProvider 在特定用户类型下提供回复长度锚点。
type NumericLengthAnchorsProvider struct{}

// SectionName 返回长度锚点动态 section 的注册名。
func (NumericLengthAnchorsProvider) SectionName() string {
	return DynamicSectionNumericLengthAnchors
}

// Resolve 在长度锚点功能开启时返回固定提示文本。
func (NumericLengthAnchorsProvider) Resolve(context.Context, SectionContext) (*string, error) {
	if !numericLengthAnchorsEnabled() {
		return nil, nil
	}
	text := numericLengthAnchorsSectionText
	return &text, nil
}

// numericLengthAnchorsEnabled 判断当前环境是否允许注入长度锚点。
func numericLengthAnchorsEnabled() bool {
	return strings.EqualFold(promptUserType(), "ant")
}
