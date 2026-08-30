package codexapp

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// requireProviderNativeSkills 在 Codex 取得 provider-owned 能力证据前拒绝显式技能选择。
// start/resume 当前明确记录 unknown context，不能把可能被 app-server 忽略的请求静默发送；
// schema=0 仅保留没有 native context 的旧手工 session 兼容行为。
func (s *session) requireProviderNativeSkills(skills []dto.SkillRef) error {
	if len(skills) == 0 || s == nil {
		return nil
	}
	ctx := s.nativeContextSnapshot()
	if ctx.SchemaVersion == 0 {
		return nil
	}
	if err := ctx.RequireProviderFeature(contract.ProviderNativeFeatureSkills); err != nil {
		return fmt.Errorf("codexapp: explicit skills require provider-native capability: %w", err)
	}
	return nil
}
