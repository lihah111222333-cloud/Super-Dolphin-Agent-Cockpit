package prompt

import (
	"context"
	"fmt"
)

var _ DynamicSectionProvider = FRCProvider{}

// FRCProvider 在模型支持且配置启用时注入工具结果自动清理提示。
type FRCProvider struct{}

// SectionName 返回 FRC 动态 section 的注册名。
func (FRCProvider) SectionName() string {
	return DynamicSectionFRC
}

// Resolve 按当前模型和 FRC 配置决定是否提示“旧工具结果会被清理”。
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
