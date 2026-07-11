package personalization

import (
	"context"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// PromptProvider 把个人资料注入 prompt 动态 section 的提供器。
type PromptProvider struct {
	service Service
}

// NewPromptProvider 创建动态 prompt 提供器，负责把非空个人资料注入对话上下文。
func NewPromptProvider(service Service) *PromptProvider {
	return &PromptProvider{service: service}
}

// SectionName 返回个性化 profile 使用的专用 prompt slot。
func (p *PromptProvider) SectionName() string {
	return contract.DynamicSectionPersonalizationProfile
}

// Resolve 读取当前 cwd 的 profile；读取失败会阻断 prompt 组装，避免静默丢失个性化信息。
func (p *PromptProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	if p.service == nil {
		err := fmt.Errorf("personalization service is required")
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionPersonalizationProfile, err)
	}
	cwd := contract.SectionContextCWD(input)
	if strings.TrimSpace(cwd) == "" {
		err := fmt.Errorf("cwd is required for dynamic section %q", contract.DynamicSectionPersonalizationProfile)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionPersonalizationProfile, err)
	}
	result, err := p.service.GetProfile(ctx, cwd)
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionPersonalizationProfile, err)
	}
	text := renderPromptProfile(result.Profile)
	if text == "" {
		return nil, nil
	}
	return &text, nil
}

// renderPromptProfile 把个人资料渲染为注入 prompt 的文本块，所有字段均为空时返回空字符串。
func renderPromptProfile(profile Profile) string {
	lines := []string{"# 用户个人资料"}
	if value := strings.TrimSpace(profile.DisplayName); value != "" {
		lines = append(lines, "- 昵称："+value)
	}
	if value := strings.TrimSpace(profile.Role); value != "" {
		lines = append(lines, "- 职业/角色："+value)
	}
	if value := strings.TrimSpace(profile.Background); value != "" {
		lines = append(lines, "- 背景："+value)
	}
	if value := strings.TrimSpace(profile.CustomInstructions); value != "" {
		lines = append(lines, "- 定制说明："+value)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}
